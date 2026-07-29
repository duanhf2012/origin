package service

import (
	"context"
	"fmt"
	"runtime"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/duanhf2012/origin/v3/internal/timerwheel"
)

func BenchmarkTimerAfterCreateCancel(b *testing.B) {
	fixture := newTimerFixture(b, 1)
	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		id := fixture.service.AfterFunc(time.Hour, noopTimerCallback)
		if id == InvalidTimerID || !fixture.service.CancelTimer(&id) {
			b.Fatal("AfterFunc 创建或取消失败")
		}
	}
}

func BenchmarkTimerPauseResume(b *testing.B) {
	fixture := newTimerFixture(b, 1)
	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		id := fixture.service.AfterFunc(time.Hour, noopTimerCallback)
		if id == InvalidTimerID ||
			!fixture.service.PauseTimer(id) ||
			!fixture.service.ResumeTimer(id) ||
			!fixture.service.CancelTimer(&id) {
			b.Fatal("Timer 暂停、恢复或取消失败")
		}
	}
}

func BenchmarkTimerTickerReschedule(b *testing.B) {
	fixture := newTimerFixture(b, 1)
	fired := make(chan struct{}, 1)
	id := fixture.service.NewTicker(
		timerwheel.TickDuration,
		func(context.Context, TimerID) {
			fired <- struct{}{}
		},
	)
	if id == InvalidTimerID {
		b.Fatal("NewTicker 创建失败")
	}

	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		advanceTimerFixture(b, fixture, timerwheel.TickDuration)
		<-fired
		expected := uint64(index + 1)
		for {
			stats := fixture.service.TimerStats()
			if stats.TriggeredTotal == expected && stats.Scheduled == 1 {
				break
			}
			runtime.Gosched()
		}
	}
	b.StopTimer()
	if !fixture.service.CancelTimer(&id) {
		b.Fatal("取消 Ticker 失败")
	}
}

func BenchmarkTimerCallback(b *testing.B) {
	fixture := newTimerFixture(b, 1)
	fired := make(chan struct{}, 1)
	callback := func(context.Context, TimerID) {
		fired <- struct{}{}
	}

	b.ReportAllocs()
	for index := 0; index < b.N; index++ {
		b.StopTimer()
		if id := fixture.service.AfterFunc(0, callback); id == InvalidTimerID {
			b.Fatal("AfterFunc 创建失败")
		}
		b.StartTimer()
		advanceTimerFixture(b, fixture, timerwheel.TickDuration)
		<-fired
		// 回调写入 Channel 后，Runner 还需要在同一执行链中提交 Timer 完成并归还 Node
		// 额度。等待 Active 归零，避免下一轮把“回调已通知”误当成“内部资源已回收”。
		for fixture.service.TimerStats().Active != 0 {
			runtime.Gosched()
		}
	}
}

func BenchmarkTimerCallbackAwait(b *testing.B) {
	fixture := newTimerFixture(b, 1)
	fired := make(chan error, 1)
	callback := func(ctx context.Context, _ TimerID) {
		fired <- fixture.service.Await(
			ctx,
			func(context.Context) error {
				return nil
			},
		)
	}

	b.ReportAllocs()
	for index := 0; index < b.N; index++ {
		b.StopTimer()
		if id := fixture.service.AfterFunc(0, callback); id == InvalidTimerID {
			b.Fatal("AfterFunc 创建失败")
		}
		b.StartTimer()
		advanceTimerFixture(b, fixture, timerwheel.TickDuration)
		if err := <-fired; err != nil {
			b.Fatal(err)
		}
		// Await 返回只代表业务回调已经继续执行；仍需等 Runner 完成本轮 Timer 回收，
		// 才能在单 Timer 额度下开始下一轮。
		for fixture.service.TimerStats().Active != 0 {
			runtime.Gosched()
		}
	}
}

func BenchmarkTimerCronParseNext(b *testing.B) {
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	b.ReportAllocs()
	for index := 0; index < b.N; index++ {
		schedule, err := parseCronExpression("*/5 * * * *")
		if err != nil || schedule.Next(now).IsZero() {
			b.Fatal("Cron 解析或 Next 失败")
		}
	}
}

