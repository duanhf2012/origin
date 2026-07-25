package log

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/duanhf2012/origin/v3/errs"
)

type eventKind uint8

const (
	writeEvent eventKind = iota
	flushEvent
	closeEvent
)

type logEvent struct {
	kind   eventKind
	record Record
	fields []Field
	done   chan error
}

// Stats 是日志 Runtime 的不可变计数快照。
type Stats struct {
	DroppedDebug          uint64
	DroppedInfo           uint64
	DroppedWarn           uint64
	DroppedError          uint64
	WriteFailures         uint64
	ReliableWriteTimeouts uint64
}

type counters struct {
	droppedDebug          atomic.Uint64
	droppedInfo           atomic.Uint64
	droppedWarn           atomic.Uint64
	droppedError          atomic.Uint64
	writeFailures         atomic.Uint64
	reliableWriteTimeouts atomic.Uint64
}

// Runtime 拥有日志队列、日志协程和 Handler 生命周期。
type Runtime struct {
	config  Config
	handler Handler
	queue   chan *logEvent
	slots   chan struct{}

	state atomic.Uint32
	stats counters

	submitMu    sync.Mutex
	submitCond  *sync.Cond
	accepting   bool
	submitCount int
	stopSubmit  chan struct{}

	closeOnce sync.Once
	closed    chan struct{}
	closeMu   sync.Mutex
	closeErr  error
}

const (
	runtimeRunning uint32 = iota
	runtimeClosing
	runtimeClosed
)

// NewRuntime 使用指定 Handler 创建独立日志 Runtime。
func NewRuntime(config Config, handler Handler) (*Runtime, error) {
	if err := config.validateRuntime(); err != nil {
		return nil, err
	}
	if handler == nil {
		return nil, invalidConfig("log handler is nil")
	}

	instance := &Runtime{
		config:     config,
		handler:    handler,
		queue:      make(chan *logEvent, eventQueueSize),
		slots:      make(chan struct{}, eventQueueSize),
		accepting:  true,
		stopSubmit: make(chan struct{}),
		closed:     make(chan struct{}),
	}
	instance.submitCond = sync.NewCond(&instance.submitMu)
	go instance.run()
	return instance, nil
}

// Logger 返回共享该 Runtime 的根 Logger。
func (runtime *Runtime) Logger() Logger {
	if runtime == nil {
		return NewNop()
	}
	return Logger{runtime: runtime}
}

// Stats 返回当前计数快照。
func (runtime *Runtime) Stats() Stats {
	if runtime == nil {
		return Stats{}
	}
	return Stats{
		DroppedDebug:          runtime.stats.droppedDebug.Load(),
		DroppedInfo:           runtime.stats.droppedInfo.Load(),
		DroppedWarn:           runtime.stats.droppedWarn.Load(),
		DroppedError:          runtime.stats.droppedError.Load(),
		WriteFailures:         runtime.stats.writeFailures.Load(),
		ReliableWriteTimeouts: runtime.stats.reliableWriteTimeouts.Load(),
	}
}

