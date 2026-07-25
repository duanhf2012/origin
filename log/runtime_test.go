package log

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/duanhf2012/origin/v3/errs"
)

type capturedLog struct {
	record Record
	fields []Field
}

type memoryHandler struct {
	enabled bool
	write   func(Record, []Field) error
	sync    func() error
	close   func() error

	mu      sync.Mutex
	records []capturedLog
}

func (handler *memoryHandler) Enabled(Level) bool {
	return handler.enabled
}

func (handler *memoryHandler) Write(record Record, fields []Field) error {
	if handler.write != nil {
		if err := handler.write(record, fields); err != nil {
			return err
		}
	}
	handler.mu.Lock()
	handler.records = append(handler.records, capturedLog{
		record: record,
		fields: append([]Field(nil), fields...),
	})
	handler.mu.Unlock()
	return nil
}

func (handler *memoryHandler) Sync() error {
	if handler.sync != nil {
		return handler.sync()
	}
	return nil
}

func (handler *memoryHandler) Close() error {
	if handler.close != nil {
		return handler.close()
	}
	return nil
}

func (handler *memoryHandler) snapshot() []capturedLog {
	handler.mu.Lock()
	defer handler.mu.Unlock()
	return append([]capturedLog(nil), handler.records...)
}

func TestLoggerAsyncFlushAndFixedFields(t *testing.T) {
	t.Parallel()

	handler := &memoryHandler{enabled: true}
	runtime, err := NewRuntime(DefaultConfig(), handler)
	if err != nil {
		t.Fatalf("NewRuntime() = %v", err)
	}
	t.Cleanup(func() {
		_ = runtime.Close(context.Background())
	})

	logger := runtime.Logger().With(String("service", "player"))
	logger.Info("started", Int64("player_id", 7), String("caller", "blocked"))
	if err := runtime.Flush(context.Background()); err != nil {
		t.Fatalf("Flush() = %v", err)
	}

	records := handler.snapshot()
	if len(records) != 1 {
		t.Fatalf("record count = %d, want 1", len(records))
	}
	if records[0].record.Message != "started" {
		t.Fatalf("message = %q", records[0].record.Message)
	}
	if len(records[0].fields) != 2 {
		t.Fatalf("field count = %d, want 2", len(records[0].fields))
	}
	if records[0].fields[0].Key() != "service" ||
		records[0].fields[1].Key() != "player_id" {
		t.Fatalf("unexpected fields: %+v", records[0].fields)
	}
}

func TestLoggerCallerAndStack(t *testing.T) {
	t.Parallel()

	handler := &memoryHandler{enabled: true}
	config := DefaultConfig()
	config.Mode = SyncMode
	runtime, err := NewRuntime(config, handler)
	if err != nil {
		t.Fatalf("NewRuntime() = %v", err)
	}
	defer runtime.Close(context.Background())

	_, _, line, _ := runtime2Caller()
	runtime.Logger().ErrorStack("stack")
	records := handler.snapshot()
	if len(records) != 1 {
		t.Fatalf("record count = %d", len(records))
	}
	record := records[0].record
	if record.Caller.Line != line+1 {
		t.Fatalf("caller line = %d, want %d (%s)", record.Caller.Line, line+1, record.Caller.File)
	}
	if !strings.HasSuffix(record.Caller.File, "log/runtime_test.go") {
		t.Fatalf("caller file = %q", record.Caller.File)
	}
	if !strings.Contains(record.Stack, "TestLoggerCallerAndStack") {
		t.Fatalf("stack does not contain test caller: %q", record.Stack)
	}
}

func runtime2Caller() (uintptr, string, int, bool) {
	return runtime.Caller(1)
}

func TestWithCallerSkip(t *testing.T) {
	t.Parallel()

	handler := &memoryHandler{enabled: true}
	config := DefaultConfig()
	config.Mode = SyncMode
	logRuntime, err := NewRuntime(config, handler)
	if err != nil {
		t.Fatalf("NewRuntime() = %v", err)
	}
	defer logRuntime.Close(context.Background())

	_, _, line, _ := runtime.Caller(0)
	writeThroughHelper(logRuntime.Logger())
	record := handler.snapshot()[0].record
	if record.Caller.Line != line+1 {
		t.Fatalf("caller line = %d, want helper caller %d", record.Caller.Line, line+1)
	}
}

