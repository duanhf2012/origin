package command

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/duanhf2012/origin/v3/command/internal/processlock"
	"github.com/duanhf2012/origin/v3/errs"
)

const (
	controlPollInterval   = 25 * time.Millisecond
	defaultControlTimeout = 30 * time.Second
	maxControlMessageSize = 2 * 1024
)

type controlPaths struct {
	pid        string
	lock       string
	request    string
	processing string
	response   string
}

type controlRequest struct {
	action   ControlAction
	ctx      context.Context
	complete func(error)
}

func (request *controlRequest) Action() ControlAction    { return request.action }
func (request *controlRequest) Context() context.Context { return request.ctx }
func (request *controlRequest) Complete(err error)       { request.complete(err) }

type controlMailbox struct {
	requests  <-chan ControlRequest
	cancel    context.CancelFunc
	result    <-chan error
	closeOnce sync.Once
	closeErr  error
	paths     controlPaths
}

func startControlMailbox(
	parent context.Context,
	pidDir string,
	appName string,
) (*controlMailbox, error) {
	if parent == nil {
		return nil, invalidArgumentf("control mailbox context is required")
	}
	paths := newControlPaths(pidDir, appName)
	for _, path := range []string{paths.request, paths.processing, paths.response} {
		if err := removeRegularControlFile(path); err != nil {
			return nil, processControlf("clean stale control file %q: %v", path, err)
		}
	}

	ctx, cancel := context.WithCancel(parent)
	queue := make(chan ControlRequest, 1)
	result := make(chan error, 1)
	mailbox := &controlMailbox{
		requests: queue,
		cancel:   cancel,
		result:   result,
		paths:    paths,
	}
	go func() {
		err := serveControlMailbox(ctx, paths, queue)
		close(queue)
		result <- err
	}()
	return mailbox, nil
}

func (mailbox *controlMailbox) close() error {
	if mailbox == nil {
		return nil
	}
	mailbox.closeOnce.Do(func() {
		mailbox.cancel()
		mailbox.closeErr = <-mailbox.result
	})
	return mailbox.closeErr
}

func serveControlMailbox(
	ctx context.Context,
	paths controlPaths,
	queue chan<- ControlRequest,
) error {
	ticker := time.NewTicker(controlPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}

		claimed, err := claimControlRequest(paths.request, paths.processing)
		if err != nil {
			return processControlf("claim control request %q: %v", paths.request, err)
		}
		if !claimed {
			continue
		}
		if err := processClaimedControlRequest(ctx, paths, queue); err != nil {
			return err
		}
	}
}

