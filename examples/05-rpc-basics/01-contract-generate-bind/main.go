// 本示例展示生成 RPC 客户端在同一 Node 内执行 Await 调用的最小外观。
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

// CallerService 保存可重复使用的轻量强类型客户端。
type CallerService struct {
	service.Service
	players tutorialrpc.PlayerRPCClient
}

// OnInit 完成客户端绑定，不在每次业务请求中重复创建绑定代码。
func (target *CallerService) OnInit() error {
	// 默认绑定名来自 PlayerRPC 的约定，对应 PlayerService。
	target.players = tutorialrpc.BindPlayerRPC(target)
	return nil
}

// OnStart 延迟到 Service Running 后发起一次顺序化 Await RPC。
func (target *CallerService) OnStart(context.Context) error {
	// Timer 回调提供当前 Service 任务 Context，RPC 完成后仍回到同一执行语义。
	target.AfterFunc(100*time.Millisecond, func(ctx context.Context, _ service.TimerID) {
		// AwaitGetPlayer 由 origingen 根据 PlayerRPC 合约生成并保留强类型参数/返回值。
		message, err := target.players.AwaitGetPlayer(ctx, 1001)
		if err != nil {
			target.Logger().Error("rpc failed")
			return
		}
		target.Logger().Info(fmt.Sprintf("rpc result: %s", message))
	})
	return nil
}

// init 同时登记 RPC 实现 Service 和调用方 Service。
func init() { app.Setup(&tutorialrpc.PlayerService{}, &CallerService{}) }

// main 启动 Application。
func main() { app.Start() }
