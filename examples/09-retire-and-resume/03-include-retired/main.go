// 本示例展示默认排除 Retired 候选以及显式 IncludeRetired 的调用方式。
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

// CallerService 保存默认只选择 Running 实例的基础客户端。
type CallerService struct {
	service.Service
	players tutorialrpc.PlayerServiceClient
}

// OnInit 绑定本 Node 中的 PlayerService。
func (target *CallerService) OnInit() error {
	target.players = tutorialrpc.BindPlayerService(target)
	return nil
}

// OnStart 先退休目标，再派生允许 Retired 候选的客户端执行调用。
func (target *CallerService) OnStart(context.Context) error {
	target.AfterFunc(200*time.Millisecond, func(ctx context.Context, _ service.TimerID) {
		// LookupLocalService 只从当前 Service 所属 Node 的本地实例中按名称查询；
		// 不会读取发现目录、查询其他 Node，或替代业务 RPC。
		player, ok := target.LookupLocalService("PlayerService")
		if !ok || player.Retire(ctx) != nil {
			target.Logger().Error("retire target failed")
			return
		}
		// IncludeRetired 返回派生客户端值，基础 target.players 的默认规则不变。
		value, err := target.players.IncludeRetired().AwaitGetPlayer(ctx, 7)
		if err != nil {
			target.Logger().Error("explicit retired call failed")
			return
		}
		target.Logger().Info("explicit retired call: " + value)
	})
	return nil
}

// init 登记目标实现和调用方。
func init() { app.Setup(&PlayerService{}, &CallerService{}) }

// main 启动 Application。
func main() { app.Start() }
