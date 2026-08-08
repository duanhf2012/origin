package service

import (
	"context"
	"errors"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/duanhf2012/origin/v3/errs"
	"github.com/duanhf2012/origin/v3/internal/timerwheel"
	originlog "github.com/duanhf2012/origin/v3/log"
)

const schedulerTestTimeout = 5 * time.Second

// schedulerTestRuntime 使用原子生命周期状态支持测试并发调用公开 Service API。
type schedulerTestRuntime struct {
	nodeID        string
	name          string
	state         atomic.Uint32
	active        atomic.Int64
	nextID        atomic.Uint64
	timerLimit    int
	timerLocation *time.Location
	// nowSource 与 gameTimeOffset 把业务逻辑时间和 TimerEngine 真实时钟显式分离。
	nowSource      func() time.Time
	gameTimeOffset atomic.Int64
	failure        atomic.Pointer[schedulerTestFailure]
}

type schedulerTestFailure struct {
	cause error
}

func (runtime *schedulerTestRuntime) ID() string          { return runtime.nodeID }
func (runtime *schedulerTestRuntime) NodeID() string      { return runtime.nodeID }
func (runtime *schedulerTestRuntime) ServiceName() string { return runtime.name }
func (runtime *schedulerTestRuntime) State() State {
	return State(runtime.state.Load())
}
func (runtime *schedulerTestRuntime) Logger() originlog.Logger { return originlog.NewNop() }
func (runtime *schedulerTestRuntime) Now() time.Time {
	now := time.Now()
	if runtime.nowSource != nil {
		now = runtime.nowSource()
	}
	return now.Add(time.Duration(runtime.gameTimeOffset.Load()))
}
func (runtime *schedulerTestRuntime) SetTime(value time.Time) error {
	base := time.Now()
	if runtime.nowSource != nil {
		base = runtime.nowSource()
	}
	runtime.gameTimeOffset.Store(int64(value.Sub(base)))
	return nil
}
func (runtime *schedulerTestRuntime) AddTime(delta time.Duration) error {
	runtime.gameTimeOffset.Add(int64(delta))
	return nil
}
func (runtime *schedulerTestRuntime) LookupService(string) (IService, bool) {
	return nil, false
}
func (runtime *schedulerTestRuntime) AcquireTimerSlot() (TimerID, bool) {
	limit := runtime.timerLimit
	if limit == 0 {
		limit = 3_000_000
	}
	if runtime.active.Add(1) > int64(limit) {
		runtime.active.Add(-1)
		return InvalidTimerID, false
	}
	return TimerID(runtime.nextID.Add(1)), true
}
func (runtime *schedulerTestRuntime) ReleaseTimerSlot() {
	runtime.active.Add(-1)
}
func (runtime *schedulerTestRuntime) TimerLimit() int {
	if runtime.timerLimit == 0 {
		return 3_000_000
	}
	return runtime.timerLimit
}
func (runtime *schedulerTestRuntime) TimerLocation() *time.Location {
	if runtime.timerLocation != nil {
		return runtime.timerLocation
	}
	return time.Local
}
func (runtime *schedulerTestRuntime) Failure() error {
	failure := runtime.failure.Load()
	if failure == nil {
		return nil
	}
	return failure.cause
}
func (runtime *schedulerTestRuntime) ReportFailure(cause error) {
	if cause == nil {
		return
	}
	if runtime.failure.CompareAndSwap(nil, &schedulerTestFailure{cause: cause}) {
		runtime.state.Store(uint32(StateFailed))
	}
}

// schedulerFixture 集中拥有测试 Service、Runtime 和 Node TimerEngine。
type schedulerFixture struct {
	service *testService
	runtime *schedulerTestRuntime
	engine  *timerwheel.Engine
}

func newSchedulerFixture(
	t testing.TB,
	config SchedulerConfig,
) *schedulerFixture {
	t.Helper()
	return newSchedulerFixtureWithServiceTimeout(t, config, 0)
}

func newSchedulerFixtureWithServiceTimeout(
	t testing.TB,
	config SchedulerConfig,
	serviceTimeout time.Duration,
) *schedulerFixture {
	t.Helper()

	// 测试严格复刻 Node 启动顺序：绑定、TimerEngine Start、Service Starting、
	// PrepareScheduler、Service Running、ActivateScheduler。
	target := &testService{}
	runtimeState := &schedulerTestRuntime{
		nodeID:    "game-1",
		name:      "PlayerService",
		nowSource: time.Now,
	}
	runtimeState.state.Store(uint32(StateCreated))
	if err := BindRuntime(target, runtimeState); err != nil {
		t.Fatalf("BindRuntime() error = %v", err)
	}
	if serviceTimeout > 0 {
		// 复刻业务 OnInit 中设置 Service 级覆盖的真实生命周期阶段。
		runtimeState.state.Store(uint32(StateInitializing))
		if err := target.SetDefaultAwaitTimeout(serviceTimeout); err != nil {
			t.Fatalf("SetDefaultAwaitTimeout() error = %v", err)
		}
		runtimeState.state.Store(uint32(StateInitialized))
	}
	engine, err := timerwheel.New(timerwheel.DefaultOptions())
	if err != nil {
		t.Fatalf("timerwheel.New() error = %v", err)
	}
	if err := engine.Start(); err != nil {
		t.Fatalf("TimerEngine.Start() error = %v", err)
	}
	runtimeState.state.Store(uint32(StateStarting))
	if err := PrepareScheduler(target, config, engine); err != nil {
		engine.Close()
		t.Fatalf("PrepareScheduler() error = %v", err)
	}
	runtimeState.state.Store(uint32(StateRunning))
	if err := ActivateScheduler(target); err != nil {
		engine.Close()
		t.Fatalf("ActivateScheduler() error = %v", err)
	}

	fixture := &schedulerFixture{
		service: target,
		runtime: runtimeState,
		engine:  engine,
	}
	t.Cleanup(func() {
		// 用例应主动排空；清理路径只为断言提前失败时回收空闲调度资源。
		if target.scheduler.Load() != nil {
			runtimeState.state.Store(uint32(StateStopping))
			stopContext, cancel := context.WithTimeout(context.Background(), schedulerTestTimeout)
			_ = StopScheduler(stopContext, target)
			cancel()
			runtimeState.state.Store(uint32(StateStopped))
		}
		_ = engine.Close()
	})
	return fixture
}

func newPreparedSchedulerFixture(
	t testing.TB,
	config SchedulerConfig,
) *schedulerFixture {
	t.Helper()

	// Prepared fixture 只创建底层资源，不启动 Runner 或 Deadline watcher，用于验证 OnStart
	// 阶段不会与任何业务任务并发。
	target := &testService{}
	runtimeState := &schedulerTestRuntime{
		nodeID: "game-1",
		name:   "PlayerService",
	}
	runtimeState.state.Store(uint32(StateStarting))
	if err := BindRuntime(target, runtimeState); err != nil {
		t.Fatalf("BindRuntime() error = %v", err)
	}
	engine, err := timerwheel.New(timerwheel.DefaultOptions())
	if err != nil {
		t.Fatalf("timerwheel.New() error = %v", err)
	}
	if err := engine.Start(); err != nil {
		t.Fatalf("TimerEngine.Start() error = %v", err)
	}
	if err := PrepareScheduler(target, config, engine); err != nil {
		_ = engine.Close()
		t.Fatalf("PrepareScheduler() error = %v", err)
	}

	fixture := &schedulerFixture{
		service: target,
		runtime: runtimeState,
		engine:  engine,
	}
	t.Cleanup(func() {
		runtimeState.state.Store(uint32(StateStopping))
		ctx, cancel := context.WithTimeout(context.Background(), schedulerTestTimeout)
		_ = StopScheduler(ctx, target)
		cancel()
		runtimeState.state.Store(uint32(StateStopped))
		_ = engine.Close()
	})
	return fixture
}

