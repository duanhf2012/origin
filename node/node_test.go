package node

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	publicdiscovery "github.com/duanhf2012/origin/v3/discovery"
	"github.com/duanhf2012/origin/v3/errs"
	internaldiscovery "github.com/duanhf2012/origin/v3/internal/discovery"
	originlog "github.com/duanhf2012/origin/v3/log"
	"github.com/duanhf2012/origin/v3/rpc"
	"github.com/duanhf2012/origin/v3/service"
)

// TestNewSessionIDReturnsNonZeroUint64 验证 Node 会话使用紧凑非零随机标识。
func TestNewSessionIDReturnsNonZeroUint64(t *testing.T) {
	for index := 0; index < 256; index++ {
		sessionID, err := newSessionID()
		if err != nil {
			t.Fatalf("newSessionID() error = %v", err)
		}
		var typed uint64 = sessionID
		if typed == 0 {
			t.Fatal("newSessionID() 返回零值")
		}
	}
}

// nodeDiscoveryListener 记录公开发现回调，并验证回调 Context 可以执行协作式 Await。
type nodeDiscoveryListener struct {
	owner  *lifecycleService
	events chan string
}

func (listener *nodeDiscoveryListener) OnDiscovered(
	ctx context.Context,
	event publicdiscovery.Event,
) {
	if err := listener.owner.Await(
		ctx,
		func(context.Context) error { return nil },
	); err != nil {
		listener.events <- "await-error"
		return
	}
	listener.events <- "discovered:" + event.Services[0].ServiceName
}

func (listener *nodeDiscoveryListener) OnStateChanged(
	_ context.Context,
	event publicdiscovery.Event,
) {
	listener.events <- "state:" + event.Services[0].ServiceName
}

func (listener *nodeDiscoveryListener) OnLost(
	_ context.Context,
	event publicdiscovery.Event,
) {
	listener.events <- "lost:" + event.Services[0].ServiceName
}

// lifecycleService 用共享事件切片记录 Node 的严格调用顺序。
type lifecycleService struct {
	service.Service
	label          string
	events         *[]string
	initErr        error
	startErr       error
	stopErr        error
	panicPhase     string
	onInit         func()
	onStart        func()
	onStartContext func(context.Context) error
	onStop         func()
	onStopContext  func(context.Context) error
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

func (target *lifecycleService) OnStart(ctx context.Context) error {
	*target.events = append(*target.events, "start:"+target.label)
	if target.onStart != nil {
		// OnStart 探针用于验证启动回调之前的框架资源已经可用。
		target.onStart()
	}
	if target.onStartContext != nil {
		if err := target.onStartContext(ctx); err != nil {
			return err
		}
	}
	if target.panicPhase == "start" {
		panic("start panic")
	}
	return target.startErr
}

// TestNodePublishesOnlyAfterAllOnStart 验证统一就绪屏障、生命周期 Await 和 Stop 撤销发布。
func TestNodePublishesOnlyAfterAllOnStart(t *testing.T) {
	source := internaldiscovery.NewSource()
	var snapshotMu sync.Mutex
	var latest internaldiscovery.RawSnapshot
	subscription, err := source.Subscribe(func(snapshot internaldiscovery.RawSnapshot) error {
		snapshotMu.Lock()
		latest = snapshot
		snapshotMu.Unlock()
		return nil
	})
	if err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}
	defer subscription.Close()

	events := make([]string, 0, 4)
	first := &lifecycleService{label: "a", events: &events}
	second := &lifecycleService{label: "b", events: &events}
	assertUnpublished := func() {
		snapshotMu.Lock()
		defer snapshotMu.Unlock()
		if len(latest.Nodes) != 0 {
			t.Errorf("全部 OnStart 完成前 Node 已发布: %+v", latest)
		}
	}
	first.onStart = assertUnpublished
	second.onStart = assertUnpublished
	first.onStartContext = func(ctx context.Context) error {
		// OnStart 复用正常 Await 外观，但等待函数在原生命周期调用链中顺序完成。
		called := false
		err := first.Await(ctx, func(context.Context) error {
			called = true
			return nil
		})
		if err == nil && !called {
			t.Error("OnStart Await 没有执行等待函数")
		}
		return err
	}

	current := newTestNodeWithConfigAndOptions(
		t,
		Config{
			ID:       "game-1",
			Labels:   map[string]string{"region": "cn-east"},
			Services: []string{"unused"},
		},
		Options{
			MaxTimersPerNode: 3_000_000,
			TimerLocation:    time.Local,
			DiscoverySource:  source,
		},
		first,
		second,
	)
	if err := current.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	snapshotMu.Lock()
	if len(latest.Nodes) != 1 || len(latest.Nodes[0].Services) != 2 ||
		latest.Nodes[0].Labels["region"] != "cn-east" {
		t.Fatalf("Ready 后完整发布错误: %+v", latest)
	}
	snapshotMu.Unlock()

	if err := current.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	snapshotMu.Lock()
	defer snapshotMu.Unlock()
	if len(latest.Nodes) != 0 {
		t.Fatalf("Stop 后没有撤销发布: %+v", latest)
	}
}

