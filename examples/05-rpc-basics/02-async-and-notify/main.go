// 本示例对比生成客户端的 Async 返回回调与无业务返回值 Notify。
package main

import (
	"context"
	"time"

	"github.com/duanhf2012/origin/v3/application"
	"github.com/duanhf2012/origin/v3/examples/_support/tutorialrpc"
	"github.com/duanhf2012/origin/v3/service"
)

// app 是当前示例唯一的 Application。
var app = application.New()

// CallerService 复用共享 PlayerRPC 合约生成的客户端。
type CallerService struct {
	service.Service
	players tutorialrpc.PlayerRPCClient
}

// OnInit 使用约定的默认 PlayerService 名完成绑定。
func (target *CallerService) OnInit() error {
	target.players = tutorialrpc.BindPlayerRPC(target)
	return nil
}

// OnStart 在后续 Service 任务中提交异步请求和单向通知。
func (target *CallerService) OnStart(context.Context) error {
	target.AfterFunc(100*time.Millisecond, func(ctx context.Context, _ service.TimerID) {
		// Async 提交成功不代表远端业务成功；最终结果由恰好一次的回调交付。
		if err := target.players.AsyncGetPlayer(ctx, 1001, func(_ context.Context, value string, err error) {
			if err != nil {
				target.Logger().Error("async rpc failed")
				return
			}
			target.Logger().Info("async result: " + value)
		}); err != nil {
			target.Logger().Error("submit async rpc failed")
		}
		// Notify 只报告提交/传输层错误，适合不需要业务返回值的单向语义。
		if err := target.players.NotifyRefresh(ctx, 7); err != nil {
			target.Logger().Error("notify failed")
		}
	})
	return nil
}

// init 登记实现与调用方两个 Service 模板。
func init() { app.Setup(&tutorialrpc.PlayerService{}, &CallerService{}) }

// main 启动 Application。
func main() { app.Start() }