func TestSchedulerInvariantFailureIsolatesOnlyService(t *testing.T) {
	fixture := newSchedulerFixture(t, DefaultSchedulerConfig())
	scheduler := fixture.service.scheduler.Load()
	if scheduler == nil {
		t.Fatal("Scheduler 未装配")
	}
	// 模拟 Failed 时仍存在一个普通 goroutine 的 RPC 调用预算。故障清理必须取消并摘除
	// 它；调用方晚到的幂等 finish 不能让已经归零的 operations 下溢。
	_, finishOperation, err := PrepareOperationContext(
		fixture.service,
		context.Background(),
	)
	if err != nil {
		t.Fatalf("PrepareOperationContext() error = %v", err)
	}

	// 直接注入只能由框架内部产生的不变量错误，验证它不会逃逸为进程 panic。
	scheduler.failInvariant(
		schedulerInvariantError{message: "injected invariant failure"},
		[]byte("injected stack"),
	)
	deadline := time.Now().Add(schedulerTestTimeout)
	for fixture.service.State() != StateFailed {
		if time.Now().After(deadline) {
			t.Fatal("Service 未进入 Failed")
		}
		runtime.Gosched()
	}
	if !errors.Is(fixture.service.DispatchAsync(func(context.Context) {}), errs.ErrServiceFailed) {
		t.Fatal("Failed Service 仍接受新任务")
	}
	if !errors.Is(fixture.service.Failure(), errs.ErrServiceFailed) {
		t.Fatalf("Failure() = %v", fixture.service.Failure())
	}

	// 正式 Stop 跳过业务 finalizer，等待真实 Runner 退出并返回首个稳定根因。
	stopCtx, cancel := context.WithTimeout(context.Background(), schedulerTestTimeout)
	defer cancel()
	if err := StopScheduler(stopCtx, fixture.service); !errors.Is(
		err,
		errs.ErrServiceFailed,
	) {
		t.Fatalf("StopScheduler() error = %v", err)
	}
	finishOperation()
}

// TestSchedulerInvariantFailureWithUnavailableLockDoesNotBlockStop 模拟内部 panic 遗留
// Scheduler 锁的最坏情况。故障边界不得等待这把锁，正式 Stop 也必须快速返回稳定错误，
// 以便 Node 继续倒序停止其他 Service。
func TestSchedulerInvariantFailureWithUnavailableLockDoesNotBlockStop(t *testing.T) {
	fixture := newSchedulerFixture(t, DefaultSchedulerConfig())
	scheduler := fixture.service.scheduler.Load()
	if scheduler == nil {
		t.Fatal("Scheduler 未装配")
	}

	scheduler.mu.Lock()
	scheduler.failInvariant(
		schedulerInvariantError{message: "injected locked invariant failure"},
		[]byte("injected locked stack"),
	)
	if !scheduler.failureLockUnsafe.Load() {
		scheduler.mu.Unlock()
		t.Fatal("无法取得 Scheduler 锁时未进入保守清理模式")
	}

	beginDone := make(chan error, 1)
	go func() {
		beginDone <- BeginStopScheduler(fixture.service)
	}()
	select {
	case err := <-beginDone:
		if err != nil {
			scheduler.mu.Unlock()
			t.Fatalf("BeginStopScheduler() error = %v", err)
		}
	case <-time.After(schedulerTestTimeout):
		scheduler.mu.Unlock()
		t.Fatal("BeginStopScheduler 被不可用状态锁卡住")
	}

	stopDone := make(chan error, 1)
	go func() {
		stopDone <- StopScheduler(context.Background(), fixture.service)
	}()
	select {
	case err := <-stopDone:
		if !errors.Is(err, errs.ErrServiceFailed) {
			t.Fatalf("StopScheduler() error = %v", err)
		}
	case <-time.After(schedulerTestTimeout):
		scheduler.mu.Unlock()
		t.Fatal("保守 Stop 被 Scheduler 状态锁卡住")
	}
	// 首次 Stop 已经证明不会等待遗留锁；现在释放测试锁，让 Cleanup 能重试完整的
	// Failed 资源回收并确认所有测试 goroutine 最终收敛。
	scheduler.mu.Unlock()
}

func TestDrainingAcceptsOnlyContinuationFromAcceptedTask(t *testing.T) {
	fixture := newSchedulerFixture(t, DefaultSchedulerConfig())
	rootStarted := make(chan struct{})
	continueRoot := make(chan struct{})
	continuationDone := make(chan struct{})
	dispatchResult := make(chan error, 1)
	if err := fixture.service.DispatchAsync(func(ctx context.Context) {
		close(rootStarted)
		<-continueRoot
		operationCtx, finish, err := PrepareOperationContext(fixture.service, ctx)
		if err != nil {
			dispatchResult <- err
			return
		}
		defer finish()
		dispatchResult <- DispatchAsyncCompletion(
			fixture.service,
			operationCtx,
			func(context.Context) error { return nil },
			func(context.Context, error) { close(continuationDone) },
		)
	}); err != nil {
		t.Fatal(err)
	}
	<-rootStarted
	if err := BeginStopScheduler(fixture.service); err != nil {
		t.Fatalf("BeginStopScheduler() error = %v", err)
	}
	if _, _, err := PrepareOperationContext(
		fixture.service,
		context.Background(),
	); !errors.Is(err, errs.ErrServiceStopping) {
		t.Fatalf("Draining 外部 PrepareOperationContext() error = %v", err)
	}
	close(continueRoot)
	if err := <-dispatchResult; err != nil {
		t.Fatalf("已接受任务的 Async 延续被拒绝: %v", err)
	}

	fixture.runtime.state.Store(uint32(StateStopping))
	stopCtx, cancel := context.WithTimeout(context.Background(), schedulerTestTimeout)
	defer cancel()
	if err := StopScheduler(stopCtx, fixture.service); err != nil {
		t.Fatalf("StopScheduler() error = %v", err)
	}
	select {
	case <-continuationDone:
	default:
		t.Fatal("Draining 没有排空已经预留的 Async 延续")
	}
}

func TestPreparedSchedulerDoesNotRunUntilActivated(t *testing.T) {
	fixture := newPreparedSchedulerFixture(t, DefaultSchedulerConfig())
	scheduler := fixture.service.scheduler.Load()
	if scheduler == nil || scheduler.state != schedulerPrepared {
		t.Fatalf("Prepared Scheduler 状态 = %v", scheduler)
	}

	// Service 仍为 Starting 时公开投递必须拒绝，且尚不存在可执行该任务的 Runner。
	ran := make(chan struct{}, 1)
	if err := fixture.service.DispatchAsync(func(context.Context) {
		ran <- struct{}{}
	}); !errors.Is(err, errs.ErrServiceNotReady) {
		t.Fatalf("Prepared DispatchAsync() error = %v", err)
	}
	select {
	case <-ran:
		t.Fatal("Prepared 阶段执行了业务任务")
	default:
	}

	// Node 先发布 Service Running，再激活 Scheduler；之后普通投递恢复既有行为。
	fixture.runtime.state.Store(uint32(StateRunning))
	if err := ActivateScheduler(fixture.service); err != nil {
		t.Fatalf("ActivateScheduler() error = %v", err)
	}
	if err := fixture.service.DispatchAsync(func(context.Context) {
		ran <- struct{}{}
	}); err != nil {
		t.Fatalf("Running DispatchAsync() error = %v", err)
	}
	receive(t, ran)
}

// TestStartContextAwaitRunsWithoutBusinessRunner 验证 OnStart 可以顺序等待，但不会开放业务调度。
func TestStartContextAwaitRunsWithoutBusinessRunner(t *testing.T) {
	fixture := newPreparedSchedulerFixture(t, DefaultSchedulerConfig())
	startContext, finish, err := PrepareStartContext(
		fixture.service,
		context.Background(),
	)
	if err != nil {
		t.Fatalf("PrepareStartContext() error = %v", err)
	}

	// 等待函数在当前 OnStart 调用链中直接完成；Prepared 阶段始终没有普通 Runner。
	called := false
	if err := fixture.service.Await(
		startContext,
		func(context.Context) error {
			called = true
			return nil
		},
	); err != nil {
		t.Fatalf("OnStart Await() error = %v", err)
	}
	if !called {
		t.Fatal("OnStart Await() 没有调用等待函数")
	}
	scheduler := fixture.service.scheduler.Load()
	if scheduler.state != schedulerPrepared || scheduler.activated {
		t.Fatalf("OnStart Await() 提前激活了业务 Runner: %+v", scheduler)
	}

	// 生命周期返回后令牌立即失效，旧 Context 不能在 Running 阶段伪造执行权。
	finish()
	if err := fixture.service.Await(
		startContext,
		func(context.Context) error { return nil },
	); !errors.Is(err, errs.ErrInvalidArgument) {
		t.Fatalf("陈旧 OnStart Context Await() error = %v", err)
	}
}

// TestStartContextAwaitUsesDefaultDeadline 验证 Prepared 阶段的 M8 watcher 管理默认超时。
func TestStartContextAwaitUsesDefaultDeadline(t *testing.T) {
	config := DefaultSchedulerConfig()
	config.DefaultAwaitTimeout = 20 * time.Millisecond
	fixture := newPreparedSchedulerFixture(t, config)
	startContext, finish, err := PrepareStartContext(
		fixture.service,
		context.Background(),
	)
	if err != nil {
		t.Fatalf("PrepareStartContext() error = %v", err)
	}
	defer finish()

	err = fixture.service.Await(
		startContext,
		func(waitContext context.Context) error {
			<-waitContext.Done()
			return context.Cause(waitContext)
		},
	)
	if !errors.Is(err, errs.ErrDeadlineExceeded) {
		t.Fatalf("默认超时 Await() error = %v", err)
	}
}

