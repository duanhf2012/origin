package node

import (
	"context"
	"errors"
	"slices"
	"testing"

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
}

func (target *lifecycleService) OnInit() error {
	*target.events = append(*target.events, "init:"+target.label)
	if target.panicPhase == "init" {
		panic("init panic")
	}
	return target.initErr
}

func (target *lifecycleService) OnStart(context.Context) error {
	*target.events = append(*target.events, "start:"+target.label)
	if target.panicPhase == "start" {
		panic("start panic")
	}
	return target.startErr
}

func (target *lifecycleService) OnStop(context.Context) error {
	*target.events = append(*target.events, "stop:"+target.label)
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
			if _, err := New(test.config, test.bindings, originlog.NewNop()); err == nil {
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
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return current
}
