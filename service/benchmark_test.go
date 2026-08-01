package service

import (
	"context"
	"fmt"
	"runtime"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/duanhf2012/origin/v3/internal/timerwheel"
)

func BenchmarkSchedulerDispatchSerial(b *testing.B) {
	fixture := newSchedulerFixture(b, SchedulerConfig{
		MaxTasks:            MaxSchedulerTasks,
		MaxAwaitTasks:       DefaultMaxAwaitTasks,
		DefaultAwaitTimeout: time.Second,
	})

	// 每轮投递一个根任务并等待它完成，记录完整准入、队列、Runner 和清理成本。
	done := make(chan struct{}, 1)
	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		if err := fixture.service.DispatchAsync(func(context.Context) {
			done <- struct{}{}
		}); err != nil {
			b.Fatalf("DispatchAsync() error = %v", err)
		}
		<-done
	}
}

func BenchmarkSchedulerDispatchParallel(b *testing.B) {
	fixture := newSchedulerFixture(b, SchedulerConfig{
		MaxTasks:            MaxSchedulerTasks,
		MaxAwaitTasks:       DefaultMaxAwaitTasks,
		DefaultAwaitTimeout: time.Second,
	})

	// 并行提交方共享同一 Scheduler；完成计数用于在 Benchmark 结束前排空全部已接收任务。
	var completed sync.WaitGroup
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(parallel *testing.PB) {
		for parallel.Next() {
			completed.Add(1)
			for {
				err := fixture.service.DispatchAsync(func(context.Context) {
					completed.Done()
				})
				if err == nil {
					break
				}
				// Benchmark 的固定硬上限可能被瞬时峰值占满；等待 Runner 消费后重试同一轮。
				completed.Done()
				runtime.Gosched()
			}
		}
	})
	completed.Wait()
}

func BenchmarkSchedulerReadyDrain(b *testing.B) {
	fixture := newSchedulerFixture(b, SchedulerConfig{
		MaxTasks:            MaxSchedulerTasks,
		MaxAwaitTasks:       DefaultMaxAwaitTasks,
		DefaultAwaitTimeout: time.Second,
	})

	// 先用一个任务占住执行槽，再批量填充 Ready；释放后测量连续排空吞吐。
	const batchSize = 10000
	b.StopTimer()
	for iteration := 0; iteration < b.N; iteration++ {
		blocked := make(chan struct{})
		started := make(chan struct{})
		if err := fixture.service.DispatchAsync(func(context.Context) {
			close(started)
			<-blocked
		}); err != nil {
			b.Fatalf("blocking DispatchAsync() error = %v", err)
		}
		<-started

		var completed sync.WaitGroup
		completed.Add(batchSize)
		b.StartTimer()
		for index := 0; index < batchSize; index++ {
			if err := fixture.service.DispatchAsync(func(context.Context) {
				completed.Done()
			}); err != nil {
				b.Fatalf("DispatchAsync() error = %v", err)
			}
		}
		close(blocked)
		completed.Wait()
		b.StopTimer()
	}
}

func BenchmarkAwaitHandoff(b *testing.B) {
	fixture := newSchedulerFixture(b, SchedulerConfig{
		MaxTasks:            MaxSchedulerTasks,
		MaxAwaitTasks:       DefaultMaxAwaitTasks,
		DefaultAwaitTimeout: time.Second,
	})
	done := make(chan struct{}, 1)

	// 已经就绪的等待函数仍完整经历释放、替补 Runner、恢复 FIFO 和执行权交接。
	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		if err := fixture.service.DispatchAsync(func(ctx context.Context) {
			_ = fixture.service.Await(ctx, func(context.Context) error {
				return nil
			})
			done <- struct{}{}
		}); err != nil {
			b.Fatalf("DispatchAsync() error = %v", err)
		}
		<-done
	}
}

func BenchmarkAwaitAlreadyReady(b *testing.B) {
	fixture := newSchedulerFixture(b, SchedulerConfig{
		MaxTasks:            MaxSchedulerTasks,
		MaxAwaitTasks:       DefaultMaxAwaitTasks,
		DefaultAwaitTimeout: time.Second,
	})
	done := make(chan struct{}, 1)

	// 等待函数立即返回，用于单独锁定“没有真实 I/O 延迟”的最小 Await 成本。
	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		if err := fixture.service.DispatchAsync(func(ctx context.Context) {
			_ = fixture.service.Await(ctx, func(context.Context) error {
				return nil
			})
			done <- struct{}{}
		}); err != nil {
			b.Fatalf("DispatchAsync() error = %v", err)
		}
		<-done
	}
}

