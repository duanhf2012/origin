// 本示例把一次 Diagnostics Summary 采样缓存后发布给多个监控消费者。
package main

import (
	"context"
	"fmt"

	"github.com/duanhf2012/origin/v3/application"
	"github.com/duanhf2012/origin/v3/diagnostics"
	"github.com/duanhf2012/origin/v3/service"
)

// app 同时是示例 Application 和低基数 Diagnostics Summary 数据源。
var app = application.New()

// summarySource 是监控适配器真正依赖的最小采样接口。
type summarySource interface {
	DiagnosticsSummary() diagnostics.Summary
}

// GaugeSink 是 Prometheus、OpenTelemetry 或其他监控 SDK 可实现的最小输出边界。
type GaugeSink interface {
	Gauge(name string, value float64)
}

// gaugeFunc 让示例用普通函数适配不同消费者，不引入额外依赖。
type gaugeFunc func(string, float64)

// Gauge 调用已绑定的输出函数。
func (publish gaugeFunc) Gauge(name string, value float64) { publish(name, value) }

// metricsBatch 持有一次低基数采样；多个消费者发布期间不会再次触发聚合。
type metricsBatch struct {
	summary diagnostics.Summary
}

// collectMetrics 只调用一次 DiagnosticsSummary，建立本轮采集的一致视图。
func collectMetrics(source summarySource) metricsBatch {
	return metricsBatch{summary: source.DiagnosticsSummary()}
}

// Publish 把缓存 Summary 映射为稳定、低基数 Gauge 名称。
func (batch metricsBatch) Publish(sink GaugeSink) {
	sink.Gauge("origin_runtime_goroutines", float64(batch.summary.Runtime.Goroutines))
	sink.Gauge("origin_go_memory_used_bytes", float64(batch.summary.Runtime.GoMemoryUsedBytes))
	sink.Gauge("origin_nodes", float64(len(batch.summary.Nodes)))

	// Node 已是固定基数列表；这里进一步汇总 Service 数和正在运行的任务数。
	var services int
	var runningTasks int
	for _, current := range batch.summary.Nodes {
		services += current.Services.Total
		runningTasks += current.Services.Execution.Running
	}
	sink.Gauge("origin_services", float64(services))
	sink.Gauge("origin_service_tasks_running", float64(runningTasks))
}

// MetricsService 在一次采样上演示控制台与日志两个消费者复用完全相同的数据。
type MetricsService struct{ service.Service }

// OnStart 完成一次无外部依赖的示范发布。
func (target *MetricsService) OnStart(context.Context) error {
	batch := collectMetrics(app)
	batch.Publish(gaugeFunc(func(name string, value float64) {
		fmt.Printf("metric %s=%.0f\n", name, value)
	}))
	batch.Publish(gaugeFunc(func(name string, value float64) {
		target.Logger().Info(fmt.Sprintf("cached metric %s=%.0f", name, value))
	}))
	return nil
}

// init 安装监控适配器示例 Service。
func init() { app.Setup(&MetricsService{}) }

// main 启动 Application。
func main() { app.Start() }