// TestNodeDiscoveryQueryWaitAndListener 验证远端快照通过 Service 外观查询和 FIFO 监听交付。
func TestNodeDiscoveryQueryWaitAndListener(t *testing.T) {
	source := internaldiscovery.NewSource()
	events := make([]string, 0, 2)
	target := &lifecycleService{label: "GatewayService", events: &events}
	current := newTestNodeWithConfigAndOptions(
		t,
		Config{
			ID:       "gateway-1",
			Services: []string{"unused"},
		},
		Options{
			MaxTimersPerNode: 3_000_000,
			TimerLocation:    time.Local,
			DiscoverySource:  source,
		},
		target,
	)
	if err := current.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	listener := &nodeDiscoveryListener{
		owner:  target,
		events: make(chan string, 8),
	}
	id, err := target.AddDiscoveryListener(listener)
	if err != nil {
		t.Fatalf("AddDiscoveryListener() error = %v", err)
	}
	remote := internaldiscovery.RawNode{
		NodeID:    "game-1",
		SessionID: 1,
		Labels:    map[string]string{"region": "cn-east"},
		Transport: internaldiscovery.TransportNone,
		Services: []internaldiscovery.RawService{{
			ServiceName: "PlayerService",
			State:       internaldiscovery.ServiceStateRunning,
		}},
	}
	if err := source.Publish(remote); err != nil {
		t.Fatalf("Publish(remote) error = %v", err)
	}
	if got := receiveNode(t, listener.events); got != "discovered:PlayerService" {
		t.Fatalf("发现事件 = %q", got)
	}

	instance, exists := target.FindDiscoveredService("game-1", "PlayerService")
	if !exists || instance.State != publicdiscovery.StateRunning ||
		instance.Labels["region"] != "cn-east" {
		t.Fatalf("FindDiscoveredService() = (%+v, %v)", instance, exists)
	}
	list := target.ListDiscoveredServices("PlayerService")
	if len(list) != 1 || list[0].SessionID != 1 {
		t.Fatalf("ListDiscoveredServices() = %+v", list)
	}
	// 修改业务副本的 Labels 不得污染 Node 内部不可变快照。
	instance.Labels["region"] = "modified"
	again, _ := target.FindDiscoveredService("game-1", "PlayerService")
	if again.Labels["region"] != "cn-east" {
		t.Fatalf("业务修改污染内部 Labels: %v", again.Labels)
	}

	remote.Services[0].State = internaldiscovery.ServiceStateRetired
	if err := source.Publish(remote); err != nil {
		t.Fatalf("Publish(retired) error = %v", err)
	}
	if got := receiveNode(t, listener.events); got != "state:PlayerService" {
		t.Fatalf("状态事件 = %q", got)
	}
	if !source.Withdraw(remote.NodeID, remote.SessionID) {
		t.Fatal("Withdraw(remote) = false")
	}
	if got := receiveNode(t, listener.events); got != "lost:PlayerService" {
		t.Fatalf("失去发现事件 = %q", got)
	}
	if !target.RemoveDiscoveryListener(&id) || id != 0 {
		t.Fatalf("RemoveDiscoveryListener() 未清零 ID: %d", id)
	}

	if err := current.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
}

