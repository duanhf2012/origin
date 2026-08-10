// 本示例对比生成客户端的 Async 返回回调，以及无业务返回值的 Notify 与 Broadcast。
package main

import (
	"context"
	"fmt"
	"time"

	"github.com/duanhf2012/origin/v3/application"
	"github.com/duanhf2012/origin/v3/examples/_support/tutorialrpc"
	"github.com/duanhf2012/origin/v3/service"
)

// app 是当前示例唯一的 Application。
var app = application.New()

// CallerService 复用共享 PlayerService RPC 合约生成的客户端。
type CallerService struct {
	service.Service
	players tutorialrpc.PlayerServiceClient
}

// OnInit 使用约定的默认 PlayerService 名完成绑定。
func (target *CallerService) OnInit() error {
	target.players = tutorialrpc.BindPlayerService(target)
	return nil
}

// OnStart 登记一个启动后 Timer Task，由它提交异步请求和单向通知。
func (target *CallerService) OnStart(context.Context) error {
	if id := target.AfterFunc(100*time.Millisecond, func(ctx context.Context, _ service.TimerID) {
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
		// Notify 只报告提交/传输层错误；nil 合法，且无响应通知不会建立 15 秒 Pending。
		if err := target.players.NotifyRefresh(nil, 7); err != nil {
			target.Logger().Error("notify failed")
		}
		// 当前 Node 只有一个匹配目标，Broadcast 仍走完整的广播准备与投递外观。
		if err := target.players.BroadcastRefresh(ctx, 8); err != nil {
			target.Logger().Error("broadcast failed")
		}
	}); id == service.InvalidTimerID {
		return fmt.Errorf("create rpc demo timer failed")
	}
	return nil
}

// init 登记实现与调用方两个 Service 模板。
func init() { app.Setup(&PlayerService{}, &CallerService{}) }

// main 启动 Application。
func main() { app.Start() }