// TestStartContextAcceptsOptionalControlContext 验证生命周期执行帧来自 Service 本身，而
// nil、Background 和 TODO 都只表示为本次 Await 建立新的默认控制预算。
func TestStartContextAcceptsOptionalControlContext(t *testing.T) {
	first := newPreparedSchedulerFixture(t, DefaultSchedulerConfig())
	second := newPreparedSchedulerFixture(t, DefaultSchedulerConfig())
	startContext, finish, err := PrepareStartContext(
		first.service,
		context.Background(),
	)
	if err != nil {
		t.Fatalf("PrepareStartContext() error = %v", err)
	}
	defer finish()

	for name, control := range map[string]context.Context{
		"nil":        nil,
		"background": context.Background(),
		"todo":       context.TODO(),
	} {
		if err := first.service.Await(
			control,
			func(context.Context) error { return nil },
		); err != nil {
			t.Fatalf("%s Await() error = %v", name, err)
		}
	}
	// Context 不再证明执行身份；second 没有活动生命周期帧，因此仍不能 Await。
	if err := second.service.Await(
		startContext,
		func(context.Context) error { return nil },
	); !errors.Is(err, errs.ErrInvalidArgument) {
		t.Fatalf("跨 Service Await() error = %v", err)
	}
}

// TestDiscoveryTaskWaitsForActivationAndCoalesces 验证 OnStart 注册只留下一个最新同步意图。
func TestDiscoveryTaskWaitsForActivationAndCoalesces(t *testing.T) {
	fixture := newPreparedSchedulerFixture(t, DefaultSchedulerConfig())
	called := make(chan int, 2)
	if err := MarkDiscoveryDirty(
		fixture.service,
		func(context.Context) { called <- 1 },
	); err != nil {
		t.Fatalf("第一次 MarkDiscoveryDirty() error = %v", err)
	}
	if err := MarkDiscoveryDirty(
		fixture.service,
		func(context.Context) { called <- 2 },
	); err != nil {
		t.Fatalf("第二次 MarkDiscoveryDirty() error = %v", err)
	}
	select {
	case value := <-called:
		t.Fatalf("Prepared 阶段提前执行发现任务: %d", value)
	default:
	}

	// 激活时只把最新同步函数加入统一 FIFO 一次。
	fixture.runtime.state.Store(uint32(StateRunning))
	if err := ActivateScheduler(fixture.service); err != nil {
		t.Fatalf("ActivateScheduler() error = %v", err)
	}
	if value := receive(t, called); value != 2 {
		t.Fatalf("发现任务值 = %d, want 2", value)
	}
	select {
	case value := <-called:
		t.Fatalf("合并更新重复执行发现任务: %d", value)
	default:
	}
}

// TestDiscoveryDirtySurvivesFullQueue 验证硬容量满时保留一个待提升标记而不是丢失最终状态。
func TestDiscoveryDirtySurvivesFullQueue(t *testing.T) {
	config := DefaultSchedulerConfig()
	config.MaxTasks = 1
	config.MaxAwaitTasks = 1
	fixture := newSchedulerFixture(t, config)

	started := make(chan struct{})
	release := make(chan struct{})
	if err := fixture.service.DispatchAsync(func(context.Context) {
		close(started)
		<-release
	}); err != nil {
		t.Fatalf("DispatchAsync() error = %v", err)
	}
	receive(t, started)

	delivered := make(chan struct{}, 1)
	if err := MarkDiscoveryDirty(
		fixture.service,
		func(context.Context) { delivered <- struct{}{} },
	); err != nil {
		t.Fatalf("满容量 MarkDiscoveryDirty() error = %v", err)
	}
	select {
	case <-delivered:
		t.Fatal("占用执行槽期间发现任务提前执行")
	default:
	}
	close(release)
	receive(t, delivered)
}

func TestStopPreparedSchedulerReleasesResourcesWithoutGoroutine(t *testing.T) {
	fixture := newPreparedSchedulerFixture(t, DefaultSchedulerConfig())
	fixture.runtime.state.Store(uint32(StateStopping))
	if err := StopScheduler(context.Background(), fixture.service); err != nil {
		t.Fatalf("StopScheduler() error = %v", err)
	}
	scheduler := fixture.service.scheduler.Load()
	if scheduler.state != schedulerStopped {
		t.Fatalf("Prepared Stop 后状态 = %v", scheduler.state)
	}
	if stats := fixture.engine.Stats(); stats.Queues != 0 {
		t.Fatalf("Prepared Stop 后 DeadlineQueue 数量 = %d", stats.Queues)
	}
	assertSchedulerStoppedStorageReleased(t, scheduler)
}

// assertSchedulerStoppedStorageReleased 验证一次性 Scheduler 停止后不会继续持有峰值队列、
// Timer 索引或时间轮资源。统计值和停止结果仍保留，业务存储则必须允许 GC 回收。
func assertSchedulerStoppedStorageReleased(
	t *testing.T,
	scheduler *serviceScheduler,
) {
	t.Helper()
	scheduler.mu.Lock()
	defer scheduler.mu.Unlock()
	if scheduler.ready != nil ||
		scheduler.deadlineQueue != nil ||
		scheduler.deadlineBindings != nil ||
		scheduler.timerEngine != nil ||
		scheduler.timers != nil ||
		scheduler.duePending != nil {
		t.Fatalf(
			"Stopped Scheduler 仍持有业务存储: ready=%v queue=%v bindings=%v engine=%v timers=%v due=%v",
			scheduler.ready != nil,
			scheduler.deadlineQueue != nil,
			scheduler.deadlineBindings != nil,
			scheduler.timerEngine != nil,
			scheduler.timers != nil,
			scheduler.duePending != nil,
		)
	}
}

func (fixture *schedulerFixture) stop(t *testing.T) {
	t.Helper()
	fixture.runtime.state.Store(uint32(StateStopping))
	ctx, cancel := context.WithTimeout(context.Background(), schedulerTestTimeout)
	defer cancel()
	if err := StopScheduler(ctx, fixture.service); err != nil {
		t.Fatalf("StopScheduler() error = %v", err)
	}
	fixture.runtime.state.Store(uint32(StateStopped))
}

func TestSchedulerDispatchFIFOAndSingleExecutionSlot(t *testing.T) {
	fixture := newSchedulerFixture(t, DefaultSchedulerConfig())

	const taskCount = 256
	var (
		active    atomic.Int32
		maxActive atomic.Int32
		mutex     sync.Mutex
		order     = make([]int, 0, taskCount)
		done      sync.WaitGroup
	)
	done.Add(taskCount)

	// 顺序投递固定编号，并在每个任务中检查同时持有业务执行权的任务数。
	for index := 0; index < taskCount; index++ {
		index := index
		if err := fixture.service.DispatchAsync(func(context.Context) {
			current := active.Add(1)
			for {
				previous := maxActive.Load()
				if current <= previous || maxActive.CompareAndSwap(previous, current) {
					break
				}
			}
			mutex.Lock()
			order = append(order, index)
			mutex.Unlock()
			active.Add(-1)
			done.Done()
		}); err != nil {
			t.Fatalf("DispatchAsync(%d) error = %v", index, err)
		}
	}
	waitGroup(t, &done)

	if maxActive.Load() != 1 {
		t.Fatalf("最大并行业务任务 = %d, want 1", maxActive.Load())
	}
	for index, actual := range order {
		if actual != index {
			t.Fatalf("FIFO[%d] = %d, want %d", index, actual, index)
		}
	}
	waitForStats(t, fixture.service, func(stats ExecutionStats) bool {
		return stats.CompletedTotal == taskCount
	})
	stats := fixture.service.ExecutionStats()
	if stats.Accepted != 0 || stats.CompletedTotal != taskCount ||
		stats.DispatchedTotal != taskCount {
		t.Fatalf("ExecutionStats() = %+v", stats)
	}
	fixture.stop(t)
}

