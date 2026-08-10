package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/duanhf2012/origin/v3/errs"
	"github.com/duanhf2012/origin/v3/internal/timerwheel"
	originlog "github.com/duanhf2012/origin/v3/log"
)

// enabledLogHandler 让 Module Logger 委托测试观察到真实 Runtime 的 Enabled 行为。
type enabledLogHandler struct{}

func (*enabledLogHandler) Enabled(originlog.Level) bool                    { return true }
func (*enabledLogHandler) Write(originlog.Record, []originlog.Field) error { return nil }
func (*enabledLogHandler) Sync() error                                     { return nil }
func (*enabledLogHandler) Close() error                                    { return nil }

// TestModuleLoggerDelegatesToOwnerService 防止 Module 建立独立 Logger 或未绑定时误用全局入口。
func TestModuleLoggerDelegatesToOwnerService(t *testing.T) {
	var unbound Module
	if unbound.Logger().Enabled(originlog.InfoLevel) {
		t.Fatal("unbound Module.Logger() is enabled")
	}

	runtime, err := originlog.NewRuntime(originlog.DefaultConfig(), &enabledLogHandler{})
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	owner := &testService{}
	if err := BindRuntime(owner, &testRuntime{
		nodeID: "game-1",
		name:   "PlayerService",
		state:  StateRunning,
		logger: runtime.Logger(),
	}); err != nil {
		t.Fatalf("BindRuntime() error = %v", err)
	}
	module := &testModule{}
	module.owner = &owner.Service
	if !module.Logger().Enabled(originlog.InfoLevel) {
		t.Fatal("bound Module.Logger() did not delegate to Service Logger")
	}
}

type testModule struct {
	Module
	init  func() error
	start func(context.Context) error
	stop  func(context.Context) error
}

type lifecycleService struct {
	Service
	order *[]string
	root  IModule
	start func(context.Context) error
	stop  func(context.Context) error
}

func (target *lifecycleService) OnInit() error {
	*target.order = append(*target.order, "service.init")
	return target.AddModule(target.root)
}

func (target *lifecycleService) OnStart(ctx context.Context) error {
	*target.order = append(*target.order, "service.start")
	if target.start != nil {
		return target.start(ctx)
	}
	return nil
}

func (target *lifecycleService) OnStop(ctx context.Context) error {
	*target.order = append(*target.order, "service.stop")
	if target.stop != nil {
		return target.stop(ctx)
	}
	return nil
}

type panicStopService struct{ Service }

func (*panicStopService) OnStop(context.Context) error { panic("service stop") }

func TestServiceStopPanicUsesServiceDiagnostic(t *testing.T) {
	target := &panicStopService{}
	if err := BindRuntime(target, &testRuntime{nodeID: "node-1", name: "Owner", state: StateStopping}); err != nil {
		t.Fatal(err)
	}
	target.moduleSealed = true
	target.serviceStartEntered = true
	err := StopWithModules(t.Context(), target)
	if err == nil || !strings.Contains(err.Error(), "Service") || strings.Contains(err.Error(), "Module <nil>") {
		t.Fatalf("StopWithModules() error = %v", err)
	}
}

func TestModuleScopeCancelsOwnedTimers(t *testing.T) {
	fixture := newSchedulerFixture(t, DefaultSchedulerConfig())
	module := &testModule{}
	module.owner = &fixture.service.Service
	id := module.NewTicker(time.Hour, func(context.Context, TimerID) {})
	if id == InvalidTimerID {
		t.Fatal("Module.NewTicker() returned InvalidTimerID")
	}
	if active := fixture.runtime.active.Load(); active != 1 {
		t.Fatalf("active timers = %d, want 1", active)
	}

	module.cleanupScope()
	if active := fixture.runtime.active.Load(); active != 0 {
		t.Fatalf("active timers after cleanup = %d, want 0", active)
	}
	module.scopeMu.Lock()
	remaining := len(module.timers)
	module.scopeMu.Unlock()
	if remaining != 0 {
		t.Fatalf("module timer registrations = %d", remaining)
	}
}

