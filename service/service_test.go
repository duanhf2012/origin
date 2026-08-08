package service

import (
	"sync/atomic"
	"testing"
	"time"

	originlog "github.com/duanhf2012/origin/v3/log"
)

// testService 使用真实嵌入方式验证业务类型获得默认生命周期和查询方法。
type testService struct {
	Service
}

// testRuntime 是单元测试可完全控制的只读运行环境。
type testRuntime struct {
	nodeID   string
	name     string
	state    State
	peer     IService
	limit    int
	active   atomic.Int64
	nextID   atomic.Uint64
	location *time.Location
	now      time.Time
	logger   originlog.Logger
}

func (runtime *testRuntime) ID() string               { return runtime.nodeID }
func (runtime *testRuntime) NodeID() string           { return runtime.nodeID }
func (runtime *testRuntime) ServiceName() string      { return runtime.name }
func (runtime *testRuntime) State() State             { return runtime.state }
func (runtime *testRuntime) Logger() originlog.Logger { return runtime.logger }
func (runtime *testRuntime) Now() time.Time           { return runtime.now }
func (runtime *testRuntime) SetTime(value time.Time) error {
	runtime.now = value
	return nil
}
func (runtime *testRuntime) AddTime(delta time.Duration) error {
	runtime.now = runtime.now.Add(delta)
	return nil
}
func (runtime *testRuntime) LookupLocalService(string) (IService, bool) {
	return runtime.peer, runtime.peer != nil
}
func (runtime *testRuntime) AcquireTimerSlot() (TimerID, bool) {
	if runtime.limit <= 0 || runtime.active.Add(1) > int64(runtime.limit) {
		runtime.active.Add(-1)
		return InvalidTimerID, false
	}
	return TimerID(runtime.nextID.Add(1)), true
}
func (runtime *testRuntime) ReleaseTimerSlot() { runtime.active.Add(-1) }
func (runtime *testRuntime) TimerLimit() int   { return runtime.limit }
func (runtime *testRuntime) TimerLocation() *time.Location {
	if runtime.location == nil {
		return time.Local
	}
	return runtime.location
}
func (runtime *testRuntime) Failure() error      { return nil }
func (runtime *testRuntime) ReportFailure(error) {}

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
	if peer, ok := target.LookupLocalService("PlayerService"); !ok || peer != target {
		t.Fatalf("LookupLocalService() = %v, %v", peer, ok)
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

// TestServiceGetNodeReturnsBoundRuntime 防止业务通过 GetNode 取得错误 Node、复制的时钟外观，
// 或在未绑定类型模板上得到伪造运行对象。
func TestServiceGetNodeReturnsBoundRuntime(t *testing.T) {
	unbound := &testService{}
	if unbound.GetNode() != nil {
		t.Fatal("未绑定 Service.GetNode() 未返回 nil")
	}

	target := &testService{}
	runtime := &testRuntime{
		nodeID: "game-1",
		name:   "PlayerService",
		state:  StateRunning,
		now:    time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC),
	}
	if err := BindRuntime(target, runtime); err != nil {
		t.Fatalf("BindRuntime() error = %v", err)
	}

	currentNode := target.GetNode()
	if currentNode == nil || currentNode.ID() != "game-1" {
		t.Fatalf("GetNode() = %#v", currentNode)
	}
	if !currentNode.Now().Equal(runtime.now) {
		t.Fatalf("GetNode().Now() = %v, want %v", currentNode.Now(), runtime.now)
	}
	if err := currentNode.AddTime(24 * time.Hour); err != nil {
		t.Fatalf("GetNode().AddTime() error = %v", err)
	}
	if want := time.Date(2030, 1, 3, 3, 4, 5, 0, time.UTC); !runtime.now.Equal(want) {
		t.Fatalf("runtime time = %v, want %v", runtime.now, want)
	}
}
