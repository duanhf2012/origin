package main

import (
	"context"

	"github.com/duanhf2012/origin/v3/application"
	"github.com/duanhf2012/origin/v3/service"
)

var app = application.New()

type InventoryService struct{ service.Service }

func (target *InventoryService) OnStart(context.Context) error {
	target.Logger().Info("inventory service is ready")
	return nil
}

func init() { app.Setup(&InventoryService{}) }

func main() { app.Start() }