// TestModuleTimerControlHonorsResourceScope 固定 Timer 的公开所有权语义：Service、兄弟
// Module 都不能控制另一个作用域创建的 Timer，创建者本身仍可正常控制和清理。
func TestModuleTimerControlHonorsResourceScope(t *testing.T) {
	fixture := newSchedulerFixture(t, DefaultSchedulerConfig())
	first := &testModule{}
	second := &testModule{}
	first.owner = &fixture.service.Service
	second.owner = &fixture.service.Service

	moduleTimer := first.NewTicker(time.Hour, func(context.Context, TimerID) {})
	if moduleTimer == InvalidTimerID {
		t.Fatal("Module.NewTicker() returned InvalidTimerID")
	}
	if second.PauseTimer(moduleTimer) {
		t.Fatal("兄弟 Module 暂停了不属于自己的 Timer")
	}
	if fixture.service.PauseTimer(moduleTimer) {
		t.Fatal("Service 暂停了 Module 作用域的 Timer")
	}
	if !first.PauseTimer(moduleTimer) {
		t.Fatal("创建 Module 无法暂停自己的 Timer")
	}
	if second.ResumeTimer(moduleTimer) || fixture.service.ResumeTimer(moduleTimer) {
		t.Fatal("非创建作用域恢复了 Module Timer")
	}
	if !first.ResumeTimer(moduleTimer) {
		t.Fatal("创建 Module 无法恢复自己的 Timer")
	}

	foreignModuleID := moduleTimer
	if second.CancelTimer(&foreignModuleID) {
		t.Fatal("兄弟 Module 取消了不属于自己的 Timer")
	}
	if foreignModuleID != InvalidTimerID {
		t.Fatal("Module.CancelTimer 未清零调用方的非零 ID")
	}
	foreignServiceID := moduleTimer
	if fixture.service.CancelTimer(&foreignServiceID) {
		t.Fatal("Service 取消了 Module 作用域的 Timer")
	}
	if foreignServiceID != InvalidTimerID {
		t.Fatal("Service.CancelTimer 未清零调用方的非零 ID")
	}
	if !first.CancelTimer(&moduleTimer) {
		t.Fatal("创建 Module 无法取消自己的 Timer")
	}

	serviceTimer := fixture.service.NewTicker(time.Hour, func(context.Context, TimerID) {})
	if serviceTimer == InvalidTimerID {
		t.Fatal("Service.NewTicker() returned InvalidTimerID")
	}
	if first.PauseTimer(serviceTimer) {
		t.Fatal("Module 暂停了 Service 作用域的 Timer")
	}
	if !fixture.service.CancelTimer(&serviceTimer) {
		t.Fatal("Service 无法取消自己的 Timer")
	}
	fixture.stop(t)
}

// TestModuleAutomaticTimerTerminationReleasesScopeRegistration 防止周期 Timer 因连续 panic
// 被框架自动取消后，只释放 Scheduler 对象却把陈旧 ID 永久留在 Module 作用域。
func TestModuleAutomaticTimerTerminationReleasesScopeRegistration(t *testing.T) {
	fixture := newTimerFixture(t, 8)
	module := &testModule{}
	module.owner = &fixture.service.Service
	id := module.NewTicker(timerwheel.TickDuration, func(context.Context, TimerID) {
		panic("expected ticker panic")
	})
	if id == InvalidTimerID {
		t.Fatal("Module.NewTicker() returned InvalidTimerID")
	}

	for index := 0; index < maxConsecutiveTimerPanics; index++ {
		advanceTimerFixture(t, fixture, timerwheel.TickDuration)
		expected := uint64(index + 1)
		waitForTimerStats(t, fixture.service, func(stats TimerStats) bool {
			if expected == maxConsecutiveTimerPanics {
				return stats.TriggeredTotal == expected && stats.Active == 0
			}
			return stats.TriggeredTotal == expected && stats.Scheduled == 1
		})
	}
	module.scopeMu.Lock()
	remaining := len(module.timers)
	module.scopeMu.Unlock()
	if remaining != 0 {
		t.Fatalf("自动终结后 Module Timer 登记数 = %d，期望 0", remaining)
	}
}

