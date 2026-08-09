// 本示例展示 --pprof 初始状态和运行期关闭、重开、地址查询、再次关闭。
package main

import (
	"context"
	"fmt"
	"time"

	"github.com/duanhf2012/origin/v3/application"
	"github.com/duanhf2012/origin/v3/service"
)

// app 是当前示例唯一的 Application。
var app = application.New()

// pprofRuntime 是本示例状态转换真正需要的最小接口，也让测试无需启动真实 Listener。
type pprofRuntime interface {
	// StartPprof 绑定独立的 pprof HTTP Listener；它不会把 pprof 路由挂到 Admin 上。
	StartPprof(address string) error
	// StopPprof 在 ctx 预算内关闭 pprof，并等待活跃 profile 请求结束或取消。
	StopPprof(ctx context.Context) error
	// PprofAddress 返回当前 pprof Listener 是否运行以及实际监听地址。
	PprofAddress() (string, bool)
	// AdminAddress 用来证明关闭 pprof 不会影响独立的 Admin Listener。
	AdminAddress() (string, bool)
}

// stopPprof 执行一次同步关闭。
//
// StopPprof 不是“发一个后台关闭通知后立即返回”；它可能要等待正在进行的 CPU/Trace
// profile。因此调用方若位于 Service Task 中，必须通过 Await 释放执行权再等待。
func stopPprof(ctx context.Context, runtime pprofRuntime) error {
	return runtime.StopPprof(ctx)
}

// restartPprof 重新绑定并立即读取实际地址。
//
// StartPprof 的 address 是绑定请求；当使用 :0 时，系统会分配实际端口，所以必须再调用
// PprofAddress，而不能继续使用原始字符串拼接 URL。
func restartPprof(runtime pprofRuntime, address string) (string, error) {
	if err := runtime.StartPprof(address); err != nil {
		return "", err
	}
	actualAddress, running := runtime.PprofAddress()
	if !running {
		return "", fmt.Errorf("pprof listener did not publish running state")
	}
	return actualAddress, nil
}

// PprofService 用受控 Timer 展示命令行只决定初始状态，运行期 API 仍可独立切换。
type PprofService struct{ service.Service }

// OnStart 安排 2 秒关闭、4 秒重开、6 秒再次关闭；单元测试直接驱动抽出的操作而不 sleep。
func (target *PprofService) OnStart(context.Context) error {
	runtime := target.Application()
	if runtime == nil {
		return fmt.Errorf("application runtime is unavailable")
	}
	pprofAddress, pprofRunning := runtime.PprofAddress()
	adminAddress, adminRunning := runtime.AdminAddress()
	if !pprofRunning || !adminRunning {
		return fmt.Errorf("--pprof and --admin must start both listeners")
	}
	target.Logger().Info(fmt.Sprintf(
		"initial listeners: pprof=http://%s/debug/pprof/ admin=http://%s/admin/v1/diagnostics",
		pprofAddress,
		adminAddress,
	))

	// 第一个 AfterFunc 在 2 秒后触发。StopPprof 可能等待活跃 Profile 请求退出；
	// Await 先释放 Service 串行执行权，再在 waitCtx 预算内同步等待关闭完成。
	target.AfterFunc(2*time.Second, func(ctx context.Context, _ service.TimerID) {
		if err := target.Await(ctx, func(waitCtx context.Context) error {
			return stopPprof(waitCtx, runtime)
		}); err != nil {
			target.Logger().Error("first StopPprof failed")
			return
		}
		target.Logger().Info("pprof stopped; admin remains independent")
	})

	// 第二个 AfterFunc 在 4 秒后触发。StartPprof 是短冷路径操作；重开后通过
	// PprofAddress 取得实际发布地址，证明“请求地址”和“实际地址”可能不同。
	target.AfterFunc(4*time.Second, func(context.Context, service.TimerID) {
		address, err := restartPprof(runtime, "127.0.0.1:6060")
		if err != nil {
			target.Logger().Error("StartPprof failed")
			return
		}
		target.Logger().Info(fmt.Sprintf("pprof restarted: http://%s/debug/pprof/", address))
	})

	// 第三个 AfterFunc 在 6 秒后触发，完成关闭—重开—查询—再次关闭流程。
	// StopPprof 只影响 pprof；Admin Listener 应当在这段时间内保持独立可用。
	target.AfterFunc(6*time.Second, func(ctx context.Context, _ service.TimerID) {
		if err := target.Await(ctx, func(waitCtx context.Context) error {
			return stopPprof(waitCtx, runtime)
		}); err != nil {
			target.Logger().Error("second StopPprof failed")
			return
		}
		target.Logger().Info("pprof stopped again")
	})
	return nil
}

// init 安装 pprof 切换示例 Service。
func init() { app.Setup(&PprofService{}) }

// main 启动 Application。
func main() { app.Start() }