func writeThroughHelper(logger Logger) {
	logger.WithCallerSkip(1).Info("helper")
}

func TestDisabledLoggerCreatesNoRecord(t *testing.T) {
	t.Parallel()

	handler := &memoryHandler{enabled: false}
	runtime, err := NewRuntime(DefaultConfig(), handler)
	if err != nil {
		t.Fatalf("NewRuntime() = %v", err)
	}
	defer runtime.Close(context.Background())

	runtime.Logger().Info("ignored")
	if got := len(handler.snapshot()); got != 0 {
		t.Fatalf("record count = %d, want 0", got)
	}
}

func TestQueueFullDropsNewLog(t *testing.T) {
	blocked := make(chan struct{})
	entered := make(chan struct{})
	var once sync.Once
	handler := &memoryHandler{
		enabled: true,
		write: func(Record, []Field) error {
			once.Do(func() { close(entered) })
			<-blocked
			return nil
		},
	}
	runtime, err := NewRuntime(DefaultConfig(), handler)
	if err != nil {
		t.Fatalf("NewRuntime() = %v", err)
	}
	logger := runtime.Logger()

	logger.Info("blocking")
	<-entered
	for range eventQueueSize {
		logger.Info("queued")
	}
	logger.Warn("dropped")
	if got := runtime.Stats().DroppedWarn; got != 1 {
		t.Fatalf("DroppedWarn = %d, want 1", got)
	}

	close(blocked)
	if err := runtime.Close(context.Background()); err != nil {
		t.Fatalf("Close() = %v", err)
	}
}

func TestWriteFailureAndLifecycleErrors(t *testing.T) {
	t.Parallel()

	cause := errors.New("disk failed")
	handler := &memoryHandler{
		enabled: true,
		write: func(Record, []Field) error {
			return cause
		},
		sync: func() error {
			return cause
		},
	}
	runtime, err := NewRuntime(DefaultConfig(), handler)
	if err != nil {
		t.Fatalf("NewRuntime() = %v", err)
	}

	runtime.Logger().Info("failed")
	err = runtime.Flush(context.Background())
	if !errs.IsCode(err, errs.CodeLogOutputFailed) || !errors.Is(err, cause) {
		t.Fatalf("Flush() = %v, want wrapped output failure", err)
	}
	if runtime.Stats().WriteFailures != 1 {
		t.Fatalf("WriteFailures = %d, want 1", runtime.Stats().WriteFailures)
	}
	if err := runtime.Close(context.Background()); !errs.IsCode(err, errs.CodeLogOutputFailed) {
		t.Fatalf("Close() = %v, want CodeLogOutputFailed", err)
	}
	if err := runtime.Flush(context.Background()); !errs.IsCode(err, errs.CodeLogClosed) {
		t.Fatalf("Flush after Close = %v, want CodeLogClosed", err)
	}
}

func TestCloseTimeoutContinuesInBackground(t *testing.T) {
	t.Parallel()

	release := make(chan struct{})
	handler := &memoryHandler{
		enabled: true,
		write: func(Record, []Field) error {
			<-release
			return nil
		},
	}
	runtime, err := NewRuntime(DefaultConfig(), handler)
	if err != nil {
		t.Fatalf("NewRuntime() = %v", err)
	}
	runtime.Logger().Info("blocked")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := runtime.Close(ctx); !errs.IsCode(err, errs.CodeDeadlineExceeded) {
		t.Fatalf("Close(timeout) = %v, want deadline", err)
	}
	close(release)
	if err := runtime.Close(context.Background()); err != nil {
		t.Fatalf("second Close() = %v", err)
	}
}

