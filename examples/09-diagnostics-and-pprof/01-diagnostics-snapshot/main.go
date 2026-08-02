package main

import (
	"context"
	"fmt"

	"github.com/duanhf2012/origin/v3/application"
	"github.com/duanhf2012/origin/v3/service"
)

var app = application.New()

type DiagnosticsService struct{ service.Service }

func (target *DiagnosticsService) OnStart(context.Context) error {
	snapshot := app.Diagnostics()
	target.Logger().Info(fmt.Sprintf("diagnostics: application=%s nodes=%d goroutines=%d", snapshot.Application.State, len(snapshot.Nodes), snapshot.Runtime.Goroutines))
	return nil
}

func init() { app.Setup(&DiagnosticsService{}) }

func main() { app.Start() }
