// Package service 定义 Origin 业务 Service 的最小生命周期和只读运行环境。
package service

import (
	"context"
	"sync"

	originlog "github.com/duanhf2012/origin/v3/log"
)

// IService 是所有 Origin 业务 Service 必须满足的最小生命周期接口。
//
// 业务类型通常嵌入 Service，并只覆盖实际需要的生命周期方法。未导出的 baseService 方法
// 保证只有嵌入 Origin Service 的类型才能进入框架装配。
type IService interface {
	OnInit() error
	OnStart(ctx context.Context) error
	OnStop(ctx context.Context) error

	baseService() *Service
}

// Service 为业务 Service 提供默认生命周期和只读运行环境查询。
//
// Service 应以值方式匿名嵌入业务结构体。它在绑定后不能复制，也不能被多个业务 Service
// 或 Node 共享。
type Service struct {
	// bindMu 只保护一次性 Runtime 绑定；正常运行查询不经过互斥锁。
	bindMu sync.Mutex
	// runtime 在 Node 完成实例装配后保持只读。
	runtime Runtime
}

// OnInit 是不需要初始化逻辑时使用的默认空实现。
func (service *Service) OnInit() error {
	return nil
}

// OnStart 是不需要启动逻辑时使用的默认空实现。
func (service *Service) OnStart(context.Context) error {
	return nil
}

// OnStop 是不需要停止逻辑时使用的默认空实现。
func (service *Service) OnStop(context.Context) error {
	return nil
}

// Name 返回当前实例在所属 Node 内的实际 ServiceName。
func (service *Service) Name() string {
	// 未绑定的类型样本没有运行身份，返回空字符串比伪造类型名更明确。
	if service == nil || service.runtime == nil {
		return ""
	}
	return service.runtime.ServiceName()
}

// NodeID 返回当前 Service 所属 Node 的稳定 ID。
func (service *Service) NodeID() string {
	// 类型样本和装配失败对象尚不属于 Node，因此返回空字符串。
	if service == nil || service.runtime == nil {
		return ""
	}
	return service.runtime.NodeID()
}

// State 返回当前 Service 的无锁生命周期状态快照。
func (service *Service) State() State {
	// 未绑定对象仍处于 Created，便于零值 Service 在测试和 Setup 时安全查询。
	if service == nil || service.runtime == nil {
		return StateCreated
	}
	return service.runtime.State()
}

// Logger 返回已经绑定 NodeID 和 ServiceName 的结构化 Logger。
func (service *Service) Logger() originlog.Logger {
	// 未绑定对象不能访问 Application Logger，返回不会产生输出的安全零值。
	if service == nil || service.runtime == nil {
		return originlog.NewNop()
	}
	return service.runtime.Logger()
}

// LookupService 查询同一 Node 中具有实际名称 name 的 Service。
func (service *Service) LookupService(name string) (IService, bool) {
	// 未绑定实例没有所属 Node；空名称也不具有有效查询语义。
	if service == nil || service.runtime == nil || name == "" {
		return nil, false
	}
	return service.runtime.LookupService(name)
}

// baseService 返回嵌入对象，供 BindRuntime 完成唯一所有权绑定。
func (service *Service) baseService() *Service {
	return service
}
