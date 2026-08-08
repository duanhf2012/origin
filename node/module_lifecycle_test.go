package node

import (
	"context"
	"errors"
	"testing"
	"time"

	originlog "github.com/duanhf2012/origin/v3/log"
	"github.com/duanhf2012/origin/v3/service"
)

type lifecycleModule struct {
	service.Module
	label       string
	events      *[]string
	child       service.IModule
	panicOnStop bool
	failOnStart bool
}

func (module *lifecycleModule) OnInit() error {
	*module.events = append(*module.events, "init:"+module.label)
	if module.child != nil {
		return module.AddModule(module.child)
	}
	return nil
}

func (module *lifecycleModule) OnStart(context.Context) error {
	*module.events = append(*module.events, "start:"+module.label)
	if module.failOnStart {
		return errors.New("module start")
	}
	return nil
}

func TestNodeRollsBackEnteredModulesWhenModuleStartFails(t *testing.T) {
	// Service 先成功进入 OnStart，随后 child Module 启动失败；回滚必须严格反转已经进入的
	// 生命周期顺序，不能遗漏失败 Module 自身，也不能在 Module 之前关闭 Service。
	var events []string
	child := &lifecycleModule{label: "child", events: &events, failOnStart: true}
	root := &lifecycleModule{label: "root", events: &events, child: child}
	owner := &moduleOwnerService{events: &events, root: root}
	current, err := New(
		Config{ID: "module-failure", Services: []string{"Owner"}},
		[]ServiceBinding{{Name: "Owner", Template: "Owner", Service: owner}},
		originlog.NewNop(),
		Options{MaxTimersPerNode: 64, TimerLocation: time.UTC},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := current.Start(t.Context()); err == nil {
		t.Fatal("Module.OnStart 错误未使 Node.Start 失败")
	}
	if err := current.Rollback(t.Context()); err != nil {
		t.Fatalf("Rollback() error = %v", err)
	}
	want := []string{
		"init:service", "init:root", "init:child",
		"start:service", "start:root", "start:child",
		"stop:child", "stop:root", "stop:service",
	}
	if len(events) != len(want) {
		t.Fatalf("events = %v", events)
	}
	for index := range want {
		if events[index] != want[index] {
			t.Fatalf("events[%d] = %q, want %q; all=%v", index, events[index], want[index], events)
		}
	}
}

func (module *lifecycleModule) OnStop(context.Context) error {
	*module.events = append(*module.events, "stop:"+module.label)
	if module.panicOnStop {
		panic("module stop")
	}
	return nil
}

type moduleOwnerService struct {
	service.Service
	events   *[]string
	root     service.IModule
	startErr error
}

func (owner *moduleOwnerService) OnInit() error {
	*owner.events = append(*owner.events, "init:service")
	return owner.AddModule(owner.root)
}

func (owner *moduleOwnerService) OnStart(context.Context) error {
	*owner.events = append(*owner.events, "start:service")
	return owner.startErr
}

func (owner *moduleOwnerService) OnStop(context.Context) error {
	*owner.events = append(*owner.events, "stop:service")
	return nil
}

func TestNodeRunsModuleLifecycleInConfirmedOrder(t *testing.T) {
	// Service 是 Module 的生命周期父级：启动从 Service 向 Module 树展开，停止则严格反向。
	// child 的停止 panic 还用于证明错误不会跳过 root 和最后的 Service.OnStop。
	var events []string
	child := &lifecycleModule{label: "child", events: &events, panicOnStop: true}
	root := &lifecycleModule{label: "root", events: &events, child: child}
	owner := &moduleOwnerService{events: &events, root: root}
	current, err := New(
		Config{ID: "module-node", Services: []string{"Owner"}},
		[]ServiceBinding{{Name: "Owner", Template: "Owner", Service: owner}},
		originlog.NewNop(),
		Options{MaxTimersPerNode: 64, TimerLocation: time.UTC},
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() {
		if !current.timerEngine.Stats().Closed {
			_ = current.Rollback(context.Background())
		}
	})
	if err := current.Start(t.Context()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := current.Stop(t.Context()); err == nil {
		t.Fatal("Module.OnStop panic 应形成聚合错误")
	}
	want := []string{
		"init:service", "init:root", "init:child",
		"start:service", "start:root", "start:child",
		"stop:child", "stop:root", "stop:service",
	}
	if len(events) != len(want) {
		t.Fatalf("events = %v", events)
	}
	for index := range want {
		if events[index] != want[index] {
			t.Fatalf("events[%d] = %q, want %q; all=%v", index, events[index], want[index], events)
		}
	}
}

func TestNodeDoesNotStartModulesWhenServiceStartFails(t *testing.T) {
	// Service.OnStart 失败时 Module 尚未进入启动阶段，因此回滚只能调用 Service.OnStop；若
	// 出现任何 Module start/stop 事件，就表示父级启动屏障被破坏。
	var events []string
	child := &lifecycleModule{label: "child", events: &events}
	root := &lifecycleModule{label: "root", events: &events, child: child}
	owner := &moduleOwnerService{
		events:   &events,
		root:     root,
		startErr: errors.New("service start"),
	}
	current, err := New(
		Config{ID: "service-start-failure", Services: []string{"Owner"}},
		[]ServiceBinding{{Name: "Owner", Template: "Owner", Service: owner}},
		originlog.NewNop(),
		Options{MaxTimersPerNode: 64, TimerLocation: time.UTC},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := current.Start(t.Context()); err == nil {
		t.Fatal("Service.OnStart 错误未使 Node.Start 失败")
	}
	if err := current.Rollback(t.Context()); err != nil {
		t.Fatalf("Rollback() error = %v", err)
	}

	want := []string{
		"init:service", "init:root", "init:child",
		"start:service", "stop:service",
	}
	if len(events) != len(want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
	for index := range want {
		if events[index] != want[index] {
			t.Fatalf("events[%d] = %q, want %q; all=%v", index, events[index], want[index], events)
		}
	}
}
