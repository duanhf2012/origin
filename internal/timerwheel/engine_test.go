package timerwheel

import (
	"errors"
	"math"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/duanhf2012/origin/v3/errs"
)

// fakeClock 为时间轮测试提供可并发推进的确定性单调时间。
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

// Now 返回当前测试时间快照。
func (clock *fakeClock) Now() time.Time {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return clock.now
}

// Advance 只向前推进测试时间。
func (clock *fakeClock) Advance(delta time.Duration) {
	clock.mu.Lock()
	clock.now = clock.now.Add(delta)
	clock.mu.Unlock()
}

// fakeWakeSource 允许测试在不真实等待的情况下唤醒 Engine 工作 goroutine。
type fakeWakeSource struct {
	channel chan time.Time

	mu         sync.Mutex
	lastDelay  time.Duration
	resetCount uint64
	stopCount  uint64
}

// newFakeWakeSource 创建容量足以合并测试唤醒的可控来源。
func newFakeWakeSource() *fakeWakeSource {
	return &fakeWakeSource{channel: make(chan time.Time, 16)}
}

// C 返回工作 goroutine 等待的测试 Channel。
func (source *fakeWakeSource) C() <-chan time.Time {
	return source.channel
}

// Reset 记录最近休眠时长，不自行推进时间。
func (source *fakeWakeSource) Reset(delay time.Duration) {
	source.mu.Lock()
	source.lastDelay = delay
	source.resetCount++
	source.mu.Unlock()
}

// Stop 记录停止次数；同一来源随后仍允许 Reset，模拟可复用 time.Timer。
func (source *fakeWakeSource) Stop() {
	source.mu.Lock()
	source.stopCount++
	source.mu.Unlock()
}

// Fire 发送一个可控 Timer 到期信号。
func (source *fakeWakeSource) Fire() {
	select {
	case source.channel <- time.Time{}:
	default:
	}
}

