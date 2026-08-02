package main

import (
	"context"
	"time"

	"github.com/duanhf2012/origin/v3/application"
	"github.com/duanhf2012/origin/v3/service"
)

var app = application.New()

type ExecutionService struct{ service.Service }

func (target *ExecutionService) OnStart(context.Context) error {
	target.AfterFunc(100*time.Millisecond, func(ctx context.Context, _ service.TimerID) {
		if err := target.Await(ctx, func(waitCtx context.Context) error {
			select {
			case <-time.After(50 * time.Millisecond):
				target.Logger().Info("awaited operation completed")
				return nil
			case <-waitCtx.Done():
				return waitCtx.Err()
			}
		}); err != nil {
			target.Logger().Error("await failed")
		}
		_ = target.RunSafe(func() { target.Logger().Info("safe synchronous job completed") })
		_ = target.GoSafe(func() { target.Logger().Info("safe background job completed") })
	})
	return nil
}

func init() { app.Setup(&ExecutionService{}) }

func main() { app.Start() }
