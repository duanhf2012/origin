// 本示例通过通用 Admin Listener 暴露内置 Diagnostics Summary 与 Full Snapshot。
package main

import (
	"context"
	"fmt"

	"github.com/duanhf2012/origin/v3/application"
	"github.com/duanhf2012/origin/v3/service"
)

// app 是当前示例唯一的 Application。
var app = application.New()

// AdminDiagnosticsService 仅输出 --admin 已建立的实际地址，不创建第二个 HTTP Server。
//
// Diagnostics 是 Admin 的内置只读路由：Application 在启动前安装它，外部客户端通过
// /admin/v1/diagnostics 访问；这个 Service 只是为了演示 OnStart 时如何取得实际绑定地址，
// 不是 Diagnostics 的实现者。
type AdminDiagnosticsService struct{ service.Service }

// OnStart 证明 Admin 在 Service 启动回调前已经可用。
func (target *AdminDiagnosticsService) OnStart(context.Context) error {
	runtime := target.Application()
	if runtime == nil {
		return fmt.Errorf("application runtime is unavailable")
	}
	// AdminAddress 返回 Listener 的实际地址。配置使用 :0 时，返回值可能是系统分配的端口；
	// running=false 表示 Admin 没有成功监听，不能据此拼接可用 URL。
	address, running := runtime.AdminAddress()
	if !running {
		return fmt.Errorf("--admin did not start the listener")
	}
	// 默认路径返回低成本 Summary；detail=full 是显式排障请求，响应更大且采集更贵。
	target.Logger().Info(fmt.Sprintf(
		"summary=http://%s/admin/v1/diagnostics full=http://%s/admin/v1/diagnostics?detail=full",
		address,
		address,
	))
	return nil
}

// init 安装示例 Service。
func init() { app.Setup(&AdminDiagnosticsService{}) }

// main 启动 Application。
func main() { app.Start() }