func TestModuleTimerFacadeCreatesControlsAndCleansAllKinds(t *testing.T) {
	fixture := newTimerFixture(t, 8)
	module := &testModule{}
	module.owner = &fixture.service.Service

	afterID := module.AfterFunc(time.Hour, func(context.Context, TimerID) {})
	tickerID := module.NewTicker(time.Hour, func(context.Context, TimerID) {})
	cronID, err := module.CronFunc("* * * * *", func(context.Context, TimerID) {})
	if afterID == InvalidTimerID || tickerID == InvalidTimerID ||
		cronID == InvalidTimerID || err != nil {
		t.Fatalf("Module Timer creation = after:%d ticker:%d cron:%d error:%v",
			afterID, tickerID, cronID, err)
	}
	if !module.PauseTimer(afterID) || !module.ResumeTimer(afterID) {
		t.Fatal("Module 无法暂停并恢复自己的 After Timer")
	}
	if stats := module.TimerStats(); stats.Active != 3 {
		t.Fatalf("Module.TimerStats() = %+v", stats)
	}

	module.cleanupScope()
	if active := fixture.runtime.active.Load(); active != 0 {
		t.Fatalf("Module cleanup 后 Node Timer 额度 = %d", active)
	}
}

func TestModuleDelegatesExecutionEventAwaitAndSafeBoundaries(t *testing.T) {
	fixture := newSchedulerFixture(t, DefaultSchedulerConfig())
	module := &testModule{}
	module.owner = &fixture.service.Service

	backgroundDone := make(chan struct{})
	if err := module.GoSafe(func() { close(backgroundDone) }); err != nil {
		t.Fatalf("Module.GoSafe() error = %v", err)
	}
	waitSignal(t, backgroundDone)

	taskDone := make(chan struct{})
	if err := module.DispatchAsync(func(ctx context.Context) {
		if err := module.Await(ctx, func(context.Context) error { return nil }); err != nil {
			t.Errorf("Module.Await() error = %v", err)
		}
		if err := module.NotifyEventSync(ctx, &testEvent{id: 91}); err != nil {
			t.Errorf("Module.NotifyEventSync() error = %v", err)
		}
		if err := module.NotifyEventAsync(&testEvent{id: 91}); err != nil {
			t.Errorf("Module.NotifyEventAsync() error = %v", err)
		}
		if stats := module.EventStats(); stats.SyncNotifiedTotal != 1 || stats.AsyncNotifiedTotal != 1 {
			t.Errorf("Module.EventStats() = %+v", stats)
		}
		if stats := module.ExecutionStats(); stats.Running != 1 {
			t.Errorf("Module.ExecutionStats() = %+v", stats)
		}
		close(taskDone)
	}); err != nil {
		t.Fatalf("Module.DispatchAsync() error = %v", err)
	}
	waitSignal(t, taskDone)
	waitForStats(t, fixture.service, func(stats ExecutionStats) bool {
		return stats.CompletedTotal == 2
	})
	fixture.stop(t)
}

func TestUnboundModuleExecutionAndTimerFacadeIsSafe(t *testing.T) {
	var module Module
	if err := module.DispatchAsync(func(context.Context) {}); !errors.Is(err, errs.ErrInvalidArgument) {
		t.Fatalf("unbound DispatchAsync() error = %v", err)
	}
	if err := module.NotifyEventSync(context.Background(), &testEvent{id: 1}); !errors.Is(err, errs.ErrInvalidArgument) {
		t.Fatalf("unbound NotifyEventSync() error = %v", err)
	}
	if err := module.NotifyEventAsync(&testEvent{id: 1}); !errors.Is(err, errs.ErrInvalidArgument) {
		t.Fatalf("unbound NotifyEventAsync() error = %v", err)
	}
	if err := module.Await(nil, func(context.Context) error { return nil }); !errors.Is(err, errs.ErrInvalidArgument) {
		t.Fatalf("unbound Await() error = %v", err)
	}
	if err := module.SetDefaultAwaitTimeout(time.Second); !errors.Is(err, errs.ErrInvalidArgument) {
		t.Fatalf("unbound SetDefaultAwaitTimeout() error = %v", err)
	}
	if err := module.GoSafe(func() {}); !errors.Is(err, errs.ErrInvalidArgument) {
		t.Fatalf("unbound GoSafe() error = %v", err)
	}
	if stats := module.EventStats(); stats != (EventStats{}) {
		t.Fatalf("unbound EventStats() = %+v", stats)
	}
	if stats := module.ExecutionStats(); stats != (ExecutionStats{}) {
		t.Fatalf("unbound ExecutionStats() = %+v", stats)
	}
	if stats := module.TimerStats(); stats != (TimerStats{}) {
		t.Fatalf("unbound TimerStats() = %+v", stats)
	}
	if module.AfterFunc(time.Second, func(context.Context, TimerID) {}) != InvalidTimerID ||
		module.NewTicker(time.Second, func(context.Context, TimerID) {}) != InvalidTimerID {
		t.Fatal("unbound Module created Timer")
	}
	if id, err := module.CronFunc("* * * * *", func(context.Context, TimerID) {}); id != InvalidTimerID || !errors.Is(err, errs.ErrInvalidArgument) {
		t.Fatalf("unbound CronFunc() = %d, %v", id, err)
	}
	if module.PauseTimer(1) || module.ResumeTimer(1) || module.CancelTimer(nil) {
		t.Fatal("unbound Module controlled Timer")
	}
	id := TimerID(1)
	if module.CancelTimer(&id) || id != InvalidTimerID {
		t.Fatalf("unbound CancelTimer() result or ID = %d", id)
	}
}

