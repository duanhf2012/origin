package service

import (
	"testing"

	originlog "github.com/duanhf2012/origin/v3/log"
)

// testService 使用真实嵌入方式验证业务类型获得默认生命周期和查询方法。
type testService struct {
	Service
}

// testRuntime 是单元测试可完全控制的只读运行环境。
type testRuntime struct {
	nodeID string
	name   string
	state  State
	peer   IService
}

func (runtime *testRuntime) NodeID() string           { return runtime.nodeID }
func (runtime *testRuntime) ServiceName() string      { return runtime.name }
func (runtime *testRuntime) State() State             { return runtime.state }
func (runtime *testRuntime) Logger() originlog.Logger { return originlog.NewNop() }
func (runtime *testRuntime) LookupService(string) (IService, bool) {
	return runtime.peer, runtime.peer != nil
}

func TestBindRuntimeAndQueries(t *testing.T) {
	target := &testService{}
	runtime := &testRuntime{
		nodeID: "game-1",
		name:   "PlayerService",
		state:  StateRunning,
		peer:   target,
	}

	// 首次绑定应建立所有只读运行身份。
	if err := BindRuntime(target, runtime); err != nil {
		t.Fatalf("BindRuntime() error = %v", err)
	}
	if target.NodeID() != "game-1" || target.Name() != "PlayerService" {
		t.Fatalf("运行身份 = %q/%q", target.NodeID(), target.Name())
	}
	if target.State() != StateRunning {
		t.Fatalf("State() = %v", target.State())
	}
	if peer, ok := target.LookupService("PlayerService"); !ok || peer != target {
		t.Fatalf("LookupService() = %v, %v", peer, ok)
	}

	// 同一个实例不能再次归属其他 Node。
	if err := BindRuntime(target, runtime); err == nil {
		t.Fatal("重复 BindRuntime() 未返回错误")
	}
}

func TestBindRuntimeRejectsTypedNil(t *testing.T) {
	var target *testService
	if err := BindRuntime(target, &testRuntime{}); err == nil {
		t.Fatal("类型化 nil Service 未返回错误")
	}
}

func TestUnboundServiceUsesSafeDefaults(t *testing.T) {
	target := &testService{}
	if target.Name() != "" || target.NodeID() != "" {
		t.Fatalf("未绑定身份不为空: %q/%q", target.Name(), target.NodeID())
	}
	if target.State() != StateCreated {
		t.Fatalf("未绑定 State() = %v", target.State())
	}
	if target.Logger().Enabled(originlog.InfoLevel) {
		t.Fatal("未绑定 Logger 不应启用输出")
	}
}
