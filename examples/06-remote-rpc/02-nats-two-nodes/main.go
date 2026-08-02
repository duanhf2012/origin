// 本示例保持业务 RPC 代码不变，仅通过 YAML 把远程传输切换为 NATS。
package main

import (
	"context"
	"errors"
	"time"

	"github.com/duanhf2012/origin/v3/application"
	"github.com/duanhf2012/origin/v3/errs"
	"github.com/duanhf2012/origin/v3/examples/_support/tutorialrpc"
	"github.com/duanhf2012/origin/v3/service"
)

// app 管理发现、远端实现和网关三个 Node。
var app = application.New()

// GatewayService 与 TCP 示例使用完全相同的生成客户端类型。
type GatewayService struct {
	service.Service
	players tutorialrpc.PlayerRPCClient
}

// OnInit 绑定合约默认的 PlayerService 名称。
func (target *GatewayService) OnInit() error {
	target.players = tutorialrpc.BindPlayerRPC(target)
	return nil
}

// OnStart 在 NATS 连接和发现目录就绪后执行远端调用。
func (target *GatewayService) OnStart(context.Context) error {
	target.AfterFunc(300*time.Millisecond, func(ctx context.Context, _ service.TimerID) {
		for attempt := 0; attempt < 20; attempt++ {
			// 业务代码仍使用 OnNode + AwaitGetPlayer，不感知底层 NATS subject。
			value, err := target.players.OnNode("player-1").AwaitGetPlayer(ctx, 1001)
			if err == nil {
				target.Logger().Info("remote NATS result: " + value)
				return
			}
			// 连接恢复期间只对 TransportUnavailable 做有界重试。
			if !errors.Is(err, errs.ErrTransportUnavailable) {
				target.Logger().Error("remote NATS call failed")
				return
			}
			if err := target.Await(ctx, func(waitCtx context.Context) error {
				timer := time.NewTimer(100 * time.Millisecond)
				defer timer.Stop()
				select {
				case <-timer.C:
					return nil
				case <-waitCtx.Done():
					return waitCtx.Err()
				}
			}); err != nil {
				return
			}
		}
		target.Logger().Error("remote NATS was not ready in time")
	})
	return nil
}

// init 登记实现与调用方模板。
func init() { app.Setup(&tutorialrpc.PlayerService{}, &GatewayService{}) }

// main 启动 Application。
func main() { app.Start() }
