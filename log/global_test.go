package log

import (
	"context"
	"errors"
	"testing"

	"github.com/duanhf2012/origin/v3/errs"
)

// controllableMemoryHandler 为包级控制测试提供不依赖默认 Zap 实现的最小 Controller。
type controllableMemoryHandler struct {
	memoryHandler
	status Status
}

func (handler *controllableMemoryHandler) SetConsoleLevel(level Level) error {
	handler.status.Console.Level = level
	return nil
}
func (handler *controllableMemoryHandler) ResetConsoleLevel() error {
	handler.status.Console.Level = handler.status.Console.ConfigLevel
	return nil
}
func (handler *controllableMemoryHandler) SetFileLevel(level Level) error {
	handler.status.File.Level = level
	return nil
}
func (handler *controllableMemoryHandler) ResetFileLevel() error {
	handler.status.File.Level = handler.status.File.ConfigLevel
	return nil
}
func (handler *controllableMemoryHandler) SetConsoleEnabled(enabled bool) error {
	handler.status.Console.Enabled = enabled
	return nil
}
func (handler *controllableMemoryHandler) SetFileEnabled(enabled bool) error {
	handler.status.File.Enabled = enabled
	return nil
}
func (handler *controllableMemoryHandler) Status() Status { return handler.status }

// TestGlobalLoggerForwardsFieldsAndCaller 防止包级便捷函数丢字段或把 caller 定位到 global.go。
func TestGlobalLoggerForwardsFieldsAndCaller(t *testing.T) {
	SetDefault(NewNop())
	handler := &memoryHandler{enabled: true}
	config := DefaultConfig()
	config.Mode = SyncMode
	runtime, err := NewRuntime(config, handler)
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}
	t.Cleanup(func() {
		SetDefault(NewNop())
		_ = runtime.Close(context.Background())
	})

	SetDefault(runtime.Logger())
	_, _, line, _ := runtime2Caller()
	Info("global message", Int("player_id", 7))
	records := handler.snapshot()
	if len(records) != 1 {
		t.Fatalf("record count = %d, want 1", len(records))
	}
	if records[0].record.Caller.File != "log/global_test.go" ||
		records[0].record.Caller.Line != line+1 {
		t.Fatalf("global caller = %+v, want global_test.go:%d", records[0].record.Caller, line+1)
	}
	if len(records[0].fields) != 1 || records[0].fields[0].Key() != "player_id" {
		t.Fatalf("global fields = %+v", records[0].fields)
	}
}

// TestGlobalLoggerMethodsUseOneDefaultRuntime 覆盖全部包级便捷方法、Enabled 和堆栈路径，
// 防止新增包装时某个级别绕过默认 Runtime 或丢失可靠 ErrorStack 语义。
func TestGlobalLoggerMethodsUseOneDefaultRuntime(t *testing.T) {
	SetDefault(NewNop())
	handler := &memoryHandler{enabled: true}
	config := DefaultConfig()
	config.Mode = SyncMode
	runtime, err := NewRuntime(config, handler)
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}
	t.Cleanup(func() {
		SetDefault(NewNop())
		_ = runtime.Close(context.Background())
	})
	SetDefault(runtime.Logger())

	if !Enabled(DebugLevel) || !Default().Enabled(ErrorLevel) {
		t.Fatal("default logger did not expose enabled levels")
	}
	Debug("debug")
	Info("info")
	Warn("warn")
	Error("error")
	ErrorStack("error stack")

	records := handler.snapshot()
	if len(records) != 5 {
		t.Fatalf("record count = %d, want 5", len(records))
	}
	wantLevels := []Level{DebugLevel, InfoLevel, WarnLevel, ErrorLevel, ErrorLevel}
	for index, want := range wantLevels {
		if records[index].record.Level != want {
			t.Fatalf("record[%d] level = %v, want %v", index, records[index].record.Level, want)
		}
	}
	if records[4].record.Stack == "" {
		t.Fatal("global ErrorStack did not capture a stack")
	}
}