func TestSchedulerConcurrentDispatchKeepsOneExecutionSlot(t *testing.T) {
	fixture := newSchedulerFixture(t, SchedulerConfig{
		MaxTasks:            4096,
		MaxAwaitTasks:       2048,
		DefaultAwaitTimeout: time.Second,
	})

	const (
		submitterCount = 8
		perSubmitter   = 256
	)
	var active atomic.Int32
	var maxActive atomic.Int32
	var done sync.WaitGroup
	done.Add(submitterCount * perSubmitter)

	// 多个提交 goroutine 同时进入热路径，业务函数仍必须严格串行。
	var submitters sync.WaitGroup
	submitters.Add(submitterCount)
	for submitter := 0; submitter < submitterCount; submitter++ {
		go func() {
			defer submitters.Done()
			for index := 0; index < perSubmitter; index++ {
				err := fixture.service.DispatchAsync(func(context.Context) {
					current := active.Add(1)
					for {
						previous := maxActive.Load()
						if current <= previous || maxActive.CompareAndSwap(previous, current) {
							break
						}
					}
					runtime.Gosched()
					active.Add(-1)
					done.Done()
				})
				if err != nil {
					t.Errorf("DispatchAsync() error = %v", err)
					return
				}
			}
		}()
	}
	submitters.Wait()
	waitGroup(t, &done)
	if maxActive.Load() != 1 {
		t.Fatalf("最大并行业务任务 = %d, want 1", maxActive.Load())
	}
	fixture.stop(t)
}

func TestOrdinaryTasksReuseCurrentRunner(t *testing.T) {
	fixture := newSchedulerFixture(t, DefaultSchedulerConfig())

	const taskCount = 128
	ids := make(chan uint64, taskCount)
	var completed sync.WaitGroup
	completed.Add(taskCount)
	for index := 0; index < taskCount; index++ {
		if err := fixture.service.DispatchAsync(func(context.Context) {
			ids <- currentGoroutineID(t)
			completed.Done()
		}); err != nil {
			t.Fatalf("DispatchAsync(%d) error = %v", index, err)
		}
	}
	waitGroup(t, &completed)
	close(ids)

	// 没有 Await 交接时，全部普通任务应由同一个长期 Runner 连续处理。
	var runnerID uint64
	for id := range ids {
		if runnerID == 0 {
			runnerID = id
			continue
		}
		if id != runnerID {
			t.Fatalf("普通任务 Runner = %d, want %d", id, runnerID)
		}
	}
	fixture.stop(t)
}

func TestAwaitReleasesAndRestoresOriginalGoroutineInFIFO(t *testing.T) {
	fixture := newSchedulerFixture(t, DefaultSchedulerConfig())

	waitStarted := make(chan [2]uint64, 1)
	releaseWait := make(chan struct{})
	done := make(chan struct{})
	var (
		mutex sync.Mutex
		order []string
	)

	if err := fixture.service.DispatchAsync(func(ctx context.Context) {
		originalGoroutine := currentGoroutineID(t)
		mutex.Lock()
		order = append(order, "task-1-start")
		mutex.Unlock()

		err := fixture.service.Await(ctx, func(context.Context) error {
			// fn 必须在原任务 goroutine 中执行，而不是另开一个等待 goroutine。
			waitStarted <- [2]uint64{originalGoroutine, currentGoroutineID(t)}
			<-releaseWait
			return nil
		})
		if err != nil {
			t.Errorf("Await() error = %v", err)
		}
		if currentGoroutineID(t) != originalGoroutine {
			t.Error("Await 返回后没有恢复到原任务 goroutine")
		}
		mutex.Lock()
		order = append(order, "task-1-resumed")
		mutex.Unlock()
		close(done)
	}); err != nil {
		t.Fatalf("first DispatchAsync() error = %v", err)
	}

	goroutineIDs := receive(t, waitStarted)
	if goroutineIDs[0] != goroutineIDs[1] {
		t.Fatalf("等待函数 goroutine = %d, want 原任务 %d", goroutineIDs[1], goroutineIDs[0])
	}
	if err := fixture.service.DispatchAsync(func(context.Context) {
		mutex.Lock()
		order = append(order, "task-2")
		mutex.Unlock()
	}); err != nil {
		t.Fatalf("second DispatchAsync() error = %v", err)
	}
	close(releaseWait)
	waitSignal(t, done)

	mutex.Lock()
	actual := append([]string(nil), order...)
	mutex.Unlock()
	want := []string{"task-1-start", "task-2", "task-1-resumed"}
	if strings.Join(actual, ",") != strings.Join(want, ",") {
		t.Fatalf("执行顺序 = %v, want %v", actual, want)
	}
	if goroutineIDs[1] == 0 {
		t.Fatal("等待函数 goroutine ID 无效")
	}
	fixture.stop(t)
}

func TestAwaitDeadlineControlIsIndependentFromBusyRunner(t *testing.T) {
	fixture := newSchedulerFixture(t, SchedulerConfig{
		MaxTasks:            16,
		MaxAwaitTasks:       8,
		DefaultAwaitTimeout: 40 * time.Millisecond,
	})

	waitStarted := make(chan struct{})
	waitCanceled := make(chan time.Time, 1)
	busyStarted := make(chan struct{})
	busyRelease := make(chan struct{})
	taskDone := make(chan struct{})

	if err := fixture.service.DispatchAsync(func(ctx context.Context) {
		err := fixture.service.Await(ctx, func(waitCtx context.Context) error {
			close(waitStarted)
			<-waitCtx.Done()
			waitCanceled <- time.Now()
			return waitCtx.Err()
		})
		if !errs.IsCode(err, errs.CodeDeadlineExceeded) {
			t.Errorf("Await() error = %v, want deadline", err)
		}
		close(taskDone)
	}); err != nil {
		t.Fatalf("Dispatch waiting task error = %v", err)
	}
	waitSignal(t, waitStarted)

	// 替补 Runner 长时间执行普通业务代码。Deadline 控制协程必须在它释放执行槽前取消
	// Waiting Context，但原任务只能等 FIFO 恢复后继续。
	if err := fixture.service.DispatchAsync(func(context.Context) {
		close(busyStarted)
		<-busyRelease
	}); err != nil {
		t.Fatalf("Dispatch busy task error = %v", err)
	}
	waitSignal(t, busyStarted)
	startedAt := time.Now()
	canceledAt := receive(t, waitCanceled)
	if elapsed := canceledAt.Sub(startedAt); elapsed > 250*time.Millisecond {
		t.Fatalf("Deadline 取消被忙 Runner 延迟 %s", elapsed)
	}
	select {
	case <-taskDone:
		t.Fatal("忙 Runner 释放前 Waiting 任务已经越过执行槽恢复")
	default:
	}
	close(busyRelease)
	waitSignal(t, taskDone)
	fixture.stop(t)
}

func TestAwaitDeadlinePriorityAndPreCanceledContext(t *testing.T) {
	fixture := newSchedulerFixtureWithServiceTimeout(t, SchedulerConfig{
		MaxTasks:            8,
		MaxAwaitTasks:       8,
		DefaultAwaitTimeout: 400 * time.Millisecond,
	}, 60*time.Millisecond)

	type result struct {
		elapsed time.Duration
		err     error
		called  bool
	}
	results := make(chan result, 3)

	// 没有显式 Deadline 时应使用更短的 Service 级覆盖，而不是 Node 默认。
	if err := fixture.service.DispatchAsync(func(ctx context.Context) {
		startedAt := time.Now()
		err := fixture.service.Await(ctx, func(waitCtx context.Context) error {
			<-waitCtx.Done()
			return waitCtx.Err()
		})
		results <- result{elapsed: time.Since(startedAt), err: err, called: true}
	}); err != nil {
		t.Fatalf("service timeout DispatchAsync() error = %v", err)
	}
	first := receive(t, results)
	if !errs.IsCode(first.err, errs.CodeDeadlineExceeded) ||
		first.elapsed < 30*time.Millisecond ||
		first.elapsed > 250*time.Millisecond {
		t.Fatalf("Service 默认超时结果 = %+v", first)
	}

	// 调用方显式 Deadline 优先级最高，即使它长于 Service 默认值也应原样继承。
	if err := fixture.service.DispatchAsync(func(ctx context.Context) {
		explicit, cancel := context.WithTimeout(ctx, 140*time.Millisecond)
		defer cancel()
		startedAt := time.Now()
		err := fixture.service.Await(explicit, func(waitCtx context.Context) error {
			<-waitCtx.Done()
			return waitCtx.Err()
		})
		results <- result{elapsed: time.Since(startedAt), err: err, called: true}
	}); err != nil {
		t.Fatalf("explicit timeout DispatchAsync() error = %v", err)
	}
	second := receive(t, results)
	if !errs.IsCode(second.err, errs.CodeDeadlineExceeded) ||
		second.elapsed < 100*time.Millisecond ||
		second.elapsed > 350*time.Millisecond {
		t.Fatalf("显式超时结果 = %+v", second)
	}

	// 进入 Await 前已经取消的 Context 必须同步返回，不能释放执行槽或调用等待函数。
	if err := fixture.service.DispatchAsync(func(ctx context.Context) {
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		called := false
		err := fixture.service.Await(canceled, func(context.Context) error {
			called = true
			return nil
		})
		results <- result{err: err, called: called}
	}); err != nil {
		t.Fatalf("pre-canceled DispatchAsync() error = %v", err)
	}
	third := receive(t, results)
	if !errs.IsCode(third.err, errs.CodeCanceled) || third.called {
		t.Fatalf("预取消结果 = %+v", third)
	}

	stats := fixture.service.ExecutionStats()
	if stats.AwaitTotal != 2 || stats.AwaitTimeoutTotal != 2 ||
		stats.AwaitCanceledTotal != 0 {
		t.Fatalf("Deadline ExecutionStats() = %+v", stats)
	}
	fixture.stop(t)
}

