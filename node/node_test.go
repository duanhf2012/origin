package node

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/duanhf2012/origin/v3/errs"
	originlog "github.com/duanhf2012/origin/v3/log"
	"github.com/duanhf2012/origin/v3/service"
)

// lifecycleService 用共享事件切片记录 Node 的严格调用顺序。
type lifecycleService struct {
	service.Service
	label      string
	events     *[]string
	initErr    error
	startErr   error
	stopErr    error
	panicPhase string
	onInit     func()
	onStart    func()
	onStop     func()
}

func (target *lifecycleService) OnInit() error {
	*target.events = append(*target.events, "init:"+target.label)
	if target.onInit != nil {
		// 可选探针只观察 Node 内部资源状态，不改变生产生命周期顺序。
		target.onInit()
	}
	if target.panicPhase == "init" {
		panic("init panic")
	}
	return target.initErr
}

func (target *lifecycleService) OnStart(context.Context) error {
	*target.events = append(*target.events, "start:"+target.label)
	if target.onStart != nil {
		// OnStart 探针用于验证启动回调之前的框架资源已经可用。
		target.onStart()
	}
	if target.panicPhase == "start" {
		panic("start panic")
	}
	return target.startErr
}

func (target *lifecycleService) OnStop(context.Context) error {
	*target.events = append(*target.events, "stop:"+target.label)
	if target.onStop != nil {
		// OnStop 探针用于验证业务清理期间 Node 资源尚未被提前回收。
		target.onStop()
	}
	if target.panicPhase == "stop" {
		panic("stop panic")
	}
	return target.stopErr
}

func TestNodeLifecycleOrder(t *testing.T) {
	events := make([]string, 0, 6)
	current := newTestNode(t,
		&lifecycleService{label: "a", events: &events},
		&lifecycleService{label: "b", events: &events},
	)
	if err := current.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := current.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	want := []string{
		"init:a", "init:b",
		"start:a", "start:b",
		"stop:b", "stop:a",
	}
	if !slices.Equal(events, want) {
		t.Fatalf("生命周期顺序 = %v, want %v", events, want)
	}
	if current.State() != StateStopped {
		t.Fatalf("State() = %v", current.State())
	}
}

func TestNodeTimerEngineLifecycleOrder(t *testing.T) {
	events := make([]string, 0, 3)
	target := &lifecycleService{label: "timer-probe", events: &events}
	current := newTestNode(t, target)

	// OnInit 发生在 Engine Start 之前，避免初始化失败后曾经启动后台 goroutine。
	target.onInit = func() {
		stats := current.timerEngine.Stats()
		if stats.Running || stats.Closed {
			t.Errorf("OnInit 中 TimerEngine 状态 = %+v，期望尚未启动", stats)
		}
	}
	// OnStart 和 OnStop 都需要时间基础设施继续运行，供后续 Await/Timer 清理复用。
	target.onStart = func() {
		stats := current.timerEngine.Stats()
		if !stats.Running || stats.Closed {
			t.Errorf("OnStart 中 TimerEngine 状态 = %+v，期望运行中", stats)
		}
	}
	target.onStop = func() {
		stats := current.timerEngine.Stats()
		if !stats.Running || stats.Closed {
			t.Errorf("OnStop 中 TimerEngine 状态 = %+v，期望仍运行", stats)
		}
	}

	if err := current.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := current.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	stats := current.timerEngine.Stats()
	if stats.Running || !stats.Closed {
		t.Fatalf("Node Stop 后 TimerEngine 状态 = %+v，期望已关闭", stats)
	}
}

func TestNodeSchedulerLifecycleOrder(t *testing.T) {
	events := make([]string, 0, 3)
	target := &lifecycleService{label: "scheduler-probe", events: &events}
	current := newTestNode(t, target)

	// OnStart 发生在 Scheduler 创建前，业务任务不能在服务尚未 Running 时提前进入。
	target.onStart = func() {
		err := target.DispatchAsync(func(context.Context) {})
		if !errors.Is(err, errs.ErrServiceNotReady) {
			t.Errorf("OnStart 中 DispatchAsync() error = %v", err)
		}
	}
	// Node 必须先停止并排空 Scheduler，再调用 OnStop。
	target.onStop = func() {
		err := target.DispatchAsync(func(context.Context) {})
		if !errors.Is(err, errs.ErrServiceStopping) {
			t.Errorf("OnStop 中 DispatchAsync() error = %v", err)
		}
		stats := target.ExecutionStats()
		if stats.Accepted != 0 || stats.Running != 0 || stats.Awaiting != 0 {
			t.Errorf("OnStop 中 Scheduler 未排空: %+v", stats)
		}
	}

	if err := current.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	taskDone := make(chan struct{})
	if err := target.DispatchAsync(func(context.Context) {
		close(taskDone)
	}); err != nil {
		t.Fatalf("Running DispatchAsync() error = %v", err)
	}
	select {
	case <-taskDone:
	case <-time.After(time.Second):
		t.Fatal("Scheduler 没有执行 Running 任务")
	}
	if err := current.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
}

