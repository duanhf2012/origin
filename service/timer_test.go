package service

import (
	"context"
	"errors"
	"math"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/duanhf2012/origin/v3/errs"
	"github.com/duanhf2012/origin/v3/internal/timerwheel"
)

// timerTestClock 提供带锁的确定性时间，使 Timer 测试不依赖真实 Sleep。
type timerTestClock struct {
	mu  sync.Mutex
	now time.Time
}

func (clock *timerTestClock) Now() time.Time {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return clock.now
}

func (clock *timerTestClock) Advance(delta time.Duration) {
	clock.mu.Lock()
	clock.now = clock.now.Add(delta)
	clock.mu.Unlock()
}

// timerTestWakeSource 由测试显式触发时间轮唤醒；Reset 只记录最新等待时长。
type timerTestWakeSource struct {
	channel chan time.Time
	mu      sync.Mutex
	delay   time.Duration
}

func newTimerTestWakeSource() *timerTestWakeSource {
	return &timerTestWakeSource{channel: make(chan time.Time, 1)}
}

func (source *timerTestWakeSource) C() <-chan time.Time {
	return source.channel
}

func (source *timerTestWakeSource) Reset(delay time.Duration) {
	source.mu.Lock()
	source.delay = delay
	source.mu.Unlock()
}

func (source *timerTestWakeSource) Stop() {}

func (source *timerTestWakeSource) Fire(now time.Time) {
	select {
	case source.channel <- now:
	default:
	}
}

// timerFixture 集中拥有真实 Scheduler、可控时间轮和测试 Runtime。
type timerFixture struct {
	service *testService
	runtime *schedulerTestRuntime
	engine  *timerwheel.Engine
	clock   *timerTestClock
	wake    *timerTestWakeSource
}

func newTimerFixture(t testing.TB, timerLimit int) *timerFixture {
	t.Helper()
	return newTimerFixtureWithConfig(t, DefaultSchedulerConfig(), timerLimit, true)
}

func newPreparedTimerFixture(t testing.TB, timerLimit int) *timerFixture {
	t.Helper()
	return newTimerFixtureWithConfig(t, DefaultSchedulerConfig(), timerLimit, false)
}

func newTimerFixtureWithConfig(
	t testing.TB,
	config SchedulerConfig,
	timerLimit int,
	activate bool,
) *timerFixture {
	t.Helper()

	// 从固定带单调等价语义的墙上时间开始，便于后续 Cron 测试复用。
	clock := &timerTestClock{
		now: time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC),
	}
	wake := newTimerTestWakeSource()
	engine, err := timerwheel.New(timerwheel.Options{
		Clock:      clock,
		WakeSource: wake,
	})
	if err != nil {
		t.Fatalf("timerwheel.New() error = %v", err)
	}
	if err := engine.Start(); err != nil {
		t.Fatalf("TimerEngine.Start() error = %v", err)
	}

	target := &testService{}
	runtimeState := &schedulerTestRuntime{
		nodeID:        "game-1",
		name:          "PlayerService",
		timerLimit:    timerLimit,
		timerLocation: time.UTC,
		nowSource:     clock.Now,
	}
	runtimeState.state.Store(uint32(StateStarting))
	if err := BindRuntime(target, runtimeState); err != nil {
		_ = engine.Close()
		t.Fatalf("BindRuntime() error = %v", err)
	}
	if err := PrepareScheduler(target, config, engine); err != nil {
		_ = engine.Close()
		t.Fatalf("PrepareScheduler() error = %v", err)
	}
	if activate {
		runtimeState.state.Store(uint32(StateRunning))
		if err := ActivateScheduler(target); err != nil {
			_ = engine.Close()
			t.Fatalf("ActivateScheduler() error = %v", err)
		}
	}

	fixture := &timerFixture{
		service: target,
		runtime: runtimeState,
		engine:  engine,
		clock:   clock,
		wake:    wake,
	}
	t.Cleanup(func() {
		runtimeState.state.Store(uint32(StateStopping))
		stopContext, cancel := context.WithTimeout(context.Background(), schedulerTestTimeout)
		_ = StopScheduler(stopContext, target)
		cancel()
		runtimeState.state.Store(uint32(StateStopped))
		_ = engine.Close()
	})
	return fixture
}

func advanceTimerFixture(
	t testing.TB,
	fixture *timerFixture,
	delta time.Duration,
) {
	t.Helper()
	fixture.clock.Advance(delta)
	fixture.wake.Fire(fixture.clock.Now())
}

func waitForTimerStats(
	t testing.TB,
	target *testService,
	predicate func(TimerStats) bool,
) TimerStats {
	t.Helper()
	deadline := time.Now().Add(schedulerTestTimeout)
	for time.Now().Before(deadline) {
		stats := target.TimerStats()
		if predicate(stats) {
			return stats
		}
		time.Sleep(time.Millisecond)
	}
	stats := target.TimerStats()
	t.Fatalf("等待 TimerStats 超时，当前值 = %+v", stats)
	return stats
}

var noopTimerCallback TimerFunc = func(context.Context, TimerID) {}

// TestRebaseTimersValidatesTargetAndLifecycle 固定框架内部入口对空对象、尚未准备的
// Service 和已关闭准入的 Scheduler 返回稳定结果。
func TestRebaseTimersValidatesTargetAndLifecycle(t *testing.T) {
	if err := RebaseTimers(nil); !errs.IsCode(err, errs.CodeInvalidArgument) {
		t.Fatalf("RebaseTimers(nil) error = %v", err)
	}
	var typedNil *testService
	if err := RebaseTimers(typedNil); !errs.IsCode(err, errs.CodeInvalidArgument) {
		t.Fatalf("RebaseTimers(typed nil) error = %v", err)
	}
	// 未进入 OnStart 的绑定 Service 没有 Scheduler 和 Timer，因此是无工作可做的成功。
	if err := RebaseTimers(&testService{}); err != nil {
		t.Fatalf("RebaseTimers(unprepared) error = %v", err)
	}

	fixture := newTimerFixture(t, 8)
	if err := BeginStopScheduler(fixture.service); err != nil {
		t.Fatalf("BeginStopScheduler() error = %v", err)
	}
	if err := RebaseTimers(fixture.service); !errors.Is(err, errs.ErrServiceStopping) {
		t.Fatalf("RebaseTimers(Draining) error = %v", err)
	}
}

// TestBusinessTimerPauseUsesNodeTime 防止 After/Ticker 的剩余时间仍从真实 TimerEngine 读取；
// 游戏时间已经前进时，暂停必须保存逻辑目标与当前逻辑时间之间的剩余量。
func TestBusinessTimerPauseUsesNodeTime(t *testing.T) {
	fixture := newTimerFixture(t, 8)
	fired := make(chan struct{}, 1)
	id := fixture.service.AfterFunc(time.Hour, func(context.Context, TimerID) {
		fired <- struct{}{}
	})
	if id == InvalidTimerID {
		t.Fatal("AfterFunc() 创建失败")
	}

	// 只推进 Node 逻辑时间，不推进真实 Engine；暂停后恢复应只再等待剩余 30 分钟。
	if err := fixture.runtime.AddTime(30 * time.Minute); err != nil {
		t.Fatalf("AddTime() error = %v", err)
	}
	if !fixture.service.PauseTimer(id) || !fixture.service.ResumeTimer(id) {
		t.Fatal("PauseTimer()/ResumeTimer() 失败")
	}
	advanceTimerFixture(t, fixture, 29*time.Minute)
	select {
	case <-fired:
		t.Fatal("AfterFunc 在逻辑剩余时间前触发")
	default:
	}
	advanceTimerFixture(t, fixture, time.Minute)
	receive(t, fired)
}

