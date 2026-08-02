package main

import (
	"context"

	"github.com/duanhf2012/origin/v3/application"
	"github.com/duanhf2012/origin/v3/service"
)

var app = application.New()

type FirstService struct{ service.Service }
type SecondService struct{ service.Service }

func (target *FirstService) OnStart(context.Context) error {
	target.Logger().Info("first starts before second")
	return nil
}

func (target *FirstService) OnStop(context.Context) error {
	target.Logger().Info("first stops last")
	return nil
}

func (target *SecondService) OnStart(context.Context) error {
	target.Logger().Info("second starts after first")
	return nil
}

func (target *SecondService) OnStop(context.Context) error {
	target.Logger().Info("second stops first")
	return nil
}

func init() { app.Setup(&FirstService{}, &SecondService{}) }

func main() { app.Start() }
