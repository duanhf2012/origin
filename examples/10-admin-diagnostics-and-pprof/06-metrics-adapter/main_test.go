package main

import (
	"reflect"
	"testing"

	"github.com/duanhf2012/origin/v3/diagnostics"
)

// countingSummarySource 记录 DiagnosticsSummary 的真实调用次数。
type countingSummarySource struct {
	calls   int
	summary diagnostics.Summary
}

// DiagnosticsSummary 返回固定样本，并累加采集次数。
func (source *countingSummarySource) DiagnosticsSummary() diagnostics.Summary {
	source.calls++
	return source.summary
}

// recordingSink 保存一次发布的全部 Gauge。
type recordingSink map[string]float64

// Gauge 实现最小指标接收接口。
func (sink recordingSink) Gauge(name string, value float64) { sink[name] = value }

// TestMetricsBatchSamplesOnceForMultipleConsumers 验证多个消费者共享同一次 Summary 采样。
func TestMetricsBatchSamplesOnceForMultipleConsumers(t *testing.T) {
	source := &countingSummarySource{summary: diagnostics.Summary{
		Runtime: diagnostics.RuntimeSummary{
			Goroutines:        17,
			GoMemoryUsedBytes: 4096,
		},
		Nodes: []diagnostics.NodeSummary{{
			Services: diagnostics.ServiceAggregate{
				Total: 3,
				Execution: diagnostics.ExecutionAggregate{
					Running: 1,
				},
			},
		}},
	}}
	batch := collectMetrics(source)
	first := recordingSink{}
	second := recordingSink{}
	batch.Publish(first)
	batch.Publish(second)

	if source.calls != 1 {
		t.Fatalf("DiagnosticsSummary calls = %d, want 1", source.calls)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("consumer metrics differ: first=%v second=%v", first, second)
	}
	if first["origin_nodes"] != 1 || first["origin_services"] != 3 ||
		first["origin_service_tasks_running"] != 1 {
		t.Fatalf("published metrics = %v", first)
	}
}
