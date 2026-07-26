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

// capturedLog 是测试 Handler 保存的一条不可变日志快照。
type capturedLog struct {
	record Record
	fields []Field
}

// memoryHandler 提供可注入阻塞或错误的内存 Handler。
type memoryHandler struct {
	// enabled 控制级别过滤；三个函数用于定制当前测试阶段行为。
	enabled bool
	write   func(Record, []Field) error
	sync    func() error
	close   func() error

	// mu 保护日志协程写入、测试协程读取的 records。
	mu      sync.Mutex
	records []capturedLog
}

func (handler *memoryHandler) Enabled(Level) bool {
	// 测试级别策略固定为单个布尔值。
	return handler.enabled
}

func (handler *memoryHandler) Write(record Record, fields []Field) error {
	// 自定义函数先执行，可用于阻塞或制造写入错误。
	if handler.write != nil {
		if err := handler.write(record, fields); err != nil {
			return err
		}
	}
	// 成功路径复制字段并保存快照，避免调用后切片失效。
	handler.mu.Lock()
	handler.records = append(handler.records, capturedLog{
		record: record,
		fields: append([]Field(nil), fields...),
	})
	handler.mu.Unlock()
	return nil
}

func (handler *memoryHandler) Sync() error {
	// 未注入行为时默认同步成功。
	if handler.sync != nil {
		return handler.sync()
	}
	return nil
}

func (handler *memoryHandler) Close() error {
	// 未注入行为时默认关闭成功。
	if handler.close != nil {
		return handler.close()
	}
	return nil
}

func (handler *memoryHandler) snapshot() []capturedLog {
	// 锁内复制切片，使断言不与日志协程追加发生竞态。
	handler.mu.Lock()
	defer handler.mu.Unlock()
	return append([]capturedLog(nil), handler.records...)
}

func TestLoggerAsyncFlushAndFixedFields(t *testing.T) {
	t.Parallel()

	// 创建异步 Runtime，并注册清理以覆盖测试提前失败路径。
	handler := &memoryHandler{enabled: true}
	runtime, err := NewRuntime(DefaultConfig(), handler)
	if err != nil {
		t.Fatalf("NewRuntime() = %v", err)
	}
	t.Cleanup(func() {
		_ = runtime.Close(context.Background())
	})

	// 派生 Logger 预绑定 service，再写动态 player_id 和被保留的 caller。
	logger := runtime.Logger().With(String("service", "player"))
	logger.Info("started", Int64("player_id", 7), String("caller", "blocked"))
	// Flush 建立顺序屏障，返回后内存 Handler 已收到记录。
	if err := runtime.Flush(context.Background()); err != nil {
		t.Fatalf("Flush() = %v", err)
	}

	// 验证消息、字段顺序以及保留字段过滤。
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

	// 同步模式确保 ErrorStack 返回时记录已经可断言。
	handler := &memoryHandler{enabled: true}
	config := DefaultConfig()
	config.Mode = SyncMode
	runtime, err := NewRuntime(config, handler)
	if err != nil {
		t.Fatalf("NewRuntime() = %v", err)
	}
	defer runtime.Close(context.Background())

	// 先取得下一行基准，再从相邻行写日志。
	_, _, line, _ := runtime2Caller()
	runtime.Logger().ErrorStack("stack")
	records := handler.snapshot()
	// 验证调用行、缩短文件路径和完整堆栈函数名。
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
	// 独立辅助层让测试稳定取得调用点上一层行号。
	return runtime.Caller(1)
}

func TestWithCallerSkip(t *testing.T) {
	t.Parallel()

	// 同步 Runtime 通过一个业务包装函数写日志。
	handler := &memoryHandler{enabled: true}
	config := DefaultConfig()
	config.Mode = SyncMode
	logRuntime, err := NewRuntime(config, handler)
	if err != nil {
		t.Fatalf("NewRuntime() = %v", err)
	}
	defer logRuntime.Close(context.Background())

	// 记录调用 helper 的行，并验证 WithCallerSkip 跳过 helper 自身。
	_, _, line, _ := runtime.Caller(0)
	writeThroughHelper(logRuntime.Logger())
	record := handler.snapshot()[0].record
	if record.Caller.Line != line+1 {
		t.Fatalf("caller line = %d, want helper caller %d", record.Caller.Line, line+1)
	}
}