// TestTickerContinuationUsesNodeTime 防止周期回调完成后忽略回调中发生的 Node 时间跳跃；
// 跳过的名义周期必须合并为一次并从新逻辑时间继续。
func TestTickerContinuationUsesNodeTime(t *testing.T) {
	fixture := newTimerFixture(t, 8)
	fired := make(chan struct{}, 1)
	id := fixture.service.NewTicker(time.Minute, func(context.Context, TimerID) {
		if err := fixture.runtime.AddTime(5 * time.Minute); err != nil {
			t.Errorf("AddTime() error = %v", err)
		}
		fired <- struct{}{}
	})
	if id == InvalidTimerID {
		t.Fatal("NewTicker() 创建失败")
	}

	advanceTimerFixture(t, fixture, time.Minute)
	receive(t, fired)
	stats := waitForTimerStats(t, fixture.service, func(stats TimerStats) bool {
		return stats.Scheduled == 1 && stats.Running == 0
	})
	if stats.CoalescedTotal != 5 {
		t.Fatalf("Ticker CoalescedTotal = %d, want 5", stats.CoalescedTotal)
	}
	if !fixture.service.CancelTimer(&id) {
		t.Fatal("CancelTimer() 失败")
	}
}

// TestGameTimeRebaseMakesOverdueAfterReady 防止 Node 时间只修改 Now 返回值而不唤醒已经登记的
// 业务 Deadline；向前跨过名义点后，After 必须经过时间轮异步进入 Service Ready 队列。
func TestGameTimeRebaseMakesOverdueAfterReady(t *testing.T) {
	fixture := newTimerFixture(t, 8)
	fired := make(chan struct{}, 1)
	id := fixture.service.AfterFunc(time.Hour, func(context.Context, TimerID) {
		fired <- struct{}{}
	})
	if id == InvalidTimerID {
		t.Fatal("AfterFunc() 创建失败")
	}

	if err := fixture.runtime.AddTime(2 * time.Hour); err != nil {
		t.Fatalf("AddTime() error = %v", err)
	}
	if err := RebaseTimers(fixture.service); err != nil {
		t.Fatalf("RebaseTimers() error = %v", err)
	}
	// 零延迟仍登记到时间轮的下一 Tick，不允许 RebaseTimers 调用栈同步执行用户回调。
	select {
	case <-fired:
		t.Fatal("RebaseTimers() 同步执行了业务回调")
	default:
	}
	advanceTimerFixture(t, fixture, timerwheel.TickDuration)
	receive(t, fired)
}

// TestGameTimeRebaseExtendsScheduledTimer 防止向后调整后旧真实 Deadline 继续提前触发。
func TestGameTimeRebaseExtendsScheduledTimer(t *testing.T) {
	fixture := newTimerFixture(t, 8)
	fired := make(chan struct{}, 1)
	id := fixture.service.AfterFunc(time.Hour, func(context.Context, TimerID) {
		fired <- struct{}{}
	})
	if id == InvalidTimerID {
		t.Fatal("AfterFunc() 创建失败")
	}

	if err := fixture.runtime.AddTime(-time.Hour); err != nil {
		t.Fatalf("AddTime() error = %v", err)
	}
	if err := RebaseTimers(fixture.service); err != nil {
		t.Fatalf("RebaseTimers() error = %v", err)
	}
	advanceTimerFixture(t, fixture, time.Hour)
	select {
	case <-fired:
		t.Fatal("AfterFunc 在向后调整后的逻辑目标前触发")
	default:
	}
	advanceTimerFixture(t, fixture, time.Hour)
	receive(t, fired)
}

// TestGameTimeRebasePreservesScheduledDeadlineID 防止大批量逻辑时间重排为每个 Timer
// 生成新 DeadlineID，从而引起时间轮与 Scheduler Map 换键、扩容和额外 GC 压力。
func TestGameTimeRebasePreservesScheduledDeadlineID(t *testing.T) {
	fixture := newTimerFixture(t, 8)
	id := fixture.service.AfterFunc(time.Hour, noopTimerCallback)
	if id == InvalidTimerID {
		t.Fatal("AfterFunc() 创建失败")
	}
	scheduler := fixture.service.scheduler.Load()
	scheduler.mu.Lock()
	before := scheduler.timers[id].deadlineID
	scheduler.mu.Unlock()

	if err := fixture.runtime.AddTime(-time.Hour); err != nil {
		t.Fatalf("AddTime() error = %v", err)
	}
	if err := RebaseTimers(fixture.service); err != nil {
		t.Fatalf("RebaseTimers() error = %v", err)
	}
	scheduler.mu.Lock()
	after := scheduler.timers[id].deadlineID
	scheduler.mu.Unlock()
	if after != before {
		t.Fatalf("RebaseTimers() deadlineID = %d, want preserved %d", after, before)
	}
	if !fixture.service.CancelTimer(&id) {
		t.Fatal("CancelTimer() 失败")
	}
}

// TestGameTimeRebaseReplacesAlreadyExpiredDeadline 模拟时间轮已经取得旧 ID、
// Scheduler watcher 尚未处理 Binding 的竞争窗口；原地重排返回 false 后必须换用新 ID。
func TestGameTimeRebaseReplacesAlreadyExpiredDeadline(t *testing.T) {
	fixture := newTimerFixture(t, 8)
	id := fixture.service.AfterFunc(time.Hour, noopTimerCallback)
	if id == InvalidTimerID {
		t.Fatal("AfterFunc() 创建失败")
	}
	scheduler := fixture.service.scheduler.Load()
	scheduler.mu.Lock()
	timer := scheduler.timers[id]
	before := timer.deadlineID
	if !scheduler.deadlineQueue.Cancel(before) {
		scheduler.mu.Unlock()
		t.Fatal("模拟旧 Deadline 离开时间轮失败")
	}
	// 故意保留 Binding 和 Timer.deadlineID，它们正是 watcher 取得 Scheduler 锁前的可见状态。
	scheduler.mu.Unlock()

	if err := fixture.runtime.AddTime(-time.Hour); err != nil {
		t.Fatalf("AddTime() error = %v", err)
	}
	if err := RebaseTimers(fixture.service); err != nil {
		t.Fatalf("RebaseTimers() error = %v", err)
	}
	scheduler.mu.Lock()
	after := scheduler.timers[id].deadlineID
	_, oldBindingExists := scheduler.deadlineBindings[before]
	_, newBindingExists := scheduler.deadlineBindings[after]
	scheduler.mu.Unlock()
	if after == before || oldBindingExists || !newBindingExists {
		t.Fatalf(
			"竞争回退状态: before=%d after=%d oldBinding=%t newBinding=%t",
			before,
			after,
			oldBindingExists,
			newBindingExists,
		)
	}
	if !fixture.service.CancelTimer(&id) {
		t.Fatal("CancelTimer() 失败")
	}
}

