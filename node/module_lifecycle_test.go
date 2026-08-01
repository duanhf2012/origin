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
		"start:root", "start:child", "stop:child", "stop:root",
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
	events *[]string
	root   service.IModule
}

func (owner *moduleOwnerService) OnInit() error {
	*owner.events = append(*owner.events, "init:service")
	return owner.AddModule(owner.root)
}

func (owner *moduleOwnerService) OnStart(context.Context) error {
	*owner.events = append(*owner.events, "start:service")
	return nil
}

func (owner *moduleOwnerService) OnStop(context.Context) error {
	*owner.events = append(*owner.events, "stop:service")
	return nil
}

func TestNodeRunsModuleLifecycleInConfirmedOrder(t *testing.T) {
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
		"start:root", "start:child", "start:service",
		"stop:service", "stop:child", "stop:root",
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