func writeThroughHelper(logger Logger) {
	// 模拟业务封装日志方法时固定增加一层 CallerSkip。
	logger.WithCallerSkip(1).Info("helper")
}

func TestDisabledLoggerCreatesNoRecord(t *testing.T) {
	t.Parallel()

	// Handler 禁用所有级别，Runtime 应在构造 Record 前过滤。
	handler := &memoryHandler{enabled: false}
	runtime, err := NewRuntime(DefaultConfig(), handler)
	if err != nil {
		t.Fatalf("NewRuntime() = %v", err)
	}
	defer runtime.Close(context.Background())

	// 写入后直接检查内存快照仍为空。
	runtime.Logger().Info("ignored")
	if got := len(handler.snapshot()); got != 0 {
		t.Fatalf("record count = %d, want 0", got)
	}
}

func TestQueueFullDropsNewLog(t *testing.T) {
	// Handler 第一条 Write 阻塞，使日志协程停止消费后续队列。
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

	// 等待日志协程进入阻塞点，再精确填满全部队列槽位。
	logger.Info("blocking")
	<-entered
	for range eventQueueSize {
		logger.Info("queued")
	}
	// 队列满后的新 Warn 必须立即丢弃并分级计数。
	logger.Warn("dropped")
	if got := runtime.Stats().DroppedWarn; got != 1 {
		t.Fatalf("DroppedWarn = %d, want 1", got)
	}

	// 解除 Handler 阻塞并完整关闭，确保大量排队日志被排空。
	close(blocked)
	if err := runtime.Close(context.Background()); err != nil {
		t.Fatalf("Close() = %v", err)
	}
}

func TestWriteFailureAndLifecycleErrors(t *testing.T) {
	t.Parallel()

	// 同一 cause 注入 Write 和 Sync，便于检查错误链保留。
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

	// 异步写失败由 Flush 屏障带出，并增加 WriteFailures。
	runtime.Logger().Info("failed")
	err = runtime.Flush(context.Background())
	if !errs.IsCode(err, errs.CodeLogOutputFailed) || !errors.Is(err, cause) {
		t.Fatalf("Flush() = %v, want wrapped output failure", err)
	}
	if runtime.Stats().WriteFailures != 1 {
		t.Fatalf("WriteFailures = %d, want 1", runtime.Stats().WriteFailures)
	}
	// Close 仍报告输出错误；关闭后的 Flush 报稳定关闭错误。
	if err := runtime.Close(context.Background()); !errs.IsCode(err, errs.CodeLogOutputFailed) {
		t.Fatalf("Close() = %v, want CodeLogOutputFailed", err)
	}
	if err := runtime.Flush(context.Background()); !errs.IsCode(err, errs.CodeLogClosed) {
		t.Fatalf("Flush after Close = %v, want CodeLogClosed", err)
	}
}

func TestCloseTimeoutContinuesInBackground(t *testing.T) {
	t.Parallel()

	// Handler Write 阻塞，用于让关闭排空超过调用方截止时间。
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

	// 第一次 Close 只停止等待并返回 DeadlineExceeded。
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := runtime.Close(ctx); !errs.IsCode(err, errs.CodeDeadlineExceeded) {
		t.Fatalf("Close(timeout) = %v, want deadline", err)
	}
	// 解除阻塞后后台关闭继续完成，第二次 Close 取得最终结果。
	close(release)
	if err := runtime.Close(context.Background()); err != nil {
		t.Fatalf("second Close() = %v", err)
	}
}