// TestGameTimeRebaseCoalescesTicker 验证向前跨过多个周期时只执行一次，
// 且后续节拍从新的 Node 逻辑时间之后继续。
func TestGameTimeRebaseCoalescesTicker(t *testing.T) {
	fixture := newTimerFixture(t, 8)
	fired := make(chan struct{}, 2)
	id := fixture.service.NewTicker(time.Minute, func(context.Context, TimerID) {
		fired <- struct{}{}
	})
	if id == InvalidTimerID {
		t.Fatal("NewTicker() 创建失败")
	}

	if err := fixture.runtime.AddTime(3 * time.Minute); err != nil {
		t.Fatalf("AddTime() error = %v", err)
	}
	if err := RebaseTimers(fixture.service); err != nil {
		t.Fatalf("RebaseTimers() error = %v", err)
	}
	advanceTimerFixture(t, fixture, timerwheel.TickDuration)
	receive(t, fired)
	stats := waitForTimerStats(t, fixture.service, func(stats TimerStats) bool {
		return stats.Running == 0 && stats.Scheduled == 1
	})
	if stats.CoalescedTotal != 2 {
		t.Fatalf("Ticker CoalescedTotal = %d, want 2", stats.CoalescedTotal)
	}
	select {
	case <-fired:
		t.Fatal("向前跳时补执行了多个 Ticker 历史回调")
	default:
	}
	if !fixture.service.CancelTimer(&id) {
		t.Fatal("CancelTimer() 失败")
	}
}

// TestGameTimeRebaseSkipsPausedTimer 固定暂停期间的时间跳跃不会消耗已保存的逻辑剩余时间。
func TestGameTimeRebaseSkipsPausedTimer(t *testing.T) {
	fixture := newTimerFixture(t, 8)
	fired := make(chan struct{}, 1)
	id := fixture.service.AfterFunc(time.Hour, func(context.Context, TimerID) {
		fired <- struct{}{}
	})
	if id == InvalidTimerID || !fixture.service.PauseTimer(id) {
		t.Fatal("AfterFunc()/PauseTimer() 失败")
	}
	if err := fixture.runtime.AddTime(24 * time.Hour); err != nil {
		t.Fatalf("AddTime() error = %v", err)
	}
	if err := RebaseTimers(fixture.service); err != nil {
		t.Fatalf("RebaseTimers() error = %v", err)
	}
	if !fixture.service.ResumeTimer(id) {
		t.Fatal("ResumeTimer() 失败")
	}
	advanceTimerFixture(t, fixture, 59*time.Minute)
	select {
	case <-fired:
		t.Fatal("暂停 Timer 的剩余时间被 Node 时间跳跃消耗")
	default:
	}
	advanceTimerFixture(t, fixture, time.Minute)
	receive(t, fired)
}

// TestGameTimeRebaseDoesNotRetractDueTimer 防止向后调时把 OnStart 期间已经到期的
// DuePending 工作撤回；Scheduler 激活后必须继续提升并执行该回调。
func TestGameTimeRebaseDoesNotRetractDueTimer(t *testing.T) {
	fixture := newPreparedTimerFixture(t, 8)
	fired := make(chan struct{}, 1)
	id := fixture.service.AfterFunc(time.Minute, func(context.Context, TimerID) {
		fired <- struct{}{}
	})
	if id == InvalidTimerID {
		t.Fatal("AfterFunc() 创建失败")
	}
	advanceTimerFixture(t, fixture, time.Minute)
	waitForTimerStats(t, fixture.service, func(stats TimerStats) bool {
		return stats.DuePending == 1
	})
	if err := fixture.runtime.AddTime(-time.Hour); err != nil {
		t.Fatalf("AddTime() error = %v", err)
	}
	if err := RebaseTimers(fixture.service); err != nil {
		t.Fatalf("RebaseTimers() error = %v", err)
	}
	fixture.runtime.state.Store(uint32(StateRunning))
	if err := ActivateScheduler(fixture.service); err != nil {
		t.Fatalf("ActivateScheduler() error = %v", err)
	}
	receive(t, fired)
}

// TestGameTimeDoesNotExpireInfrastructureDeadline 证明 Node 游戏时间只改变业务 Timer，
// 同一 TimerEngine 上的 RPC/Await 类基础设施 Deadline 仍按真实单调时间等待。
func TestGameTimeDoesNotExpireInfrastructureDeadline(t *testing.T) {
	fixture := newTimerFixture(t, 8)
	queue, err := fixture.engine.NewDeadlineQueue()
	if err != nil {
		t.Fatalf("NewDeadlineQueue() error = %v", err)
	}
	defer queue.Close()
	deadlineID, err := queue.ScheduleAfter(time.Hour)
	if err != nil {
		t.Fatalf("ScheduleAfter() error = %v", err)
	}

	if err := fixture.runtime.AddTime(24 * time.Hour); err != nil {
		t.Fatalf("AddTime() error = %v", err)
	}
	if err := RebaseTimers(fixture.service); err != nil {
		t.Fatalf("RebaseTimers() error = %v", err)
	}
	advanceTimerFixture(t, fixture, timerwheel.TickDuration)
	select {
	case <-queue.ExpiredSignal():
		t.Fatal("Node 游戏时间使基础设施 Deadline 提前到期")
	default:
	}

	advanceTimerFixture(t, fixture, time.Hour)
	select {
	case <-queue.ExpiredSignal():
		ids, drainErr := queue.DrainExpired(nil, 1)
		if drainErr != nil {
			t.Fatalf("DrainExpired() error = %v", drainErr)
		}
		if len(ids) != 1 || ids[0] != deadlineID {
			t.Fatalf("expired IDs = %v, want [%d]", ids, deadlineID)
		}
	case <-time.After(schedulerTestTimeout):
		t.Fatal("等待真实基础设施 Deadline 超时")
	}
}

func TestAfterFuncRunsAsServiceTaskAndReleasesQuota(t *testing.T) {
	fixture := newTimerFixture(t, 8)
	ran := make(chan TimerID, 1)

	// 零延迟仍必须经过 M8 后续 Tick 和 M9 Runner，不能在创建调用栈同步执行。
	id := fixture.service.AfterFunc(0, func(
		_ context.Context,
		callbackID TimerID,
	) {
		ran <- callbackID
	})
	if id == InvalidTimerID {
		t.Fatal("AfterFunc 创建失败")
	}
	select {
	case <-ran:
		t.Fatal("AfterFunc 在创建调用栈同步执行")
	default:
	}

	advanceTimerFixture(t, fixture, timerwheel.TickDuration)
	if callbackID := receive(t, ran); callbackID != id {
		t.Fatalf("callback TimerID = %d，期望 %d", callbackID, id)
	}
	waitForTimerStats(t, fixture.service, func(stats TimerStats) bool {
		return stats.Active == 0 && stats.CompletedTotal == 1
	})
	if active := fixture.runtime.active.Load(); active != 0 {
		t.Fatalf("AfterFunc 完成后 Node 活跃额度 = %d", active)
	}
}