func TestFlushTimeoutContinuesInBackground(t *testing.T) {
	t.Parallel()

	release := make(chan struct{})
	handler := &memoryHandler{
		enabled: true,
		sync: func() error {
			<-release
			return nil
		},
	}
	runtime, err := NewRuntime(DefaultConfig(), handler)
	if err != nil {
		t.Fatalf("NewRuntime() = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := runtime.Flush(ctx); !errs.IsCode(err, errs.CodeDeadlineExceeded) {
		t.Fatalf("Flush(timeout) = %v, want deadline", err)
	}
	close(release)
	if err := runtime.Close(context.Background()); err != nil {
		t.Fatalf("Close() = %v", err)
	}
}

func TestErrorStackReliableWriteTimeout(t *testing.T) {
	release := make(chan struct{})
	handler := &memoryHandler{
		enabled: true,
		write: func(Record, []Field) error {
			<-release
			return nil
		},
	}
	runtime, err := NewRuntime(DefaultConfig(), handler)
	if err != nil {
		t.Fatalf("NewRuntime() = %v", err)
	}

	start := time.Now()
	runtime.Logger().ErrorStack("blocked stack")
	elapsed := time.Since(start)
	if elapsed < reliableWriteTimeout || elapsed > 3*reliableWriteTimeout {
		t.Fatalf("ErrorStack elapsed = %v, want about %v", elapsed, reliableWriteTimeout)
	}
	if got := runtime.Stats().ReliableWriteTimeouts; got != 1 {
		t.Fatalf("ReliableWriteTimeouts = %d, want 1", got)
	}

	close(release)
	if err := runtime.Close(context.Background()); err != nil {
		t.Fatalf("Close() = %v", err)
	}
}

func TestConcurrentCloseIsIdempotent(t *testing.T) {
	t.Parallel()

	var closes atomic.Uint64
	handler := &memoryHandler{
		enabled: true,
		close: func() error {
			closes.Add(1)
			return nil
		},
	}
	runtime, err := NewRuntime(DefaultConfig(), handler)
	if err != nil {
		t.Fatalf("NewRuntime() = %v", err)
	}

	var wait sync.WaitGroup
	for range 16 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			if err := runtime.Close(context.Background()); err != nil {
				t.Errorf("Close() = %v", err)
			}
		}()
	}
	wait.Wait()
	if closes.Load() != 1 {
		t.Fatalf("handler Close calls = %d, want 1", closes.Load())
	}
}

func TestCloseDoesNotRaceHandlerEnabled(t *testing.T) {
	t.Parallel()

	handler := &lifecycleHandler{}
	runtime, err := NewRuntime(DefaultConfig(), handler)
	if err != nil {
		t.Fatalf("NewRuntime() = %v", err)
	}
	logger := runtime.Logger()

	stop := make(chan struct{})
	var wait sync.WaitGroup
	for range 8 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_ = logger.Enabled(InfoLevel)
				}
			}
		}()
	}
	if err := runtime.Close(context.Background()); err != nil {
		t.Fatalf("Close() = %v", err)
	}
	close(stop)
	wait.Wait()
	if handler.enabledAfterClose.Load() != 0 {
		t.Fatalf("Handler.Enabled called after Handler.Close")
	}
}

func TestNewNop(t *testing.T) {
	t.Parallel()

	logger := NewNop()
	if logger.Enabled(InfoLevel) {
		t.Fatalf("Nop logger is enabled")
	}
	logger.Info("ignored")
	logger.ErrorStack("ignored")
}

func TestNewRuntimeValidation(t *testing.T) {
	t.Parallel()

	if _, err := NewRuntime(Config{}, &memoryHandler{enabled: true}); !errs.IsCode(err, errs.CodeInvalidConfig) {
		t.Fatalf("zero Config error = %v, want CodeInvalidConfig", err)
	}
	if _, err := NewRuntime(DefaultConfig(), nil); !errs.IsCode(err, errs.CodeInvalidConfig) {
		t.Fatalf("nil Handler error = %v, want CodeInvalidConfig", err)
	}

	customConfig := Config{Mode: AsyncMode}
	runtime, err := NewRuntime(customConfig, &memoryHandler{enabled: true})
	if err != nil {
		t.Fatalf("custom Handler with no built-in outputs = %v", err)
	}
	if err := runtime.Close(context.Background()); err != nil {
		t.Fatalf("Close() = %v", err)
	}
}

type lifecycleHandler struct {
	closed            atomic.Bool
	enabledAfterClose atomic.Uint64
}

func (handler *lifecycleHandler) Enabled(Level) bool {
	if handler.closed.Load() {
		handler.enabledAfterClose.Add(1)
	}
	return true
}

func (*lifecycleHandler) Write(Record, []Field) error { return nil }
func (*lifecycleHandler) Sync() error                 { return nil }
func (handler *lifecycleHandler) Close() error {
	handler.closed.Store(true)
	return nil
}