func claimControlRequest(requestPath string, processingPath string) (bool, error) {
	info, err := os.Lstat(requestPath)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() {
		return false, fmt.Errorf("control path is not a regular file")
	}
	if err := os.Rename(requestPath, processingPath); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func processClaimedControlRequest(
	mailboxCtx context.Context,
	paths controlPaths,
	queue chan<- ControlRequest,
) error {
	data, err := readBoundedRegularFile(paths.processing)
	if err != nil {
		return processControlf("read processing control request %q: %v", paths.processing, err)
	}
	record, err := decodeControlRequest(data)
	if err != nil {
		return processControlf("decode processing control request %q: %v", paths.processing, err)
	}
	action, err := parseControlAction(record.Action)
	if err != nil {
		return processControlf("decode control action: %v", err)
	}
	deadline, err := time.Parse(time.RFC3339Nano, record.Deadline)
	if err != nil {
		return processControlf("decode control deadline: %v", err)
	}
	requestCtx, cancel := context.WithDeadline(mailboxCtx, deadline)
	defer cancel()

	completion := make(chan error, 1)
	var completeOnce sync.Once
	request := &controlRequest{
		action: action,
		ctx:    requestCtx,
		complete: func(result error) {
			completeOnce.Do(func() { completion <- result })
		},
	}

	var result error
	select {
	case queue <- request:
		select {
		case result = <-completion:
		case <-requestCtx.Done():
			result = controlContextError(requestCtx, "control request execution")
		}
	case <-requestCtx.Done():
		result = controlContextError(requestCtx, "control request delivery")
	}

	response := newControlResponse(record.ID, result)
	responseData, err := encodeControlResponse(response)
	if err != nil {
		return processControlf("encode control response: %v", err)
	}
	if err := writeControlRecordAtomic(paths.response, responseData); err != nil {
		return processControlf("write control response %q: %v", paths.response, err)
	}
	if err := os.Remove(paths.processing); err != nil && !os.IsNotExist(err) {
		return processControlf("remove processing control request %q: %v", paths.processing, err)
	}
	return nil
}

func requestApplicationControl(
	parent context.Context,
	pidDir string,
	appName string,
	action ControlAction,
) error {
	if parent == nil {
		return invalidArgumentf("control request context is required")
	}
	actionText, err := formatControlAction(action)
	if err != nil {
		return invalidArgumentf("%v", err)
	}
	ctx, cancel := controlDeadlineContext(parent)
	defer cancel()
	paths := newControlPaths(pidDir, appName)

	running, _, err := readRunningPID(pidFilePath(pidDir, appName))
	if err != nil {
		return err
	}
	if !running {
		return processControlf("application %q is not running", appName)
	}

	lock, err := acquireControlLock(ctx, paths.lock)
	if err != nil {
		return err
	}
	defer func() { _ = processlock.Release(lock) }()

	if err := waitForControlMailboxIdle(ctx, paths, appName); err != nil {
		return err
	}
	requestID, err := newControlRequestID()
	if err != nil {
		return processControlf("generate control request ID: %v", err)
	}
	deadline, _ := ctx.Deadline()
	requestData, err := encodeControlRequest(controlRequestRecord{
		ID:       requestID,
		Action:   actionText,
		Deadline: deadline.UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		return processControlf("encode control request: %v", err)
	}
	if err := writeControlRecordAtomic(paths.request, requestData); err != nil {
		return processControlf("write control request %q: %v", paths.request, err)
	}
	defer removeOwnedControlRecords(paths, requestID)

	return waitForControlResponse(ctx, paths, appName, requestID)
}

func acquireControlLock(ctx context.Context, path string) (*os.File, error) {
	ticker := time.NewTicker(controlPollInterval)
	defer ticker.Stop()
	for {
		file, err := openControlLock(path)
		if err != nil {
			return nil, processControlf("open control lock %q: %v", path, err)
		}
		acquired, lockErr := processlock.TryLock(file)
		if lockErr != nil {
			_ = file.Close()
			return nil, processControlf("lock control file %q: %v", path, lockErr)
		}
		if acquired {
			return file, nil
		}
		if err := file.Close(); err != nil {
			return nil, processControlf("close contended control lock %q: %v", path, err)
		}
		select {
		case <-ctx.Done():
			return nil, controlContextError(ctx, "waiting for control lock")
		case <-ticker.C:
		}
	}
}

func openControlLock(path string) (*os.File, error) {
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("control lock is not a regular file")
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	return os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
}

func waitForControlMailboxIdle(ctx context.Context, paths controlPaths, appName string) error {
	ticker := time.NewTicker(controlPollInterval)
	defer ticker.Stop()
	for {
		requestExists, err := regularControlFileExists(paths.request)
		if err != nil {
			return processControlf("inspect control request %q: %v", paths.request, err)
		}
		processingExists, err := regularControlFileExists(paths.processing)
		if err != nil {
			return processControlf("inspect processing request %q: %v", paths.processing, err)
		}
		if !requestExists && !processingExists {
			if err := removeRegularControlFile(paths.response); err != nil {
				return processControlf("clean completed control response %q: %v", paths.response, err)
			}
			return ensureApplicationRunning(paths.pid, appName)
		}
		if err := ensureApplicationRunning(paths.pid, appName); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return controlContextError(ctx, "waiting for control mailbox")
		case <-ticker.C:
		}
	}
}

func waitForControlResponse(
	ctx context.Context,
	paths controlPaths,
	appName string,
	requestID string,
) error {
	return waitForControlResponseWithReader(
		ctx,
		paths,
		appName,
		requestID,
		readOptionalRegularControlFile,
	)
}

// waitForControlResponseWithReader 等待目标进程原子发布与 requestID 匹配的响应。
//
// readFile 参数只把平台文件读取边界显式化，便于确定性验证 Windows 瞬时共享冲突；正式
// 路径始终传入 readOptionalRegularControlFile，不提供运行时替换或全局测试钩子。
func waitForControlResponseWithReader(
	ctx context.Context,
	paths controlPaths,
	appName string,
	requestID string,
	readFile func(string) ([]byte, error),
) error {
	ticker := time.NewTicker(controlPollInterval)
	defer ticker.Stop()
	for {
		data, err := readFile(paths.response)
		if err != nil && !isTransientControlResponseReadError(err) {
			return processControlf("read control response %q: %v", paths.response, err)
		}
		if err == nil && data != nil {
			response, decodeErr := decodeControlResponse(data, requestID)
			if decodeErr == nil {
				if response.Success {
					return nil
				}
				return errs.NewMessage(response.ErrorCode, response.Message)
			}
		}
		if err := ensureApplicationRunning(paths.pid, appName); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return controlContextError(ctx, "waiting for control response")
		case <-ticker.C:
		}
	}
}