func TestAfterFuncRejectsInvalidStageArgumentsAndQuota(t *testing.T) {
	// 未绑定的零值 Service、负延迟和空回调都必须稳定返回零 ID。
	var unbound Service
	if unbound.AfterFunc(time.Second, noopTimerCallback) != InvalidTimerID {
		t.Fatal("未绑定 Service 创建 Timer 成功")
	}
	fixture := newTimerFixture(t, 1)
	if fixture.service.AfterFunc(-time.Second, noopTimerCallback) != InvalidTimerID {
		t.Fatal("负延迟创建 Timer 成功")
	}
	if fixture.service.AfterFunc(time.Second, nil) != InvalidTimerID {
		t.Fatal("nil callback 创建 Timer 成功")
	}

	// Node 额度为一时第二个活跃 Timer 必须被拒绝；取消或自然完成后额度才可复用。
	first := fixture.service.AfterFunc(time.Hour, noopTimerCallback)
	if first == InvalidTimerID {
		t.Fatal("第一个 Timer 创建失败")
	}
	if second := fixture.service.AfterFunc(time.Hour, noopTimerCallback); second != InvalidTimerID {
		t.Fatalf("超过额度仍创建 TimerID %d", second)
	}
}

func TestTimerCreationRejectsPublishedStoppingState(t *testing.T) {
	fixture := newTimerFixture(t, 8)
	// 模拟创建方已经完成外层 Service 状态快照，但尚未取得 Scheduler 锁。停止方以同一把
	// Scheduler 锁先关闭准入后，旧快照不能再让 Timer 创建成功。
	if err := fixture.service.timerCreationError(); err != nil {
		t.Fatalf("Running timerCreationError() = %v", err)
	}
	if err := BeginStopScheduler(fixture.service); err != nil {
		t.Fatalf("BeginStopScheduler() error = %v", err)
	}
	fixture.runtime.state.Store(uint32(StateStopping))
	if fixture.service.AfterFunc(time.Second, noopTimerCallback) != InvalidTimerID {
		t.Fatal("Service Stopping 后 AfterFunc 仍创建成功")
	}
	if fixture.service.NewTicker(time.Second, noopTimerCallback) != InvalidTimerID {
		t.Fatal("Service Stopping 后 NewTicker 仍创建成功")
	}
	if id := fixture.service.scheduler.Load().createAfterTimer(
		time.Second,
		noopTimerCallback,
	); id != InvalidTimerID {
		t.Fatalf("旧状态快照在停止线性化后仍创建 Timer: id=%d", id)
	}
	if id, err := fixture.service.CronFunc(
		"* * * * *",
		noopTimerCallback,
	); id != InvalidTimerID || !errors.Is(err, errs.ErrServiceStopping) {
		t.Fatalf("Service Stopping 后 CronFunc() id=%d error=%v", id, err)
	}

	// 即使旧调用方在 Stop 完成、运行资源已释放后才取得锁，也只能返回无效 ID，不能访问
	// 已清空的 TimerEngine。
	if err := StopScheduler(context.Background(), fixture.service); err != nil {
		t.Fatalf("StopScheduler() error = %v", err)
	}
	fixture.runtime.state.Store(uint32(StateStopped))
	if id := fixture.service.scheduler.Load().createAfterTimer(
		time.Second,
		noopTimerCallback,
	); id != InvalidTimerID {
		t.Fatalf("Stopped Scheduler 仍创建 Timer: id=%d", id)
	}
}

func TestPreparedTimerExpiresOnlyAfterSchedulerActivation(t *testing.T) {
	fixture := newPreparedTimerFixture(t, 8)
	fired := make(chan struct{}, 1)
	id := fixture.service.AfterFunc(0, func(context.Context, TimerID) {
		fired <- struct{}{}
	})
	if id == InvalidTimerID {
		t.Fatal("OnStart 等价 Prepared 阶段不能创建 Timer")
	}

	advanceTimerFixture(t, fixture, timerwheel.TickDuration)
	select {
	case <-fired:
		t.Fatal("Activate 前执行了 Timer 回调")
	default:
	}

	fixture.runtime.state.Store(uint32(StateRunning))
	if err := ActivateScheduler(fixture.service); err != nil {
		t.Fatalf("ActivateScheduler() error = %v", err)
	}
	receive(t, fired)
}

func TestCancelTimerAlwaysClearsNonZeroCallerID(t *testing.T) {
	fixture := newTimerFixture(t, 8)
	id := fixture.service.AfterFunc(time.Hour, noopTimerCallback)
	if id == InvalidTimerID {
		t.Fatal("AfterFunc 创建失败")
	}

	if !fixture.service.CancelTimer(&id) || id != InvalidTimerID {
		t.Fatalf("CancelTimer() 成功路径 id = %d，期望清零", id)
	}
	if active := fixture.runtime.active.Load(); active != 0 {
		t.Fatalf("取消 Scheduled Timer 后 Node 活跃额度 = %d", active)
	}

	unknown := TimerID(42_424_242)
	if fixture.service.CancelTimer(&unknown) || unknown != InvalidTimerID {
		t.Fatalf("未知 ID 必须返回 false 且清零，id = %d", unknown)
	}
	var zero TimerID
	if fixture.service.CancelTimer(&zero) || zero != InvalidTimerID {
		t.Fatal("零 ID 不应取消成功")
	}
	if fixture.service.CancelTimer(nil) {
		t.Fatal("nil TimerID 指针不应取消成功")
	}
}

func TestPauseResumeAfterPreservesRemainingDelay(t *testing.T) {
	fixture := newTimerFixture(t, 8)
	fired := make(chan struct{}, 1)
	id := fixture.service.AfterFunc(time.Second, func(context.Context, TimerID) {
		fired <- struct{}{}
	})
	if id == InvalidTimerID {
		t.Fatal("AfterFunc 创建失败")
	}

	advanceTimerFixture(t, fixture, 400*time.Millisecond)
	if !fixture.service.PauseTimer(id) {
		t.Fatal("PauseTimer() 失败")
	}
	if fixture.service.PauseTimer(id) {
		t.Fatal("重复 PauseTimer() 成功")
	}

	advanceTimerFixture(t, fixture, time.Second)
	select {
	case <-fired:
		t.Fatal("暂停期间触发了回调")
	default:
	}

	if !fixture.service.ResumeTimer(id) {
		t.Fatal("ResumeTimer() 失败")
	}
	if fixture.service.ResumeTimer(id) {
		t.Fatal("重复 ResumeTimer() 成功")
	}
	advanceTimerFixture(t, fixture, 590*time.Millisecond)
	select {
	case <-fired:
		t.Fatal("剩余时间未到时提前触发")
	default:
	}
	advanceTimerFixture(t, fixture, 10*time.Millisecond)
	receive(t, fired)
}