func TestNodeSchedulerPrepareFailureDoesNotEnterServiceStart(t *testing.T) {
	events := make([]string, 0, 3)
	current := newTestNodeWithConfig(t, Config{
		ID: "game-1",
		Scheduler: service.SchedulerConfig{
			MaxTasks:            1,
			MaxAwaitTasks:       2,
			DefaultAwaitTimeout: time.Second,
		},
		Services: []string{"unused"},
	}, &lifecycleService{label: "a", events: &events})

	err := current.Start(context.Background())
	if !errs.IsCode(err, errs.CodeInvalidConfig) {
		t.Fatalf("Start() error = %v", err)
	}
	var located interface {
		LifecycleContext() (nodeID, serviceName, phase string)
	}
	if !errors.As(err, &located) {
		t.Fatalf("Scheduler 启动错误没有生命周期位置: %v", err)
	}
	_, _, phase := located.LifecycleContext()
	if phase != "scheduler_prepare" {
		t.Fatalf("Scheduler 错误 phase = %q", phase)
	}
	if rollbackErr := current.Rollback(context.Background()); rollbackErr != nil {
		t.Fatalf("Rollback() error = %v", rollbackErr)
	}
	if !slices.Equal(events, []string{"init:a"}) {
		t.Fatalf("Scheduler Prepare 失败不应进入 Start/Stop，事件 = %v", events)
	}
}

func TestNodeInitFailureDoesNotStartOrStop(t *testing.T) {
	events := make([]string, 0, 2)
	cause := errors.New("init failed")
	current := newTestNode(t,
		&lifecycleService{label: "a", events: &events},
		&lifecycleService{label: "b", events: &events, initErr: cause},
	)
	err := current.Start(context.Background())
	if !errors.Is(err, cause) {
		t.Fatalf("Start() error = %v", err)
	}
	if !slices.Equal(events, []string{"init:a", "init:b"}) {
		t.Fatalf("OnInit 失败后的事件 = %v", events)
	}
	if rollbackErr := current.Rollback(context.Background()); rollbackErr != nil {
		t.Fatalf("空 Rollback() error = %v", rollbackErr)
	}
	stats := current.timerEngine.Stats()
	if stats.Running || !stats.Closed {
		t.Fatalf("OnInit 失败回滚后的 TimerEngine 状态 = %+v，期望未启动即关闭", stats)
	}
}

func TestNodeStartFailureRollsBackEnteredServices(t *testing.T) {
	events := make([]string, 0, 6)
	cause := errors.New("start failed")
	current := newTestNode(t,
		&lifecycleService{label: "a", events: &events},
		&lifecycleService{label: "b", events: &events, startErr: cause},
	)
	err := current.Start(context.Background())
	if !errors.Is(err, cause) {
		t.Fatalf("Start() error = %v", err)
	}
	if rollbackErr := current.Rollback(context.Background()); rollbackErr != nil {
		t.Fatalf("Rollback() error = %v", rollbackErr)
	}
	want := []string{
		"init:a", "init:b",
		"start:a", "start:b",
		"stop:b", "stop:a",
	}
	if !slices.Equal(events, want) {
		t.Fatalf("回滚顺序 = %v, want %v", events, want)
	}
	if stats := current.timerEngine.Stats(); stats.Running || !stats.Closed {
		t.Fatalf("OnStart 失败回滚后的 TimerEngine 状态 = %+v，期望已关闭", stats)
	}
}