// TestModuleGetNodeDelegatesOwnerRuntime 防止 Module 建立第二份时间作用域；它必须返回所属
// Service 已经绑定的同一个 Node 运行外观。
func TestModuleGetNodeDelegatesOwnerRuntime(t *testing.T) {
	unbound := &testModule{}
	if unbound.GetNode() != nil {
		t.Fatal("未绑定 Module.GetNode() 未返回 nil")
	}

	owner := &testService{}
	runtime := &testRuntime{
		nodeID: "game-1",
		name:   "Owner",
		state:  StateRunning,
		now:    time.Date(2031, 2, 3, 4, 5, 6, 0, time.UTC),
	}
	if err := BindRuntime(owner, runtime); err != nil {
		t.Fatalf("BindRuntime() error = %v", err)
	}
	module := &testModule{}
	module.owner = &owner.Service

	currentNode := module.GetNode()
	if currentNode == nil || currentNode.ID() != "game-1" {
		t.Fatalf("Module.GetNode() = %#v", currentNode)
	}
	if !currentNode.Now().Equal(runtime.now) {
		t.Fatalf("Module.GetNode().Now() = %v, want %v", currentNode.Now(), runtime.now)
	}
}

func (module *testModule) OnInit() error {
	if module.init != nil {
		return module.init()
	}
	return nil
}

func (module *testModule) OnStart(ctx context.Context) error {
	if module.start != nil {
		return module.start(ctx)
	}
	return nil
}

func (module *testModule) OnStop(ctx context.Context) error {
	if module.stop != nil {
		return module.stop(ctx)
	}
	return nil
}

func TestAddModuleInitializesNestedTreeSynchronously(t *testing.T) {
	owner := &Service{}
	runtime := &testRuntime{nodeID: "node-1", name: "Owner", state: StateInitializing}
	if err := BindRuntime(owner, runtime); err != nil {
		t.Fatal(err)
	}
	if err := BeginModuleInitialization(owner); err != nil {
		t.Fatal(err)
	}
	var order []string
	child := &testModule{init: func() error {
		order = append(order, "child")
		return nil
	}}
	parent := &testModule{}
	parent.init = func() error {
		order = append(order, "parent")
		return parent.AddModule(child)
	}
	if err := owner.AddModule(parent); err != nil {
		t.Fatalf("AddModule() error = %v", err)
	}
	if err := CompleteModuleInitialization(owner, true); err != nil {
		t.Fatalf("CompleteModuleInitialization() error = %v", err)
	}
	if len(order) != 2 || order[0] != "parent" || order[1] != "child" {
		t.Fatalf("init order = %v", order)
	}
	if parent.Service() != owner || child.Service() != owner {
		t.Fatal("Module.Service() 未返回所属 Service")
	}
	if err := owner.AddModule(&testModule{}); err == nil {
		t.Fatal("初始化封树后 AddModule() 未失败")
	}
	if err := parent.AddModule(&testModule{}); err == nil {
		t.Fatal("父 Module OnInit 返回后 AddModule() 未失败")
	}
}