// newFakeTimerEngine 创建、启动并登记一个 Queue，供确定性测试复用。
func newFakeTimerEngine(
	t *testing.T,
	trackPool bool,
) (*Engine, *DeadlineQueue, *fakeClock, *fakeWakeSource) {
	t.Helper()
	clock := &fakeClock{now: time.Unix(1_000, 0)}
	wake := newFakeWakeSource()
	engine, err := New(Options{
		Clock:          clock,
		WakeSource:     wake,
		TrackEntryPool: trackPool,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := engine.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	queue, err := engine.NewDeadlineQueue()
	if err != nil {
		t.Fatalf("NewDeadlineQueue() error = %v", err)
	}
	t.Cleanup(func() {
		if err := engine.Close(); err != nil {
			t.Errorf("清理 Engine error = %v", err)
		}
	})
	return engine, queue, clock, wake
}

// advanceFake 推进测试时钟并主动唤醒 Engine。
func advanceFake(clock *fakeClock, wake *fakeWakeSource, delta time.Duration) {
	clock.Advance(delta)
	wake.Fire()
}

// waitForStats 等待异步工作循环达到明确状态，超时说明调度或唤醒丢失。
func waitForStats(t *testing.T, engine *Engine, predicate func(Stats) bool) Stats {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		stats := engine.Stats()
		if predicate(stats) {
			return stats
		}
		if time.Now().After(deadline) {
			t.Fatalf("等待 Engine 状态超时，最后统计：%+v", stats)
		}
		runtime.Gosched()
	}
}

// drainAfterSignal 等待合并通知并取出当前全部预期 ID。
func drainAfterSignal(
	t *testing.T,
	queue *DeadlineQueue,
	limit int,
) []DeadlineID {
	t.Helper()
	select {
	case _, open := <-queue.ExpiredSignal():
		if !open {
			t.Fatal("DeadlineQueue 在等待到期时意外关闭")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("等待 Deadline 到期通知超时")
	}
	result, err := queue.DrainExpired(nil, limit)
	if err != nil {
		t.Fatalf("DrainExpired() error = %v", err)
	}
	return result
}

func TestTickRoundingAndNoEarlyExpiry(t *testing.T) {
	engine, queue, clock, wake := newFakeTimerEngine(t, false)

	// 三个样本覆盖一个 Tick 内、恰好 Tick 边界和超过边界一纳秒。
	first, err := queue.ScheduleAfter(time.Nanosecond)
	if err != nil {
		t.Fatalf("ScheduleAfter(1ns) error = %v", err)
	}
	second, err := queue.ScheduleAfter(TickDuration)
	if err != nil {
		t.Fatalf("ScheduleAfter(10ms) error = %v", err)
	}
	third, err := queue.ScheduleAfter(TickDuration + time.Nanosecond)
	if err != nil {
		t.Fatalf("ScheduleAfter(10ms+1ns) error = %v", err)
	}

	// 直接检查内部 Tick，确保量化规则没有被异步调度时序掩盖。
	engine.mu.Lock()
	firstTick := engine.entries[first].deadlineTick
	secondTick := engine.entries[second].deadlineTick
	thirdTick := engine.entries[third].deadlineTick
	engine.mu.Unlock()
	if firstTick != 1 || secondTick != 1 || thirdTick != 2 {
		t.Fatalf("Deadline Tick = %d/%d/%d，期望 1/1/2", firstTick, secondTick, thirdTick)
	}

	// 9ms 时不能提前触发任何 ID。
	advanceFake(clock, wake, 9*time.Millisecond)
	waitForStats(t, engine, func(stats Stats) bool {
		return stats.TimerWakeups > 0
	})
	if stats := engine.Stats(); stats.Expired != 0 {
		t.Fatalf("9ms 时提前到期：%+v", stats)
	}

	// 到达 10ms 后前两个 ID 按登记顺序一起到期。
	advanceFake(clock, wake, time.Millisecond)
	got := drainAfterSignal(t, queue, 8)
	if len(got) != 2 || got[0] != first || got[1] != second {
		t.Fatalf("第一个 Tick 到期顺序 = %v，期望 [%d %d]", got, first, second)
	}

	// 第三个 ID 必须等到第二个 Tick。
	advanceFake(clock, wake, TickDuration)
	got = drainAfterSignal(t, queue, 8)
	if len(got) != 1 || got[0] != third {
		t.Fatalf("第二个 Tick 到期 = %v，期望 [%d]", got, third)
	}
}

func TestEngineNowUsesInjectedClock(t *testing.T) {
	engine, _, clock, _ := newFakeTimerEngine(t, false)
	expected := clock.Now()
	if actual := engine.Now(); !actual.Equal(expected) {
		t.Fatalf("Engine.Now() = %v，期望 %v", actual, expected)
	}
	clock.Advance(37 * time.Millisecond)
	expected = clock.Now()
	if actual := engine.Now(); !actual.Equal(expected) {
		t.Fatalf("Clock 前进后 Engine.Now() = %v，期望 %v", actual, expected)
	}
}

func TestZeroDelayRunsOnLaterEngineRound(t *testing.T) {
	_, queue, clock, wake := newFakeTimerEngine(t, false)

	// 零延迟登记不能在 ScheduleAfter 调用栈内同步交付。
	id, err := queue.ScheduleAfter(0)
	if err != nil {
		t.Fatalf("ScheduleAfter(0) error = %v", err)
	}
	select {
	case <-queue.ExpiredSignal():
		t.Fatal("零延迟 Deadline 在调用栈内同步到期")
	default:
	}

	// 推进一个基础 Tick 后由工作 goroutine 正常交付。
	advanceFake(clock, wake, TickDuration)
	got := drainAfterSignal(t, queue, 1)
	if len(got) != 1 || got[0] != id {
		t.Fatalf("零延迟到期 = %v，期望 [%d]", got, id)
	}
}

func TestSameTickOrderAndPartialDrainSignal(t *testing.T) {
	engine, queue, clock, wake := newFakeTimerEngine(t, false)

	// 所有条目落入同一 Tick，用于验证桶尾插和环形队列顺序。
	ids := make([]DeadlineID, 3)
	for index := range ids {
		id, err := queue.ScheduleAfter(15 * time.Millisecond)
		if err != nil {
			t.Fatalf("ScheduleAfter() error = %v", err)
		}
		ids[index] = id
	}
	advanceFake(clock, wake, 2*TickDuration)
	waitForStats(t, engine, func(stats Stats) bool { return stats.Expired == 3 })

	// 先消费通知，再限制只取两个；Queue 必须补回剩余通知。
	select {
	case <-queue.ExpiredSignal():
	case <-time.After(2 * time.Second):
		t.Fatal("等待合并通知超时")
	}
	firstBatch, err := queue.DrainExpired(make([]DeadlineID, 0, 3), 2)
	if err != nil {
		t.Fatalf("第一次 DrainExpired() error = %v", err)
	}
	if len(firstBatch) != 2 || firstBatch[0] != ids[0] || firstBatch[1] != ids[1] {
		t.Fatalf("第一批 = %v，期望前两个 %v", firstBatch, ids)
	}

	select {
	case <-queue.ExpiredSignal():
	case <-time.After(2 * time.Second):
		t.Fatal("部分 Drain 后没有补回通知")
	}
	secondBatch, err := queue.DrainExpired(firstBatch[:0], 2)
	if err != nil {
		t.Fatalf("第二次 DrainExpired() error = %v", err)
	}
	if len(secondBatch) != 1 || secondBatch[0] != ids[2] {
		t.Fatalf("第二批 = %v，期望 [%d]", secondBatch, ids[2])
	}
}

func TestCascadeAcrossAllLevelsAndLargeAdvance(t *testing.T) {
	engine, queue, clock, wake := newFakeTimerEngine(t, false)

	// 样本覆盖 L0、L1、L2、L3 和 L4 的最小边界。
	ticks := []uint64{1, 1 << 8, 1 << 16, 1 << 24, 1 << 32}
	ids := make([]DeadlineID, len(ticks))
	for index, tick := range ticks {
		id, err := queue.ScheduleAfter(durationAtTick(tick))
		if err != nil {
			t.Fatalf("ScheduleAfter(level=%d) error = %v", index, err)
		}
		ids[index] = id
		engine.mu.Lock()
		level := int(engine.entries[id].level)
		engine.mu.Unlock()
		if level != index {
			t.Fatalf("Tick %d 位于 L%d，期望 L%d", tick, level, index)
		}
	}

	// 一次跨越到 L4 边界，Engine 只访问非空事件点并按 Deadline 顺序交付。
	advanceFake(clock, wake, durationAtTick(1<<32))
	got := drainAfterSignal(t, queue, len(ids))
	if len(got) != len(ids) {
		t.Fatalf("大步推进到期数量 = %d，期望 %d：%v", len(got), len(ids), got)
	}
	for index := range ids {
		if got[index] != ids[index] {
			t.Fatalf("大步推进顺序[%d] = %d，期望 %d", index, got[index], ids[index])
		}
	}
	stats := engine.Stats()
	if stats.Cascades < 4 || stats.CascadedEntries < 4 {
		t.Fatalf("五层级联统计不足：%+v", stats)
	}
}

func TestCancelOwnershipAndQueueIsolation(t *testing.T) {
	engine, first, _, _ := newFakeTimerEngine(t, false)
	second, err := engine.NewDeadlineQueue()
	if err != nil {
		t.Fatalf("NewDeadlineQueue() error = %v", err)
	}

	// 两条 Queue 分别登记；跨 Queue 取消必须失败且不影响真实所有者。
	firstID, err := first.ScheduleAfter(time.Hour)
	if err != nil {
		t.Fatalf("first.ScheduleAfter() error = %v", err)
	}
	secondID, err := second.ScheduleAfter(time.Hour)
	if err != nil {
		t.Fatalf("second.ScheduleAfter() error = %v", err)
	}
	if second.Cancel(firstID) {
		t.Fatal("跨 Queue Cancel 意外成功")
	}
	if !first.Cancel(firstID) || first.Cancel(firstID) {
		t.Fatal("首次取消应成功，重复取消应失败")
	}
	if !second.Cancel(secondID) {
		t.Fatal("第二条 Queue 的自身取消失败")
	}
	stats := engine.Stats()
	if stats.Scheduled != 0 || stats.CanceledTotal != 2 || stats.Queues != 2 {
		t.Fatalf("取消后统计错误：%+v", stats)
	}
}

func TestQueueCloseCleansScheduledAndExpired(t *testing.T) {
	engine, queue, clock, wake := newFakeTimerEngine(t, false)

	// 一个条目先到期留在 Queue，另一个仍保留在时间轮。
	if _, err := queue.ScheduleAfter(TickDuration); err != nil {
		t.Fatalf("短 Deadline error = %v", err)
	}
	if _, err := queue.ScheduleAfter(time.Hour); err != nil {
		t.Fatalf("长 Deadline error = %v", err)
	}
	advanceFake(clock, wake, TickDuration)
	waitForStats(t, engine, func(stats Stats) bool { return stats.Expired == 1 })

	// Close 必须一次清理两个阶段的 ID，并关闭通知 Channel。
	queue.Close()
	queue.Close()
	stats := engine.Stats()
	if stats.Scheduled != 0 || stats.Expired != 0 || stats.Queues != 0 {
		t.Fatalf("Queue Close 后仍有活跃资源：%+v", stats)
	}
	if stats.CleanedTotal != 2 {
		t.Fatalf("CleanedTotal = %d，期望 2", stats.CleanedTotal)
	}
	if _, err := queue.ScheduleAfter(time.Second); !errors.Is(err, ErrDeadlineQueueClosed) {
		t.Fatalf("关闭后 ScheduleAfter() error = %v", err)
	}
	if _, err := queue.DrainExpired(nil, 1); !errors.Is(err, ErrDeadlineQueueClosed) {
		t.Fatalf("关闭后 DrainExpired() error = %v", err)
	}
	select {
	case _, open := <-queue.ExpiredSignal():
		if open {
			t.Fatal("Queue Close 后通知 Channel 仍打开")
		}
	default:
		t.Fatal("Queue Close 后通知 Channel 没有关闭")
	}
}

func TestEngineCloseImplicitlyCleansEveryQueue(t *testing.T) {
	engine, first, clock, wake := newFakeTimerEngine(t, false)
	second, err := engine.NewDeadlineQueue()
	if err != nil {
		t.Fatalf("NewDeadlineQueue() error = %v", err)
	}

	// 两条 Queue 同时保留已到期和待调度条目，Engine Close 必须取得全部最终清理权。
	if _, err := first.ScheduleAfter(TickDuration); err != nil {
		t.Fatalf("first short ScheduleAfter() error = %v", err)
	}
	if _, err := first.ScheduleAfter(time.Hour); err != nil {
		t.Fatalf("first long ScheduleAfter() error = %v", err)
	}
	if _, err := second.ScheduleAfter(TickDuration); err != nil {
		t.Fatalf("second short ScheduleAfter() error = %v", err)
	}
	if _, err := second.ScheduleAfter(time.Hour); err != nil {
		t.Fatalf("second long ScheduleAfter() error = %v", err)
	}
	advanceFake(clock, wake, TickDuration)
	waitForStats(t, engine, func(stats Stats) bool { return stats.Expired == 2 })

	if err := engine.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	stats := engine.Stats()
	if !stats.Closed || stats.Running ||
		stats.Scheduled != 0 || stats.Expired != 0 || stats.Queues != 0 {
		t.Fatalf("Engine Close 后仍有活跃资源：%+v", stats)
	}
	if stats.CleanedTotal != 4 {
		t.Fatalf("CleanedTotal = %d，期望 4", stats.CleanedTotal)
	}
	engine.mu.Lock()
	if engine.entries != nil || engine.queues != nil {
		engine.mu.Unlock()
		t.Fatal("Engine Close 后仍持有高水位 Map")
	}
	engine.mu.Unlock()
	for index, queue := range []*DeadlineQueue{first, second} {
		select {
		case _, open := <-queue.ExpiredSignal():
			if open {
				t.Fatalf("Queue[%d] 通知 Channel 仍打开", index)
			}
		default:
			t.Fatalf("Queue[%d] 通知 Channel 没有关闭", index)
		}
	}
}

func TestEntryPoolReuseAndReferenceClearing(t *testing.T) {
	engine, queue, _, _ := newFakeTimerEngine(t, true)

	// 第一次登记并取消，把唯一条目完整清零后放入 Engine 私有 Pool。
	first, err := queue.ScheduleAfter(time.Hour)
	if err != nil {
		t.Fatalf("第一次 ScheduleAfter() error = %v", err)
	}
	engine.mu.Lock()
	firstEntry := engine.entries[first]
	engine.mu.Unlock()
	if !queue.Cancel(first) {
		t.Fatal("第一次 Cancel() 失败")
	}
	if firstEntry.queue != nil ||
		firstEntry.wheelPrev != nil ||
		firstEntry.wheelNext != nil ||
		firstEntry.queuePrev != nil ||
		firstEntry.queueNext != nil ||
		firstEntry.state != entryFree {
		t.Fatalf("回池条目没有完整清零：%+v", firstEntry)
	}

	// 同一测试 P 上再次登记应优先复用条目；即使 sync.Pool 丢弃对象，统计仍需自洽。
	second, err := queue.ScheduleAfter(time.Hour)
	if err != nil {
		t.Fatalf("第二次 ScheduleAfter() error = %v", err)
	}
	if !queue.Cancel(second) {
		t.Fatal("第二次 Cancel() 失败")
	}
	stats := engine.Stats()
	if stats.EntryAllocations == 0 ||
		stats.EntryReleases != 2 ||
		stats.EntryAllocations+stats.EntryReuses != 2 {
		t.Fatalf("对象池统计错误：%+v", stats)
	}
}

func TestInvalidLifecycleAndArguments(t *testing.T) {
	// New 必须拒绝缺失的实例依赖。
	if _, err := New(Options{}); !errs.IsCode(err, errs.CodeInvalidArgument) {
		t.Fatalf("空 Options error = %v", err)
	}
	clock := &fakeClock{now: time.Unix(1_000, 0)}
	if _, err := New(Options{Clock: clock}); !errs.IsCode(err, errs.CodeInvalidArgument) {
		t.Fatalf("nil WakeSource error = %v", err)
	}

	// Created 状态不能创建 Queue；重复 Start 也不能产生第二个 goroutine。
	wake := newFakeWakeSource()
	engine, err := New(Options{Clock: clock, WakeSource: wake})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if _, err := engine.NewDeadlineQueue(); !errs.IsCode(err, errs.CodeInvalidArgument) {
		t.Fatalf("启动前 NewDeadlineQueue() error = %v", err)
	}
	if err := engine.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := engine.Start(); !errs.IsCode(err, errs.CodeInvalidArgument) {
		t.Fatalf("重复 Start() error = %v", err)
	}
	queue, err := engine.NewDeadlineQueue()
	if err != nil {
		t.Fatalf("NewDeadlineQueue() error = %v", err)
	}
	if _, err := queue.ScheduleAfter(-time.Nanosecond); !errs.IsCode(err, errs.CodeInvalidArgument) {
		t.Fatalf("负延迟 error = %v", err)
	}
	if _, err := queue.DrainExpired(nil, 0); !errs.IsCode(err, errs.CodeInvalidArgument) {
		t.Fatalf("非法 limit error = %v", err)
	}
	if queue.Cancel(InvalidDeadlineID) {
		t.Fatal("取消零 ID 意外成功")
	}

	// Close 幂等，并使所有新建和登记操作返回稳定关闭错误。
	if err := engine.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := engine.Close(); err != nil {
		t.Fatalf("重复 Close() error = %v", err)
	}
	if _, err := engine.NewDeadlineQueue(); !errors.Is(err, ErrEngineClosed) {
		t.Fatalf("关闭后 NewDeadlineQueue() error = %v", err)
	}
}

func TestMaximumDurationDoesNotOverflow(t *testing.T) {
	engine, queue, _, _ := newFakeTimerEngine(t, false)

	// time.Duration 最大值向上取整会落到最后一个不完整 Tick，不能乘法环绕成负数。
	id, err := queue.ScheduleAfter(time.Duration(math.MaxInt64))
	if err != nil {
		t.Fatalf("最大 Duration ScheduleAfter() error = %v", err)
	}
	engine.mu.Lock()
	entry := engine.entries[id]
	level := int(entry.level)
	tick := entry.deadlineTick
	engine.mu.Unlock()
	if level != LevelCount-1 {
		t.Fatalf("最大 Duration 位于 L%d，期望 L%d", level, LevelCount-1)
	}
	if got := durationAtTick(tick); got != time.Duration(math.MaxInt64) {
		t.Fatalf("最后 Tick Duration = %v，期望 MaxInt64", got)
	}
}

func TestConcurrentScheduleCancelAndClose(t *testing.T) {
	engine, queue, _, _ := newFakeTimerEngine(t, false)
	const (
		workers    = 16
		iterations = 1000
	)

	// 多 goroutine 反复登记和取消，覆盖 ID、Map、桶、Queue 链表和对象池竞态。
	var wait sync.WaitGroup
	wait.Add(workers)
	for worker := 0; worker < workers; worker++ {
		go func() {
			defer wait.Done()
			for iteration := 0; iteration < iterations; iteration++ {
				id, err := queue.ScheduleAfter(time.Hour)
				if err != nil {
					// Queue 可能被测试主路径关闭；关闭之后退出本 worker 即可。
					if errors.Is(err, ErrDeadlineQueueClosed) || errors.Is(err, ErrEngineClosed) {
						return
					}
					t.Errorf("ScheduleAfter() error = %v", err)
					return
				}
				queue.Cancel(id)
			}
		}()
	}

	// 与剩余 worker 并发关闭，验证 Close 取得唯一最终清理权。
	time.Sleep(time.Millisecond)
	queue.Close()
	wait.Wait()
	stats := engine.Stats()
	if stats.Scheduled != 0 || stats.Expired != 0 || stats.Queues != 0 {
		t.Fatalf("并发关闭后仍有资源：%+v", stats)
	}
}

func TestIndependentEnginesDoNotShareState(t *testing.T) {
	firstEngine, firstQueue, _, _ := newFakeTimerEngine(t, false)
	secondEngine, secondQueue, _, _ := newFakeTimerEngine(t, false)

	// 两个 Engine 都从 ID 1 开始，但取消和统计必须完全隔离。
	firstID, err := firstQueue.ScheduleAfter(time.Hour)
	if err != nil {
		t.Fatalf("first ScheduleAfter() error = %v", err)
	}
	secondID, err := secondQueue.ScheduleAfter(time.Hour)
	if err != nil {
		t.Fatalf("second ScheduleAfter() error = %v", err)
	}
	if firstID != 1 || secondID != 1 {
		t.Fatalf("独立 Engine 首个 ID = %d/%d，期望都为 1", firstID, secondID)
	}
	firstQueue.Cancel(firstID)
	if firstEngine.Stats().Scheduled != 0 || secondEngine.Stats().Scheduled != 1 {
		t.Fatal("独立 Engine 的 Scheduled 统计相互污染")
	}
}

func TestBackloggedQueueDoesNotBlockOtherQueues(t *testing.T) {
	engine, backlog, clock, wake := newFakeTimerEngine(t, false)
	healthy, err := engine.NewDeadlineQueue()
	if err != nil {
		t.Fatalf("NewDeadlineQueue() error = %v", err)
	}

	// 第一条 Queue 故意积压大量 ID，第二条 Queue 只登记一个相同 Tick 的 Deadline。
	const backlogCount = 10_000
	for index := 0; index < backlogCount; index++ {
		if _, err := backlog.ScheduleAfter(TickDuration); err != nil {
			t.Fatalf("backlog ScheduleAfter() error = %v", err)
		}
	}
	healthyID, err := healthy.ScheduleAfter(TickDuration)
	if err != nil {
		t.Fatalf("healthy ScheduleAfter() error = %v", err)
	}
	advanceFake(clock, wake, TickDuration)
	waitForStats(t, engine, func(stats Stats) bool {
		return stats.Expired == backlogCount+1
	})

	// 不消费 backlog 也必须能够独立收到并取出 healthy Queue 的 ID。
	got := drainAfterSignal(t, healthy, 1)
	if len(got) != 1 || got[0] != healthyID {
		t.Fatalf("健康 Queue 到期 = %v，期望 [%d]", got, healthyID)
	}
	if stats := engine.Stats(); stats.Expired != backlogCount {
		t.Fatalf("健康 Queue Drain 后积压数量 = %d，期望 %d", stats.Expired, backlogCount)
	}
}

func TestCancelAndExpireRaceHasSingleOwner(t *testing.T) {
	engine, queue, clock, wake := newFakeTimerEngine(t, false)
	const count = 5_000
	ids := make([]DeadlineID, count)
	for index := range ids {
		id, err := queue.ScheduleAfter(TickDuration)
		if err != nil {
			t.Fatalf("ScheduleAfter() error = %v", err)
		}
		ids[index] = id
	}

	// 取消方与到期推进同时开始；Engine 单锁必须为每个 ID 裁决唯一完成路径。
	start := make(chan struct{})
	canceled := make(chan int, 1)
	go func() {
		<-start
		total := 0
		for _, id := range ids {
			if queue.Cancel(id) {
				total++
			}
		}
		canceled <- total
	}()
	close(start)
	advanceFake(clock, wake, TickDuration)
	canceledCount := <-canceled
	waitForStats(t, engine, func(stats Stats) bool {
		return int(stats.CanceledTotal+stats.ExpiredTotal) == count
	})

	// 到期列表只包含取消竞争失败的 ID，且任何 ID 都不能重复交付。
	expired := make([]DeadlineID, 0, count-canceledCount)
	for len(expired) < count-canceledCount {
		select {
		case _, open := <-queue.ExpiredSignal():
			if !open {
				t.Fatal("竞争期间 Queue 意外关闭")
			}
		case <-time.After(2 * time.Second):
			t.Fatal("等待竞争到期通知超时")
		}
		var err error
		expired, err = queue.DrainExpired(expired, count)
		if err != nil {
			t.Fatalf("DrainExpired() error = %v", err)
		}
	}
	seen := make(map[DeadlineID]struct{}, len(expired))
	for _, id := range expired {
		if _, duplicate := seen[id]; duplicate {
			t.Fatalf("DeadlineID %d 被重复交付", id)
		}
		seen[id] = struct{}{}
	}
	if canceledCount+len(expired) != count {
		t.Fatalf("唯一完成数量 = cancel:%d expire:%d，期望 %d", canceledCount, len(expired), count)
	}
}

func TestEmptyEngineDoesNotPoll(t *testing.T) {
	engine, _, _, _ := newFakeTimerEngine(t, false)

	// Engine 没有 Deadline 时应停用唤醒源；短暂让出调度后统计仍必须保持零 Timer 唤醒。
	deadline := time.Now().Add(50 * time.Millisecond)
	for time.Now().Before(deadline) {
		runtime.Gosched()
	}
	stats := engine.Stats()
	if stats.TimerWakeups != 0 || stats.EmptyWakeups != 0 {
		t.Fatalf("空 Engine 发生周期唤醒：%+v", stats)
	}
}

func TestEarlyTimerWakeIsCountedWithoutExpiry(t *testing.T) {
	engine, queue, _, wake := newFakeTimerEngine(t, false)
	if _, err := queue.ScheduleAfter(time.Hour); err != nil {
		t.Fatalf("ScheduleAfter() error = %v", err)
	}

	// 不推进 Clock 就触发底层 Timer，必须识别为空唤醒且不能提前交付。
	wake.Fire()
	stats := waitForStats(t, engine, func(stats Stats) bool {
		return stats.EmptyWakeups > 0
	})
	if stats.Expired != 0 || stats.Scheduled != 1 {
		t.Fatalf("提前唤醒改变了 Deadline：%+v", stats)
	}
}
