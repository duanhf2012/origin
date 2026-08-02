package main

import (
	"context"
	"fmt"
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
	// 默认绑定名来自 PlayerRPC 的约定，对应 PlayerService。
	target.players = tutorialrpc.BindPlayerRPC(target)
	return nil
}

func (target *CallerService) OnStart(context.Context) error {
	target.AfterFunc(100*time.Millisecond, func(ctx context.Context, _ service.TimerID) {
		message, err := target.players.AwaitGetPlayer(ctx, 1001)
		if err != nil {
			target.Logger().Error("rpc failed")
			return
		}
		target.Logger().Info(fmt.Sprintf("rpc result: %s", message))
	})
	return nil
}

func init() { app.Setup(&tutorialrpc.PlayerService{}, &CallerService{}) }

func main() { app.Start() }