// TestServiceAndModulesStartAndStopInStrictLifecycleOrder 固定教程承诺的父先子后启动、
// 子先父后停止语义，并覆盖 Service/Module 默认装配边界的完整成功路径。
func TestServiceAndModulesStartAndStopInStrictLifecycleOrder(t *testing.T) {
	var order []string
	child := &testModule{
		init: func() error {
			order = append(order, "child.init")
			return nil
		},
		start: func(context.Context) error {
			order = append(order, "child.start")
			return nil
		},
		stop: func(context.Context) error {
			order = append(order, "child.stop")
			return nil
		},
	}
	root := &testModule{
		start: func(context.Context) error {
			order = append(order, "root.start")
			return nil
		},
		stop: func(context.Context) error {
			order = append(order, "root.stop")
			return nil
		},
	}
	root.init = func() error {
		order = append(order, "root.init")
		return root.AddModule(child)
	}
	target := &lifecycleService{order: &order, root: root}
	runtime := &testRuntime{nodeID: "node-1", name: "Owner", state: StateInitializing}
	if err := BindRuntime(target, runtime); err != nil {
		t.Fatal(err)
	}
	if err := BeginModuleInitialization(target); err != nil {
		t.Fatal(err)
	}
	if err := target.OnInit(); err != nil {
		t.Fatal(err)
	}
	if err := CompleteModuleInitialization(target, true); err != nil {
		t.Fatal(err)
	}

	runtime.state = StateStarting
	if err := StartWithModules(t.Context(), target); err != nil {
		t.Fatalf("StartWithModules() error = %v", err)
	}
	runtime.state = StateStopping
	if err := StopWithModules(t.Context(), target); err != nil {
		t.Fatalf("StopWithModules() error = %v", err)
	}

	want := "service.init,root.init,child.init,service.start,root.start,child.start," +
		"child.stop,root.stop,service.stop"
	if got := strings.Join(order, ","); got != want {
		t.Fatalf("lifecycle order = %q, want %q", got, want)
	}
}

// TestModuleStartFailureRollsBackEnteredObjects 验证失败 Module 自身也会进入 OnStop，
// 后续 Module 不启动，且停止错误或 panic 不会跳过其余 Module 与最终 Service 清理。
func TestModuleStartFailureRollsBackEnteredObjects(t *testing.T) {
	startErr := errors.New("child start failed")
	childStopErr := errors.New("child stop failed")
	var order []string
	child := &testModule{
		start: func(context.Context) error {
			order = append(order, "child.start")
			return startErr
		},
		stop: func(context.Context) error {
			order = append(order, "child.stop")
			return childStopErr
		},
	}
	root := &testModule{
		start: func(context.Context) error {
			order = append(order, "root.start")
			return nil
		},
		stop: func(context.Context) error {
			order = append(order, "root.stop")
			panic("root stop panic")
		},
	}
	root.init = func() error { return root.AddModule(child) }
	target := &lifecycleService{order: &order, root: root}
	runtime := &testRuntime{nodeID: "node-1", name: "Owner", state: StateInitializing}
	if err := BindRuntime(target, runtime); err != nil {
		t.Fatal(err)
	}
	if err := BeginModuleInitialization(target); err != nil {
		t.Fatal(err)
	}
	if err := target.OnInit(); err != nil {
		t.Fatal(err)
	}
	if err := CompleteModuleInitialization(target, true); err != nil {
		t.Fatal(err)
	}

	runtime.state = StateStarting
	if err := StartWithModules(t.Context(), target); !errors.Is(err, startErr) {
		t.Fatalf("StartWithModules() error = %v, want child start error", err)
	}
	runtime.state = StateStopping
	stopErr := StopWithModules(t.Context(), target)
	if !errors.Is(stopErr, childStopErr) || !strings.Contains(stopErr.Error(), "root stop panic") {
		t.Fatalf("StopWithModules() error = %v", stopErr)
	}
	if got, want := strings.Join(order, ","),
		"service.init,service.start,root.start,child.start,child.stop,root.stop,service.stop"; got != want {
		t.Fatalf("rollback order = %q, want %q", got, want)
	}
	if err := StopWithModules(t.Context(), target); err != nil {
		t.Fatalf("repeated StopWithModules() error = %v", err)
	}
}