func controlDeadlineContext(parent context.Context) (context.Context, context.CancelFunc) {
	if _, exists := parent.Deadline(); exists {
		return context.WithCancel(parent)
	}
	return context.WithTimeout(parent, defaultControlTimeout)
}

func controlContextError(ctx context.Context, operation string) error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return errs.NewMessage(errs.CodeDeadlineExceeded, operation+": deadline exceeded")
	}
	return errs.Wrap(errs.CodeCanceled, ctx.Err())
}

func newControlPaths(pidDir string, appName string) controlPaths {
	return controlPaths{
		pid:        pidFilePath(pidDir, appName),
		lock:       controlLockPath(pidDir, appName),
		request:    controlRequestPath(pidDir, appName),
		processing: controlProcessingPath(pidDir, appName),
		response:   controlResponsePath(pidDir, appName),
	}
}

func newControlRequestID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}

func newControlResponse(id string, result error) controlResponseRecord {
	if result == nil {
		return controlResponseRecord{ID: id, Success: true}
	}
	message := result.Error()
	if len(message) > maxControlMessageSize {
		message = message[:maxControlMessageSize]
		for !utf8.ValidString(message) {
			message = message[:len(message)-1]
		}
	}
	return controlResponseRecord{
		ID:        id,
		ErrorCode: errs.CodeOf(result),
		Message:   message,
	}
}

func regularControlFileExists(path string) (bool, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() {
		return false, fmt.Errorf("control path is not a regular file")
	}
	return true, nil
}

func readOptionalRegularControlFile(path string) ([]byte, error) {
	exists, err := regularControlFileExists(path)
	if err != nil || !exists {
		return nil, err
	}
	return readBoundedRegularFile(path)
}

func removeRegularControlFile(path string) error {
	exists, err := regularControlFileExists(path)
	if err != nil || !exists {
		return err
	}
	return os.Remove(path)
}

func removeOwnedControlRecords(paths controlPaths, requestID string) {
	if data, err := readOptionalRegularControlFile(paths.request); err == nil && data != nil {
		if record, decodeErr := decodeControlRequest(data); decodeErr == nil && record.ID == requestID {
			_ = os.Remove(paths.request)
		}
	}
	if data, err := readOptionalRegularControlFile(paths.response); err == nil && data != nil {
		if record, decodeErr := decodeControlResponse(data, requestID); decodeErr == nil && record.ID == requestID {
			_ = os.Remove(paths.response)
		}
	}
}

func ensureApplicationRunning(pidPath string, appName string) error {
	running, _, err := readRunningPID(pidPath)
	if err != nil {
		return err
	}
	if !running {
		return processControlf("application %q stopped before completing control request", appName)
	}
	return nil
}
