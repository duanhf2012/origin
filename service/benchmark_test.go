package service

import (
	"context"
	"runtime"
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

func BenchmarkEventHandlerFanout(b *testing.B) {
	owner := &Service{}
	slot := &eventSlot{id: 1}
	listener := &eventListener{handler: func(context.Context, Event) error { return nil }}
	listener.active.Store(true)
	slot.listeners = []*eventListener{listener}
	event := &testEvent{id: 1}
	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		if result, failures := owner.notifyEventHandlers(context.Background(), slot, event); result != nil || failures != 0 {
			b.Fatal(result)
		}
	}
}

func BenchmarkNotifyEventAsync(b *testing.B) {
	completed := make(chan struct{}, 1)
	fixture := newEventFixture(b, DefaultSchedulerConfig(), func(target *testService) error {
		return target.SubscribeEvent(1, func(context.Context, Event) error {
			completed <- struct{}{}
			return nil
		})
	})
	event := &testEvent{id: 1}
	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		if err := fixture.service.NotifyEventAsync(event); err != nil {
			b.Fatal(err)
		}
		<-completed
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
