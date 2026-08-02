package main

import (
	"context"
	"time"

	"github.com/duanhf2012/origin/v3/application"
	"github.com/duanhf2012/origin/v3/service"
)

var app = application.New()

type MaintenanceService struct{ service.Service }

func (target *MaintenanceService) OnStart(context.Context) error {
	target.AfterFunc(200*time.Millisecond, func(ctx context.Context, _ service.TimerID) {
		if err := target.Retire(ctx); err != nil {
			target.Logger().Error("retire failed")
			return
		}
		target.Logger().Info("service state after retire: " + target.State().String())
	})
	target.AfterFunc(500*time.Millisecond, func(ctx context.Context, _ service.TimerID) {
		if err := target.Resume(ctx); err != nil {
			target.Logger().Error("resume failed")
			return
		}
		target.Logger().Info("service state after resume: " + target.State().String())
	})
	return nil
}

func init() { app.Setup(&MaintenanceService{}) }

func main() { app.Start() }
