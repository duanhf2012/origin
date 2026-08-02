package main

import (
	"context"

	"github.com/duanhf2012/origin/v3/application"
	"github.com/duanhf2012/origin/v3/service"
)

var app = application.New()

type ConfigService struct{ service.Service }

func (target *ConfigService) OnStart(context.Context) error {
	target.Logger().Info("minimal YAML loaded")
	return nil
}

func init() { app.Setup(&ConfigService{}) }

func main() { app.Start() }
