package main

import (
	"context"
	"time"

	"github.com/duanhf2012/origin/v3/application"
	"github.com/duanhf2012/origin/v3/service"
)

var app = application.New()

type WorkerService struct{ service.Service }

type ControlService struct{ service.Service }

func (target *ControlService) OnStart(context.Context) error {
	target.AfterFunc(200*time.Millisecond, func(ctx context.Context, _ service.TimerID) {
		if err := app.Retire(ctx); err != nil {
			target.Logger().Error("application retire failed")
			return
		}
		target.Logger().Info("application retired in reverse Node order")
	})
	target.AfterFunc(500*time.Millisecond, func(ctx context.Context, _ service.TimerID) {
		if err := app.Resume(ctx); err != nil {
			target.Logger().Error("application resume failed")
			return
		}
		target.Logger().Info("application resumed in Node start order")
	})
	return nil
}

func init() { app.Setup(&WorkerService{}, &ControlService{}) }

func main() { app.Start() }
