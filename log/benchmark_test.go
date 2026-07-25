package log

import (
	"context"
	"runtime"
	"sync"
	"testing"
)

type benchmarkHandler struct {
	enabled bool
}

func (handler benchmarkHandler) Enabled(Level) bool          { return handler.enabled }
func (handler benchmarkHandler) Write(Record, []Field) error { return nil }
func (handler benchmarkHandler) Sync() error                 { return nil }
func (handler benchmarkHandler) Close() error                { return nil }

func BenchmarkDisabled(b *testing.B) {
	runtime, err := NewRuntime(DefaultConfig(), benchmarkHandler{})
	if err != nil {
		b.Fatal(err)
	}
	defer runtime.Close(context.Background())
	logger := runtime.Logger()

	b.ReportAllocs()
	for b.Loop() {
		logger.Debug("disabled", Int64("player_id", 1))
	}
}

func BenchmarkAsyncNoFields(b *testing.B) {
	runtime, err := NewRuntime(DefaultConfig(), benchmarkHandler{enabled: true})
	if err != nil {
		b.Fatal(err)
	}
	defer runtime.Close(context.Background())
	logger := runtime.Logger()

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
	b.StopTimer()
	if err := runtime.Flush(context.Background()); err != nil {
		b.Fatal(err)
	}
	if dropped(runtime.Stats()) != 0 {
		b.Fatalf("benchmark dropped %d log events", dropped(runtime.Stats()))
	}
}

func BenchmarkAsyncFields(b *testing.B) {
	runtime, err := NewRuntime(DefaultConfig(), benchmarkHandler{enabled: true})
	if err != nil {
		b.Fatal(err)
	}
	defer runtime.Close(context.Background())
	logger := runtime.Logger().With(String("service", "player"))

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
	if err := runtime.Flush(context.Background()); err != nil {
		b.Fatal(err)
	}
	if dropped(runtime.Stats()) != 0 {
		b.Fatalf("benchmark dropped %d log events", dropped(runtime.Stats()))
	}
}

func BenchmarkSyncFields(b *testing.B) {
	config := DefaultConfig()
	config.Mode = SyncMode
	logRuntime, err := NewRuntime(config, benchmarkHandler{enabled: true})
	if err != nil {
		b.Fatal(err)
	}
	defer logRuntime.Close(context.Background())
	logger := logRuntime.Logger()

	b.ReportAllocs()
	for b.Loop() {
		logger.Info("message", Int64("player_id", 7))
	}
}

func BenchmarkCaller(b *testing.B) {
	var caller Caller
	b.ReportAllocs()
	for b.Loop() {
		caller = captureCaller(0)
	}
	runtime.KeepAlive(caller)
}

func BenchmarkErrorStack(b *testing.B) {
	config := DefaultConfig()
	config.Mode = SyncMode
	logRuntime, err := NewRuntime(config, benchmarkHandler{enabled: true})
	if err != nil {
		b.Fatal(err)
	}
	defer logRuntime.Close(context.Background())
	logger := logRuntime.Logger()

	b.ReportAllocs()
	for b.Loop() {
		logger.ErrorStack("message")
	}
}

func BenchmarkQueueFull(b *testing.B) {
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
	for range eventQueueSize {
		logger.Info("queued")
	}

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		logger.Info("dropped")
	}
	b.StopTimer()
	close(blocked)
	if err := logRuntime.Close(context.Background()); err != nil {
		b.Fatal(err)
	}
}

func dropped(stats Stats) uint64 {
	return stats.DroppedDebug + stats.DroppedInfo + stats.DroppedWarn + stats.DroppedError
}