func TestPauseReadyAfterPreventsCallbackUntilResume(t *testing.T) {
	fixture := newTimerFixture(t, 8)
	runnerStarted := make(chan struct{})
	releaseRunner := make(chan struct{})
	if err := fixture.service.DispatchAsync(func(context.Context) {
		close(runnerStarted)
		<-releaseRunner
	}); err != nil {
		t.Fatalf("DispatchAsync() error = %v", err)
	}
	receive(t, runnerStarted)

	fired := make(chan struct{}, 1)
	id := fixture.service.AfterFunc(0, func(context.Context, TimerID) {
		fired <- struct{}{}
	})
	advanceTimerFixture(t, fixture, timerwheel.TickDuration)
	waitForTimerStats(t, fixture.service, func(stats TimerStats) bool {
		return stats.Ready == 1
	})

	if !fixture.service.PauseTimer(id) {
		t.Fatal("Ready Timer PauseTimer() 失败")
	}
	close(releaseRunner)
	waitForTimerStats(t, fixture.service, func(stats TimerStats) bool {
		return stats.Paused == 1
	})
	select {
	case <-fired:
		t.Fatal("PauseTimer() 返回 true 后回调仍开始执行")
	default:
	}

	if !fixture.service.ResumeTimer(id) {
		t.Fatal("Ready Timer ResumeTimer() 失败")
	}
	advanceTimerFixture(t, fixture, timerwheel.TickDuration)
	receive(t, fired)
}

func TestCancelReadyAfterPreventsCallbackAndReleasesSlot(t *testing.T) {
	fixture := newTimerFixture(t, 8)
	runnerStarted := make(chan struct{})
	releaseRunner := make(chan struct{})
	if err := fixture.service.DispatchAsync(func(context.Context) {
		close(runnerStarted)
		<-releaseRunner
	}); err != nil {
		t.Fatalf("DispatchAsync() error = %v", err)
	}
	receive(t, runnerStarted)

	fired := make(chan struct{}, 1)
	id := fixture.service.AfterFunc(0, func(context.Context, TimerID) {
		fired <- struct{}{}
	})
	advanceTimerFixture(t, fixture, timerwheel.TickDuration)
	waitForTimerStats(t, fixture.service, func(stats TimerStats) bool {
		return stats.Ready == 1
	})

	if !fixture.service.CancelTimer(&id) || id != InvalidTimerID {
		t.Fatalf("Ready Timer CancelTimer() = false 或未清零，id = %d", id)
	}
	close(releaseRunner)
	waitForTimerStats(t, fixture.service, func(stats TimerStats) bool {
		return stats.Active == 0 &&
			stats.CanceledTotal == 1 &&
			fixture.runtime.active.Load() == 0
	})
	if active := fixture.runtime.active.Load(); active != 0 {
		t.Fatalf("取消 Ready Timer 后 Node 活跃额度 = %d", active)
	}
	select {
	case <-fired:
		t.Fatal("CancelTimer() 返回 true 后回调仍开始执行")
	default:
	}
}

func TestCancelRacesWithDeadlineDelivery(t *testing.T) {
	fixture := newTimerFixture(t, 1)
	scheduler := fixture.service.scheduler.Load()

	for iteration := 0; iteration < 100; iteration++ {
		fired := make(chan struct{}, 1)
		id := fixture.service.AfterFunc(0, func(context.Context, TimerID) {
			fired <- struct{}{}
		})
		if id == InvalidTimerID {
			t.Fatalf("第 %d 轮 AfterFunc 创建失败", iteration)
		}

		// 先持有 Scheduler 锁，再让时间轮完成到期交付。此时 watcher 与 Cancel 都会在同一
		// Scheduler 锁边界竞争，测试覆盖“到期已发生但尚未提交业务状态”的真实交错。
		expiredBefore := fixture.engine.Stats().ExpiredTotal
		scheduler.mu.Lock()
		fixture.clock.Advance(timerwheel.TickDuration)
		fixture.wake.Fire(fixture.clock.Now())
		deadline := time.Now().Add(schedulerTestTimeout)
		for fixture.engine.Stats().ExpiredTotal == expiredBefore {
			if time.Now().After(deadline) {
				scheduler.mu.Unlock()
				t.Fatalf("第 %d 轮等待时间轮到期超时", iteration)
			}
			runtime.Gosched()
		}

		cancelResult := make(chan bool, 1)
		go func() {
			cancelResult <- fixture.service.CancelTimer(&id)
		}()
		// 解锁后 watcher 和取消方由运行时决定先后；任一结果都必须满足线性化语义。
		scheduler.mu.Unlock()
		canceled := receive(t, cancelResult)
		if canceled {
			select {
			case <-fired:
				t.Fatalf("第 %d 轮 Cancel 成功后仍执行回调", iteration)
			default:
			}
		} else {
			receive(t, fired)
		}
		// callback 信号发生在用户函数返回前；等待 Active 归零后才能复用唯一 Node 额度
		// 进入下一轮，Race 模式下不能依赖 Runner 恰好先完成内部收尾。
		waitForTimerStats(t, fixture.service, func(stats TimerStats) bool {
			return stats.Active == 0 && fixture.runtime.active.Load() == 0
		})
		if id != InvalidTimerID {
			t.Fatalf("第 %d 轮 Cancel 后调用方 ID 未清零: %d", iteration, id)
		}
	}
}

func TestPauseRacesWithReadyTaskStart(t *testing.T) {
	fixture := newTimerFixture(t, 8)

	for iteration := 0; iteration < 50; iteration++ {
		blockerStarted := make(chan struct{})
		releaseBlocker := make(chan struct{})
		if err := fixture.service.DispatchAsync(func(context.Context) {
			close(blockerStarted)
			<-releaseBlocker
		}); err != nil {
			t.Fatalf("第 %d 轮 blocker DispatchAsync() error = %v", iteration, err)
		}
		receive(t, blockerStarted)

		fired := make(chan struct{}, 1)
		id := fixture.service.AfterFunc(0, func(context.Context, TimerID) {
			fired <- struct{}{}
		})
		if id == InvalidTimerID {
			t.Fatalf("第 %d 轮 AfterFunc 创建失败", iteration)
		}
		advanceTimerFixture(t, fixture, timerwheel.TickDuration)
		waitForTimerStats(t, fixture.service, func(stats TimerStats) bool {
			return stats.Ready == 1
		})

		// 同一个屏障同时释放前序任务并提交 Pause，使 Pause 与 Runner 取得下一任务执行槽
		// 在 Scheduler 锁上真实竞争。Pause 成功则回调不能开始；Runner 先开始则 Pause 必须失败。
		raceStart := make(chan struct{})
		pauseResult := make(chan bool, 1)
		go func() {
			<-raceStart
			pauseResult <- fixture.service.PauseTimer(id)
		}()
		go func() {
			<-raceStart
			close(releaseBlocker)
		}()
		close(raceStart)

		if receive(t, pauseResult) {
			waitForTimerStats(t, fixture.service, func(stats TimerStats) bool {
				return stats.Paused == 1
			})
			select {
			case <-fired:
				t.Fatalf("第 %d 轮 Pause 成功后回调仍开始", iteration)
			default:
			}
			if !fixture.service.ResumeTimer(id) {
				t.Fatalf("第 %d 轮 ResumeTimer() 失败", iteration)
			}
			advanceTimerFixture(t, fixture, timerwheel.TickDuration)
			receive(t, fired)
		} else {
			receive(t, fired)
		}
		waitForTimerStats(t, fixture.service, func(stats TimerStats) bool {
			return stats.Active == 0
		})
	}
}

