// 本示例使用 Origin Discovery 和直连 TCP 在两个业务 Node 间调用生成 RPC。
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

// app 同时管理发现 Node、Player Node 和 Gateway Node。
var app = application.New()

// GatewayService 保存按 PlayerService RPC 合约生成的客户端。
type GatewayService struct {
	service.Service
	players tutorialrpc.PlayerServiceClient
}

// OnInit 只绑定默认 ServiceName，不锁定具体远端 Node。
func (target *GatewayService) OnInit() error {
	target.players = tutorialrpc.BindPlayerService(target)
	return nil
}

// OnStart 等待 Service Running 后发起远端调用。
func (target *GatewayService) OnStart(context.Context) error {
	// 延迟首次调用，让教程可以观察发现同步和 TCP 建连日志。
	target.AfterFunc(300*time.Millisecond, func(ctx context.Context, _ service.TimerID) {
		for attempt := 0; attempt < 20; attempt++ {
			// OnNode 派生精确目标客户端，基础 target.players 值保持不变。
			value, err := target.players.OnNode("player-1").AwaitGetPlayer(ctx, 1001)
			if err == nil {
				target.Logger().Info("remote TCP result: " + value)
				return
			}
			// 只对连接尚未就绪做有限重试，其他错误立即交给业务处理。
			if !errors.Is(err, errs.ErrTransportUnavailable) {
				target.Logger().Error("remote TCP call failed")
				return
			}
			// Await 释放 GatewayService 执行权，避免重试间隔阻塞其他任务。
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
				target.Logger().Error("remote TCP retry cancelled")
				return
			}
		}
		target.Logger().Error("remote TCP was not ready in time")
	})
	return nil
}

// init 登记远端实现和网关调用方两个 Service 类型。
func init() { app.Setup(&PlayerService{}, &GatewayService{}) }

// main 启动 Application。
func main() { app.Start() }
