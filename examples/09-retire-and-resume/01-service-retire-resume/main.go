// 本示例展示 --retired 初始状态，以及单个 Service 在不停止进程时的 Retire 和 Resume。
package main

import (
	"context"
	"time"

	"github.com/duanhf2012/origin/v3/application"
	"github.com/duanhf2012/origin/v3/service"
)

// app 是当前示例唯一的 Application。
var app = application.New()

// MaintenanceService 在两个 Timer 回调中切换自己的可路由状态。
type MaintenanceService struct{ service.Service }

// OnStart 登记状态切换任务；run 脚本使用 --retired，因此首个 Retire 是幂等调用。
func (target *MaintenanceService) OnStart(context.Context) error {
	// --retired 会在 OnStart 全部完成后、首次发现发布前提交 Retired；Timer 到期时
	// 再调用 Retire 会幂等成功，证明初始状态仍使用同一套运行期状态机。
	target.AfterFunc(200*time.Millisecond, func(ctx context.Context, _ service.TimerID) {
		if err := target.Retire(ctx); err != nil {
			target.Logger().Error("retire failed")
			return
		}
		target.Logger().Info("service state after retire: " + target.State().String())
	})
	// Resume 把同一实例恢复为 Running，无需重新执行 OnInit/OnStart。
	target.AfterFunc(500*time.Millisecond, func(ctx context.Context, _ service.TimerID) {
		if err := target.Resume(ctx); err != nil {
			target.Logger().Error("resume failed")
			return
		}
		target.Logger().Info("service state after resume: " + target.State().String())
	})
	return nil
}

// init 登记维护状态示例 Service。
func init() { app.Setup(&MaintenanceService{}) }

// main 启动 Application。
func main() { app.Start() }