func BenchmarkTimerDuePendingPromote(b *testing.B) {
	config := DefaultSchedulerConfig()
	config.MaxTasks = 1
	config.MaxAwaitTasks = 1
	fixture := newTimerFixtureWithConfig(b, config, b.N+1, true)

	block := make(chan struct{})
	started := make(chan struct{})
	if err := fixture.service.DispatchAsync(func(context.Context) {
		close(started)
		<-block
	}); err != nil {
		b.Fatal(err)
	}
	<-started
	for index := 0; index < b.N; index++ {
		if id := fixture.service.AfterFunc(0, noopTimerCallback); id == InvalidTimerID {
			b.Fatal("AfterFunc 创建失败")
		}
	}
	advanceTimerFixture(b, fixture, timerwheel.TickDuration)
	for fixture.service.TimerStats().DuePending != b.N {
		runtime.Gosched()
	}

	b.ReportAllocs()
	b.ResetTimer()
	close(block)
	for fixture.service.TimerStats().CompletedTotal != uint64(b.N) {
		runtime.Gosched()
	}
}

func BenchmarkTimerBacklogLatency(b *testing.B) {
	const timerCount = 10_000
	config := DefaultSchedulerConfig()
	config.MaxTasks = timerCount + 2
	config.MaxAwaitTasks = 1

	b.ReportAllocs()
	for iteration := 0; iteration < b.N; iteration++ {
		b.StopTimer()
		fixture := newTimerFixtureWithConfig(
			b,
			config,
			timerCount,
			true,
		)

		// 先占用唯一执行槽，使全部同 Tick Timer 可以稳定进入 Ready，再把普通任务追加到
		// 同一 FIFO 尾部；释放执行槽后统一测量回调分位数和普通任务在峰值后的等待时间。
		blockerStarted := make(chan struct{})
		releaseBlocker := make(chan struct{})
		if err := fixture.service.DispatchAsync(func(context.Context) {
			close(blockerStarted)
			<-releaseBlocker
		}); err != nil {
			b.Fatal(err)
		}
		<-blockerStarted

		latencies := make([]int64, timerCount)
		callbackIndex := 0
		var start time.Time
		callback := func(context.Context, TimerID) {
			latencies[callbackIndex] = time.Since(start).Nanoseconds()
			callbackIndex++
		}
		for index := 0; index < timerCount; index++ {
			if id := fixture.service.AfterFunc(
				timerwheel.TickDuration,
				callback,
			); id == InvalidTimerID {
				b.Fatalf("第 %d 个 Timer 创建失败", index)
			}
		}
		advanceTimerFixture(b, fixture, timerwheel.TickDuration)
		for fixture.service.TimerStats().Ready != timerCount {
			runtime.Gosched()
		}

		ordinaryDone := make(chan time.Duration, 1)
		if err := fixture.service.DispatchAsync(func(context.Context) {
			ordinaryDone <- time.Since(start)
		}); err != nil {
			b.Fatal(err)
		}

		start = time.Now()
		b.StartTimer()
		close(releaseBlocker)
		ordinaryLatency := <-ordinaryDone
		b.StopTimer()

		if callbackIndex != timerCount {
			b.Fatalf(
				"Timer 回调数量 = %d, want %d",
				callbackIndex,
				timerCount,
			)
		}
		slices.Sort(latencies)
		b.ReportMetric(float64(latencies[timerCount*50/100]), "timer-p50-ns")
		b.ReportMetric(float64(latencies[timerCount*95/100]), "timer-p95-ns")
		b.ReportMetric(float64(latencies[timerCount*99/100]), "timer-p99-ns")
		b.ReportMetric(
			float64(ordinaryLatency.Nanoseconds()),
			"ordinary-task-ns",
		)
	}
}

func BenchmarkBusinessTimerPoolComparison(b *testing.B) {
	fixture := newPreparedTimerFixture(b, 1)
	scheduler := fixture.service.scheduler.Load()
	var sink *businessTimer

	b.Run("no_pool", func(b *testing.B) {
		b.ReportAllocs()
		for index := 0; index < b.N; index++ {
			timer := &businessTimer{id: TimerID(index + 1)}
			sink = timer
		}
	})
	b.Run("service_private_pool", func(b *testing.B) {
		b.ReportAllocs()
		for index := 0; index < b.N; index++ {
			scheduler.mu.Lock()
			timer := scheduler.acquireBusinessTimerLocked()
			timer.id = TimerID(index + 1)
			sink = timer
			*timer = businessTimer{}
			timer.pooled = true
			scheduler.timerPool.Put(timer)
			scheduler.mu.Unlock()
		}
	})
	runtime.KeepAlive(sink)
}

