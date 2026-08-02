package main

import (
	"context"
	"fmt"

	"github.com/duanhf2012/origin/v3/application"
	"github.com/duanhf2012/origin/v3/diagnostics"
	"github.com/duanhf2012/origin/v3/service"
)

var app = application.New()

// GaugeSink 是业务接入 Prometheus、OpenTelemetry 或其他系统时可替换的最小适配面。
type GaugeSink interface {
	Gauge(name string, value float64)
}

type consoleSink struct{}

func (consoleSink) Gauge(name string, value float64) { fmt.Printf("metric %s=%0.f\n", name, value) }

func publishSnapshot(source diagnostics.Source, sink GaugeSink) {
	snapshot := source.Diagnostics()
	sink.Gauge("origin_runtime_goroutines", float64(snapshot.Runtime.Goroutines))
	sink.Gauge("origin_nodes", float64(len(snapshot.Nodes)))
}

type MetricsService struct{ service.Service }

func (target *MetricsService) OnStart(context.Context) error {
	publishSnapshot(app, consoleSink{})
	target.Logger().Info("snapshot adapted by business code")
	return nil
}

func init() { app.Setup(&MetricsService{}) }

func main() { app.Start() }
