package main

import (
	"context"
	"time"

	"github.com/duanhf2012/origin/v3/application"
	"github.com/duanhf2012/origin/v3/examples/_support/tutorialrpc"
	"github.com/duanhf2012/origin/v3/service"
)

var app = application.New()

type CallerService struct {
	service.Service
	players tutorialrpc.PlayerRPCClient
}

func (target *CallerService) OnInit() error {
	target.players = tutorialrpc.BindPlayerRPC(target)
	return nil
}

func (target *CallerService) OnStart(context.Context) error {
	target.AfterFunc(200*time.Millisecond, func(ctx context.Context, _ service.TimerID) {
		player, ok := target.LookupService("PlayerService")
		if !ok || player.Retire(ctx) != nil {
			target.Logger().Error("retire target failed")
			return
		}
		value, err := target.players.IncludeRetired().AwaitGetPlayer(ctx, 7)
		if err != nil {
			target.Logger().Error("explicit retired call failed")
			return
		}
		target.Logger().Info("explicit retired call: " + value)
	})
	return nil
}

func init() { app.Setup(&tutorialrpc.PlayerService{}, &CallerService{}) }

func main() { app.Start() }
