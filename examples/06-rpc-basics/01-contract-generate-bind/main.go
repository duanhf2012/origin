// 本示例展示生成 RPC 客户端在同一 Node 内执行 Await 调用的最小外观。
package main

import (
	"context"
	"fmt"

	"github.com/duanhf2012/origin/v3/application"
	"github.com/duanhf2012/origin/v3/examples/_support/tutorialrpc"
	"github.com/duanhf2012/origin/v3/service"
)

// app 是当前示例唯一的 Application。
var app = application.New()

// CallerService 保存可重复使用的轻量强类型客户端。
type CallerService struct {
	service.Service
	players tutorialrpc.PlayerServiceClient
}

// OnInit 完成客户端绑定，不在每次业务请求中重复创建绑定代码。
func (target *CallerService) OnInit() error {
	// 契约名就是 PlayerService，因此默认绑定同名业务 Service。
	target.players = tutorialrpc.BindPlayerService(target)
	return nil
}

// OnStart 只登记启动后任务。同 Node 的 Service 会在全部 OnStart 成功后统一进入 Running，
// 因此不能在这里直接调用尚处于 Starting 的本地 RPC 目标。
func (target *CallerService) OnStart(context.Context) error {
	if id := target.AfterFunc(0, func(ctx context.Context, _ service.TimerID) {
		// AwaitGetPlayer 由 origingen 根据 PlayerService 合约生成；没有显式 Deadline 时使用
		// Service/Node 默认 Await 超时，最终回退到内置 15 秒。
		message, err := target.players.AwaitGetPlayer(ctx, 1001)
		if err != nil {
			target.Logger().Error("await rpc failed")
			return
		}
		target.Logger().Info(fmt.Sprintf("rpc result: %s", message))

		// GoSafe 创建的是普通 goroutine，没有 Service 执行槽；请求—响应调用应使用 Call，
		// 结果会返回到这个 goroutine 的同一调用栈。nil 会取得本次独立的默认 15 秒预算链。
		if err := target.GoSafe(func() {
			callMessage, callErr := target.players.CallGetPlayer(nil, 2002)
			if callErr != nil {
				target.Logger().Error("call rpc failed")
				return
			}
			// Logger 可并发使用；若要修改 target 的业务状态，应再 DispatchAsync 回串行队列。
			target.Logger().Info(fmt.Sprintf("call result: %s", callMessage))
		}); err != nil {
			target.Logger().Error("start ordinary rpc caller failed")
		}
	}); id == service.InvalidTimerID {
		return fmt.Errorf("create rpc demo timer failed")
	}
	return nil
}

// init 同时登记 RPC 实现 Service 和调用方 Service。
func init() { app.Setup(&PlayerService{}, &CallerService{}) }

// main 启动 Application。
func main() { app.Start() }
