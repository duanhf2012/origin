package service

import (
	"context"

	"github.com/duanhf2012/origin/v3/discovery"
	"github.com/duanhf2012/origin/v3/errs"
)

// discoveryRuntime 是 Service 发现外观对所属 Node 定义的最小使用方接口。
//
// 它不加入基础 Runtime，避免不需要发现能力的独立测试和低层组件被迫实现空方法。
type discoveryRuntime interface {
	FindDiscoveredService(
		nodeID string,
		serviceName string,
	) (discovery.Instance, bool)
	ListDiscoveredServices(serviceName string) []discovery.Instance
	AwaitDiscoveredService(
		ctx context.Context,
		nodeID string,
		serviceName string,
	) error
	AddDiscoveryListener(
		listener discovery.IListener,
	) (discovery.ListenerID, error)
	RemoveDiscoveryListener(id *discovery.ListenerID) bool
}

// FindDiscoveredService 精确查询当前 Node 可见的远端 NodeID 和 ServiceName。
func (service *Service) FindDiscoveredService(
	nodeID string,
	serviceName string,
) (discovery.Instance, bool) {
	// 空参数不具有通配语义；未绑定或尚未装配 M14 的 Runtime 返回不存在。
	runtime, ok := service.discoveryRuntime()
	if !ok || nodeID == "" || serviceName == "" {
		return discovery.Instance{}, false
	}
	return runtime.FindDiscoveredService(nodeID, serviceName)
}

// ListDiscoveredServices 返回指定 ServiceName 的全部远端可见实例。
//
// 返回 Slice、Instance 和 Labels 均归业务独立持有；空名称和无候选返回 nil。
func (service *Service) ListDiscoveredServices(
	serviceName string,
) []discovery.Instance {
	runtime, ok := service.discoveryRuntime()
	if !ok || serviceName == "" {
		return nil
	}
	return runtime.ListDiscoveredServices(serviceName)
}

// AwaitService 等待任意远端 Node 上出现指定可见 Service。
//
// 本方法复用 Service.Await，因此普通 Task 会协作式让出执行权，OnStart 则在原生命周期
// goroutine 中顺序等待；统一默认超时和显式 Deadline 规则保持不变。
func (service *Service) AwaitService(
	ctx context.Context,
	serviceName string,
) error {
	runtime, ok := service.discoveryRuntime()
	if !ok {
		return errs.ErrServiceNotReady
	}
	if serviceName == "" {
		return errs.ErrInvalidArgument
	}
	return service.Await(ctx, func(waitContext context.Context) error {
		return runtime.AwaitDiscoveredService(waitContext, "", serviceName)
	})
}

// AwaitNodeService 等待指定远端 Node 上出现指定可见 Service。
func (service *Service) AwaitNodeService(
	ctx context.Context,
	nodeID string,
	serviceName string,
) error {
	runtime, ok := service.discoveryRuntime()
	if !ok {
		return errs.ErrServiceNotReady
	}
	// 当前 Node 永远从自己的远端目录中过滤，等待自己只能形成无法完成的启动循环。
	if nodeID == "" || serviceName == "" || nodeID == service.NodeID() {
		return errs.ErrInvalidArgument
	}
	return service.Await(ctx, func(waitContext context.Context) error {
		return runtime.AwaitDiscoveredService(waitContext, nodeID, serviceName)
	})
}

// AddDiscoveryListener 注册一个同时接收发现、状态变化和失去发现的监听器。
//
// 监听器归属于当前 Service，并在进入 OnStop 前自动移除；返回 ID 只在业务需要提前
// 取消这一次注册时交给 RemoveDiscoveryListener。
func (service *Service) AddDiscoveryListener(
	listener discovery.IListener,
) (discovery.ListenerID, error) {
	runtime, ok := service.discoveryRuntime()
	if !ok {
		return 0, errs.ErrServiceNotReady
	}
	if listener == nil {
		return 0, errs.ErrInvalidArgument
	}
	return runtime.AddDiscoveryListener(listener)
}

// RemoveDiscoveryListener 删除当前 Service 的一次监听注册，并在成功后把外部 ID 置零。
func (service *Service) RemoveDiscoveryListener(
	id *discovery.ListenerID,
) bool {
	runtime, ok := service.discoveryRuntime()
	if !ok || id == nil || *id == 0 {
		return false
	}
	return runtime.RemoveDiscoveryListener(id)
}

// discoveryRuntime 取得已经由 Node 装配的可选发现桥。
func (service *Service) discoveryRuntime() (discoveryRuntime, bool) {
	if service == nil || service.runtime == nil {
		return nil, false
	}
	runtime, ok := service.runtime.(discoveryRuntime)
	return runtime, ok
}
