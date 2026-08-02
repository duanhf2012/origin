package main

import (
	"context"
	"fmt"

	"github.com/duanhf2012/origin/v3/application"
	"github.com/duanhf2012/origin/v3/service"
)

var app = application.New()

type DiagnosticsServerService struct{ service.Service }

func (target *DiagnosticsServerService) OnStart(context.Context) error {
	if err := app.StartDiagnosticsServer("127.0.0.1:6061"); err != nil {
		return err
	}
	address, _ := app.DiagnosticsAddress()
	target.Logger().Info(fmt.Sprintf("diagnostics JSON: http://%s/debug/origin/diagnostics", address))
	return nil
}

func init() { app.Setup(&DiagnosticsServerService{}) }

func main() { app.Start() }
