package timerwheel

import (
	"fmt"
	"runtime"
	"slices"
	"testing"
	"time"
)

// newBenchmarkEngine 创建真实工作循环但使用长 Deadline，避免 Benchmark 被到期异步干扰。
func newBenchmarkEngine(b *testing.B) (*Engine, *DeadlineQueue) {
	b.Helper()
	engine, queue := startBenchmarkEngine(b)
	b.Cleanup(func() {
		if err := engine.Close(); err != nil {
			b.Errorf("Close() error = %v", err)
		}
	})
	return engine, queue
}

// startBenchmarkEngine 创建由调用方自行关闭的生产依赖 Engine。
func startBenchmarkEngine(b *testing.B) (*Engine, *DeadlineQueue) {
	b.Helper()
	engine, err := New(DefaultOptions())
	if err != nil {
		b.Fatalf("New() error = %v", err)
	}
	if err := engine.Start(); err != nil {
		b.Fatalf("Start() error = %v", err)
	}
	queue, err := engine.NewDeadlineQueue()
	if err != nil {
		b.Fatalf("NewDeadlineQueue() error = %v", err)
	}
	return engine, queue
}

// BenchmarkTimerWheelAfter 测量单 Queue 长 Deadline 登记热路径。
func BenchmarkTimerWheelAfter(b *testing.B) {
	_, queue := newBenchmarkEngine(b)
	ids := make([]DeadlineID, 0, b.N)
	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		id, err := queue.ScheduleAfter(time.Hour)
		if err != nil {
			b.Fatal(err)
		}
		ids = append(ids, id)
	}
	b.StopTimer()
	for _, id := range ids {
		queue.Cancel(id)
	}
}

// BenchmarkTimerWheelCancel 测量已知 ID 的 O(1) 取消和对象回池路径。
func BenchmarkTimerWheelCancel(b *testing.B) {
	_, queue := newBenchmarkEngine(b)
	ids := make([]DeadlineID, b.N)
	for index := range ids {
		id, err := queue.ScheduleAfter(time.Hour)
		if err != nil {
			b.Fatal(err)
		}
		ids[index] = id
	}
	b.ReportAllocs()
	b.ResetTimer()
	for _, id := range ids {
		if !queue.Cancel(id) {
			b.Fatal("Cancel() 失败")
		}
	}
}

// BenchmarkTimerWheelExpire 直接使用可控时钟测量同 Tick 批量到期内核路径。
func BenchmarkTimerWheelExpire(b *testing.B) {
	for _, count := range []int{10_000, 100_000} {
		b.Run(fmt.Sprintf("%d", count), func(b *testing.B) {
			b.StopTimer()
			for iteration := 0; iteration < b.N; iteration++ {
				engine, queue, clock, _ := newBenchmarkFakeEngine(b)
				for index := 0; index < count; index++ {
					if _, err := queue.ScheduleAfter(TickDuration); err != nil {
						b.Fatal(err)
					}
				}
				// 持有 Engine 锁后再推进测试时钟，排除工作 goroutine 消费残留变更信号的竞态。
				engine.mu.Lock()
				b.StartTimer()
				clock.Advance(TickDuration)
				engine.advanceLocked(1, TickDuration)
				b.StopTimer()
				engine.mu.Unlock()
				if err := engine.Close(); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkTimerWheelConcurrentAfter 测量并发登记时单锁和 ID 索引开销。
func BenchmarkTimerWheelConcurrentAfter(b *testing.B) {
	_, queue := newBenchmarkEngine(b)
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if _, err := queue.ScheduleAfter(time.Hour); err != nil {
				b.Fatal(err)
			}
		}
	})
}

// BenchmarkTimerWheelConcurrentCancel 测量并发登记后立即取消的稳定复用路径。
func BenchmarkTimerWheelConcurrentCancel(b *testing.B) {
	_, queue := newBenchmarkEngine(b)
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			id, err := queue.ScheduleAfter(time.Hour)
			if err != nil {
				b.Fatal(err)
			}
			if !queue.Cancel(id) {
				b.Fatal("Cancel() 失败")
			}
		}
	})
}

