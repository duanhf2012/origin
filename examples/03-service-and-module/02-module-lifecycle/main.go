package main

import (
	"context"
	"fmt"

	"github.com/duanhf2012/origin/v3/application"
	"github.com/duanhf2012/origin/v3/service"
)

var app = application.New()

type ChildModule struct{ service.Module }

func (target *ChildModule) OnStart(context.Context) error {
	fmt.Println("child module started")
	return nil
}

func (target *ChildModule) OnStop(context.Context) error {
	fmt.Println("child module stopped")
	return nil
}

type RootModule struct{ service.Module }

func (target *RootModule) OnInit() error {
	return target.AddModule(&ChildModule{})
}

func (target *RootModule) OnStart(context.Context) error {
	fmt.Println("root module started")
	return nil
}

func (target *RootModule) OnStop(context.Context) error {
	fmt.Println("root module stopped")
	return nil
}

type GameService struct{ service.Service }

func (target *GameService) OnInit() error { return target.AddModule(&RootModule{}) }

func init() { app.Setup(&GameService{}) }

func main() { app.Start() }
