package main

import (
	"context"

	"github.com/duanhf2012/origin/v3/application"
	originlog "github.com/duanhf2012/origin/v3/log"
	"github.com/duanhf2012/origin/v3/service"
)

var app = application.New()

type GatewayService struct{ service.Service }

func (target *GatewayService) OnStart(context.Context) error {
	target.Logger().Info("gateway is ready")
	return nil
}

type PlayerService struct{ service.Service }

func (target *PlayerService) OnInit() error {
	target.Logger().Info("player initialized", originlog.String("node_id", target.NodeID()))
	return nil
}

func (target *PlayerService) OnStart(context.Context) error {
	target.Logger().Info("player is ready")
	return nil
}

func (target *PlayerService) OnStop(context.Context) error {
	target.Logger().Info("player stopped")
	return nil
}

func init() { app.Setup(&GatewayService{}, &PlayerService{}) }

func main() { app.Start() }
