package log

import (
	"context"
	"runtime"
	"sync"
	"testing"
)

// benchmarkHandler 消除真实输出成本，只保留 Runtime 调度成本。
type benchmarkHandler struct {
	enabled bool
}

// Enabled 返回基准固定的级别开关。
func (handler benchmarkHandler) Enabled(Level) bool { return handler.enabled }

// Write 模拟立即成功的同步输出。
func (handler benchmarkHandler) Write(Record, []Field) error { return nil }

// Sync 模拟无需刷新的输出。
func (handler benchmarkHandler) Sync() error { return nil }

// Close 模拟无需释放的输出。
func (handler benchmarkHandler) Close() error { return nil }

func BenchmarkDisabled(b *testing.B) {
	// 构造禁用全部级别的 Runtime，测量最外层过滤快路径。
	runtime, err := NewRuntime(DefaultConfig(), benchmarkHandler{})
	if err != nil {
		b.Fatal(err)
	}
	defer runtime.Close(context.Background())
	logger := runtime.Logger()

	// 包含一个字段构造，验证禁用路径的真实调用外观。
	b.ReportAllocs()
	for b.Loop() {
		logger.Debug("disabled", Int64("player_id", 1))
	}
}

func BenchmarkGlobalDisabled(b *testing.B) {
	// 安装一个禁用全部级别的 Runtime，单独衡量包级原子读取和 caller-skip 包装的过滤快路径。
	logRuntime, err := NewRuntime(DefaultConfig(), benchmarkHandler{})
	if err != nil {
		b.Fatal(err)
	}
	SetDefault(logRuntime.Logger())
	defer func() {
		SetDefault(NewNop())
		_ = logRuntime.Close(context.Background())
	}()

	// 字段构造与直接 Logger 基准保持相同，便于比较便捷外观的增量成本。
	b.ReportAllocs()
	for b.Loop() {
		Info("disabled", Int64("player_id", 1))
	}
}

func BenchmarkGlobalSyncFields(b *testing.B) {
	// 同步 Handler 排除队列堆积差异，对比包级 Info 与 BenchmarkSyncFields 的完整调用成本。
	config := DefaultConfig()
	config.Mode = SyncMode
	logRuntime, err := NewRuntime(config, benchmarkHandler{enabled: true})
	if err != nil {
		b.Fatal(err)
	}
	SetDefault(logRuntime.Logger())
	defer func() {
		SetDefault(NewNop())
		_ = logRuntime.Close(context.Background())
	}()

	b.ReportAllocs()
	for b.Loop() {
		Info("message", Int64("player_id", 7))
	}
}

func BenchmarkAsyncNoFields(b *testing.B) {
	// 构造开启输出的默认异步 Runtime。
	runtime, err := NewRuntime(DefaultConfig(), benchmarkHandler{enabled: true})
	if err != nil {
		b.Fatal(err)
	}
	defer runtime.Close(context.Background())
	logger := runtime.Logger()

	// 定期 Flush 防止基准生产速度填满队列并污染丢弃数据。
	b.ReportAllocs()
	b.ResetTimer()
	count := 0
	for b.Loop() {
		logger.Info("message")
		count++
		if count == 4096 {
			if err := runtime.Flush(context.Background()); err != nil {
				b.Fatal(err)
			}
			count = 0
		}
	}
	// 计时外排空尾部事件并确认基准没有发生丢弃。
	b.StopTimer()
	if err := runtime.Flush(context.Background()); err != nil {
		b.Fatal(err)
	}
	if dropped(runtime.Stats()) != 0 {
		b.Fatalf("benchmark dropped %d log events", dropped(runtime.Stats()))
	}
}

func BenchmarkAsyncFields(b *testing.B) {
	// 异步字段基准预绑定一个稳定字段，并每条增加两个动态字段。
	runtime, err := NewRuntime(DefaultConfig(), benchmarkHandler{enabled: true})
	if err != nil {
		b.Fatal(err)
	}
	defer runtime.Close(context.Background())
	logger := runtime.Logger().With(String("service", "player"))

	// 与无字段基准使用相同分批 Flush 规则以便比较。
	b.ReportAllocs()
	b.ResetTimer()
	count := 0
	for b.Loop() {
		logger.Info("message", Int64("player_id", 7), Bool("online", true))
		count++
		if count == 4096 {
			if err := runtime.Flush(context.Background()); err != nil {
				b.Fatal(err)
			}
			count = 0
		}
	}
	b.StopTimer()
	// 排空和丢弃检查不计入热路径。
	if err := runtime.Flush(context.Background()); err != nil {
		b.Fatal(err)
	}
	if dropped(runtime.Stats()) != 0 {
		b.Fatalf("benchmark dropped %d log events", dropped(runtime.Stats()))
	}
}

func BenchmarkSyncFields(b *testing.B) {
	// 把默认模式切换为同步，测量每条等待 done 通知的完整成本。
	config := DefaultConfig()
	config.Mode = SyncMode
	logRuntime, err := NewRuntime(config, benchmarkHandler{enabled: true})
	if err != nil {
		b.Fatal(err)
	}
	defer logRuntime.Close(context.Background())
	logger := logRuntime.Logger()

	// Handler 立即返回，因此结果主要反映队列往返和字段复制。
	b.ReportAllocs()
	for b.Loop() {
		logger.Info("message", Int64("player_id", 7))
	}
}

func BenchmarkCaller(b *testing.B) {
	// 单独测量 runtime.Caller 和路径缩短成本。
	var caller Caller
	b.ReportAllocs()
	for b.Loop() {
		caller = captureCaller(0)
	}
	runtime.KeepAlive(caller)
}

func BenchmarkErrorStack(b *testing.B) {
	// 同步模式避免异步积压，聚焦完整调用栈采集与事件处理成本。
	config := DefaultConfig()
	config.Mode = SyncMode
	logRuntime, err := NewRuntime(config, benchmarkHandler{enabled: true})
	if err != nil {
		b.Fatal(err)
	}
	defer logRuntime.Close(context.Background())
	logger := logRuntime.Logger()

	// 每轮都请求真实堆栈。
	b.ReportAllocs()
	for b.Loop() {
		logger.ErrorStack("message")
	}
}

func BenchmarkQueueFull(b *testing.B) {
	// 阻塞 Handler 并等待其进入 Write，冻结日志协程消费。
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
	logRuntime, err := NewRuntime(DefaultConfig(), handler)
	if err != nil {
		b.Fatal(err)
	}
	logger := logRuntime.Logger()
	logger.Info("blocking")
	<-entered
	// 填满全部剩余槽位，建立稳定的队列满状态。
	for range eventQueueSize {
		logger.Info("queued")
	}

	// 热循环只测量异步日志被立即丢弃的路径。
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		logger.Info("dropped")
	}
	b.StopTimer()
	// 计时外解除阻塞并完整回收 Runtime。
	close(blocked)
	if err := logRuntime.Close(context.Background()); err != nil {
		b.Fatal(err)
	}
}

func dropped(stats Stats) uint64 {
	// 汇总四个级别，供基准统一确认没有队列污染。
	return stats.DroppedDebug + stats.DroppedInfo + stats.DroppedWarn + stats.DroppedError
}