func TestNodeTimerEngineStartFailureIsLocatedAndRecoverable(t *testing.T) {
	events := make([]string, 0, 2)
	current := newTestNode(t, &lifecycleService{label: "a", events: &events})

	// 人工预启动内部 Engine，稳定制造 Node 正常路径中的重复 Start 错误。
	if err := current.timerEngine.Start(); err != nil {
		t.Fatalf("预启动 TimerEngine error = %v", err)
	}
	err := current.Start(context.Background())
	var located interface {
		LifecycleContext() (nodeID, serviceName, phase string)
	}
	if !errors.As(err, &located) {
		t.Fatalf("TimerEngine 启动错误没有生命周期位置: %v", err)
	}
	nodeID, serviceName, phase := located.LifecycleContext()
	if nodeID != "game-1" || serviceName != "" || phase != "timer_engine_start" {
		t.Fatalf("TimerEngine 错误位置 = %q/%q/%q", nodeID, serviceName, phase)
	}
	if !slices.Equal(events, []string{"init:a"}) {
		t.Fatalf("TimerEngine 启动失败后仍执行了 Service 启停: %v", events)
	}

	// 失败回滚仍需关闭已运行 Engine，且没有进入 OnStart 的 Service 不能收到 OnStop。
	if rollbackErr := current.Rollback(context.Background()); rollbackErr != nil {
		t.Fatalf("Rollback() error = %v", rollbackErr)
	}
	if stats := current.timerEngine.Stats(); stats.Running || !stats.Closed {
		t.Fatalf("回滚后 TimerEngine 状态 = %+v，期望已关闭", stats)
	}
}

func TestNodeConvertsPanicAndPreservesStack(t *testing.T) {
	events := make([]string, 0, 2)
	current := newTestNode(t,
		&lifecycleService{label: "panic", events: &events, panicPhase: "init"},
	)
	err := current.Start(context.Background())
	if !errs.IsCode(err, errs.CodeInternal) {
		t.Fatalf("panic 错误码 = %v, error = %v", errs.CodeOf(err), err)
	}
	var stack interface{ PanicStack() string }
	if !errors.As(err, &stack) || stack.PanicStack() == "" {
		t.Fatal("panic 错误没有保留堆栈")
	}
}

func TestNodeAggregatesStopErrors(t *testing.T) {
	events := make([]string, 0, 6)
	first := errors.New("first stop failed")
	second := errors.New("second stop failed")
	current := newTestNode(t,
		&lifecycleService{label: "a", events: &events, stopErr: first},
		&lifecycleService{label: "b", events: &events, stopErr: second},
	)
	if err := current.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	err := current.Stop(context.Background())
	if !errors.Is(err, first) || !errors.Is(err, second) {
		t.Fatalf("Stop() 未聚合两个错误: %v", err)
	}
	if !slices.Contains(events, "stop:a") || !slices.Contains(events, "stop:b") {
		t.Fatalf("停止错误跳过了后续 Service: %v", events)
	}
}

func TestNodeMetadataAndServiceRuntimeQueries(t *testing.T) {
	events := make([]string, 0, 6)
	first := &lifecycleService{label: "a", events: &events}
	second := &lifecycleService{label: "b", events: &events}
	current := newTestNodeWithConfig(t, Config{
		ID:       "private-1",
		Private:  true,
		Services: []string{"unused"},
	}, first, second)

	if current.ID() != "private-1" || !current.Private() {
		t.Fatalf("Node 元数据 = %q, private=%v", current.ID(), current.Private())
	}
	if current.Logger().Enabled(originlog.InfoLevel) {
		t.Fatal("测试 Nop Logger 不应启用")
	}
	found, ok := current.Service("a")
	if !ok || found != first {
		t.Fatalf("Service(a) = %v, %v", found, ok)
	}
	if _, ok := current.Service("missing"); ok {
		t.Fatal("不存在的 Service 被错误发现")
	}
	snapshot := current.Services()
	snapshot[0] = nil
	if found, _ := current.Service("a"); found == nil {
		t.Fatal("修改 Services 快照污染了 Node")
	}

	// 通过嵌入的 Service 查询可以覆盖 Node 私有 Runtime 适配层。
	if first.NodeID() != "private-1" || first.Name() != "a" {
		t.Fatalf("Service 运行身份 = %q/%q", first.NodeID(), first.Name())
	}
	if first.State() != service.StateCreated {
		t.Fatalf("启动前 Service State = %v", first.State())
	}
	if peer, ok := first.LookupService("b"); !ok || peer != second {
		t.Fatalf("LookupService(b) = %v, %v", peer, ok)
	}

	if err := current.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if first.State() != service.StateRunning {
		t.Fatalf("启动后 Service State = %v", first.State())
	}
	if err := current.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if err := current.Stop(context.Background()); err != nil {
		t.Fatalf("重复 Stop() error = %v", err)
	}
}

