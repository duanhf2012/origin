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
	target.AfterFunc(100*time.Millisecond, func(ctx context.Context, _ service.TimerID) {
		if err := target.players.AsyncGetPlayer(ctx, 1001, func(_ context.Context, value string, err error) {
			if err != nil {
				target.Logger().Error("async rpc failed")
				return
			}
			target.Logger().Info("async result: " + value)
		}); err != nil {
			target.Logger().Error("submit async rpc failed")
		}
		if err := target.players.NotifyRefresh(ctx, 7); err != nil {
			target.Logger().Error("notify failed")
		}
	})
	return nil
}

func init() { app.Setup(&tutorialrpc.PlayerService{}, &CallerService{}) }

func main() { app.Start() }
