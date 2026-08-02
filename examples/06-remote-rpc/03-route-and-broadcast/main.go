package main

import (
	"context"
	"time"

	"github.com/duanhf2012/origin/v3/application"
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
	target.AfterFunc(time.Second, func(ctx context.Context, _ service.TimerID) {
		value, err := target.players.Route(int64(1001)).AwaitGetPlayer(ctx, 1001)
		if err == nil {
			target.Logger().Info("key route result: " + value)
		}
		if err := target.players.BroadcastRefresh(ctx, 3); err != nil {
			target.Logger().Error("broadcast has failures")
		}
	})
	return nil
}

func init() { app.Setup(&tutorialrpc.PlayerService{}, &GatewayService{}) }

func main() { app.Start() }