func BenchmarkAwaitConcurrentWaiting(b *testing.B) {
	fixture := newSchedulerFixture(b, SchedulerConfig{
		MaxTasks:            MaxSchedulerTasks,
		MaxAwaitTasks:       DefaultMaxAwaitTasks,
		DefaultAwaitTimeout: 30 * time.Second,
	})
	const batchSize = 1000
	b.StopTimer()

	// 每轮建立 1000 个真实 Waiting goroutine，再统一释放并测量恢复风暴。
	for iteration := 0; iteration < b.N; iteration++ {
		release := make(chan struct{})
		var completed sync.WaitGroup
		completed.Add(batchSize)
		b.StartTimer()
		for index := 0; index < batchSize; index++ {
			if err := fixture.service.DispatchAsync(func(ctx context.Context) {
				_ = fixture.service.Await(ctx, func(context.Context) error {
					<-release
					return nil
				})
				completed.Done()
			}); err != nil {
				b.Fatalf("DispatchAsync() error = %v", err)
			}
		}
		for fixture.service.ExecutionStats().Awaiting != batchSize {
			runtime.Gosched()
		}
		close(release)
		completed.Wait()
		b.StopTimer()
	}
}

func BenchmarkAwaitTimeout(b *testing.B) {
	fixture := newSchedulerFixture(b, SchedulerConfig{
		MaxTasks:            MaxSchedulerTasks,
		MaxAwaitTasks:       DefaultMaxAwaitTasks,
		DefaultAwaitTimeout: timerwheel.TickDuration,
	})
	done := make(chan struct{}, 1)

	// 真实等待一个 10ms Tick，覆盖 M8 登记、到期交付、Context 取消和恢复交接。
	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		if err := fixture.service.DispatchAsync(func(ctx context.Context) {
			_ = fixture.service.Await(ctx, func(waitCtx context.Context) error {
				<-waitCtx.Done()
				return waitCtx.Err()
			})
			done <- struct{}{}
		}); err != nil {
			b.Fatalf("DispatchAsync() error = %v", err)
		}
		<-done
	}
}

func BenchmarkSchedulerStats(b *testing.B) {
	fixture := newSchedulerFixture(b, DefaultSchedulerConfig())

	// 统计不是业务热路径，但必须保持低分配的一致锁内快照。
	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		_ = fixture.service.ExecutionStats()
	}
}

func BenchmarkNotifyEventSyncHandlerFanout(b *testing.B) {
	for _, listeners := range []int{0, 1, 10, 100, 1000} {
		b.Run(fmt.Sprintf("listeners_%d", listeners), func(b *testing.B) {
			owner := &Service{}
			slot := benchmarkEventSlot(listeners)
			event := &testEvent{id: 1}
			b.ReportAllocs()
			b.ResetTimer()
			for index := 0; index < b.N; index++ {
				if result, failures := owner.notifyEventHandlers(context.Background(), slot, event); result != nil || failures != 0 {
					b.Fatal(result)
				}
			}
		})
	}
}

