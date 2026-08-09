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
//
// 适配器只依赖 DiagnosticsSummary，而不依赖 Application 的完整生命周期 API，便于在
// Prometheus/OpenTelemetry 等不同 SDK 中复用；每次调用都会产生一轮新的 Summary 采样。
type summarySource interface {
	DiagnosticsSummary() diagnostics.Summary
}

// GaugeSink 是 Prometheus、OpenTelemetry 或其他监控 SDK 可实现的最小输出边界。
//
// 示例只演示输出边界，不引入第三方 SDK。真实适配器可以在这里把 name/value 转成 Gauge、
// ObservableGauge 或日志字段，但应保持指标名称和标签基数稳定。
type GaugeSink interface {
	Gauge(name string, value float64)
}

// gaugeFunc 让示例用普通函数适配不同消费者，不引入额外依赖。
type gaugeFunc func(string, float64)

// Gauge 调用已绑定的输出函数。
func (publish gaugeFunc) Gauge(name string, value float64) { publish(name, value) }

// metricsBatch 持有一次低基数采样；多个消费者发布期间不会再次触发聚合。
//
// 它不是长期缓存，也不是实时状态对象；下一轮监控周期应重新调用 collectMetrics。
type metricsBatch struct {
	summary diagnostics.Summary
}

// collectMetrics 只调用一次 DiagnosticsSummary，建立本轮采集的一致视图。
//
// 如果同时有控制台、日志和外部指标三个消费者，应该让它们共享这个 batch，避免每个
// 消费者各自遍历 Node/Service 并得到不同采样时刻的数据。
func collectMetrics(source summarySource) metricsBatch {
	return metricsBatch{summary: source.DiagnosticsSummary()}
}

// Publish 把缓存 Summary 映射为稳定、低基数 Gauge 名称。
//
// 这里使用总量 Gauge，不把 NodeID、ServiceName 等高基数业务名称拼进指标名；如果需要
// 保留 Node 维度，应先确认标签数量上限和生命周期，避免监控系统标签无限增长。
func (batch metricsBatch) Publish(sink GaugeSink) {
	sink.Gauge("origin_runtime_goroutines", float64(batch.summary.Runtime.Goroutines))
	sink.Gauge("origin_go_memory_used_bytes", float64(batch.summary.Runtime.GoMemoryUsedBytes))
	sink.Gauge("origin_nodes", float64(len(batch.summary.Nodes)))

	// Node 已是固定基数列表；这里进一步汇总 Service 数和正在运行的任务数。
	// Execution.Running 是当前占用的任务数量（Gauge），不是累计完成次数；累计结果应使用
	// Summary 中对应的 CompletedTotal/RejectedTotal 等 Counter 语义字段。
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
//
// 真实程序通常会在自己的定时采集循环中执行同样的 collect -> Publish 流程；不要在每个
// sink 内部再次调用 DiagnosticsSummary。
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