// TestNodeWithoutSourceClosesDiscoveryRuntime 验证独立构造的 Node 停止后也会关闭发现外观。
func TestNodeWithoutSourceClosesDiscoveryRuntime(t *testing.T) {
	events := make([]string, 0, 2)
	target := &lifecycleService{label: "GatewayService", events: &events}
	current := newTestNode(t, target)
	if err := current.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	remote := internaldiscovery.RawSnapshot{Nodes: []internaldiscovery.RawNode{{
		NodeID:    "game-2",
		SessionID: 2,
		Transport: internaldiscovery.TransportNone,
		Services: []internaldiscovery.RawService{{
			ServiceName: "PlayerService",
			State:       internaldiscovery.ServiceStateRunning,
		}},
	}}}
	if err := current.discovery.apply(remote); err != nil {
		t.Fatalf("apply() error = %v", err)
	}
	if _, exists := target.FindDiscoveredService("game-2", "PlayerService"); !exists {
		t.Fatal("Stop 前没有读到测试发现记录")
	}

	if err := current.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if _, exists := target.FindDiscoveredService("game-2", "PlayerService"); exists {
		t.Fatal("Stop 后发现外观仍返回旧记录")
	}
	if err := current.discovery.apply(remote); !errs.IsCode(
		err,
		errs.CodeServiceStopped,
	) {
		t.Fatalf("Stop 后 apply() error = %v", err)
	}
}

// TestBuildDiscoveryActionsBatchesNodeSessionReplacement 验证同一 Node 的全部旧 Service 先按
// 一个 Lost 事件交付，再以一个 Discovered 事件发布新会话。
func TestBuildDiscoveryActionsBatchesNodeSessionReplacement(t *testing.T) {
	oldPlayer := &internaldiscovery.Instance{
		NodeID: "game-1", SessionID: 10, ServiceName: "PlayerService",
		State: internaldiscovery.ServiceStateRunning,
	}
	oldChat := &internaldiscovery.Instance{
		NodeID: "game-1", SessionID: 10, ServiceName: "ChatService",
		State: internaldiscovery.ServiceStateRetired,
	}
	newPlayer := &internaldiscovery.Instance{
		NodeID: "game-1", SessionID: 11, ServiceName: "PlayerService",
		State: internaldiscovery.ServiceStateRunning,
	}
	newChat := &internaldiscovery.Instance{
		NodeID: "game-1", SessionID: 11, ServiceName: "ChatService",
		State: internaldiscovery.ServiceStateRunning,
	}
	delivered := map[internaldiscovery.InstanceKey]*internaldiscovery.Instance{
		{NodeID: "game-1", ServiceName: "PlayerService"}: oldPlayer,
		{NodeID: "game-1", ServiceName: "ChatService"}:   oldChat,
	}
	current := map[internaldiscovery.InstanceKey]*internaldiscovery.Instance{
		{NodeID: "game-1", ServiceName: "PlayerService"}: newPlayer,
		{NodeID: "game-1", ServiceName: "ChatService"}:   newChat,
	}

	actions := buildDiscoveryActions(delivered, current)
	if len(actions) != 2 ||
		actions[0].kind != internaldiscovery.ChangeLost ||
		actions[1].kind != internaldiscovery.ChangeDiscovered {
		t.Fatalf("Session 替换 actions = %+v", actions)
	}
	for index, action := range actions {
		if action.event.NodeID != "game-1" ||
			len(action.event.Services) != 2 ||
			action.event.Services[0].ServiceName != "ChatService" ||
			action.event.Services[1].ServiceName != "PlayerService" {
			t.Fatalf("actions[%d] 没有按 Node 批量稳定排序: %+v", index, action)
		}
	}
}