// TestAwaitUsesExactlyOnePhysicalDeadline 验证显式超时不重复登记 M8，而默认超时既登记
// 唯一 M8 Deadline，又向等待函数公开标准 Context Deadline。
func TestAwaitUsesExactlyOnePhysicalDeadline(t *testing.T) {
	fixture := newSchedulerFixture(t, SchedulerConfig{
		MaxTasks:            8,
		MaxAwaitTasks:       8,
		DefaultAwaitTimeout: 300 * time.Millisecond,
	})

	type observation struct {
		hasDeadline bool
		remaining   time.Duration
		m8Bindings  int
		err         error
	}
	results := make(chan observation, 2)

	// 第一项没有调用方 Deadline，应只由 M8 管理，同时让下游读取到约 300ms 的截止时间。
	if err := fixture.service.DispatchAsync(func(ctx context.Context) {
		err := fixture.service.Await(ctx, func(waitCtx context.Context) error {
			deadline, ok := waitCtx.Deadline()
			fixture.service.scheduler.Load().mu.Lock()
			bindings := len(fixture.service.scheduler.Load().deadlineBindings)
			fixture.service.scheduler.Load().mu.Unlock()
			results <- observation{
				hasDeadline: ok,
				remaining:   time.Until(deadline),
				m8Bindings:  bindings,
			}
			return nil
		})
		if err != nil {
			results <- observation{err: err}
		}
	}); err != nil {
		t.Fatalf("默认 Deadline DispatchAsync() error = %v", err)
	}
	defaultResult := receive(t, results)
	if defaultResult.err != nil ||
		!defaultResult.hasDeadline ||
		defaultResult.remaining <= 0 ||
		defaultResult.remaining > 300*time.Millisecond ||
		defaultResult.m8Bindings != 1 {
		t.Fatalf("默认 Deadline 观察结果 = %+v", defaultResult)
	}

	// 第二项显式使用 Go Context Timer。Await 必须原样继承它，并且 M8 绑定数量保持为零。
	if err := fixture.service.DispatchAsync(func(ctx context.Context) {
		explicit, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
		defer cancel()
		err := fixture.service.Await(explicit, func(waitCtx context.Context) error {
			deadline, ok := waitCtx.Deadline()
			fixture.service.scheduler.Load().mu.Lock()
			bindings := len(fixture.service.scheduler.Load().deadlineBindings)
			fixture.service.scheduler.Load().mu.Unlock()
			results <- observation{
				hasDeadline: ok,
				remaining:   time.Until(deadline),
				m8Bindings:  bindings,
			}
			return nil
		})
		if err != nil {
			results <- observation{err: err}
		}
	}); err != nil {
		t.Fatalf("显式 Deadline DispatchAsync() error = %v", err)
	}
	explicitResult := receive(t, results)
	if explicitResult.err != nil ||
		!explicitResult.hasDeadline ||
		explicitResult.remaining <= 0 ||
		explicitResult.remaining > 500*time.Millisecond ||
		explicitResult.m8Bindings != 0 {
		t.Fatalf("显式 Deadline 观察结果 = %+v", explicitResult)
	}

	fixture.stop(t)
}

func TestTaskCanAwaitRepeatedlyInSequence(t *testing.T) {
	fixture := newSchedulerFixture(t, DefaultSchedulerConfig())

	done := make(chan struct{})
	if err := fixture.service.DispatchAsync(func(ctx context.Context) {
		for index := 0; index < 8; index++ {
			if err := fixture.service.Await(ctx, func(context.Context) error {
				return nil
			}); err != nil {
				t.Errorf("Await(%d) error = %v", index, err)
				break
			}
		}
		close(done)
	}); err != nil {
		t.Fatalf("DispatchAsync() error = %v", err)
	}
	waitSignal(t, done)
	waitForStats(t, fixture.service, func(stats ExecutionStats) bool {
		return stats.CompletedTotal == 1
	})
	stats := fixture.service.ExecutionStats()
	if stats.AwaitTotal != 8 || stats.CompletedTotal != 1 ||
		stats.Accepted != 0 || stats.Awaiting != 0 {
		t.Fatalf("连续 Await 后 ExecutionStats() = %+v", stats)
	}
	fixture.stop(t)
}

func TestAwaitLimitAndRootTaskLimit(t *testing.T) {
	fixture := newSchedulerFixture(t, SchedulerConfig{
		MaxTasks:            2,
		MaxAwaitTasks:       1,
		DefaultAwaitTimeout: time.Second,
	})

	firstWaiting := make(chan struct{})
	releaseFirst := make(chan struct{})
	secondRunning := make(chan struct{})
	releaseSecond := make(chan struct{})
	var done sync.WaitGroup
	done.Add(2)

	if err := fixture.service.DispatchAsync(func(ctx context.Context) {
		defer done.Done()
		_ = fixture.service.Await(ctx, func(context.Context) error {
			close(firstWaiting)
			<-releaseFirst
			return nil
		})
	}); err != nil {
		t.Fatalf("first DispatchAsync() error = %v", err)
	}
	waitSignal(t, firstWaiting)

	if err := fixture.service.DispatchAsync(func(ctx context.Context) {
		defer done.Done()
		close(secondRunning)
		// Awaiting 已由第一个任务占满，本调用必须在仍持有执行槽时返回，不调用 fn。
		called := false
		err := fixture.service.Await(ctx, func(context.Context) error {
			called = true
			return nil
		})
		if !errors.Is(err, errs.ErrServiceQueueFull) || called {
			t.Errorf("Await limit result = %v, called=%v", err, called)
		}
		<-releaseSecond
	}); err != nil {
		t.Fatalf("second DispatchAsync() error = %v", err)
	}
	waitSignal(t, secondRunning)

	// 两个根任务均尚未结束，第三个任务必须被硬上限立即拒绝。
	if err := fixture.service.DispatchAsync(func(context.Context) {}); !errors.Is(err, errs.ErrServiceQueueFull) {
		t.Fatalf("third DispatchAsync() error = %v, want queue full", err)
	}
	close(releaseSecond)
	close(releaseFirst)
	waitGroup(t, &done)

	stats := fixture.service.ExecutionStats()
	if stats.RejectedTotal != 2 {
		t.Fatalf("RejectedTotal = %d, want 2", stats.RejectedTotal)
	}
	fixture.stop(t)
}

func TestAwaitRejectsInvalidAndCrossServiceContexts(t *testing.T) {
	first := newSchedulerFixture(t, DefaultSchedulerConfig())
	second := newSchedulerFixture(t, DefaultSchedulerConfig())

	if err := first.service.Await(context.Background(), func(context.Context) error {
		return nil
	}); !errors.Is(err, errs.ErrInvalidArgument) {
		t.Fatalf("Background Await() error = %v", err)
	}

	done := make(chan struct{})
	if err := first.service.DispatchAsync(func(ctx context.Context) {
		// Context 属于 first，不能用于释放 second 的执行槽。
		err := second.service.Await(ctx, func(context.Context) error {
			return nil
		})
		if !errors.Is(err, errs.ErrInvalidArgument) {
			t.Errorf("cross-service Await() error = %v", err)
		}
		close(done)
	}); err != nil {
		t.Fatalf("DispatchAsync() error = %v", err)
	}
	waitSignal(t, done)
	first.stop(t)
	second.stop(t)
}

// TestAwaitAcceptsOptionalControlContextInTask 验证普通 Service Task 中的可选 Context
// 外观；每次调用都保持同一串行任务调用栈，不要求调用方传递框架私有令牌。
func TestAwaitAcceptsOptionalControlContextInTask(t *testing.T) {
	fixture := newSchedulerFixture(t, DefaultSchedulerConfig())
	done := make(chan struct{})
	if err := fixture.service.DispatchAsync(func(context.Context) {
		for name, control := range map[string]context.Context{
			"nil":        nil,
			"background": context.Background(),
			"todo":       context.TODO(),
		} {
			called := false
			err := fixture.service.Await(control, func(waitCtx context.Context) error {
				called = true
				if _, exists := waitCtx.Deadline(); !exists {
					t.Errorf("%s Await Context 没有默认 Deadline", name)
				}
				return nil
			})
			if err != nil || !called {
				t.Errorf("%s Await() = %v, called=%v", name, err, called)
			}
		}
		close(done)
	}); err != nil {
		t.Fatalf("DispatchAsync() error = %v", err)
	}
	waitSignal(t, done)
	fixture.stop(t)
}