func TestRunningAfterCannotPauseOrCancel(t *testing.T) {
	fixture := newTimerFixture(t, 8)
	started := make(chan struct{})
	release := make(chan struct{})
	id := fixture.service.AfterFunc(0, func(context.Context, TimerID) {
		close(started)
		<-release
	})
	advanceTimerFixture(t, fixture, timerwheel.TickDuration)
	receive(t, started)

	if fixture.service.PauseTimer(id) {
		t.Fatal("Running AfterFunc 被暂停")
	}
	cancelID := id
	if fixture.service.CancelTimer(&cancelID) {
		t.Fatal("Running AfterFunc 被取消")
	}
	if cancelID != InvalidTimerID {
		t.Fatalf("取消失败后调用方 ID 未清零: %d", cancelID)
	}
	close(release)
	waitForTimerStats(t, fixture.service, func(stats TimerStats) bool {
		return stats.Active == 0 && stats.CompletedTotal == 1
	})
}

func TestDuePendingIsPromotedBeforeNewDispatch(t *testing.T) {
	config := DefaultSchedulerConfig()
	config.MaxTasks = 1
	config.MaxAwaitTasks = 1
	fixture := newTimerFixtureWithConfig(t, config, 8, true)

	block := make(chan struct{})
	started := make(chan struct{})
	if err := fixture.service.DispatchAsync(func(context.Context) {
		close(started)
		<-block
	}); err != nil {
		t.Fatalf("DispatchAsync() error = %v", err)
	}
	receive(t, started)

	fired := make(chan struct{}, 1)
	if id := fixture.service.AfterFunc(0, func(context.Context, TimerID) {
		fired <- struct{}{}
	}); id == InvalidTimerID {
		t.Fatal("AfterFunc 创建失败")
	}
	advanceTimerFixture(t, fixture, timerwheel.TickDuration)
	waitForTimerStats(t, fixture.service, func(stats TimerStats) bool {
		return stats.DuePending == 1
	})

	// 到期 Timer 已经等待准入时，新根任务不能在它前面抢占刚释放的唯一额度。
	if err := fixture.service.DispatchAsync(func(context.Context) {}); !errors.Is(err, errs.ErrServiceQueueFull) {
		t.Fatalf("DispatchAsync() error = %v，期望 ErrServiceQueueFull", err)
	}
	close(block)
	receive(t, fired)
}

func TestDuePendingPreservesAllSameTickAfterTimers(t *testing.T) {
	const timerCount = 100
	config := DefaultSchedulerConfig()
	config.MaxTasks = 1
	config.MaxAwaitTasks = 1
	fixture := newTimerFixtureWithConfig(t, config, timerCount+1, true)

	block := make(chan struct{})
	started := make(chan struct{})
	if err := fixture.service.DispatchAsync(func(context.Context) {
		close(started)
		<-block
	}); err != nil {
		t.Fatalf("DispatchAsync() error = %v", err)
	}
	receive(t, started)

	fired := make(chan TimerID, timerCount)
	for index := 0; index < timerCount; index++ {
		id := fixture.service.AfterFunc(0, func(
			_ context.Context,
			timerID TimerID,
		) {
			fired <- timerID
		})
		if id == InvalidTimerID {
			t.Fatalf("第 %d 个 AfterFunc 创建失败", index)
		}
	}
	advanceTimerFixture(t, fixture, timerwheel.TickDuration)
	waitForTimerStats(t, fixture.service, func(stats TimerStats) bool {
		return stats.DuePending == timerCount
	})

	close(block)
	seen := make(map[TimerID]struct{}, timerCount)
	for index := 0; index < timerCount; index++ {
		id := receive(t, fired)
		if _, duplicated := seen[id]; duplicated {
			t.Fatalf("TimerID %d 被重复触发", id)
		}
		seen[id] = struct{}{}
	}
	waitForTimerStats(t, fixture.service, func(stats TimerStats) bool {
		return stats.Active == 0 && stats.CompletedTotal == timerCount
	})
}

func TestPauseResumeDuePendingSkipsOldGeneration(t *testing.T) {
	config := DefaultSchedulerConfig()
	config.MaxTasks = 1
	config.MaxAwaitTasks = 1
	fixture := newTimerFixtureWithConfig(t, config, 8, true)

	block := make(chan struct{})
	started := make(chan struct{})
	if err := fixture.service.DispatchAsync(func(context.Context) {
		close(started)
		<-block
	}); err != nil {
		t.Fatalf("DispatchAsync() error = %v", err)
	}
	receive(t, started)

	fired := make(chan struct{}, 1)
	id := fixture.service.AfterFunc(0, func(context.Context, TimerID) {
		fired <- struct{}{}
	})
	advanceTimerFixture(t, fixture, timerwheel.TickDuration)
	waitForTimerStats(t, fixture.service, func(stats TimerStats) bool {
		return stats.DuePending == 1
	})
	if !fixture.service.PauseTimer(id) || !fixture.service.ResumeTimer(id) {
		t.Fatal("DuePending Timer 暂停或恢复失败")
	}

	// 恢复产生新一代 Deadline；旧 DuePending 条目必须只作为墓碑跳过。
	advanceTimerFixture(t, fixture, timerwheel.TickDuration)
	waitForTimerStats(t, fixture.service, func(stats TimerStats) bool {
		return stats.DuePending == 1
	})
	close(block)
	receive(t, fired)
	select {
	case <-fired:
		t.Fatal("旧 DuePending 代次导致重复回调")
	default:
	}
}

func TestCancelDuePendingPreventsCallback(t *testing.T) {
	config := DefaultSchedulerConfig()
	config.MaxTasks = 1
	config.MaxAwaitTasks = 1
	fixture := newTimerFixtureWithConfig(t, config, 8, true)

	block := make(chan struct{})
	started := make(chan struct{})
	if err := fixture.service.DispatchAsync(func(context.Context) {
		close(started)
		<-block
	}); err != nil {
		t.Fatalf("DispatchAsync() error = %v", err)
	}
	receive(t, started)

	fired := make(chan struct{}, 1)
	id := fixture.service.AfterFunc(0, func(context.Context, TimerID) {
		fired <- struct{}{}
	})
	advanceTimerFixture(t, fixture, timerwheel.TickDuration)
	waitForTimerStats(t, fixture.service, func(stats TimerStats) bool {
		return stats.DuePending == 1
	})
	if !fixture.service.CancelTimer(&id) {
		t.Fatal("DuePending Timer 取消失败")
	}

	close(block)
	waitForTimerStats(t, fixture.service, func(stats TimerStats) bool {
		return stats.Active == 0 &&
			stats.CanceledTotal == 1 &&
			fixture.runtime.active.Load() == 0
	})
	select {
	case <-fired:
		t.Fatal("取消后的 DuePending Timer 仍触发回调")
	default:
	}
}

