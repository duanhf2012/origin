// 本示例展示 Diagnostics HTTP Listener 的启动、地址查询和显式停止。
package main

import (
	"context"
	"fmt"

	"github.com/duanhf2012/origin/v3/application"
	"github.com/duanhf2012/origin/v3/service"
)

// app 是当前示例唯一的 Application。
var app = application.New()

// DiagnosticsServerService 把 Listener 生命周期归到业务明确控制的 Service。
type DiagnosticsServerService struct{ service.Service }

// OnStart 仅绑定回环地址，避免教程默认把诊断信息暴露到外部网络。
func (target *DiagnosticsServerService) OnStart(context.Context) error {
	// 管理 Service 通过受限 Application 外观控制诊断资源，无需依赖包级 app 变量。
	runtime := target.Application()
	if runtime == nil {
		return fmt.Errorf("application runtime is unavailable")
	}
	if err := runtime.StartDiagnosticsServer("127.0.0.1:6061"); err != nil {
		return err
	}
	// DiagnosticsAddress 返回实际监听地址，端口为 0 时也可取得系统分配结果。
	address, _ := runtime.DiagnosticsAddress()
	target.Logger().Info(fmt.Sprintf("diagnostics JSON: http://%s/debug/origin/diagnostics", address))
	return nil
}

// OnStop 显式关闭 Listener；Application 的兜底关闭仍保证异常路径不会泄漏端口。
func (target *DiagnosticsServerService) OnStop(ctx context.Context) error {
	// OnStop 已经位于生命周期清理路径，直接使用其停止 Context 等待 HTTP 请求排空。
	runtime := target.Application()
	if runtime == nil {
		return nil
	}
	return runtime.StopDiagnosticsServer(ctx)
}

// init 登记诊断 HTTP Service。
func init() { app.Setup(&DiagnosticsServerService{}) }

// main 启动 Application。
func main() { app.Start() }