// Flush 等待此前已进入队列的日志完成并刷新 Handler。
func (runtime *Runtime) Flush(ctx context.Context) error {
	if runtime == nil {
		return errs.ErrLogClosed
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if !runtime.beginSubmit() {
		return errs.ErrLogClosed
	}
	defer runtime.endSubmit()

	if err := runtime.reserve(ctx); err != nil {
		return err
	}
	event := &logEvent{kind: flushEvent, done: make(chan error, 1)}
	runtime.queue <- event

	select {
	case err := <-event.done:
		return err
	case <-ctx.Done():
		return contextError(ctx.Err())
	}
}

// Close 停止准入、排空队列并关闭 Handler。重复调用安全。
func (runtime *Runtime) Close(ctx context.Context) error {
	if runtime == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	runtime.closeOnce.Do(runtime.startClose)
	select {
	case <-runtime.closed:
		runtime.closeMu.Lock()
		err := runtime.closeErr
		runtime.closeMu.Unlock()
		return err
	case <-ctx.Done():
		return contextError(ctx.Err())
	}
}

func (runtime *Runtime) enabled(level Level) bool {
	if !level.valid() || runtime.state.Load() != runtimeRunning || !runtime.beginSubmit() {
		return false
	}
	defer runtime.endSubmit()
	return runtime.handler.Enabled(level)
}

func (runtime *Runtime) write(logger Logger, level Level, message string, withStack bool, fields []Field) {
	if !level.valid() || !runtime.beginSubmit() {
		return
	}
	defer runtime.endSubmit()
	if !runtime.handler.Enabled(level) {
		return
	}

	if withStack {
		runtime.writeReliable(logger, level, message, fields)
		return
	}

	if runtime.config.Mode == AsyncMode {
		select {
		case runtime.slots <- struct{}{}:
		default:
			runtime.recordDrop(level)
			return
		}
	} else {
		runtime.slots <- struct{}{}
	}

	event := runtime.makeWriteEvent(
		logger,
		newRecord(level, message, logger.callerSkip, false),
		fields,
		runtime.config.Mode == SyncMode,
	)
	runtime.queue <- event
	if event.done != nil {
		<-event.done
	}
}

func (runtime *Runtime) writeReliable(logger Logger, level Level, message string, fields []Field) {
	record := newRecord(level, message, logger.callerSkip+1, true)
	timer := time.NewTimer(reliableWriteTimeout)
	defer timer.Stop()

	select {
	case runtime.slots <- struct{}{}:
	case <-timer.C:
		runtime.recordReliableTimeout(record)
		return
	}

	event := runtime.makeWriteEvent(logger, record, fields, true)
	runtime.queue <- event

	select {
	case <-event.done:
	case <-timer.C:
		runtime.recordReliableTimeout(record)
	}
}

func (runtime *Runtime) makeWriteEvent(
	logger Logger,
	record Record,
	fields []Field,
	wait bool,
) *logEvent {
	ownedFields := make([]Field, 0, len(logger.fields)+len(fields))
	ownedFields = append(ownedFields, logger.fields...)
	ownedFields = appendValidFields(ownedFields, fields)

	event := &logEvent{
		kind:   writeEvent,
		record: record,
		fields: ownedFields,
	}
	if wait {
		event.done = make(chan error, 1)
	}
	return event
}

func (runtime *Runtime) run() {
	for event := range runtime.queue {
		<-runtime.slots

		switch event.kind {
		case writeEvent:
			err := runtime.handler.Write(event.record, event.fields)
			if err != nil {
				runtime.stats.writeFailures.Add(1)
				fallback(event.record, err)
			}
			notify(event.done, err)
		case flushEvent:
			notify(event.done, outputError(runtime.handler.Sync()))
		case closeEvent:
			err := errors.Join(runtime.handler.Sync(), runtime.handler.Close())
			notify(event.done, outputError(err))
			return
		}
	}
}

func (runtime *Runtime) beginSubmit() bool {
	runtime.submitMu.Lock()
	defer runtime.submitMu.Unlock()

	if !runtime.accepting {
		return false
	}
	runtime.submitCount++
	return true
}

func (runtime *Runtime) endSubmit() {
	runtime.submitMu.Lock()
	runtime.submitCount--
	if runtime.submitCount == 0 {
		runtime.submitCond.Broadcast()
	}
	runtime.submitMu.Unlock()
}

func (runtime *Runtime) startClose() {
	runtime.submitMu.Lock()
	runtime.state.Store(runtimeClosing)
	runtime.accepting = false
	close(runtime.stopSubmit)
	runtime.submitMu.Unlock()

	go runtime.finishClose()
}

func (runtime *Runtime) finishClose() {
	runtime.submitMu.Lock()
	for runtime.submitCount != 0 {
		runtime.submitCond.Wait()
	}
	runtime.submitMu.Unlock()

	runtime.slots <- struct{}{}
	event := &logEvent{kind: closeEvent, done: make(chan error, 1)}
	runtime.queue <- event
	err := <-event.done

	runtime.closeMu.Lock()
	runtime.closeErr = err
	runtime.closeMu.Unlock()
	runtime.state.Store(runtimeClosed)
	close(runtime.closed)
}

func (runtime *Runtime) reserve(ctx context.Context) error {
	select {
	case runtime.slots <- struct{}{}:
		return nil
	case <-runtime.stopSubmit:
		return errs.ErrLogClosed
	case <-ctx.Done():
		return contextError(ctx.Err())
	}
}

func (runtime *Runtime) recordDrop(level Level) {
	switch level {
	case DebugLevel:
		runtime.stats.droppedDebug.Add(1)
	case InfoLevel:
		runtime.stats.droppedInfo.Add(1)
	case WarnLevel:
		runtime.stats.droppedWarn.Add(1)
	case ErrorLevel:
		runtime.stats.droppedError.Add(1)
	}
}

func (runtime *Runtime) recordReliableTimeout(record Record) {
	runtime.stats.reliableWriteTimeouts.Add(1)
	fallback(record, context.DeadlineExceeded)
}

func notify(target chan error, err error) {
	if target != nil {
		target <- err
	}
}

func outputError(err error) error {
	if err == nil {
		return nil
	}
	return errs.Wrap(errs.CodeLogOutputFailed, err)
}

func contextError(err error) error {
	switch err {
	case context.Canceled:
		return errs.Wrap(errs.CodeCanceled, err)
	case context.DeadlineExceeded:
		return errs.Wrap(errs.CodeDeadlineExceeded, err)
	default:
		return err
	}
}

func fallback(record Record, err error) {
	_, _ = fmt.Fprintf(
		os.Stderr,
		"origin log fallback: level=%s caller=%s:%d message=%q error=%q\n",
		record.Level,
		record.Caller.File,
		record.Caller.Line,
		record.Message,
		err.Error(),
	)
}
