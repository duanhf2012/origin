package service

import (
	"context"

	"github.com/duanhf2012/origin/v3/diagnostics"
)

// ApplicationRuntime 是业务管理 Service 可以访问的最小进程级诊断外观。
//
// 接口故意不包含 Application.Stop、Setup、Provider 注册、Node 构建或配置修改。两个 Stop
// 方法可能等待 HTTP 请求排空，业务 RPC 应通过 Service.Await 调用。
type ApplicationRuntime interface {
	diagnostics.Source

	StartDiagnosticsServer(address string) error
	StopDiagnosticsServer(ctx context.Context) error
	DiagnosticsAddress() (string, bool)

	StartPprof(address string) error
	StopPprof(ctx context.Context) error
	PprofAddress() (string, bool)
}

// applicationRuntimeProvider 是 Node 私有 Runtime 可选实现的装配桥。
//
// 它不加入 Runtime 主接口，避免扩张既有框架测试替身和只需要本地能力的 Runtime。
type applicationRuntimeProvider interface {
	Application() ApplicationRuntime
}

// Application 返回所属进程的受限诊断外观。
//
// 零值 Service、Setup 类型样本和未提供可选桥的自定义测试 Runtime 返回 nil；真实 Node
// 运行实例从 OnInit 开始返回 Application 注入的同一接口。
func (service *Service) Application() ApplicationRuntime {
	if service == nil || service.runtime == nil {
		return nil
	}
	provider, ok := service.runtime.(applicationRuntimeProvider)
	if !ok {
		return nil
	}
	return provider.Application()
}