func (target *lifecycleService) OnStop(ctx context.Context) error {
	*target.events = append(*target.events, "stop:"+target.label)
	if target.onStop != nil {
		// OnStop 探针用于验证业务清理期间 Node 资源尚未被提前回收。
		target.onStop()
	}
	if target.panicPhase == "stop" {
		panic("stop panic")
	}
	if target.onStopContext != nil {
		if err := target.onStopContext(ctx); err != nil {
			return err
		}
	}
	return target.stopErr
}

func TestNodeOnStopRunsAfterDrainAndCanAwait(t *testing.T) {
	events := make([]string, 0, 6)
	taskStarted := make(chan struct{})
	releaseTask := make(chan struct{})
	finalizerAwaited := make(chan struct{})
	target := &lifecycleService{label: "a", events: &events}
	target.onStopContext = func(ctx context.Context) error {
		return target.Await(ctx, func(waitCtx context.Context) error {
			if waitCtx == nil {
				t.Error("OnStop Await 收到 nil Context")
			}
			close(finalizerAwaited)
			return nil
		})
	}
	current := newTestNode(t, target)
	if err := current.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := target.DispatchAsync(func(context.Context) {
		close(taskStarted)
		<-releaseTask
		events = append(events, "task:done")
	}); err != nil {
		t.Fatalf("DispatchAsync() error = %v", err)
	}
	<-taskStarted

	stopDone := make(chan error, 1)
	go func() {
		stopDone <- current.Stop(context.Background())
	}()
	// Draining 已经关闭普通根任务准入，但不能越过尚未完成的已接受任务执行 OnStop。
	deadline := time.Now().Add(time.Second)
	for target.State() != service.StateStopping {
		if time.Now().After(deadline) {
			t.Fatal("Service 未进入 Stopping")
		}
		time.Sleep(time.Millisecond)
	}
	if err := target.DispatchAsync(func(context.Context) {}); !errors.Is(
		err,
		errs.ErrServiceStopping,
	) {
		t.Fatalf("Draining DispatchAsync() error = %v", err)
	}
	select {
	case <-finalizerAwaited:
		t.Fatal("已接受任务返回前提前执行 OnStop")
	default:
	}
	close(releaseTask)
	if err := receiveNode(t, stopDone); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	select {
	case <-finalizerAwaited:
	default:
		t.Fatal("OnStop Await 没有执行")
	}
	if got := events[len(events)-2:]; !slices.Equal(
		got,
		[]string{"task:done", "stop:a"},
	) {
		t.Fatalf("排空与 OnStop 顺序 = %v", events)
	}
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
		{name: "empty label key", config: Config{
			ID: "game-1", Labels: map[string]string{"": "cn-east"},
		}, bindings: []ServiceBinding{{
			Name: "a", Template: "service", Service: target,
		}}},
		{name: "empty label value", config: Config{
			ID: "game-1", Labels: map[string]string{"region": ""},
		}, bindings: []ServiceBinding{{
			Name: "a", Template: "service", Service: target,
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

func TestTransportRecoveryWithdrawsAndRepublishesDiscovery(t *testing.T) {
	t.Parallel()

	source := internaldiscovery.NewSource()
	events := make([]string, 0, 3)
	current := newTestNodeWithConfigAndOptions(
		t,
		Config{
			ID:       "game-1",
			Services: []string{"unused"},
		},
		Options{
			MaxTimersPerNode: 3_000_000,
			TimerLocation:    time.Local,
			DiscoverySource:  source,
		},
		&lifecycleService{label: "service-a", events: &events},
	)
	if err := current.Start(context.Background()); err != nil {
		t.Fatalf("Node.Start() error = %v", err)
	}

	first := errors.New("transport interruption")
	current.handleTransportEvent(rpc.TransportEvent{
		Kind:                rpc.TransportKindTCP,
		State:               rpc.TransportStateRecovering,
		ConsecutiveFailures: 1,
		ErrorCode:           errs.CodeTransportUnavailable,
		Cause:               first,
	})

	var records int
	subscription, err := source.Subscribe(func(snapshot internaldiscovery.RawSnapshot) error {
		records = len(snapshot.Nodes)
		return nil
	})
	if err != nil {
		t.Fatalf("Source.Subscribe() error = %v", err)
	}
	subscription.Close()
	if records != 0 {
		t.Fatalf("Transport 恢复期间发现记录数 = %d", records)
	}
	status := current.TransportStatus()
	if status.State != TransportRecovering ||
		status.ErrorCode != errs.CodeTransportUnavailable {
		t.Fatalf("TransportStatus() = %+v", status)
	}
	health := current.HealthStatus()
	if !health.Liveness || health.Readiness || !health.Degraded {
		t.Fatalf("HealthStatus() = %+v", health)
	}

	current.handleTransportEvent(rpc.TransportEvent{
		Kind:       rpc.TransportKindTCP,
		State:      rpc.TransportStateReady,
		Reconnects: 1,
	})
	subscription, err = source.Subscribe(func(snapshot internaldiscovery.RawSnapshot) error {
		records = len(snapshot.Nodes)
		return nil
	})
	if err != nil {
		t.Fatalf("Source.Subscribe() after recovery error = %v", err)
	}
	subscription.Close()
	if records != 1 {
		t.Fatalf("Transport 恢复后发现记录数 = %d", records)
	}
}

func TestHealthStatusTracksPartialAndCompleteServiceFailure(t *testing.T) {
	events := make([]string, 0, 8)
	current := newTestNode(t,
		&lifecycleService{label: "a", events: &events},
		&lifecycleService{label: "b", events: &events},
	)
	if err := current.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if health := current.HealthStatus(); !health.Liveness ||
		!health.Readiness ||
		health.Degraded ||
		health.ErrorCode != errs.CodeOK {
		t.Fatalf("Ready HealthStatus() = %+v", health)
	}

	first := errors.New("first scheduler failure")
	current.recordServiceFailure(current.byName["a"], first)
	partial := current.HealthStatus()
	if !partial.Liveness || !partial.Readiness || !partial.Degraded ||
		partial.ErrorCode != errs.CodeServiceFailed {
		t.Fatalf("partial failure HealthStatus() = %+v", partial)
	}
	status, exists := current.ServiceStatus("a")
	if !exists || status.State != service.StateFailed ||
		!errors.Is(status.Failure, first) {
		t.Fatalf("ServiceStatus(a) = %+v, %v", status, exists)
	}

	current.recordServiceFailure(
		current.byName["b"],
		errors.New("second scheduler failure"),
	)
	allFailed := current.HealthStatus()
	if !allFailed.Liveness || allFailed.Readiness || !allFailed.Degraded ||
		allFailed.ErrorCode != errs.CodeServiceFailed {
		t.Fatalf("all failed HealthStatus() = %+v", allFailed)
	}
	if allocations := testing.AllocsPerRun(1000, func() {
		_ = current.HealthStatus()
		_ = current.TransportStatus()
		_, _ = current.ServiceStatus("a")
	}); allocations != 0 {
		t.Fatalf("状态查询分配 = %f", allocations)
	}

	if err := current.Stop(context.Background()); !errors.Is(
		err,
		errs.ErrServiceFailed,
	) {
		t.Fatalf("Failed Node Stop() error = %v", err)
	}
	if current.State() != StateFailed {
		t.Fatalf("Failed Node final State = %v", current.State())
	}
}

func newTestNode(t *testing.T, services ...*lifecycleService) *Node {
	t.Helper()
	return newTestNodeWithConfig(t, Config{
		ID:       "game-1",
		Services: []string{"unused"},
	}, services...)
}

// receiveNode 使用统一上限等待异步 Node 回调，超时立即给出明确测试位置。
func receiveNode[T any](t *testing.T, input <-chan T) T {
	t.Helper()
	select {
	case value := <-input:
		return value
	case <-time.After(time.Second):
		t.Fatal("等待 Node 异步结果超时")
		var zero T
		return zero
	}
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