// TestClosingOldRuntimeDoesNotClearNewDefault 防止并行 Application 的旧所有者误清新默认 Logger。
func TestClosingOldRuntimeDoesNotClearNewDefault(t *testing.T) {
	SetDefault(NewNop())
	firstHandler := &memoryHandler{enabled: true}
	secondHandler := &memoryHandler{enabled: true}
	config := DefaultConfig()
	config.Mode = SyncMode
	first, err := NewRuntime(config, firstHandler)
	if err != nil {
		t.Fatalf("first NewRuntime() error = %v", err)
	}
	second, err := NewRuntime(config, secondHandler)
	if err != nil {
		_ = first.Close(context.Background())
		t.Fatalf("second NewRuntime() error = %v", err)
	}
	t.Cleanup(func() {
		SetDefault(NewNop())
		_ = first.Close(context.Background())
		_ = second.Close(context.Background())
	})

	SetDefault(first.Logger())
	SetDefault(second.Logger())
	if err := first.Close(context.Background()); err != nil {
		t.Fatalf("first Close() error = %v", err)
	}
	Info("belongs to second")
	if got := len(firstHandler.snapshot()); got != 0 {
		t.Fatalf("first handler records = %d, want 0", got)
	}
	if got := len(secondHandler.snapshot()); got != 1 {
		t.Fatalf("second handler records = %d, want 1", got)
	}

	if err := second.Close(context.Background()); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
	if Default().Enabled(InfoLevel) {
		t.Fatal("default logger remains enabled after owner closes")
	}
}

// TestGlobalOutputControlUsesCurrentDefaultRuntime 防止包级控制误用第二套全局配置，或 Reset
// 恢复到硬编码级别而不是 Handler 保存的启动配置。
func TestGlobalOutputControlUsesCurrentDefaultRuntime(t *testing.T) {
	SetDefault(NewNop())
	handler := &controllableMemoryHandler{
		memoryHandler: memoryHandler{enabled: true},
		status: Status{
			Console: OutputStatus{
				Available:   true,
				Enabled:     true,
				Level:       InfoLevel,
				ConfigLevel: InfoLevel,
			},
			File: OutputStatus{
				Available:   true,
				Enabled:     true,
				Level:       DebugLevel,
				ConfigLevel: DebugLevel,
			},
		},
	}
	runtime, err := NewRuntime(DefaultConfig(), handler)
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}
	t.Cleanup(func() {
		SetDefault(NewNop())
		_ = runtime.Close(context.Background())
	})
	SetDefault(runtime.Logger())

	if err := SetConsoleLevel(DebugLevel); err != nil {
		t.Fatalf("SetConsoleLevel() error = %v", err)
	}
	if err := SetFileLevel(ErrorLevel); err != nil {
		t.Fatalf("SetFileLevel() error = %v", err)
	}
	if err := SetConsoleEnabled(false); err != nil {
		t.Fatalf("SetConsoleEnabled() error = %v", err)
	}
	if err := SetFileEnabled(false); err != nil {
		t.Fatalf("SetFileEnabled() error = %v", err)
	}
	if err := ResetConsoleLevel(); err != nil {
		t.Fatalf("ResetConsoleLevel() error = %v", err)
	}
	if err := ResetFileLevel(); err != nil {
		t.Fatalf("ResetFileLevel() error = %v", err)
	}
	status, err := CurrentStatus()
	if err != nil {
		t.Fatalf("CurrentStatus() error = %v", err)
	}
	if status.Console.Level != InfoLevel || status.File.Level != DebugLevel ||
		status.Console.Enabled || status.File.Enabled {
		t.Fatalf("CurrentStatus() = %+v", status)
	}

	if err := runtime.Close(context.Background()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if _, err := CurrentStatus(); !errors.Is(err, errs.ErrLogClosed) {
		t.Fatalf("CurrentStatus() after close error = %v", err)
	}
}

// TestGlobalOutputControlReportsUnsupportedHandler 防止自定义固定 Handler 被强制实现 Controller，
// 同时保证业务得到可判断的稳定错误。
func TestGlobalOutputControlReportsUnsupportedHandler(t *testing.T) {
	SetDefault(NewNop())
	handler := &memoryHandler{enabled: true}
	runtime, err := NewRuntime(DefaultConfig(), handler)
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}
	t.Cleanup(func() {
		SetDefault(NewNop())
		_ = runtime.Close(context.Background())
	})
	SetDefault(runtime.Logger())
	if err := SetConsoleLevel(DebugLevel); !errors.Is(err, errs.ErrLogControlUnsupported) {
		t.Fatalf("SetConsoleLevel() error = %v", err)
	}
	if _, err := runtime.OutputStatus(); !errors.Is(err, errs.ErrLogControlUnsupported) {
		t.Fatalf("OutputStatus() error = %v", err)
	}
}