func BenchmarkNotifyEventSync(b *testing.B) {
	for _, listeners := range []int{0, 1, 10, 100, 1000} {
		b.Run(fmt.Sprintf("listeners_%d", listeners), func(b *testing.B) {
			fixture := newEventFixture(b, DefaultSchedulerConfig(), func(target *testService) error {
				for range listeners {
					if err := target.SubscribeEvent(1, func(context.Context, Event) error { return nil }); err != nil {
						return err
					}
				}
				return nil
			})
			event := &testEvent{id: 1}
			completed := make(chan error, 1)
			notify := func(ctx context.Context) {
				completed <- fixture.service.NotifyEventSync(ctx, event)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for index := 0; index < b.N; index++ {
				if err := fixture.service.DispatchAsync(notify); err != nil {
					b.Fatal(err)
				}
				if err := <-completed; err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkNotifyEventAsync(b *testing.B) {
	for _, listeners := range []int{0, 1, 10, 100, 1000} {
		b.Run(fmt.Sprintf("listeners_%d", listeners), func(b *testing.B) {
			fixture := newEventFixture(b, DefaultSchedulerConfig(), func(target *testService) error {
				for range listeners {
					if err := target.SubscribeEvent(1, func(context.Context, Event) error { return nil }); err != nil {
						return err
					}
				}
				return nil
			})
			event := &testEvent{id: 1}
			completed := make(chan struct{}, 1)
			barrier := func(context.Context) { completed <- struct{}{} }
			b.ReportAllocs()
			b.ResetTimer()
			for index := 0; index < b.N; index++ {
				if err := fixture.service.NotifyEventAsync(event); err != nil {
					b.Fatal(err)
				}
				if err := fixture.service.DispatchAsync(barrier); err != nil {
					b.Fatal(err)
				}
				<-completed
			}
		})
	}
}

func BenchmarkNotifyEventLatency(b *testing.B) {
	for _, listeners := range []int{0, 1, 10, 100, 1000} {
		b.Run(fmt.Sprintf("sync_listeners_%d", listeners), func(b *testing.B) {
			owner := &Service{}
			slot := benchmarkEventSlot(listeners)
			event := &testEvent{id: 1}
			samples := measureLatencySamples(b, func() error {
				result, failures := owner.notifyEventHandlers(context.Background(), slot, event)
				if result != nil || failures != 0 {
					return fmt.Errorf("notify event: failures=%d result=%v", failures, result)
				}
				return nil
			})
			reportLatencyPercentiles(b, samples)
		})

		b.Run(fmt.Sprintf("async_listeners_%d", listeners), func(b *testing.B) {
			fixture := newEventFixture(b, DefaultSchedulerConfig(), func(target *testService) error {
				for range listeners {
					if err := target.SubscribeEvent(1, func(context.Context, Event) error { return nil }); err != nil {
						return err
					}
				}
				return nil
			})
			event := &testEvent{id: 1}
			completed := make(chan struct{}, 1)
			barrier := func(context.Context) { completed <- struct{}{} }
			samples := measureLatencySamples(b, func() error {
				if err := fixture.service.NotifyEventAsync(event); err != nil {
					return err
				}
				if err := fixture.service.DispatchAsync(barrier); err != nil {
					return err
				}
				<-completed
				return nil
			})
			reportLatencyPercentiles(b, samples)
		})
	}
}

func BenchmarkEventPayloadOwnership(b *testing.B) {
	owner := &Service{}
	slot := benchmarkEventSlot(1)
	benchmarks := []struct {
		name  string
		event Event
	}{
		{name: "small_value", event: benchmarkSmallEvent{value: 1}},
		{name: "pointer", event: &benchmarkPointerEvent{value: 1}},
		{name: "large_64KiB_pointer", event: &benchmarkLargeEvent{}},
	}
	for _, benchmark := range benchmarks {
		b.Run(benchmark.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if result, failures := owner.notifyEventHandlers(context.Background(), slot, benchmark.event); result != nil || failures != 0 {
					b.Fatal(result)
				}
			}
		})
	}
}

func BenchmarkModuleLifecycle(b *testing.B) {
	for _, modules := range []int{1, 100, 1000, MaxModulesPerService} {
		b.Run(fmt.Sprintf("modules_%d", modules), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				target := benchmarkModuleService(modules)
				if err := StartWithModules(context.Background(), target); err != nil {
					b.Fatal(err)
				}
				if err := StopWithModules(context.Background(), target); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkModuleDirectService(b *testing.B) {
	owner := &testService{}
	module := &Module{owner: &owner.Service, target: owner}
	b.ReportAllocs()
	for b.Loop() {
		if module.Service() == nil {
			b.Fatal("Module.Service() returned nil")
		}
	}
}

type benchmarkSmallEvent struct{ value uint64 }

func (benchmarkSmallEvent) EventID() EventID { return 1 }

type benchmarkPointerEvent struct{ value uint64 }

func (*benchmarkPointerEvent) EventID() EventID { return 1 }

type benchmarkLargeEvent struct{ payload [64 << 10]byte }

func (*benchmarkLargeEvent) EventID() EventID { return 1 }

func benchmarkEventSlot(listeners int) *eventSlot {
	slot := &eventSlot{id: 1, listeners: make([]*eventListener, listeners)}
	for index := range slot.listeners {
		listener := &eventListener{handler: func(context.Context, Event) error { return nil }}
		listener.active.Store(true)
		slot.listeners[index] = listener
	}
	return slot
}

func benchmarkModuleService(modules int) *testService {
	target := &testService{}
	target.moduleSealed = true
	target.moduleTarget = target
	target.modules = make([]*moduleEntry, modules)
	for index := range target.modules {
		module := &testModule{}
		module.owner = &target.Service
		module.target = target
		target.modules[index] = &moduleEntry{
			target:      module,
			base:        &module.Module,
			initialized: true,
		}
	}
	return target
}

func reportLatencyPercentiles(b *testing.B, samples []int64) {
	b.Helper()
	if len(samples) < 10 {
		return
	}
	sort.Slice(samples, func(left, right int) bool { return samples[left] < samples[right] })
	var total int64
	for _, sample := range samples {
		total += sample
	}
	p50 := samples[(len(samples)-1)*50/100]
	p99 := samples[(len(samples)-1)*99/100]
	b.ReportMetric(float64(total)/float64(len(samples)), "ns/op")
	b.ReportMetric(float64(p50), "p50-sample-ns/op")
	b.ReportMetric(float64(p99), "p99-sample-ns/op")
}

func measureLatencySamples(b *testing.B, operation func() error) []int64 {
	b.Helper()
	b.StopTimer()
	batchSize := calibrateLatencyBatch(b, operation)
	samples := make([]int64, b.N)
	b.ResetTimer()
	for index := range samples {
		started := time.Now()
		for range batchSize {
			if err := operation(); err != nil {
				b.Fatal(err)
			}
		}
		samples[index] = time.Since(started).Nanoseconds() / int64(batchSize)
	}
	b.StopTimer()
	b.ReportMetric(float64(batchSize), "latency-batch")
	return samples
}

func calibrateLatencyBatch(b *testing.B, operation func() error) int {
	b.Helper()
	// Measure calibrated batches rather than individual sub-microsecond calls so
	// percentile samples remain meaningful on hosts with coarse monotonic clocks.
	const (
		minimumSampleTime    = 2 * time.Millisecond
		minimumBatchSize     = 1 << 10
		maximumBatchSize     = 1 << 24
		warmupOperations     = 32
		calibrationRepeats   = 3
		unmeasuredSampleTime = time.Duration(1<<63 - 1)
	)
	for range warmupOperations {
		if err := operation(); err != nil {
			b.Fatal(err)
		}
	}
	for batchSize := 1; ; batchSize *= 2 {
		minimumElapsed := unmeasuredSampleTime
		for range calibrationRepeats {
			started := time.Now()
			for range batchSize {
				if err := operation(); err != nil {
					b.Fatal(err)
				}
			}
			if elapsed := time.Since(started); elapsed < minimumElapsed {
				minimumElapsed = elapsed
			}
		}
		if batchSize >= minimumBatchSize && (minimumElapsed >= minimumSampleTime || batchSize >= maximumBatchSize) {
			return batchSize
		}
	}
}

func BenchmarkTaskPoolComparison(b *testing.B) {
	fixture := newSchedulerFixture(b, DefaultSchedulerConfig())
	scheduler := fixture.service.scheduler.Load()
	noOp := func(context.Context) {}

	// 两组都创建不可复用的 taskContext；差异只在 serviceTask 主对象是否从私有池复用。
	b.Run("safe_no_pool", func(b *testing.B) {
		var sink *taskContext
		b.ReportAllocs()
		for index := 0; index < b.N; index++ {
			scheduler.mu.Lock()
			task := &serviceTask{
				scheduler: scheduler,
				fn:        noOp,
				state:     taskReady,
			}
			token := &taskContext{
				Context:   scheduler.lifetimeContext,
				scheduler: scheduler,
			}
			task.context = token
			token.task.Store(task)
			token.task.Store(nil)
			sink = token
			scheduler.mu.Unlock()
		}
		_ = sink
	})
	b.Run("safe_task_pool", func(b *testing.B) {
		var sink *taskContext
		b.ReportAllocs()
		for index := 0; index < b.N; index++ {
			scheduler.mu.Lock()
			task := scheduler.acquireTaskLocked(noOp)
			sink = task.context
			task.state = taskCompleted
			scheduler.releaseTaskLocked(task)
			scheduler.mu.Unlock()
		}
		_ = sink
	})
}