// TestAwaitNilGetsFreshDefaultBudgetPerPublicCall 验证两个独立 Await(nil) 不共享已经消耗的
// 默认 Deadline；每次公开调用都在进入点重新冻结一份完整预算。
func TestAwaitNilGetsFreshDefaultBudgetPerPublicCall(t *testing.T) {
	config := DefaultSchedulerConfig()
	config.DefaultAwaitTimeout = 25 * time.Millisecond
	fixture := newSchedulerFixture(t, config)
	result := make(chan []time.Duration, 1)
	if err := fixture.service.DispatchAsync(func(context.Context) {
		elapsed := make([]time.Duration, 0, 2)
		for range 2 {
			started := time.Now()
			err := fixture.service.Await(nil, func(waitCtx context.Context) error {
				<-waitCtx.Done()
				return context.Cause(waitCtx)
			})
			if !errors.Is(err, errs.ErrDeadlineExceeded) {
				t.Errorf("Await(nil) error = %v", err)
			}
			elapsed = append(elapsed, time.Since(started))
		}
		result <- elapsed
	}); err != nil {
		t.Fatalf("DispatchAsync() error = %v", err)
	}
	for index, elapsed := range receive(t, result) {
		if elapsed < 15*time.Millisecond || elapsed > 250*time.Millisecond {
			t.Fatalf("Await(nil) #%d elapsed = %s", index+1, elapsed)
		}
	}
	fixture.stop(t)
}

// TestPreparedAwaitContextFreezesOneDeadlineAcrossPhases 验证 RPC Prepare 和响应等待等内部
// 阶段复用同一 operationContext，后续阶段不会重新得到一份默认超时。
func TestPreparedAwaitContextFreezesOneDeadlineAcrossPhases(t *testing.T) {
	fixture := newSchedulerFixture(t, DefaultSchedulerConfig())
	done := make(chan struct{})
	if err := fixture.service.DispatchAsync(func(context.Context) {
		operationCtx, finish, err := PrepareAwaitContext(fixture.service, nil)
		if err != nil {
			t.Errorf("PrepareAwaitContext() error = %v", err)
			close(done)
			return
		}
		defer finish()
		frozen, exists := operationCtx.Deadline()
		if !exists {
			t.Error("prepared operation 没有 Deadline")
			close(done)
			return
		}
		for phase := 0; phase < 2; phase++ {
			err = fixture.service.Await(operationCtx, func(waitCtx context.Context) error {
				current, currentExists := waitCtx.Deadline()
				if !currentExists || !current.Equal(frozen) {
					t.Errorf("phase %d Deadline = %v, want %v", phase, current, frozen)
				}
				return nil
			})
			if err != nil {
				t.Errorf("phase %d Await() error = %v", phase, err)
			}
		}
		close(done)
	}); err != nil {
		t.Fatalf("DispatchAsync() error = %v", err)
	}
	waitSignal(t, done)
	fixture.stop(t)
}

func TestAwaitPanicRestoresSlotAndNextTaskContinues(t *testing.T) {
	fixture := newSchedulerFixture(t, DefaultSchedulerConfig())

	firstDeferred := make(chan struct{})
	secondDone := make(chan struct{})
	if err := fixture.service.DispatchAsync(func(ctx context.Context) {
		defer close(firstDeferred)
		_ = fixture.service.Await(ctx, func(context.Context) error {
			panic("await panic")
		})
	}); err != nil {
		t.Fatalf("first DispatchAsync() error = %v", err)
	}
	if err := fixture.service.DispatchAsync(func(context.Context) {
		close(secondDone)
	}); err != nil {
		t.Fatalf("second DispatchAsync() error = %v", err)
	}
	waitSignal(t, firstDeferred)
	waitSignal(t, secondDone)
	waitForStats(t, fixture.service, func(stats ExecutionStats) bool {
		return stats.CompletedTotal == 2
	})

	stats := fixture.service.ExecutionStats()
	if stats.PanicTotal != 1 || stats.CompletedTotal != 2 ||
		stats.Accepted != 0 || stats.Running != 0 || stats.Awaiting != 0 {
		t.Fatalf("panic 后 ExecutionStats() = %+v", stats)
	}
	fixture.stop(t)
}

func TestStopCancelsAwaitAndWaitsForRealReturn(t *testing.T) {
	fixture := newSchedulerFixture(t, SchedulerConfig{
		MaxTasks:            8,
		MaxAwaitTasks:       8,
		DefaultAwaitTimeout: time.Second,
	})

	waitStarted := make(chan struct{})
	allowReturn := make(chan struct{})
	waitObservedCancel := make(chan struct{})
	taskDone := make(chan struct{})
	if err := fixture.service.DispatchAsync(func(ctx context.Context) {
		err := fixture.service.Await(ctx, func(waitCtx context.Context) error {
			close(waitStarted)
			<-waitCtx.Done()
			close(waitObservedCancel)
			<-allowReturn
			return waitCtx.Err()
		})
		if !errs.IsCode(err, errs.CodeCanceled) {
			t.Errorf("Await() error = %v, want canceled", err)
		}
		close(taskDone)
	}); err != nil {
		t.Fatalf("DispatchAsync() error = %v", err)
	}
	waitSignal(t, waitStarted)

	fixture.runtime.state.Store(uint32(StateStopping))
	stopContext, cancelStop := context.WithCancel(context.Background())
	stopReturned := make(chan error, 1)
	go func() {
		stopReturned <- StopScheduler(stopContext, fixture.service)
	}()
	cancelStop()
	waitSignal(t, waitObservedCancel)

	// fn 已观察取消但尚未真实返回时，StopScheduler 不能提前让 OnStop 获得并发访问机会。
	select {
	case err := <-stopReturned:
		t.Fatalf("等待函数真实返回前 StopScheduler 已返回: %v", err)
	default:
	}
	close(allowReturn)
	waitSignal(t, taskDone)
	stopErr := receive(t, stopReturned)
	if !errs.IsCode(stopErr, errs.CodeCanceled) {
		t.Fatalf("StopScheduler() error = %v, want canceled", stopErr)
	}
	fixture.runtime.state.Store(uint32(StateStopped))
}

func TestSchedulerTenThousandAwaitRecovery(t *testing.T) {
	if testing.Short() {
		t.Skip("短测试模式跳过 10000 Waiting 容量验收")
	}
	const taskCount = 10000
	fixture := newSchedulerFixture(t, SchedulerConfig{
		MaxTasks:            taskCount,
		MaxAwaitTasks:       taskCount,
		DefaultAwaitTimeout: 30 * time.Second,
	})

	release := make(chan struct{})
	var completed sync.WaitGroup
	completed.Add(taskCount)

	// 所有根任务依次进入 Await，每个任务保留原 goroutine；达到配置上限后统一释放，覆盖
	// 恢复风暴、环形队列扩容和连续 Runner 交接。
	for index := 0; index < taskCount; index++ {
		if err := fixture.service.DispatchAsync(func(ctx context.Context) {
			defer completed.Done()
			if err := fixture.service.Await(ctx, func(context.Context) error {
				<-release
				return nil
			}); err != nil {
				t.Errorf("Await() error = %v", err)
			}
		}); err != nil {
			t.Fatalf("DispatchAsync(%d) error = %v", index, err)
		}
	}
	waitForStats(t, fixture.service, func(stats ExecutionStats) bool {
		return stats.Awaiting == taskCount
	})
	close(release)
	waitGroup(t, &completed)
	waitForStats(t, fixture.service, func(stats ExecutionStats) bool {
		return stats.CompletedTotal == taskCount
	})

	stats := fixture.service.ExecutionStats()
	if stats.Accepted != 0 || stats.Awaiting != 0 ||
		stats.CompletedTotal != taskCount || stats.AwaitTotal != taskCount {
		t.Fatalf("10000 Waiting 完成后 ExecutionStats() = %+v", stats)
	}
	fixture.stop(t)
}

