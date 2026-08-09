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
	StartPprof(address string) error
	StopPprof(ctx context.Context) error
	PprofAddress() (string, bool)
	AdminAddress() (string, bool)
}

// stopPprof 执行一次同步关闭；调用方若位于 Service Task 中，必须用 Await 包裹。
func stopPprof(ctx context.Context, runtime pprofRuntime) error {
	return runtime.StopPprof(ctx)
}

// restartPprof 重新绑定并立即读取实际地址；端口为 0 时返回值尤其重要。
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

	// StopPprof 可能等待活跃 Profile 请求退出；Await 释放 Service 串行执行权再同步等待。
	target.AfterFunc(2*time.Second, func(ctx context.Context, _ service.TimerID) {
		if err := target.Await(ctx, func(waitCtx context.Context) error {
			return stopPprof(waitCtx, runtime)
		}); err != nil {
			target.Logger().Error("first StopPprof failed")
			return
		}
		target.Logger().Info("pprof stopped; admin remains independent")
	})

	// StartPprof 是短冷路径操作，重开后通过 PprofAddress 取得实际发布地址。
	target.AfterFunc(4*time.Second, func(context.Context, service.TimerID) {
		address, err := restartPprof(runtime, "127.0.0.1:6060")
		if err != nil {
			target.Logger().Error("StartPprof failed")
			return
		}
		target.Logger().Info(fmt.Sprintf("pprof restarted: http://%s/debug/pprof/", address))
	})

	// 再次关闭固定完整的关闭—重开—查询—关闭流程。
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