func BenchmarkBusinessTimersActive(b *testing.B) {
	for _, count := range []int{10_000, 100_000, 1_000_000, 3_000_000} {
		b.Run(fmt.Sprintf("%d", count), func(b *testing.B) {
			fixture := newTimerFixture(b, count)
			ids := make([]TimerID, count)

			b.ReportAllocs()
			for iteration := 0; iteration < b.N; iteration++ {
				runtime.GC()
				var before runtime.MemStats
				runtime.ReadMemStats(&before)

				b.StartTimer()
				for index := range ids {
					ids[index] = fixture.service.AfterFunc(
						time.Hour,
						noopTimerCallback,
					)
					if ids[index] == InvalidTimerID {
						b.Fatalf("第 %d 个 Timer 创建失败", index)
					}
				}
				b.StopTimer()

				runtime.GC()
				var active runtime.MemStats
				runtime.ReadMemStats(&active)
				if active.HeapAlloc > before.HeapAlloc {
					retained := active.HeapAlloc - before.HeapAlloc
					b.ReportMetric(
						float64(retained)/float64(count),
						"active-B/timer",
					)
				}
				b.ReportMetric(float64(active.NumGC-before.NumGC), "setup-GCs")

				for index := range ids {
					if !fixture.service.CancelTimer(&ids[index]) {
						b.Fatalf("第 %d 个 Timer 取消失败", index)
					}
				}
			}
		})
	}
}

func BenchmarkBusinessTimersSameTick(b *testing.B) {
	for _, count := range []int{10_000, 100_000, 1_000_000, 3_000_000} {
		b.Run(fmt.Sprintf("%d", count), func(b *testing.B) {
			fixture := newTimerFixture(b, count)
			var callbackCount atomic.Uint64
			callback := func(context.Context, TimerID) {
				callbackCount.Add(1)
			}

			b.ReportAllocs()
			for iteration := 0; iteration < b.N; iteration++ {
				for index := 0; index < count; index++ {
					if id := fixture.service.AfterFunc(
						timerwheel.TickDuration,
						callback,
					); id == InvalidTimerID {
						b.Fatalf("第 %d 个 Timer 创建失败", index)
					}
				}
				expected := uint64((iteration + 1) * count)
				b.StartTimer()
				advanceTimerFixture(b, fixture, timerwheel.TickDuration)
				for callbackCount.Load() != expected ||
					fixture.service.TimerStats().Active != 0 {
					runtime.Gosched()
				}
				b.StopTimer()
			}
		})
	}
}

func BenchmarkStopBusinessTimers(b *testing.B) {
	for _, count := range []int{10_000, 100_000, 1_000_000, 3_000_000} {
		b.Run(fmt.Sprintf("%d", count), func(b *testing.B) {
			b.ReportAllocs()
			for iteration := 0; iteration < b.N; iteration++ {
				b.StopTimer()
				fixture := newTimerFixture(b, count)
				runtime.GC()
				runtime.GC()
				var before runtime.MemStats
				runtime.ReadMemStats(&before)
				for index := 0; index < count; index++ {
					if id := fixture.service.AfterFunc(
						time.Hour,
						noopTimerCallback,
					); id == InvalidTimerID {
						b.Fatalf("第 %d 个 Timer 创建失败", index)
					}
				}
				fixture.runtime.state.Store(uint32(StateStopping))

				b.StartTimer()
				if err := StopScheduler(
					context.Background(),
					fixture.service,
				); err != nil {
					b.Fatal(err)
				}
				b.StopTimer()
				if err := fixture.engine.Close(); err != nil {
					b.Fatal(err)
				}

				// Service Stop 与随后 Node Engine Close 已主动断开高水位容器和池引用。
				// sync.Pool 项会先进入 victim cache，连续两次 GC 后再报告相对空
				// Fixture 的进程堆变化，避免把一代池缓存误判为框架泄漏。
				runtime.GC()
				runtime.GC()
				var after runtime.MemStats
				runtime.ReadMemStats(&after)
				retained := int64(after.HeapAlloc) - int64(before.HeapAlloc)
				b.ReportMetric(float64(retained), "stopped-retained-B")
			}
		})
	}
}
