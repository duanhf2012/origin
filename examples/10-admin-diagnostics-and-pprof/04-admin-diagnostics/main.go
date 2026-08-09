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
type AdminDiagnosticsService struct{ service.Service }

// OnStart 证明 Admin 在 Service 启动回调前已经可用。
func (target *AdminDiagnosticsService) OnStart(context.Context) error {
	runtime := target.Application()
	if runtime == nil {
		return fmt.Errorf("application runtime is unavailable")
	}
	address, running := runtime.AdminAddress()
	if !running {
		return fmt.Errorf("--admin did not start the listener")
	}
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