// BenchmarkTimerWheelCascade 测量 L4 条目一次大步推进经过多层级联的成本。
func BenchmarkTimerWheelCascade(b *testing.B) {
	const count = 10_000
	b.StopTimer()
	for iteration := 0; iteration < b.N; iteration++ {
		engine, queue, clock, _ := newBenchmarkFakeEngine(b)
		delay := durationAtTick(1 << 32)
		for index := 0; index < count; index++ {
			if _, err := queue.ScheduleAfter(delay); err != nil {
				b.Fatal(err)
			}
		}
		// 推进时钟和时间轮处于同一临界区，确保计时只包含本次确定性的级联工作。
		engine.mu.Lock()
		b.StartTimer()
		clock.Advance(delay)
		engine.advanceLocked(1<<32, delay)
		b.StopTimer()
		engine.mu.Unlock()
		if err := engine.Close(); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkTimerWheelDrainExpired 测量复用 Slice 的批量 ID 消费路径。
func BenchmarkTimerWheelDrainExpired(b *testing.B) {
	const batch = 1024
	engine, queue, clock, _ := newBenchmarkFakeEngine(b)
	b.Cleanup(func() {
		if err := engine.Close(); err != nil {
			b.Errorf("Close() error = %v", err)
		}
	})
	for index := 0; index < batch; index++ {
		if _, err := queue.ScheduleAfter(TickDuration); err != nil {
			b.Fatal(err)
		}
	}
	clock.Advance(TickDuration)
	engine.mu.Lock()
	engine.advanceLocked(1, TickDuration)
	engine.mu.Unlock()
	dst := make([]DeadlineID, 0, batch)

	b.ReportAllocs()
	b.ResetTimer()
	for iteration := 0; iteration < b.N; iteration++ {
		engine.mu.Lock()
		empty := queue.expired.Len() == 0
		engine.mu.Unlock()
		if empty {
			b.StopTimer()
			engine.mu.Lock()
			for index := 0; index < batch; index++ {
				id := DeadlineID(index + 1)
				queue.expired.Push(id)
				engine.stats.expired++
			}
			engine.mu.Unlock()
			b.StartTimer()
		}
		var err error
		dst, err = queue.DrainExpired(dst[:0], batch)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkTimerWheelManyQueues 测量一百条 Queue 并存时的登记和取消。
func BenchmarkTimerWheelManyQueues(b *testing.B) {
	engine, first := newBenchmarkEngine(b)
	queues := make([]*DeadlineQueue, 100)
	queues[0] = first
	for index := 1; index < len(queues); index++ {
		queue, err := engine.NewDeadlineQueue()
		if err != nil {
			b.Fatal(err)
		}
		queues[index] = queue
	}
	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		queue := queues[index%len(queues)]
		id, err := queue.ScheduleAfter(time.Hour)
		if err != nil {
			b.Fatal(err)
		}
		queue.Cancel(id)
	}
}

// BenchmarkTimerWheelTickPrecision 测量 10ms 边界计算本身的纯算术开销。
func BenchmarkTimerWheelTickPrecision(b *testing.B) {
	durations := [...]time.Duration{
		time.Nanosecond,
		TickDuration - time.Nanosecond,
		TickDuration,
		TickDuration + time.Nanosecond,
		time.Second,
		15 * time.Second,
	}
	var result uint64
	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		result += ceilTick(durations[index%len(durations)])
	}
	if result == 0 {
		b.Fatal("防止编译器消除结果")
	}
}

// BenchmarkTimerWheelActiveDeadlines 记录 M8/M10 要求的四档活跃容量建立和清理基线。
func BenchmarkTimerWheelActiveDeadlines(b *testing.B) {
	for _, count := range []int{10_000, 100_000, 1_000_000, 3_000_000} {
		b.Run(fmt.Sprintf("%d", count), func(b *testing.B) {
			b.StopTimer()
			for iteration := 0; iteration < b.N; iteration++ {
				engine, queue := startBenchmarkEngine(b)
				ids := make([]DeadlineID, count)

				// 强制清理前序垃圾并记录仅由活跃时间轮条目增长的近似保留内存。
				runtime.GC()
				var before runtime.MemStats
				runtime.ReadMemStats(&before)
				b.StartTimer()
				for index := range ids {
					id, err := queue.ScheduleAfter(time.Hour)
					if err != nil {
						b.Fatal(err)
					}
					ids[index] = id
				}
				b.StopTimer()
				runtime.GC()
				var active runtime.MemStats
				runtime.ReadMemStats(&active)
				retained := uint64(0)
				if active.HeapAlloc > before.HeapAlloc {
					retained = active.HeapAlloc - before.HeapAlloc
				}
				if retained > 0 {
					// 小容量样本可能被进程内其他并发垃圾回收噪声抵消，此时不报告误导性的零值。
					b.ReportMetric(float64(retained)/float64(count), "active-B/deadline")
				}
				b.ReportMetric(float64(active.NumGC-before.NumGC), "setup-GCs")

				// 取消和关闭不计入“建立活跃集合”的耗时，但必须完整回收本轮资源。
				for _, id := range ids {
					queue.Cancel(id)
				}
				if err := engine.Close(); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkTimerWheelMixedWorkload 模拟短 Deadline 中九成提前取消的游戏负载。
func BenchmarkTimerWheelMixedWorkload(b *testing.B) {
	const count = 10_000
	b.StopTimer()
	for iteration := 0; iteration < b.N; iteration++ {
		engine, queue, clock, _ := newBenchmarkFakeEngine(b)
		ids := make([]DeadlineID, count)
		seed := uint64(0x9e3779b97f4a7c15)

		b.StartTimer()
		for index := range ids {
			// xorshift 只提供确定性分布，不把通用随机数锁和接口开销混入基准。
			seed ^= seed << 13
			seed ^= seed >> 7
			seed ^= seed << 17
			delay := time.Duration(seed%1_000+1) * time.Millisecond
			id, err := queue.ScheduleAfter(delay)
			if err != nil {
				b.Fatal(err)
			}
			ids[index] = id
		}
		for index, id := range ids {
			if index%10 != 0 && !queue.Cancel(id) {
				b.Fatal("90% Cancel() 失败")
			}
		}
		engine.mu.Lock()
		clock.Advance(time.Second)
		engine.advanceLocked(100, time.Second)
		engine.mu.Unlock()
		b.StopTimer()

		// 剩余一成必须全部到期；关闭本轮 Engine 后再建立下一组独立状态。
		if stats := engine.Stats(); stats.Expired != count/10 {
			b.Fatalf("混合负载 Expired = %d，期望 %d", stats.Expired, count/10)
		}
		if err := engine.Close(); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkTimerWheelMultipleEngines 测量同进程八个 Node 等价 Engine 的隔离登记路径。
func BenchmarkTimerWheelMultipleEngines(b *testing.B) {
	const engineCount = 8
	engines := make([]*Engine, engineCount)
	queues := make([]*DeadlineQueue, engineCount)
	for index := range engines {
		engines[index], queues[index] = startBenchmarkEngine(b)
	}
	b.Cleanup(func() {
		for _, engine := range engines {
			if err := engine.Close(); err != nil {
				b.Errorf("Close() error = %v", err)
			}
		}
	})

	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		queue := queues[index%len(queues)]
		id, err := queue.ScheduleAfter(time.Hour)
		if err != nil {
			b.Fatal(err)
		}
		if !queue.Cancel(id) {
			b.Fatal("Cancel() 失败")
		}
	}
}

// BenchmarkTimerWheelExpiryLatency 使用生产 Timer 记录基础 Tick 到期延迟分位数。
func BenchmarkTimerWheelExpiryLatency(b *testing.B) {
	const count = 2_000
	var p50Total time.Duration
	var p95Total time.Duration
	var p99Total time.Duration
	var p999Total time.Duration

	b.StopTimer()
	for iteration := 0; iteration < b.N; iteration++ {
		engine, queue := startBenchmarkEngine(b)
		expected := make(map[DeadlineID]time.Time, count)
		for index := 0; index < count; index++ {
			id, err := queue.ScheduleAfter(20 * time.Millisecond)
			if err != nil {
				b.Fatal(err)
			}
			engine.mu.Lock()
			expected[id] = engine.startTime.Add(durationAtTick(engine.entries[id].deadlineTick))
			engine.mu.Unlock()
		}

		// 消费所有合并批次，并按每批真正被观察到的时刻计算“不早于目标”的延迟。
		latencies := make([]time.Duration, 0, count)
		b.StartTimer()
		for len(latencies) < count {
			select {
			case _, open := <-queue.ExpiredSignal():
				if !open {
					b.Fatal("DeadlineQueue 提前关闭")
				}
			case <-time.After(2 * time.Second):
				b.Fatal("等待到期延迟样本超时")
			}
			observed := time.Now()
			ids, err := queue.DrainExpired(nil, count)
			if err != nil {
				b.Fatal(err)
			}
			for _, id := range ids {
				delay := observed.Sub(expected[id])
				if delay < 0 {
					b.Fatalf("Deadline %d 提前 %v 到期", id, -delay)
				}
				latencies = append(latencies, delay)
			}
		}
		b.StopTimer()

		// 分位数排序和 Engine 清理不计入 Timer 等待耗时。
		slices.Sort(latencies)
		p50Total += percentileDuration(latencies, 0.50)
		p95Total += percentileDuration(latencies, 0.95)
		p99Total += percentileDuration(latencies, 0.99)
		p999Total += percentileDuration(latencies, 0.999)
		if err := engine.Close(); err != nil {
			b.Fatal(err)
		}
	}
	b.ReportMetric(float64(p50Total/time.Duration(b.N)), "p50-ns")
	b.ReportMetric(float64(p95Total/time.Duration(b.N)), "p95-ns")
	b.ReportMetric(float64(p99Total/time.Duration(b.N)), "p99-ns")
	b.ReportMetric(float64(p999Total/time.Duration(b.N)), "p99.9-ns")
}

// percentileDuration 使用向上取整秩返回非空延迟样本的指定分位点。
func percentileDuration(samples []time.Duration, percentile float64) time.Duration {
	if len(samples) == 0 {
		return 0
	}
	index := int(float64(len(samples))*percentile+0.999999999) - 1
	if index < 0 {
		index = 0
	}
	if index >= len(samples) {
		index = len(samples) - 1
	}
	return samples[index]
}

// newBenchmarkFakeEngine 创建不依赖真实时间推进的 Benchmark Engine。
func newBenchmarkFakeEngine(b *testing.B) (*Engine, *DeadlineQueue, *fakeClock, *fakeWakeSource) {
	b.Helper()
	clock := &fakeClock{now: time.Unix(1_000, 0)}
	wake := newFakeWakeSource()
	engine, err := New(Options{Clock: clock, WakeSource: wake})
	if err != nil {
		b.Fatal(err)
	}
	if err := engine.Start(); err != nil {
		b.Fatal(err)
	}
	queue, err := engine.NewDeadlineQueue()
	if err != nil {
		b.Fatal(err)
	}
	return engine, queue, clock, wake
}