func TestNodeTimerSlotsAreBoundedAndIDsNeverRepeat(t *testing.T) {
	// 使用两个额度建立最小边界，验证额度只限制活跃数量而不复用已经发出的 ID。
	current := newTestNodeWithOptions(
		t,
		Options{
			MaxTimersPerNode: 2,
			TimerLocation:    time.UTC,
		},
		&lifecycleService{label: "a"},
	)
	first, ok := current.acquireTimerSlot()
	if !ok || first == service.InvalidTimerID {
		t.Fatal("第一次 Timer Slot 申请失败")
	}
	second, ok := current.acquireTimerSlot()
	if !ok || second == service.InvalidTimerID || second == first {
		t.Fatalf("第二次 TimerID = %d，与第一次 %d 冲突", second, first)
	}
	if _, ok := current.acquireTimerSlot(); ok {
		t.Fatal("超过 Node Timer 额度后仍然申请成功")
	}

	// 归还一个活跃额度后可以继续创建，但新 ID 必须保持单调且不复用。
	current.releaseTimerSlot()
	third, ok := current.acquireTimerSlot()
	if !ok || third == first || third == second {
		t.Fatalf("释放额度后的 TimerID = %d，历史值为 %d/%d", third, first, second)
	}
	current.releaseTimerSlot()
	current.releaseTimerSlot()
}

func TestNodeTimerSlotsConcurrentLimitAndUniqueIDs(t *testing.T) {
	const (
		timerLimit = 128
		attempts   = 1024
	)
	current := newTestNodeWithOptions(
		t,
		Options{
			MaxTimersPerNode: timerLimit,
			TimerLocation:    time.UTC,
		},
		&lifecycleService{label: "a"},
	)

	// 同时竞争远多于额度的申请，成功数量必须精确等于上限，且每个 ID 全局唯一。
	ids := make(chan service.TimerID, attempts)
	var waitGroup sync.WaitGroup
	waitGroup.Add(attempts)
	for index := 0; index < attempts; index++ {
		go func() {
			defer waitGroup.Done()
			if id, ok := current.acquireTimerSlot(); ok {
				ids <- id
			}
		}()
	}
	waitGroup.Wait()
	close(ids)

	unique := make(map[service.TimerID]struct{}, timerLimit)
	for id := range ids {
		if _, exists := unique[id]; exists {
			t.Fatalf("并发申请返回重复 TimerID %d", id)
		}
		unique[id] = struct{}{}
	}
	if len(unique) != timerLimit {
		t.Fatalf("并发申请成功数 = %d, want %d", len(unique), timerLimit)
	}

	// 每个成功申请只归还一次，最终活跃额度必须严格回到零。
	for range unique {
		current.releaseTimerSlot()
	}
	if active := current.timerResources.activeTimers.Load(); active != 0 {
		t.Fatalf("全部归还后 activeTimers = %d", active)
	}

	// 额外归还属于框架内部状态损坏，必须立即 panic，不能静默下溢。
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("重复归还 Timer Slot 未 panic")
			}
		}()
		current.releaseTimerSlot()
	}()
}

func TestTimerIDCannotControlAnotherServiceInSameNode(t *testing.T) {
	events := make([]string, 0, 8)
	first := &lifecycleService{label: "a", events: &events}
	second := &lifecycleService{label: "b", events: &events}
	current := newTestNode(t, first, second)
	if err := current.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	// TimerID 在 Node 内唯一，但控制入口仍必须校验所属 Service 的私有索引。另一个 Service
	// 即使拿到真实 ID，也不能暂停、恢复或取消该 Timer。
	id := first.AfterFunc(time.Hour, func(context.Context, service.TimerID) {})
	if id == service.InvalidTimerID {
		t.Fatal("第一个 Service 创建 Timer 失败")
	}
	if second.PauseTimer(id) || second.ResumeTimer(id) {
		t.Fatal("另一个 Service 控制了不属于自己的 TimerID")
	}
	foreignID := id
	if second.CancelTimer(&foreignID) {
		t.Fatal("另一个 Service 取消了不属于自己的 TimerID")
	}
	if foreignID != service.InvalidTimerID {
		t.Fatalf("跨 Service Cancel 后调用方 ID 未清零: %d", foreignID)
	}
	if !first.CancelTimer(&id) {
		t.Fatal("所属 Service 不能取消自己的 Timer")
	}
	if err := current.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
}

func TestNodeTimerOptionsRejectInvalidValues(t *testing.T) {
	serviceBinding := []ServiceBinding{{
		Name:     "a",
		Template: "lifecycleService",
		Service:  &lifecycleService{label: "a"},
	}}
	for _, test := range []struct {
		name    string
		options Options
	}{
		{name: "zero limit", options: Options{TimerLocation: time.UTC}},
		{name: "negative limit", options: Options{MaxTimersPerNode: -1, TimerLocation: time.UTC}},
		{name: "nil location", options: Options{MaxTimersPerNode: 1}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := New(
				Config{ID: "game-1", Services: []string{"a"}},
				serviceBinding,
				originlog.NewNop(),
				test.options,
			); err == nil {
				t.Fatal("无效 Node Timer Options 未返回错误")
			}
		})
	}
}

