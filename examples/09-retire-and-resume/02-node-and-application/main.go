// 本示例对比 Application 批量状态切换与指定 Node 状态切换。
package main

import (
	"context"
	"time"

	"github.com/duanhf2012/origin/v3/application"
	"github.com/duanhf2012/origin/v3/service"
)

// app 是当前示例唯一的 Application。
var app = application.New()

// WorkerService 代表被批量控制或单独控制的上游业务。
type WorkerService struct{ service.Service }

// ControlService 从另一个 Node 发起受控状态切换。
type ControlService struct{ service.Service }

// OnStart 使用不同延迟把批量操作和单 Node 操作按顺序展示。
func (target *ControlService) OnStart(context.Context) error {
	// Application.Retire 按 Node 启动顺序倒序退休全部 Node。
	target.AfterFunc(200*time.Millisecond, func(ctx context.Context, _ service.TimerID) {
		if err := app.Retire(ctx); err != nil {
			target.Logger().Error("application retire failed")
			return
		}
		target.Logger().Info("application retired in reverse Node order")
	})

	// Application.Resume 按启动正序恢复全部 Node。
	target.AfterFunc(500*time.Millisecond, func(ctx context.Context, _ service.TimerID) {
		if err := app.Resume(ctx); err != nil {
			target.Logger().Error("application resume failed")
			return
		}
		target.Logger().Info("application resumed in Node start order")
	})

	// Application.Node 查询具体 Node，再演示 Node.Retire 和 Node.Resume。
	target.AfterFunc(800*time.Millisecond, func(ctx context.Context, _ service.TimerID) {
		workerNode, ok := app.Node("upstream-1")
		if !ok {
			target.Logger().Error("upstream node not found")
			return
		}
		if err := workerNode.Retire(ctx); err != nil {
			target.Logger().Error("node retire failed")
			return
		}
		target.Logger().Info("upstream-1 retired explicitly")
		if err := workerNode.Resume(ctx); err != nil {
			target.Logger().Error("node resume failed")
			return
		}
		target.Logger().Info("upstream-1 resumed explicitly")
	})
	return nil
}

// init 登记两个 Node 将使用的 Service 类型模板。
func init() { app.Setup(&WorkerService{}, &ControlService{}) }

// main 启动 Application。
func main() { app.Start() }