// TestServiceStartFailureStopsOnlyService 固定 Service 已进入 OnStart 后的清理责任：Module
// 尚未启动，因此不能调用 Module.OnStop，但 Service.OnStop 必须执行一次。
func TestServiceStartFailureStopsOnlyService(t *testing.T) {
	startErr := errors.New("service start failed")
	var order []string
	root := &testModule{
		start: func(context.Context) error {
			order = append(order, "root.start")
			return nil
		},
		stop: func(context.Context) error {
			order = append(order, "root.stop")
			return nil
		},
	}
	target := &lifecycleService{
		order: &order,
		root:  root,
		start: func(context.Context) error { return startErr },
	}
	runtime := &testRuntime{nodeID: "node-1", name: "Owner", state: StateInitializing}
	if err := BindRuntime(target, runtime); err != nil {
		t.Fatal(err)
	}
	if err := BeginModuleInitialization(target); err != nil {
		t.Fatal(err)
	}
	if err := target.OnInit(); err != nil {
		t.Fatal(err)
	}
	if err := CompleteModuleInitialization(target, true); err != nil {
		t.Fatal(err)
	}

	runtime.state = StateStarting
	if err := StartWithModules(t.Context(), target); !errors.Is(err, startErr) {
		t.Fatalf("StartWithModules() error = %v, want service start error", err)
	}
	runtime.state = StateStopping
	if err := StopWithModules(t.Context(), target); err != nil {
		t.Fatalf("StopWithModules() error = %v", err)
	}
	if got, want := strings.Join(order, ","), "service.init,service.start,service.stop"; got != want {
		t.Fatalf("rollback order = %q, want %q", got, want)
	}
}

func TestAddModuleRejectsDuplicateOwnershipAndDepthOverflow(t *testing.T) {
	first := &Service{}
	second := &Service{}
	firstRuntime := &testRuntime{nodeID: "node-1", name: "First", state: StateInitializing}
	secondRuntime := &testRuntime{nodeID: "node-1", name: "Second", state: StateInitializing}
	if err := BindRuntime(first, firstRuntime); err != nil {
		t.Fatal(err)
	}
	if err := BindRuntime(second, secondRuntime); err != nil {
		t.Fatal(err)
	}
	if err := BeginModuleInitialization(first); err != nil {
		t.Fatal(err)
	}
	if err := BeginModuleInitialization(second); err != nil {
		t.Fatal(err)
	}
	shared := &testModule{}
	if err := first.AddModule(shared); err != nil {
		t.Fatal(err)
	}
	if err := second.AddModule(shared); err == nil {
		t.Fatal("跨 Service 重复绑定未失败")
	}

	root := &testModule{}
	current := root
	for depth := 1; depth < MaxModuleDepth; depth++ {
		next := &testModule{}
		parent := current
		parent.init = func() error { return parent.AddModule(next) }
		current = next
	}
	current.init = func() error { return current.AddModule(&testModule{}) }
	third := &Service{}
	thirdRuntime := &testRuntime{nodeID: "node-1", name: "Third", state: StateInitializing}
	if err := BindRuntime(third, thirdRuntime); err != nil {
		t.Fatal(err)
	}
	if err := BeginModuleInitialization(third); err != nil {
		t.Fatal(err)
	}
	if err := third.AddModule(root); err == nil {
		t.Fatal("超过 Module 深度上限未失败")
	}
}

func TestModuleDelegatesConfigAndSafeBoundary(t *testing.T) {
	owner := &Service{}
	runtime := &testRuntime{nodeID: "node-1", name: "Owner", state: StateInitializing}
	if err := BindRuntime(owner, runtime); err != nil {
		t.Fatal(err)
	}
	if err := BeginModuleInitialization(owner); err != nil {
		t.Fatal(err)
	}
	module := &testModule{}
	module.init = func() error { return module.SetDefaultAwaitTimeout(2 * time.Second) }
	if err := owner.AddModule(module); err != nil {
		t.Fatal(err)
	}
	if owner.defaultAwaitTimeout != 2*time.Second {
		t.Fatalf("Module.SetDefaultAwaitTimeout() result = %s", owner.defaultAwaitTimeout)
	}
	if err := module.RunSafe(func() { panic("job") }); err == nil {
		t.Fatal("Module.RunSafe() 未返回 panic 错误")
	}
	configured := struct{ Value int }{Value: 7}
	if err := module.ParseServiceConfig(&configured); err != nil || configured.Value != 7 {
		t.Fatalf("Module.ParseServiceConfig() = %+v, %v", configured, err)
	}
}