func TestNodeCancellationAndInvalidCalls(t *testing.T) {
	events := make([]string, 0, 2)
	current := newTestNode(t, &lifecycleService{label: "a", events: &events})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := current.Start(ctx)
	if !errs.IsCode(err, errs.CodeCanceled) {
		t.Fatalf("取消 Start() error = %v", err)
	}
	if rollbackErr := current.Rollback(context.Background()); rollbackErr != nil {
		t.Fatalf("Rollback() error = %v", rollbackErr)
	}

	created := newTestNode(t, &lifecycleService{
		label:  "created",
		events: &events,
	})
	if err := created.Stop(context.Background()); err == nil {
		t.Fatal("Created Node Stop() 未返回错误")
	}
	if err := created.Start(nil); err == nil {
		t.Fatal("nil Context Start() 未返回错误")
	}
}

func TestNodeNewRejectsInvalidBindings(t *testing.T) {
	target := &lifecycleService{label: "a", events: &[]string{}}
	tests := []struct {
		name     string
		config   Config
		bindings []ServiceBinding
	}{
		{name: "empty node id", config: Config{}, bindings: []ServiceBinding{{
			Name: "a", Template: "service", Service: target,
		}}},
		{name: "empty bindings", config: Config{ID: "game-1"}},
		{name: "empty binding name", config: Config{ID: "game-1"}, bindings: []ServiceBinding{{
			Template: "service", Service: target,
		}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := New(
				test.config,
				test.bindings,
				originlog.NewNop(),
				Options{
					MaxTimersPerNode: 3_000_000,
					TimerLocation:    time.Local,
				},
			); err == nil {
				t.Fatal("New() 未返回错误")
			}
		})
	}
}

func TestLifecycleErrorExposesLocation(t *testing.T) {
	events := make([]string, 0, 2)
	cause := errors.New("start failed")
	current := newTestNode(t, &lifecycleService{
		label:    "a",
		events:   &events,
		startErr: cause,
	})
	err := current.Start(context.Background())
	var located interface {
		LifecycleContext() (nodeID, serviceName, phase string)
	}
	if !errors.As(err, &located) {
		t.Fatalf("错误没有生命周期位置: %v", err)
	}
	nodeID, serviceName, phase := located.LifecycleContext()
	if nodeID != "game-1" || serviceName != "a" || phase != "on_start" {
		t.Fatalf("生命周期位置 = %q/%q/%q", nodeID, serviceName, phase)
	}
	if err.Error() == "" {
		t.Fatal("生命周期错误文本为空")
	}
}

func newTestNode(t *testing.T, services ...*lifecycleService) *Node {
	t.Helper()
	return newTestNodeWithConfig(t, Config{
		ID:       "game-1",
		Services: []string{"unused"},
	}, services...)
}

func newTestNodeWithConfig(
	t *testing.T,
	config Config,
	services ...*lifecycleService,
) *Node {
	return newTestNodeWithConfigAndOptions(
		t,
		config,
		Options{
			MaxTimersPerNode: 3_000_000,
			TimerLocation:    time.Local,
		},
		services...,
	)
}

func newTestNodeWithOptions(
	t *testing.T,
	options Options,
	services ...*lifecycleService,
) *Node {
	return newTestNodeWithConfigAndOptions(
		t,
		Config{
			ID:       "game-1",
			Services: []string{"unused"},
		},
		options,
		services...,
	)
}

func newTestNodeWithConfigAndOptions(
	t *testing.T,
	config Config,
	options Options,
	services ...*lifecycleService,
) *Node {
	t.Helper()
	bindings := make([]ServiceBinding, len(services))
	for index, target := range services {
		name := target.label
		bindings[index] = ServiceBinding{
			Name:     name,
			Template: "lifecycleService",
			Service:  target,
		}
	}
	current, err := New(
		config,
		bindings,
		originlog.NewNop(),
		options,
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() {
		// 测试若在断言前提前失败，仍回收尚未关闭的 Engine，避免 goroutine 或 Timer 泄漏污染后续用例。
		if stats := current.timerEngine.Stats(); !stats.Closed {
			if rollbackErr := current.Rollback(context.Background()); rollbackErr != nil {
				t.Errorf("测试清理 Rollback() error = %v", rollbackErr)
			}
		}
	})
	return current
}
