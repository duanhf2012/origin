// 本示例把统一 Diagnostics 快照转换为业务自定义指标接口。
package main

import (
	"context"
	"fmt"

	"github.com/duanhf2012/origin/v3/application"
	"github.com/duanhf2012/origin/v3/diagnostics"
	"github.com/duanhf2012/origin/v3/service"
)

// app 同时实现 diagnostics.Source，可直接被适配器读取。
var app = application.New()

// GaugeSink 是业务接入 Prometheus、OpenTelemetry 或其他系统时可替换的最小适配面。
type GaugeSink interface {
	Gauge(name string, value float64)
}

// consoleSink 是便于直接运行的控制台实现，生产可替换为监控 SDK。
type consoleSink struct{}

// Gauge 输出一个低基数数值指标。
func (consoleSink) Gauge(name string, value float64) { fmt.Printf("metric %s=%0.f\n", name, value) }

// publishSnapshot 只依赖 diagnostics.Source 和 GaugeSink 两个稳定小接口。
func publishSnapshot(source diagnostics.Source, sink GaugeSink) {
	// 每次采集重新获取快照，旧 Snapshot 不会自动更新。
	snapshot := source.Diagnostics()
	sink.Gauge("origin_runtime_goroutines", float64(snapshot.Runtime.Goroutines))
	sink.Gauge("origin_nodes", float64(len(snapshot.Nodes)))
}

// MetricsService 在启动后执行一次示范采集。
type MetricsService struct{ service.Service }

// OnStart 展示业务监控适配器不需要访问 Node/RPC 内部对象。
func (target *MetricsService) OnStart(context.Context) error {
	publishSnapshot(app, consoleSink{})
	target.Logger().Info("snapshot adapted by business code")
	return nil
}

// init 登记监控示例 Service。
func init() { app.Setup(&MetricsService{}) }

// main 启动 Application。
func main() { app.Start() }