func TestSchedulerStopCancellationStorm(t *testing.T) {
	const taskCount = 1000
	fixture := newSchedulerFixture(t, SchedulerConfig{
		MaxTasks:            taskCount,
		MaxAwaitTasks:       taskCount,
		DefaultAwaitTimeout: 30 * time.Second,
	})

	var completed sync.WaitGroup
	completed.Add(taskCount)
	for index := 0; index < taskCount; index++ {
		if err := fixture.service.DispatchAsync(func(ctx context.Context) {
			defer completed.Done()
			err := fixture.service.Await(ctx, func(waitCtx context.Context) error {
				<-waitCtx.Done()
				return waitCtx.Err()
			})
			if !errs.IsCode(err, errs.CodeCanceled) {
				t.Errorf("停止取消 Await() error = %v", err)
			}
		}); err != nil {
			t.Fatalf("DispatchAsync(%d) error = %v", index, err)
		}
	}
	waitForStats(t, fixture.service, func(stats ExecutionStats) bool {
		return stats.Awaiting == taskCount
	})

	// 已取消停止 Context 会一次性传播到全部 Task Context；Stop 仍等待 1000 个原
	// goroutine 依次恢复并真正返回。
	fixture.runtime.state.Store(uint32(StateStopping))
	stopContext, cancel := context.WithCancel(context.Background())
	cancel()
	err := StopScheduler(stopContext, fixture.service)
	if !errs.IsCode(err, errs.CodeCanceled) {
		t.Fatalf("StopScheduler() error = %v", err)
	}
	waitGroup(t, &completed)
	fixture.runtime.state.Store(uint32(StateStopped))

	stats := fixture.service.ExecutionStats()
	if stats.AwaitCanceledTotal != taskCount || stats.Accepted != 0 ||
		stats.Awaiting != 0 || stats.CompletedTotal != taskCount {
		t.Fatalf("停止取消风暴 ExecutionStats() = %+v", stats)
	}
}

func TestSchedulerPublicStateErrorsAndSetDefaultTimeout(t *testing.T) {
	target := &testService{}
	if err := target.DispatchAsync(func(context.Context) {}); !errors.Is(err, errs.ErrServiceNotReady) {
		t.Fatalf("unbound DispatchAsync() error = %v", err)
	}
	if err := target.SetDefaultAwaitTimeout(time.Second); !errors.Is(err, errs.ErrInvalidArgument) {
		t.Fatalf("unbound SetDefaultAwaitTimeout() error = %v", err)
	}

	runtimeState := &schedulerTestRuntime{nodeID: "game-1", name: "PlayerService"}
	runtimeState.state.Store(uint32(StateInitializing))
	if err := BindRuntime(target, runtimeState); err != nil {
		t.Fatalf("BindRuntime() error = %v", err)
	}
	if err := target.SetDefaultAwaitTimeout(25 * time.Millisecond); err != nil {
		t.Fatalf("SetDefaultAwaitTimeout() error = %v", err)
	}
	runtimeState.state.Store(uint32(StateInitialized))
	if err := target.SetDefaultAwaitTimeout(time.Second); !errors.Is(err, errs.ErrInvalidArgument) {
		t.Fatalf("late SetDefaultAwaitTimeout() error = %v", err)
	}
}

func TestSchedulerDispatchStateAndNilErrors(t *testing.T) {
	fixture := newSchedulerFixture(t, DefaultSchedulerConfig())
	if err := fixture.service.DispatchAsync(nil); !errors.Is(err, errs.ErrInvalidArgument) {
		t.Fatalf("nil DispatchAsync() error = %v", err)
	}

	fixture.stop(t)
	if err := fixture.service.DispatchAsync(func(context.Context) {}); !errors.Is(err, errs.ErrServiceStopped) {
		t.Fatalf("stopped DispatchAsync() error = %v", err)
	}
	if err := StopScheduler(context.Background(), fixture.service); err != nil {
		t.Fatalf("重复 StopScheduler() error = %v", err)
	}
}

func TestPrepareActivateAndStopSchedulerValidation(t *testing.T) {
	var typedNil *testService
	if err := PrepareScheduler(nil, SchedulerConfig{}, nil); !errors.Is(err, errs.ErrInvalidArgument) {
		t.Fatalf("nil PrepareScheduler() error = %v", err)
	}
	if err := PrepareScheduler(typedNil, SchedulerConfig{}, nil); !errors.Is(err, errs.ErrInvalidArgument) {
		t.Fatalf("typed nil PrepareScheduler() error = %v", err)
	}
	if err := PrepareScheduler(&testService{}, SchedulerConfig{}, nil); !errors.Is(err, errs.ErrInvalidArgument) {
		t.Fatalf("unbound PrepareScheduler() error = %v", err)
	}
	if err := StopScheduler(nil, &testService{}); !errors.Is(err, errs.ErrInvalidArgument) {
		t.Fatalf("nil Context StopScheduler() error = %v", err)
	}

	target := &testService{}
	runtimeState := &schedulerTestRuntime{nodeID: "game-1", name: "PlayerService"}
	runtimeState.state.Store(uint32(StateCreated))
	if err := BindRuntime(target, runtimeState); err != nil {
		t.Fatalf("BindRuntime() error = %v", err)
	}
	engine, err := timerwheel.New(timerwheel.DefaultOptions())
	if err != nil {
		t.Fatalf("timerwheel.New() error = %v", err)
	}
	defer engine.Close()

	// 错误生命周期、nil Engine 和非法部分配置都必须在创建 DeadlineQueue 前拒绝。
	if err := PrepareScheduler(target, SchedulerConfig{}, engine); !errors.Is(err, errs.ErrInvalidArgument) {
		t.Fatalf("Created PrepareScheduler() error = %v", err)
	}
	runtimeState.state.Store(uint32(StateStarting))
	if err := PrepareScheduler(target, SchedulerConfig{}, nil); !errors.Is(err, errs.ErrInvalidArgument) {
		t.Fatalf("nil Engine PrepareScheduler() error = %v", err)
	}
	if err := PrepareScheduler(target, SchedulerConfig{
		MaxTasks:            1,
		MaxAwaitTasks:       2,
		DefaultAwaitTimeout: time.Second,
	}, engine); !errs.IsCode(err, errs.CodeInvalidConfig) {
		t.Fatalf("invalid config PrepareScheduler() error = %v", err)
	}

	// 尚未运行的 Engine 不能创建 DeadlineQueue；启动后完全零值配置自动使用稳定默认。
	if err := PrepareScheduler(target, SchedulerConfig{}, engine); err == nil {
		t.Fatal("未启动 Engine 的 PrepareScheduler() 未返回错误")
	}
	if err := engine.Start(); err != nil {
		t.Fatalf("Engine.Start() error = %v", err)
	}
	if err := PrepareScheduler(target, SchedulerConfig{}, engine); err != nil {
		t.Fatalf("zero config PrepareScheduler() error = %v", err)
	}
	if err := PrepareScheduler(target, SchedulerConfig{}, engine); !errors.Is(err, errs.ErrInvalidArgument) {
		t.Fatalf("duplicate PrepareScheduler() error = %v", err)
	}
	if err := ActivateScheduler(nil); !errors.Is(err, errs.ErrInvalidArgument) {
		t.Fatalf("nil ActivateScheduler() error = %v", err)
	}
	if err := ActivateScheduler(target); !errors.Is(err, errs.ErrInvalidArgument) {
		t.Fatalf("Starting ActivateScheduler() error = %v", err)
	}
	runtimeState.state.Store(uint32(StateRunning))
	if err := ActivateScheduler(target); err != nil {
		t.Fatalf("ActivateScheduler() error = %v", err)
	}
	if err := ActivateScheduler(target); !errors.Is(err, errs.ErrInvalidArgument) {
		t.Fatalf("duplicate ActivateScheduler() error = %v", err)
	}

	runtimeState.state.Store(uint32(StateStopping))
	if err := StopScheduler(context.Background(), target); err != nil {
		t.Fatalf("StopScheduler() error = %v", err)
	}
	runtimeState.state.Store(uint32(StateStopped))
	if err := StopScheduler(context.Background(), target); err != nil {
		t.Fatalf("duplicate StopScheduler() error = %v", err)
	}
	if err := StopScheduler(context.Background(), &testService{}); err != nil {
		t.Fatalf("未启动 Service StopScheduler() error = %v", err)
	}
}

func TestStopSchedulerDeadlineReturnsGracefulTimeoutCode(t *testing.T) {
	fixture := newSchedulerFixture(t, SchedulerConfig{
		MaxTasks:            4,
		MaxAwaitTasks:       4,
		DefaultAwaitTimeout: time.Second,
	})

	waitStarted := make(chan struct{})
	if err := fixture.service.DispatchAsync(func(ctx context.Context) {
		_ = fixture.service.Await(ctx, func(waitCtx context.Context) error {
			close(waitStarted)
			<-waitCtx.Done()
			return waitCtx.Err()
		})
	}); err != nil {
		t.Fatalf("DispatchAsync() error = %v", err)
	}
	waitSignal(t, waitStarted)

	fixture.runtime.state.Store(uint32(StateStopping))
	stopContext, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	err := StopScheduler(stopContext, fixture.service)
	if !errs.IsCode(err, errs.CodeGracefulShutdownTimeout) {
		t.Fatalf("StopScheduler() error = %v, want graceful timeout", err)
	}
	fixture.runtime.state.Store(uint32(StateStopped))
}

