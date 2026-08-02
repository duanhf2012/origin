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

var app = application.New()

type GatewayService struct {
	service.Service
	players tutorialrpc.PlayerRPCClient
}

func (target *GatewayService) OnInit() error {
	target.players = tutorialrpc.BindPlayerRPC(target)
	return nil
}

func (target *GatewayService) OnStart(context.Context) error {
	target.AfterFunc(300*time.Millisecond, func(ctx context.Context, _ service.TimerID) {
		for attempt := 0; attempt < 20; attempt++ {
			value, err := target.players.OnNode("player-1").AwaitGetPlayer(ctx, 1001)
			if err == nil {
				target.Logger().Info("remote TCP result: " + value)
				return
			}
			if !errors.Is(err, errs.ErrTransportUnavailable) {
				target.Logger().Error("remote TCP call failed")
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
				target.Logger().Error("remote TCP retry cancelled")
				return
			}
		}
		target.Logger().Error("remote TCP was not ready in time")
	})
	return nil
}

func init() { app.Setup(&tutorialrpc.PlayerService{}, &GatewayService{}) }

func main() { app.Start() }
