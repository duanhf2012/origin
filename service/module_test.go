package service

import (
	"context"
	"strings"
	"testing"
	"time"

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
	if err := owner.AddModule(module); err != nil {
		t.Fatal(err)
	}
	if err := module.RunSafe(func() { panic("job") }); err == nil {
		t.Fatal("Module.RunSafe() 未返回 panic 错误")
	}
	configured := struct{ Value int }{Value: 7}
	if err := module.ParseServiceConfig(&configured); err != nil || configured.Value != 7 {
		t.Fatalf("Module.ParseServiceConfig() = %+v, %v", configured, err)
	}
}
