package service

import (
	"context"
	"testing"
)

type testModule struct {
	Module
	init  func() error
	start func(context.Context) error
	stop  func(context.Context) error
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