func TestTickerUsesFixedCadenceAndCoalescesMissedPeriods(t *testing.T) {
	fixture := newTimerFixture(t, 8)
	started := make(chan struct{})
	release := make(chan struct{})
	firedAgain := make(chan struct{}, 1)
	calls := 0

	id := fixture.service.NewTicker(timerwheel.TickDuration, func(
		context.Context,
		TimerID,
	) {
		calls++
		if calls == 1 {
			close(started)
			<-release
			return
		}
		firedAgain <- struct{}{}
	})
	if id == InvalidTimerID {
		t.Fatal("NewTicker 创建失败")
	}

	advanceTimerFixture(t, fixture, timerwheel.TickDuration)
	receive(t, started)
	// 第一轮回调尚未完成时推进五个周期；同一 Ticker 不得产生重叠任务或补偿风暴。
	advanceTimerFixture(t, fixture, 5*timerwheel.TickDuration)
	if calls != 1 {
		t.Fatalf("Ticker 回调并发或补执行，calls = %d", calls)
	}
	close(release)
	waitForTimerStats(t, fixture.service, func(stats TimerStats) bool {
		return stats.Scheduled == 1 &&
			stats.TriggeredTotal == 1 &&
			stats.CoalescedTotal == 5
	})

	// 下一名义点严格晚于当前时刻，因此再推进一个周期只触发一次。
	advanceTimerFixture(t, fixture, timerwheel.TickDuration)
	receive(t, firedAgain)
	if !fixture.service.CancelTimer(&id) {
		t.Fatal("取消仍活跃的 Ticker 失败")
	}
}

func TestTickerStopsAfterNinetyNinthConsecutivePanic(t *testing.T) {
	fixture := newTimerFixture(t, 8)
	calls := 0
	id := fixture.service.NewTicker(timerwheel.TickDuration, func(
		context.Context,
		TimerID,
	) {
		calls++
		panic("expected ticker panic")
	})
	if id == InvalidTimerID {
		t.Fatal("NewTicker 创建失败")
	}

	for index := 0; index < 99; index++ {
		advanceTimerFixture(t, fixture, timerwheel.TickDuration)
		expected := uint64(index + 1)
		waitForTimerStats(t, fixture.service, func(stats TimerStats) bool {
			if expected == 99 {
				return stats.TriggeredTotal == expected &&
					stats.Active == 0 &&
					stats.PanicLimitCanceledTotal == 1
			}
			return stats.TriggeredTotal == expected && stats.Scheduled == 1
		})
	}
	advanceTimerFixture(t, fixture, timerwheel.TickDuration)
	if calls != 99 {
		t.Fatalf("连续 panic 回调次数 = %d，期望 99", calls)
	}
	if fixture.service.CancelTimer(&id) {
		t.Fatal("达到 panic 上限后 Ticker 仍然活跃")
	}
}

func TestTickerNormalReturnResetsPanicCounter(t *testing.T) {
	fixture := newTimerFixture(t, 8)
	calls := 0
	id := fixture.service.NewTicker(timerwheel.TickDuration, func(
		context.Context,
		TimerID,
	) {
		calls++
		if calls == 99 {
			return
		}
		panic("expected ticker panic")
	})
	if id == InvalidTimerID {
		t.Fatal("NewTicker 创建失败")
	}

	for index := 0; index < 197; index++ {
		advanceTimerFixture(t, fixture, timerwheel.TickDuration)
		expected := uint64(index + 1)
		waitForTimerStats(t, fixture.service, func(stats TimerStats) bool {
			return stats.TriggeredTotal == expected && stats.Scheduled == 1
		})
	}
	if calls != 197 {
		t.Fatalf("正常返回重置后 calls = %d，期望 197", calls)
	}
	if !fixture.service.CancelTimer(&id) {
		t.Fatal("正常返回清零 panic 计数后 Ticker 被错误自动取消")
	}
}

func TestRunningTickerPausesAfterCurrentCallback(t *testing.T) {
	fixture := newTimerFixture(t, 8)
	started := make(chan struct{})
	release := make(chan struct{})
	firedAgain := make(chan struct{}, 1)
	calls := 0
	id := fixture.service.NewTicker(timerwheel.TickDuration, func(
		context.Context,
		TimerID,
	) {
		calls++
		if calls == 1 {
			close(started)
			<-release
			return
		}
		firedAgain <- struct{}{}
	})
	advanceTimerFixture(t, fixture, timerwheel.TickDuration)
	receive(t, started)
	if !fixture.service.PauseTimer(id) {
		t.Fatal("Running Ticker PauseTimer() 失败")
	}
	if fixture.service.PauseTimer(id) {
		t.Fatal("Running Ticker 重复 PauseTimer() 成功")
	}
	close(release)
	waitForTimerStats(t, fixture.service, func(stats TimerStats) bool {
		return stats.Paused == 1 && stats.Running == 0
	})

	advanceTimerFixture(t, fixture, 10*timerwheel.TickDuration)
	select {
	case <-firedAgain:
		t.Fatal("Running Ticker 暂停完成后仍继续触发")
	default:
	}
	if !fixture.service.ResumeTimer(id) {
		t.Fatal("暂停后的 Ticker ResumeTimer() 失败")
	}
	advanceTimerFixture(t, fixture, timerwheel.TickDuration)
	receive(t, firedAgain)
	cancelID := id
	if !fixture.service.CancelTimer(&cancelID) {
		t.Fatal("恢复后的 Ticker 取消失败")
	}
}

func TestRunningTickerCancelStopsFutureCallbacks(t *testing.T) {
	fixture := newTimerFixture(t, 8)
	started := make(chan struct{})
	release := make(chan struct{})
	calls := 0
	id := fixture.service.NewTicker(timerwheel.TickDuration, func(
		context.Context,
		TimerID,
	) {
		calls++
		if calls == 1 {
			close(started)
			<-release
		}
	})
	advanceTimerFixture(t, fixture, timerwheel.TickDuration)
	receive(t, started)

	if !fixture.service.CancelTimer(&id) || id != InvalidTimerID {
		t.Fatalf("Running Ticker 取消失败或 ID 未清零: %d", id)
	}
	close(release)
	waitForTimerStats(t, fixture.service, func(stats TimerStats) bool {
		return stats.Active == 0 &&
			stats.Running == 0 &&
			stats.CanceledTotal == 1
	})
	advanceTimerFixture(t, fixture, 10*timerwheel.TickDuration)
	if calls != 1 {
		t.Fatalf("取消后的 Ticker 回调次数 = %d，期望 1", calls)
	}
}

func TestNewTickerRejectsInvalidArguments(t *testing.T) {
	var unbound Service
	if unbound.NewTicker(time.Second, noopTimerCallback) != InvalidTimerID {
		t.Fatal("未绑定 Service 创建 Ticker 成功")
	}
	fixture := newTimerFixture(t, 8)
	if fixture.service.NewTicker(0, noopTimerCallback) != InvalidTimerID {
		t.Fatal("零周期创建 Ticker 成功")
	}
	if fixture.service.NewTicker(-time.Second, noopTimerCallback) != InvalidTimerID {
		t.Fatal("负周期创建 Ticker 成功")
	}
	if fixture.service.NewTicker(time.Second, nil) != InvalidTimerID {
		t.Fatal("nil callback 创建 Ticker 成功")
	}
}

