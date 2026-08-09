// 本示例展示：--admin 只决定初始状态；运行中的 Application 仍可用代码开关 Admin Listener。
package main

import (
	"context"
	"fmt"
	"time"

	"github.com/duanhf2012/origin/v3/application"
	"github.com/duanhf2012/origin/v3/service"
)

// app 是当前示例唯一的 Application。运行脚本故意不传 --admin，因此它起初没有 Admin Listener。
var app = application.New()

// adminRuntime 是本示例需要的运行期能力；真实 Service 中 target.Application() 满足这个接口。
// 抽成小接口后，单元测试可以验证启停流程而无需打开真实 TCP 端口或等待计时器。
type adminRuntime interface {
	// StartAdminServer 开始监听已在启动阶段冻结好的 Admin 路由。
	StartAdminServer(address string) error
	// StopAdminServer 停止接受新请求，并在 ctx 的等待预算内关闭已有请求。
	StopAdminServer(ctx context.Context) error
	// AdminAddress 返回实际监听地址；传入 :0 时应使用它返回的系统分配端口。
	AdminAddress() (string, bool)
}

// startAdmin 打开 Listener 后立即读取实际地址。
//
// address 只是绑定请求，可能包含 :0；日志、curl 和其他调用方都必须使用返回的 actualAddress。
func startAdmin(runtime adminRuntime, address string) (actualAddress string, err error) {
	if err := runtime.StartAdminServer(address); err != nil {
		return "", err
	}
	actualAddress, running := runtime.AdminAddress()
	if !running {
		return "", fmt.Errorf("Admin listener did not publish running state")
	}
	return actualAddress, nil
}

// stopAdmin 是一次同步关闭：只有所有活跃 Admin 请求结束或 ctx 到期后才返回。
// 在 Service Timer/Task 中调用它时，调用方要先用 Await 释放 Service 的唯一执行槽。
func stopAdmin(ctx context.Context, runtime adminRuntime) error {
	return runtime.StopAdminServer(ctx)
}

// AdminToggleService 用受控计时器演示运行期启停。单元测试直接驱动 startAdmin/stopAdmin，不依赖 sleep。
type AdminToggleService struct{ service.Service }

// OnStart 依次安排：2 秒打开、4 秒关闭、6 秒重开、8 秒再次关闭。
// 运行脚本没有 --admin，因此第一个 StartAdminServer 是真正的运行期开启动作。
func (target *AdminToggleService) OnStart(context.Context) error {
	runtime := target.Application()
	if runtime == nil {
		return fmt.Errorf("application runtime is unavailable")
	}
	if address, running := runtime.AdminAddress(); running {
		return fmt.Errorf("Admin listener is already running at %s; run this example without --admin", address)
	}

	const address = "127.0.0.1:6065"
	target.AfterFunc(2*time.Second, func(context.Context, service.TimerID) {
		actualAddress, err := startAdmin(runtime, address)
		if err != nil {
			target.Logger().Error("StartAdminServer failed")
			return
		}
		target.Logger().Info(fmt.Sprintf("admin started: http://%s/admin/v1/diagnostics", actualAddress))
	})

	target.AfterFunc(4*time.Second, func(ctx context.Context, _ service.TimerID) {
		if err := target.Await(ctx, func(waitCtx context.Context) error {
			return stopAdmin(waitCtx, runtime)
		}); err != nil {
			target.Logger().Error("first StopAdminServer failed")
			return
		}
		target.Logger().Info("admin stopped")
	})

	target.AfterFunc(6*time.Second, func(context.Context, service.TimerID) {
		actualAddress, err := startAdmin(runtime, address)
		if err != nil {
			target.Logger().Error("second StartAdminServer failed")
			return
		}
		target.Logger().Info(fmt.Sprintf("admin restarted: http://%s/admin/v1/diagnostics", actualAddress))
	})

	target.AfterFunc(8*time.Second, func(ctx context.Context, _ service.TimerID) {
		if err := target.Await(ctx, func(waitCtx context.Context) error {
			return stopAdmin(waitCtx, runtime)
		}); err != nil {
			target.Logger().Error("second StopAdminServer failed")
			return
		}
		target.Logger().Info("admin stopped again")
	})
	return nil
}

// init 只安装 Service 类型；实际 Admin 路由仍由 Application 在启动阶段冻结。
func init() { app.Setup(&AdminToggleService{}) }

// main 运行 Application。run.bat/run.sh 故意不传 --admin，以演示代码决定运行期状态。
func main() { app.Start() }
