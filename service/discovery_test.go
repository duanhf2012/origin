package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/duanhf2012/origin/v3/discovery"
	"github.com/duanhf2012/origin/v3/errs"
	originlog "github.com/duanhf2012/origin/v3/log"
)

// discoveryServiceTestRuntime 提供公开 Service 外观所需的最小 Node 发现桥。
type discoveryServiceTestRuntime struct {
	state     State
	instances []discovery.Instance
	listener  discovery.IListener
	nextID    discovery.ListenerID
}

func (runtime *discoveryServiceTestRuntime) NodeID() string      { return "gateway-1" }
func (runtime *discoveryServiceTestRuntime) ServiceName() string { return "GatewayService" }
func (runtime *discoveryServiceTestRuntime) State() State        { return runtime.state }
func (runtime *discoveryServiceTestRuntime) Logger() originlog.Logger {
	return originlog.NewNop()
}
func (runtime *discoveryServiceTestRuntime) LookupService(string) (IService, bool) {
	return nil, false
}
func (runtime *discoveryServiceTestRuntime) AcquireTimerSlot() (TimerID, bool) {
	return 1, true
}
func (runtime *discoveryServiceTestRuntime) ReleaseTimerSlot() {}
func (runtime *discoveryServiceTestRuntime) TimerLimit() int   { return 1 }
func (runtime *discoveryServiceTestRuntime) TimerLocation() *time.Location {
	return time.Local
}
func (runtime *discoveryServiceTestRuntime) Failure() error      { return nil }
func (runtime *discoveryServiceTestRuntime) ReportFailure(error) {}

func (runtime *discoveryServiceTestRuntime) FindDiscoveredService(
	nodeID string,
	serviceName string,
) (discovery.Instance, bool) {
	for _, instance := range runtime.instances {
		if instance.NodeID == nodeID && instance.ServiceName == serviceName {
			return instance, true
		}
	}
	return discovery.Instance{}, false
}

func (runtime *discoveryServiceTestRuntime) ListDiscoveredServices(
	serviceName string,
) []discovery.Instance {
	var result []discovery.Instance
	for _, instance := range runtime.instances {
		if instance.ServiceName == serviceName {
			result = append(result, instance)
		}
	}
	return result
}

func (runtime *discoveryServiceTestRuntime) AwaitDiscoveredService(
	context.Context,
	string,
	string,
) error {
	return nil
}

func (runtime *discoveryServiceTestRuntime) AddDiscoveryListener(
	listener discovery.IListener,
) (discovery.ListenerID, error) {
	runtime.nextID++
	runtime.listener = listener
	return runtime.nextID, nil
}

func (runtime *discoveryServiceTestRuntime) RemoveDiscoveryListener(
	id *discovery.ListenerID,
) bool {
	if id == nil || *id == 0 || runtime.listener == nil {
		return false
	}
	runtime.listener = nil
	*id = 0
	return true
}

// discoveryTestListener 是不执行额外逻辑的公开监听器测试对象。
type discoveryTestListener struct{}

func (discoveryTestListener) OnDiscovered(context.Context, discovery.Event)   {}
func (discoveryTestListener) OnStateChanged(context.Context, discovery.Event) {}
func (discoveryTestListener) OnLost(context.Context, discovery.Event)         {}

// TestServiceDiscoveryQueryAndListenerFacade 验证业务只通过 Service 取得只读发现能力。
func TestServiceDiscoveryQueryAndListenerFacade(t *testing.T) {
	runtime := &discoveryServiceTestRuntime{
		state: StateRunning,
		instances: []discovery.Instance{{
			NodeID:      "game-1",
			SessionID:   1,
			ServiceName: "PlayerService",
			State:       discovery.StateRunning,
			Labels:      map[string]string{"region": "cn-east"},
		}},
	}
	target := &testService{}
	if err := BindRuntime(target, runtime); err != nil {
		t.Fatalf("BindRuntime() error = %v", err)
	}

	instance, exists := target.FindDiscoveredService("game-1", "PlayerService")
	if !exists || instance.SessionID != 1 {
		t.Fatalf("FindDiscoveredService() = (%+v, %v)", instance, exists)
	}
	list := target.ListDiscoveredServices("PlayerService")
	if len(list) != 1 || list[0].NodeID != "game-1" {
		t.Fatalf("ListDiscoveredServices() = %+v", list)
	}

	id, err := target.AddDiscoveryListener(discoveryTestListener{})
	if err != nil || id == 0 {
		t.Fatalf("AddDiscoveryListener() = (%d, %v)", id, err)
	}
	if !target.RemoveDiscoveryListener(&id) || id != 0 {
		t.Fatalf("RemoveDiscoveryListener() = false 或 ID 未清零: %d", id)
	}
}

// TestServiceDiscoveryFacadeRejectsUnboundAndSelfWait 验证无运行时和当前 Node 等待快速失败。
func TestServiceDiscoveryFacadeRejectsUnboundAndSelfWait(t *testing.T) {
	// 未绑定 Runtime 时，查询和监听必须使用稳定的未就绪语义快速失败。
	var unbound Service
	if _, exists := unbound.FindDiscoveredService("game-1", "PlayerService"); exists {
		t.Fatal("未绑定 Service 查询到了发现实例")
	}
	if _, err := unbound.AddDiscoveryListener(discoveryTestListener{}); !errors.Is(
		err,
		errs.ErrServiceNotReady,
	) {
		t.Fatalf("未绑定 AddDiscoveryListener() error = %v", err)
	}

	// 已绑定 Service 等待当前 Node 只会形成启动循环，必须在进入 Await 前直接拒绝。
	runtime := &discoveryServiceTestRuntime{state: StateRunning}
	target := &testService{}
	if err := BindRuntime(target, runtime); err != nil {
		t.Fatalf("BindRuntime() error = %v", err)
	}
	if err := target.AwaitNodeService(
		context.Background(),
		runtime.NodeID(),
		"PlayerService",
	); !errors.Is(err, errs.ErrInvalidArgument) {
		t.Fatalf("AwaitNodeService(self) error = %v", err)
	}
}