func TestStopCancelsUnreadyTimersAndDrainsRunningCallback(t *testing.T) {
	fixture := newTimerFixture(t, 16)
	scheduled := fixture.service.AfterFunc(time.Hour, noopTimerCallback)
	if scheduled == InvalidTimerID {
		t.Fatal("Scheduled Timer 创建失败")
	}

	started := make(chan struct{})
	release := make(chan struct{})
	released := false
	t.Cleanup(func() {
		if !released {
			close(release)
		}
	})
	if id := fixture.service.AfterFunc(0, func(context.Context, TimerID) {
		close(started)
		<-release
	}); id == InvalidTimerID {
		t.Fatal("Running Timer 创建失败")
	}
	advanceTimerFixture(t, fixture, timerwheel.TickDuration)
	receive(t, started)

	fixture.runtime.state.Store(uint32(StateStopping))
	stopResult := make(chan error, 1)
	go func() {
		stopResult <- StopScheduler(context.Background(), fixture.service)
	}()

	// Stop 先取消未进入 Ready 的 Timer，但当前已开始回调仍参与真实排空。
	waitForTimerStats(t, fixture.service, func(stats TimerStats) bool {
		return stats.Active == 1 &&
			stats.Scheduled == 0 &&
			stats.Running == 1
	})
	staleID := scheduled
	if fixture.service.CancelTimer(&staleID) {
		t.Fatal("Stop 开始后 Scheduled Timer 仍可控制")
	}

	close(release)
	released = true
	if err := receive(t, stopResult); err != nil {
		t.Fatalf("StopScheduler() error = %v", err)
	}
	stats := fixture.service.TimerStats()
	if stats.Active != 0 || fixture.runtime.active.Load() != 0 {
		t.Fatalf(
			"停止后 Timer 未清空: stats=%+v slots=%d",
			stats,
			fixture.runtime.active.Load(),
		)
	}
	assertSchedulerStoppedStorageReleased(t, fixture.service.scheduler.Load())
}

func TestPooledTimerCannotBeControlledByOldID(t *testing.T) {
	fixture := newTimerFixture(t, 8)
	oldID := fixture.service.AfterFunc(time.Hour, noopTimerCallback)
	cancelID := oldID
	if !fixture.service.CancelTimer(&cancelID) {
		t.Fatal("取消旧 Timer 失败")
	}

	freshID := fixture.service.AfterFunc(time.Hour, noopTimerCallback)
	if freshID == oldID {
		t.Fatal("池化对象复用导致 TimerID 复用")
	}
	if fixture.service.PauseTimer(oldID) ||
		fixture.service.ResumeTimer(oldID) {
		t.Fatal("旧 ID 控制了新 Timer")
	}
	staleCancel := oldID
	if fixture.service.CancelTimer(&staleCancel) {
		t.Fatal("旧 ID 取消了新 Timer")
	}
	if !fixture.service.CancelTimer(&freshID) {
		t.Fatal("新 Timer 已被旧 ID 破坏")
	}
}

func TestStopCleansPausedDuePendingAndCanceledTombstones(t *testing.T) {
	config := DefaultSchedulerConfig()
	config.MaxTasks = 1
	config.MaxAwaitTasks = 1
	fixture := newTimerFixtureWithConfig(t, config, 8, true)

	block := make(chan struct{})
	started := make(chan struct{})
	if err := fixture.service.DispatchAsync(func(context.Context) {
		close(started)
		<-block
	}); err != nil {
		t.Fatalf("DispatchAsync() error = %v", err)
	}
	receive(t, started)

	paused := fixture.service.AfterFunc(0, noopTimerCallback)
	pending := fixture.service.AfterFunc(0, noopTimerCallback)
	canceled := fixture.service.AfterFunc(0, noopTimerCallback)
	advanceTimerFixture(t, fixture, timerwheel.TickDuration)
	waitForTimerStats(t, fixture.service, func(stats TimerStats) bool {
		return stats.DuePending == 3
	})
	if !fixture.service.PauseTimer(paused) {
		t.Fatal("DuePending Timer 暂停失败")
	}
	if !fixture.service.CancelTimer(&canceled) {
		t.Fatal("DuePending Timer 取消失败")
	}

	fixture.runtime.state.Store(uint32(StateStopping))
	stopResult := make(chan error, 1)
	go func() {
		stopResult <- StopScheduler(context.Background(), fixture.service)
	}()
	waitForTimerStats(t, fixture.service, func(stats TimerStats) bool {
		return stats.Active == 0 &&
			stats.Paused == 0 &&
			stats.DuePending == 0 &&
			fixture.runtime.active.Load() == 0
	})
	if stale := pending; fixture.service.CancelTimer(&stale) {
		t.Fatal("Stop 后 DuePending Timer 仍可控制")
	}
	close(block)
	if err := receive(t, stopResult); err != nil {
		t.Fatalf("StopScheduler() error = %v", err)
	}
}

func TestNextTickerTimeRejectsDurationOverflow(t *testing.T) {
	nominal := time.Unix(0, 0)
	if _, _, valid := nextTickerTime(
		nominal,
		nominal.Add(time.Duration(math.MaxInt64)),
		time.Duration(math.MaxInt64),
	); valid {
		t.Fatal("Ticker Duration 乘法溢出仍返回有效名义点")
	}
}

func TestTimerCallbackCanAwaitAndLetsOtherTaskRun(t *testing.T) {
	fixture := newTimerFixture(t, 8)
	awaitStarted := make(chan struct{})
	releaseAwait := make(chan struct{})
	otherTaskDone := make(chan struct{})
	timerDone := make(chan error, 1)

	// Timer 回调本身就是普通 Service 根任务，因此可以使用收到的 Task Context 调用 Await。
	// Await 期间当前 goroutine 等待外部结果，替补 Runner 仍应继续处理同一 Service 的任务。
	id := fixture.service.AfterFunc(0, func(ctx context.Context, _ TimerID) {
		timerDone <- fixture.service.Await(ctx, func(waitCtx context.Context) error {
			close(awaitStarted)
			select {
			case <-releaseAwait:
				return nil
			case <-waitCtx.Done():
				return waitCtx.Err()
			}
		})
	})
	if id == InvalidTimerID {
		t.Fatal("AfterFunc 创建失败")
	}
	advanceTimerFixture(t, fixture, timerwheel.TickDuration)
	receive(t, awaitStarted)

	if err := fixture.service.DispatchAsync(func(context.Context) {
		close(otherTaskDone)
	}); err != nil {
		t.Fatalf("Await 期间 DispatchAsync() error = %v", err)
	}
	receive(t, otherTaskDone)
	close(releaseAwait)
	if err := receive(t, timerDone); err != nil {
		t.Fatalf("Timer callback Await() error = %v", err)
	}
	waitForTimerStats(t, fixture.service, func(stats TimerStats) bool {
		return stats.Active == 0 && stats.CompletedTotal == 1
	})
}

func TestTimerQuotaLogDecisionAggregates(t *testing.T) {
	scheduler := &serviceScheduler{}
	now := time.Unix(200, 0)
	if logNow, suppressed := scheduler.timerQuotaLogDecisionLocked(
		now,
	); !logNow || suppressed != 0 {
		t.Fatalf("首次额度拒绝日志决策 = (%v, %d)", logNow, suppressed)
	}
	if logNow, suppressed := scheduler.timerQuotaLogDecisionLocked(
		now.Add(time.Millisecond),
	); logNow || suppressed != 0 {
		t.Fatalf("窗口内额度拒绝日志决策 = (%v, %d)", logNow, suppressed)
	}
	if logNow, suppressed := scheduler.timerQuotaLogDecisionLocked(
		now.Add(timerQuotaLogInterval),
	); !logNow || suppressed != 1 {
		t.Fatalf("新窗口额度拒绝日志决策 = (%v, %d)", logNow, suppressed)
	}
}