func TestRootTaskPanicDoesNotStopRunner(t *testing.T) {
	fixture := newSchedulerFixture(t, DefaultSchedulerConfig())
	nextDone := make(chan struct{})

	if err := fixture.service.DispatchAsync(func(context.Context) {
		panic("root panic")
	}); err != nil {
		t.Fatalf("panic DispatchAsync() error = %v", err)
	}
	if err := fixture.service.DispatchAsync(func(context.Context) {
		close(nextDone)
	}); err != nil {
		t.Fatalf("next DispatchAsync() error = %v", err)
	}
	waitSignal(t, nextDone)
	waitForStats(t, fixture.service, func(stats ExecutionStats) bool {
		return stats.CompletedTotal == 2
	})
	stats := fixture.service.ExecutionStats()
	if stats.PanicTotal != 1 || stats.Accepted != 0 {
		t.Fatalf("根任务 panic 后 ExecutionStats() = %+v", stats)
	}
	fixture.stop(t)
}

func TestSchedulerInternalDispatchStateMapping(t *testing.T) {
	fixture := newSchedulerFixture(t, DefaultSchedulerConfig())
	scheduler := fixture.service.scheduler.Load()

	// 公开 Service 状态通常先行拒绝这些情况；直接调用内部入口锁定 Scheduler 自身的
	// Created、Draining 和 Stopped 映射，供后续框架投递入口复用。
	for _, testCase := range []struct {
		state schedulerState
		want  error
	}{
		{state: schedulerCreated, want: errs.ErrServiceNotReady},
		{state: schedulerDraining, want: errs.ErrServiceStopping},
		{state: schedulerStopped, want: errs.ErrServiceStopped},
	} {
		scheduler.mu.Lock()
		scheduler.state = testCase.state
		scheduler.mu.Unlock()
		if err := scheduler.dispatch(func(context.Context) {}); !errors.Is(err, testCase.want) {
			t.Fatalf("dispatch state %d error = %v, want %v", testCase.state, err, testCase.want)
		}
	}
	scheduler.mu.Lock()
	scheduler.state = schedulerRunning
	scheduler.mu.Unlock()
	fixture.stop(t)
}

func TestNestedAwaitIsRejectedWithoutReleasingSecondSlot(t *testing.T) {
	fixture := newSchedulerFixture(t, DefaultSchedulerConfig())
	done := make(chan struct{})

	if err := fixture.service.DispatchAsync(func(ctx context.Context) {
		err := fixture.service.Await(ctx, func(waitCtx context.Context) error {
			called := false
			nestedErr := fixture.service.Await(waitCtx, func(context.Context) error {
				called = true
				return nil
			})
			if !errors.Is(nestedErr, errs.ErrInvalidArgument) || called {
				t.Errorf("nested Await() = %v, called=%v", nestedErr, called)
			}
			return nil
		})
		if err != nil {
			t.Errorf("outer Await() error = %v", err)
		}
		close(done)
	}); err != nil {
		t.Fatalf("DispatchAsync() error = %v", err)
	}
	waitSignal(t, done)
	stats := fixture.service.ExecutionStats()
	if stats.AwaitTotal != 1 {
		t.Fatalf("AwaitTotal = %d, want 1", stats.AwaitTotal)
	}
	fixture.stop(t)
}

func TestCompletedContextDoesNotCreateExecutionFrame(t *testing.T) {
	fixture := newSchedulerFixture(t, DefaultSchedulerConfig())

	oldContext := make(chan context.Context, 1)
	firstDone := make(chan struct{})
	if err := fixture.service.DispatchAsync(func(ctx context.Context) {
		oldContext <- ctx
		close(firstDone)
	}); err != nil {
		t.Fatalf("first DispatchAsync() error = %v", err)
	}
	retained := receive(t, oldContext)
	waitSignal(t, firstDone)
	waitForStats(t, fixture.service, func(stats ExecutionStats) bool {
		return stats.CompletedTotal == 1
	})

	// Context 只携带控制语义，不再是执行权令牌。先让任务池经历充分复用，再在没有活动
	// 执行帧时验证旧 Context 不能凭空创建 Await 环境。普通 goroutine 与活动 Task 并发
	// 调用 Await 属于 API 误用，应使用生成的 CallXxx，框架不依赖 goid 猜测该情况。
	var completed sync.WaitGroup
	completed.Add(1000)
	for index := 0; index < 1000; index++ {
		if err := fixture.service.DispatchAsync(func(context.Context) {
			completed.Done()
		}); err != nil {
			t.Fatalf("reuse DispatchAsync(%d) error = %v", index, err)
		}
	}
	waitGroup(t, &completed)

	for attempt := 0; attempt < 1000; attempt++ {
		called := false
		err := fixture.service.Await(retained, func(context.Context) error {
			called = true
			return nil
		})
		if !errors.Is(err, errs.ErrInvalidArgument) || called {
			t.Fatalf("旧 Context Await() = %v, called=%v", err, called)
		}
	}
	fixture.stop(t)
}

func TestSchedulerConfigValidation(t *testing.T) {
	t.Parallel()

	if err := DefaultSchedulerConfig().Validate(); err != nil {
		t.Fatalf("DefaultSchedulerConfig().Validate() error = %v", err)
	}
	for _, config := range []SchedulerConfig{
		{MaxTasks: 0, MaxAwaitTasks: 1, DefaultAwaitTimeout: time.Second},
		{MaxTasks: MaxSchedulerTasks + 1, MaxAwaitTasks: 1, DefaultAwaitTimeout: time.Second},
		{MaxTasks: 2, MaxAwaitTasks: 0, DefaultAwaitTimeout: time.Second},
		{MaxTasks: 2, MaxAwaitTasks: 3, DefaultAwaitTimeout: time.Second},
		{MaxTasks: 2, MaxAwaitTasks: 1, DefaultAwaitTimeout: 0},
	} {
		if err := config.Validate(); !errs.IsCode(err, errs.CodeInvalidConfig) {
			t.Fatalf("Validate(%+v) error = %v", config, err)
		}
	}
}

func TestSchedulerGoroutinesExitAfterRepeatedStartStop(t *testing.T) {
	baseline := runtime.NumGoroutine()

	// 重复建立和停止多个独立 Scheduler，覆盖 Runner、Deadline watcher、DeadlineQueue 和
	// TimerEngine 的完整所有权回收。
	for iteration := 0; iteration < 50; iteration++ {
		fixture := newSchedulerFixture(t, DefaultSchedulerConfig())
		fixture.stop(t)
		if err := fixture.engine.Close(); err != nil {
			t.Fatalf("Engine.Close(%d) error = %v", iteration, err)
		}
	}

	// Go 测试运行时自身可能保留少量辅助 goroutine，因此等待回落到有界余量而不是要求
	// 与瞬时基线完全相等。
	deadline := time.Now().Add(schedulerTestTimeout)
	for time.Now().Before(deadline) {
		runtime.GC()
		if current := runtime.NumGoroutine(); current <= baseline+8 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf(
		"重复停止后 goroutine = %d, baseline = %d",
		runtime.NumGoroutine(),
		baseline,
	)
}

func waitSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(schedulerTestTimeout):
		t.Fatal("等待测试信号超时")
	}
}

func waitGroup(t *testing.T, group *sync.WaitGroup) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		group.Wait()
		close(done)
	}()
	waitSignal(t, done)
}

func receive[T any](t *testing.T, values <-chan T) T {
	t.Helper()
	select {
	case value := <-values:
		return value
	case <-time.After(schedulerTestTimeout):
		t.Fatal("等待测试结果超时")
		var zero T
		return zero
	}
}

func waitForStats(
	t *testing.T,
	target *testService,
	predicate func(ExecutionStats) bool,
) {
	t.Helper()
	deadline := time.Now().Add(schedulerTestTimeout)
	for time.Now().Before(deadline) {
		if predicate(target.ExecutionStats()) {
			return
		}
		runtime.Gosched()
	}
	t.Fatalf("等待统计条件超时，最终值 = %+v", target.ExecutionStats())
}

// currentGoroutineID 只在测试中解析 runtime.Stack 首行，用于验证 Await 没有更换原 goroutine。
//
// 生产实现不依赖 goroutine ID；该格式不是公共 Go API，因此该辅助函数不得进入非测试代码。
func currentGoroutineID(t *testing.T) uint64 {
	t.Helper()
	var stack [64]byte
	length := runtime.Stack(stack[:], false)
	fields := strings.Fields(string(stack[:length]))
	if len(fields) < 2 {
		t.Fatalf("无法解析 goroutine stack: %q", stack[:length])
	}
	id, err := strconv.ParseUint(fields[1], 10, 64)
	if err != nil {
		t.Fatalf("解析 goroutine ID: %v", err)
	}
	return id
}
