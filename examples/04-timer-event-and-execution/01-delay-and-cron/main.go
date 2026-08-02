package main

import (
	"context"
	"fmt"
	"time"

	"github.com/duanhf2012/origin/v3/application"
	"github.com/duanhf2012/origin/v3/service"
)

var app = application.New()

type TimerService struct{ service.Service }

func (target *TimerService) OnStart(context.Context) error {
	target.AfterFunc(300*time.Millisecond, func(context.Context, service.TimerID) {
		target.Logger().Info("after timer fired once")
	})
	if _, err := target.CronFunc("*/1 * * * * *", func(context.Context, service.TimerID) {
		target.Logger().Info(fmt.Sprintf("cron fired at %s", time.Now().Format(time.RFC3339)))
	}); err != nil {
		return err
	}
	return nil
}

func init() { app.Setup(&TimerService{}) }

func main() { app.Start() }
