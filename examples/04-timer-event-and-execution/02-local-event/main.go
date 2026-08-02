package main

import (
	"context"
	"fmt"
	"time"

	"github.com/duanhf2012/origin/v3/application"
	"github.com/duanhf2012/origin/v3/service"
)

const playerJoinedEvent service.EventID = 1

type PlayerJoined struct{ PlayerID int64 }

func (PlayerJoined) EventID() service.EventID { return playerJoinedEvent }

var app = application.New()

type EventService struct{ service.Service }

func (target *EventService) OnInit() error {
	return target.SubscribeEvent(playerJoinedEvent, func(_ context.Context, event service.Event) error {
		joined := event.(PlayerJoined)
		target.Logger().Info(fmt.Sprintf("player %d joined", joined.PlayerID))
		return nil
	})
}

func (target *EventService) OnStart(context.Context) error {
	target.AfterFunc(100*time.Millisecond, func(ctx context.Context, _ service.TimerID) {
		if err := target.NotifyEventSync(ctx, PlayerJoined{PlayerID: 1001}); err != nil {
			target.Logger().Error("notify event failed")
		}
	})
	return nil
}

func init() { app.Setup(&EventService{}) }

func main() { app.Start() }
