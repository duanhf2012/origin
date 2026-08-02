package main

import (
	"context"
	"fmt"
	"time"

	"github.com/duanhf2012/origin/v3/application"
	"github.com/duanhf2012/origin/v3/service"
)

var app = application.New()

type PprofService struct{ service.Service }

func (target *PprofService) OnStart(context.Context) error {
	if err := app.StartPprof("127.0.0.1:6060"); err != nil {
		return err
	}
	address, _ := app.PprofAddress()
	target.Logger().Info(fmt.Sprintf("pprof started: http://%s/debug/pprof/", address))
	target.AfterFunc(2*time.Second, func(ctx context.Context, _ service.TimerID) {
		if err := app.StopPprof(ctx); err == nil {
			target.Logger().Info("pprof stopped by application code")
		}
	})
	return nil
}

func init() { app.Setup(&PprofService{}) }

func main() { app.Start() }