func TestFlushTimeoutContinuesInBackground(t *testing.T) {
	t.Parallel()

	// Handler Sync 阻塞，用于验证 Flush Context 只取消调用方等待。
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

	// Flush 超时后释放 Sync，随后 Close 仍应成功完成。
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
	// 阻塞 Handler Write，触发 ErrorStack 的一秒可靠写入预算。
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

	// 记录调用耗时并检查在合理调度误差内返回。
	start := time.Now()
	runtime.Logger().ErrorStack("blocked stack")
	elapsed := time.Since(start)
	if elapsed < reliableWriteTimeout || elapsed > 3*reliableWriteTimeout {
		t.Fatalf("ErrorStack elapsed = %v, want about %v", elapsed, reliableWriteTimeout)
	}
	if got := runtime.Stats().ReliableWriteTimeouts; got != 1 {
		t.Fatalf("ReliableWriteTimeouts = %d, want 1", got)
	}

	// 解除日志协程阻塞并关闭，避免测试泄漏 goroutine。
	close(release)
	if err := runtime.Close(context.Background()); err != nil {
		t.Fatalf("Close() = %v", err)
	}
}

func TestConcurrentCloseIsIdempotent(t *testing.T) {
	t.Parallel()

	// 原子计数记录底层 Handler.Close 的真实调用次数。
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

	// 16 个调用方并发关闭同一 Runtime，所有调用都应成功。
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
	// 底层资源只能释放一次。
	if closes.Load() != 1 {
		t.Fatalf("handler Close calls = %d, want 1", closes.Load())
	}
}

func TestCloseDoesNotRaceHandlerEnabled(t *testing.T) {
	t.Parallel()

	// lifecycleHandler 会记录 Close 之后发生的 Enabled 调用。
	handler := &lifecycleHandler{}
	runtime, err := NewRuntime(DefaultConfig(), handler)
	if err != nil {
		t.Fatalf("NewRuntime() = %v", err)
	}
	logger := runtime.Logger()

	// 多个 goroutine 持续查询 Enabled，与 Close 并发竞争。
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
	// Close 返回后停止查询并等待全部 worker。
	if err := runtime.Close(context.Background()); err != nil {
		t.Fatalf("Close() = %v", err)
	}
	close(stop)
	wait.Wait()
	// 生命周期准入必须保证 Handler.Close 后再无 Enabled。
	if handler.enabledAfterClose.Load() != 0 {
		t.Fatalf("Handler.Enabled called after Handler.Close")
	}
}

func TestNewNop(t *testing.T) {
	t.Parallel()

	// Nop Logger 应始终禁用，且所有写入方法都可安全调用。
	logger := NewNop()
	if logger.Enabled(InfoLevel) {
		t.Fatalf("Nop logger is enabled")
	}
	logger.Info("ignored")
	logger.ErrorStack("ignored")
}

func TestNewRuntimeValidation(t *testing.T) {
	t.Parallel()

	// 零配置和 nil Handler 都属于构造阶段配置错误。
	if _, err := NewRuntime(Config{}, &memoryHandler{enabled: true}); !errs.IsCode(err, errs.CodeInvalidConfig) {
		t.Fatalf("zero Config error = %v, want CodeInvalidConfig", err)
	}
	if _, err := NewRuntime(DefaultConfig(), nil); !errs.IsCode(err, errs.CodeInvalidConfig) {
		t.Fatalf("nil Handler error = %v, want CodeInvalidConfig", err)
	}

	// 自定义 Handler 只要求合法 Runtime 模式，不要求内置输出配置。
	customConfig := Config{Mode: AsyncMode}
	runtime, err := NewRuntime(customConfig, &memoryHandler{enabled: true})
	if err != nil {
		t.Fatalf("custom Handler with no built-in outputs = %v", err)
	}
	if err := runtime.Close(context.Background()); err != nil {
		t.Fatalf("Close() = %v", err)
	}
}

// lifecycleHandler 检测 Runtime 是否在底层 Close 后继续调用 Enabled。
type lifecycleHandler struct {
	closed            atomic.Bool
	enabledAfterClose atomic.Uint64
}

func (handler *lifecycleHandler) Enabled(Level) bool {
	// 关闭标记之后的每次调用都计数，便于竞态测试断言。
	if handler.closed.Load() {
		handler.enabledAfterClose.Add(1)
	}
	return true
}

// Write 在本测试中不产生副作用。
func (*lifecycleHandler) Write(Record, []Field) error { return nil }

// Sync 在本测试中不产生副作用。
func (*lifecycleHandler) Sync() error { return nil }

// Close 发布关闭标记，后续 Enabled 将被记录。
func (handler *lifecycleHandler) Close() error {
	handler.closed.Store(true)
	return nil
}
