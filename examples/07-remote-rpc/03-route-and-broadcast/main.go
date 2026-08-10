// 本示例展示生成 RPC 客户端的稳定 Key、轮询、随机、自定义路由和广播外观。
package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/duanhf2012/origin/v3/application"
	"github.com/duanhf2012/origin/v3/examples/_support/tutorialrpc"
	"github.com/duanhf2012/origin/v3/rpc"
	"github.com/duanhf2012/origin/v3/service"
)

// app 是当前示例唯一的 Application。
var app = application.New()

// firstCandidateSelector 是无状态、零堆分配的自定义选择器示例。
// Select 必须同步、快速且可安全并发调用；返回 false 表示拒绝当前候选集。
type firstCandidateSelector struct{}

// Select 固定选择已过滤候选中的第一个实例。
func (firstCandidateSelector) Select(candidates rpc.RouteCandidates) (int, bool) {
	return 0, candidates.Len() > 0
}

// GatewayService 保存生成的轻量 PlayerService 客户端值。
type GatewayService struct {
	service.Service
	players tutorialrpc.PlayerServiceClient
}

// OnInit 使用合约默认 ServiceName 绑定客户端；它不会立刻建立某个目标连接。
func (target *GatewayService) OnInit() error {
	target.players = tutorialrpc.BindPlayerService(target)
	return nil
}

// OnStart 等发现目录和 TCP 连接建立后，依次演示各种候选选择策略。
func (target *GatewayService) OnStart(context.Context) error {
	if id := target.AfterFunc(time.Second, func(ctx context.Context, _ service.TimerID) {
		// Route 使用稳定业务 Key，同一 Key 在候选不变时映射到同一实例。
		value, err := target.players.Route(int64(1001)).AwaitGetPlayer(ctx, 1001)
		if err == nil {
			target.Logger().Info("key route result: " + value)
		} else {
			target.Logger().Error(fmt.Sprintf("key route failed: %v", err))
		}

		// RoundRobin、Random 和 RouteBy 都返回派生值，不会修改基础客户端。
		if err := target.players.RouteRoundRobin().NotifyRefresh(ctx, 1); err != nil {
			target.Logger().Error(fmt.Sprintf("round-robin notify failed: %v", err))
		}
		if err := target.players.RouteRandom().NotifyRefresh(ctx, 2); err != nil {
			target.Logger().Error(fmt.Sprintf("random notify failed: %v", err))
		}
		if err := target.players.RouteBy(firstCandidateSelector{}).NotifyRefresh(ctx, 3); err != nil {
			target.Logger().Error(fmt.Sprintf("custom route notify failed: %v", err))
		}

		// Broadcast 向全部合格实例发送通知；部分失败通过 BroadcastError 聚合返回。
		if err := target.players.BroadcastRefresh(ctx, 4); err != nil {
			var broadcastErr *rpc.BroadcastError
			if !errors.As(err, &broadcastErr) {
				target.Logger().Error(fmt.Sprintf("broadcast failed: %v", err))
				return
			}
			target.Logger().Error(fmt.Sprintf(
				"broadcast result: total=%d succeeded=%d failed=%d",
				broadcastErr.Total(), broadcastErr.Succeeded(), broadcastErr.FailureCount(),
			))
			for index := 0; index < broadcastErr.FailureCount(); index++ {
				failure, _ := broadcastErr.Failure(index)
				target.Logger().Error(fmt.Sprintf(
					"broadcast target failed: node=%s error=%v", failure.NodeID, failure.Err,
				))
			}
			return
		}
		target.Logger().Info("broadcast submitted to all candidates")
	}); id == service.InvalidTimerID {
		return fmt.Errorf("create route and broadcast demonstration timer failed")
	}
	return nil
}

// init 登记远端实现模板和网关调用方模板。
func init() { app.Setup(&PlayerService{}, &GatewayService{}) }

// main 启动 Application。
func main() { app.Start() }
