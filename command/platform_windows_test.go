//go:build windows

package command

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/duanhf2012/origin/v3/errs"
	"golang.org/x/sys/windows"
)

func TestWindowsControlResponseSharingViolationIsTransient(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "game.control.response")
	if err := os.WriteFile(path, []byte("response"), 0o600); err != nil {
		t.Fatalf("WriteFile(response) error = %v", err)
	}
	pathPointer, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatalf("UTF16PtrFromString(response path) error = %v", err)
	}
	handle, err := windows.CreateFile(
		pathPointer,
		windows.GENERIC_READ,
		0,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_ATTRIBUTE_NORMAL,
		0,
	)
	if err != nil {
		t.Fatalf("CreateFile(exclusive response) error = %v", err)
	}
	t.Cleanup(func() {
		_ = windows.CloseHandle(handle)
	})

	_, readErr := readOptionalRegularControlFile(path)
	if readErr == nil {
		t.Fatal("readOptionalRegularControlFile(exclusive response) error = nil")
	}
	if !isTransientControlResponseReadError(readErr) {
		t.Fatalf("exclusive response error = %v, want transient", readErr)
	}
}

func TestWindowsControlResponseRetriesTransientRead(t *testing.T) {
	t.Parallel()

	pidDir := t.TempDir()
	lease, err := acquirePIDLease(pidDir, "game")
	if err != nil {
		t.Fatalf("acquirePIDLease() error = %v", err)
	}
	t.Cleanup(func() {
		_ = lease.close()
	})
	paths := newControlPaths(pidDir, "game")
	encoded, err := encodeControlResponse(controlResponseRecord{
		ID:      testControlID,
		Success: true,
	})
	if err != nil {
		t.Fatalf("encodeControlResponse() error = %v", err)
	}

	reads := 0
	readFile := func(path string) ([]byte, error) {
		if path != paths.response {
			t.Fatalf("response path = %q, want %q", path, paths.response)
		}
		reads++
		if reads == 1 {
			return nil, &os.PathError{
				Op:   "open",
				Path: path,
				Err:  windows.ERROR_SHARING_VIOLATION,
			}
		}
		return encoded, nil
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if err := waitForControlResponseWithReader(
		ctx,
		paths,
		"game",
		testControlID,
		readFile,
	); err != nil {
		t.Fatalf("waitForControlResponseWithReader() error = %v", err)
	}
	if reads != 2 {
		t.Fatalf("response reads = %d, want 2", reads)
	}

	if !isTransientControlResponseReadError(
		&os.PathError{Err: windows.ERROR_LOCK_VIOLATION},
	) {
		t.Fatal("ERROR_LOCK_VIOLATION was not classified as transient")
	}
	if isTransientControlResponseReadError(
		&os.PathError{Err: windows.ERROR_ACCESS_DENIED},
	) {
		t.Fatal("ERROR_ACCESS_DENIED was classified as transient")
	}
	if isTransientControlResponseReadError(errors.New("sharing violation text only")) {
		t.Fatal("plain text error was classified as transient")
	}
}

func TestWindowsStopControlLifecycle(t *testing.T) {
	t.Parallel()

	stopPath := filepath.Join(t.TempDir(), "game.stop")
	if err := os.WriteFile(stopPath, nil, 0o600); err != nil {
		t.Fatalf("write stale stop file error = %v", err)
	}

	runCtx, closeControl, err := startPlatformControl(context.Background(), stopPath)
	if err != nil {
		t.Fatalf("startPlatformControl() error = %v", err)
	}
	defer func() {
		_ = closeControl()
	}()
	if _, err := os.Stat(stopPath); !os.IsNotExist(err) {
		t.Fatalf("stale stop file was not removed: %v", err)
	}

	// 第一次创建请求成功，第二次创建命中 O_EXCL 幂等分支。
	if err := requestPlatformStop(0, stopPath); err != nil {
		t.Fatalf("first requestPlatformStop() error = %v", err)
	}
	if err := requestPlatformStop(0, stopPath); err != nil {
		t.Fatalf("second requestPlatformStop() error = %v", err)
	}
	select {
	case <-runCtx.Done():
	case <-time.After(time.Second):
		t.Fatalf("Windows stop file did not cancel run context")
	}
	if err := closeControl(); err != nil {
		t.Fatalf("closeControl() error = %v", err)
	}
}

func TestWindowsStopControlRejectsUnremovableStalePath(t *testing.T) {
	t.Parallel()

	// 非空目录不能按文件删除，稳定覆盖陈旧 stop 请求清理失败。
	stopPath := filepath.Join(t.TempDir(), "game.stop")
	if err := os.Mkdir(stopPath, 0o755); err != nil {
		t.Fatalf("Mkdir(stop path) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(stopPath, "child"), nil, 0o600); err != nil {
		t.Fatalf("WriteFile(child) error = %v", err)
	}
	if _, _, err := startPlatformControl(context.Background(), stopPath); err == nil {
		t.Fatalf("startPlatformControl(non-empty directory) error = nil")
	}
}

func TestWindowsRequestStopRejectsDirectoryParentFile(t *testing.T) {
	t.Parallel()

	parentFile := filepath.Join(t.TempDir(), "parent")
	if err := os.WriteFile(parentFile, nil, 0o600); err != nil {
		t.Fatalf("WriteFile(parent) error = %v", err)
	}
	if err := requestPlatformStop(0, filepath.Join(parentFile, "game.stop")); err == nil {
		t.Fatalf("requestPlatformStop(invalid parent) error = nil")
	}

	// 已存在目录不能被当作另一个 stop 创建的幂等普通文件。
	directoryPath := filepath.Join(t.TempDir(), "directory.stop")
	if err := os.Mkdir(directoryPath, 0o755); err != nil {
		t.Fatalf("Mkdir(stop directory) error = %v", err)
	}
	if err := requestPlatformStop(0, directoryPath); err == nil {
		t.Fatalf("requestPlatformStop(directory) error = nil")
	}
}

func TestWindowsRunStopRequestErrorAndParentCancellation(t *testing.T) {
	t.Parallel()

	t.Run("request error", func(t *testing.T) {
		pidDir := t.TempDir()
		lease, err := acquirePIDLease(pidDir, "request-error")
		if err != nil {
			t.Fatalf("acquirePIDLease() error = %v", err)
		}
		defer func() {
			_ = lease.close()
		}()

		// 同名 stop 路径使用目录，验证 runStop 把平台请求失败映射为退出码 3。
		if err := os.Mkdir(stopFilePath(pidDir, "request-error"), 0o755); err != nil {
			t.Fatalf("Mkdir(stop path) error = %v", err)
		}
		runner, _, _ := newTestRunner(t, noOpStart)
		code, stopErr := runner.Run(context.Background(), []string{
			"stop",
			"--app-name", "request-error",
			"--pid-dir", pidDir,
			"--timeout", "50ms",
		})
		if code != ExitProcessControl || !errs.IsCode(stopErr, errs.CodeProcessControlFailed) {
			t.Fatalf("runStop request error = (%d, %v)", code, stopErr)
		}
	})

	t.Run("parent cancellation", func(t *testing.T) {
		pidDir := t.TempDir()
		lease, err := acquirePIDLease(pidDir, "cancel-stop")
		if err != nil {
			t.Fatalf("acquirePIDLease() error = %v", err)
		}
		defer func() {
			_ = lease.close()
		}()

		// 等到 stop 已表达请求并进入锁等待后再取消父 Context，验证非 timeout 取消分支。
		ctx, cancel := context.WithCancel(context.Background())
		runner, _, _ := newTestRunner(t, noOpStart)
		result := make(chan struct {
			code ExitCode
			err  error
		}, 1)
		go func() {
			code, stopErr := runner.Run(ctx, []string{
				"stop",
				"--app-name", "cancel-stop",
				"--pid-dir", pidDir,
				"--timeout", "1s",
			})
			result <- struct {
				code ExitCode
				err  error
			}{code: code, err: stopErr}
		}()

		deadline := time.Now().Add(time.Second)
		requested := false
		for time.Now().Before(deadline) {
			if _, statErr := os.Stat(stopFilePath(pidDir, "cancel-stop")); statErr == nil {
				requested = true
				break
			}
			time.Sleep(time.Millisecond)
		}
		if !requested {
			cancel()
			t.Fatalf("stop request was not created before cancellation")
		}
		cancel()
		got := <-result
		code, stopErr := got.code, got.err
		if code != ExitFailure || !errs.IsCode(stopErr, errs.CodeCanceled) {
			t.Fatalf("runStop canceled = (%d, %v)", code, stopErr)
		}
	})
}
