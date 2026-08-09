package diagnostics_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/duanhf2012/origin/v3/diagnostics"
)

// TestSummaryZeroJSONContract 固定监控默认文档的顶层字段、零值 Duration 和非 nil Nodes。
func TestSummaryZeroJSONContract(t *testing.T) {
	encoded, err := json.Marshal(diagnostics.Summary{})
	if err != nil {
		t.Fatalf("json.Marshal(Summary{}) error = %v", err)
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &document); err != nil {
		t.Fatalf("json.Unmarshal(Summary{}) error = %v", err)
	}
	wantKeys := []string{
		"schema_version", "collected_at", "started_at", "collect_cost",
		"application", "runtime", "buffer_pool", "nodes",
	}
	if len(document) != len(wantKeys) {
		t.Fatalf("Summary JSON fields = %v, want %v", document, wantKeys)
	}
	for _, key := range wantKeys {
		if _, exists := document[key]; !exists {
			t.Fatalf("Summary JSON missing %q: %s", key, encoded)
		}
	}
	if got := string(document["collect_cost"]); got != `"0s"` {
		t.Fatalf("collect_cost = %s, want %q", got, "0s")
	}
	if got := string(document["nodes"]); got != `[]` {
		t.Fatalf("nodes = %s, want []", got)
	}
}

// TestSummaryJSONFieldNames 固定所有低基数 DTO 的 JSON 名称和 Duration 单位；监控适配器
// 可以据此直接解码而不依赖 Go 字段名。
func TestSummaryJSONFieldNames(t *testing.T) {
	collectedAt := time.Date(2026, 8, 9, 1, 2, 3, 0, time.UTC)
	summary := diagnostics.Summary{
		SchemaVersion: 1,
		CollectedAt:   collectedAt,
		StartedAt:     collectedAt.Add(-time.Minute),
		CollectCost:   diagnostics.Duration(2 * time.Millisecond),
		Application: diagnostics.ApplicationSummary{
			Name: "game", State: "running",
			AdminServer: diagnostics.ServerSnapshot{State: "serving", Address: "127.0.0.1:6060"},
			Pprof:       diagnostics.ServerSnapshot{State: "stopped"},
		},
		Runtime: diagnostics.RuntimeSummary{
			Goroutines:            10,
			RunnableGoroutines:    2,
			GOMAXPROCS:            8,
			GoMemoryUsedBytes:     100,
			MemoryLimitBytes:      200,
			HeapAllocBytes:        50,
			HeapObjects:           4,
			TotalAllocBytes:       300,
			GCCycles:              3,
			GCPauseTotal:          diagnostics.Duration(time.Millisecond),
			GCCPUSecondsTotal:     0.25,
			MutexWaitSecondsTotal: 0.5,
		},
		Nodes: []diagnostics.NodeSummary{{
			NodeID: "game-1",
			State:  "ready",
			Services: diagnostics.ServiceAggregate{
				Total:  1,
				States: diagnostics.ServiceStateAggregate{Running: 1},
				Execution: diagnostics.ExecutionAggregate{
					Accepted: 1, Ready: 2, Running: 1, Awaiting: 3,
					RejectedTotal: 4, PanicTotal: 5,
				},
				Timer: diagnostics.TimerAggregate{
					Active: 6, DuePending: 7, Ready: 8, Running: 9,
					RejectedTotal: 10, PanicTotal: 11,
				},
				Event: diagnostics.EventAggregate{HandlerFailureTotal: 12},
			},
		}},
	}
	encoded, err := json.Marshal(summary)
	if err != nil {
		t.Fatalf("json.Marshal(Summary) error = %v", err)
	}
	var document map[string]any
	if err := json.Unmarshal(encoded, &document); err != nil {
		t.Fatalf("json.Unmarshal(Summary) error = %v", err)
	}
	runtimeDocument := document["runtime"].(map[string]any)
	for _, key := range []string{
		"goroutines", "runnable_goroutines", "gomaxprocs", "go_memory_used_bytes",
		"memory_limit_bytes", "heap_alloc_bytes", "heap_objects", "total_alloc_bytes",
		"gc_cycles", "gc_pause_total", "gc_cpu_seconds_total", "mutex_wait_seconds_total",
	} {
		if _, exists := runtimeDocument[key]; !exists {
			t.Fatalf("RuntimeSummary missing %q: %s", key, encoded)
		}
	}
	applicationDocument := document["application"].(map[string]any)
	if _, exists := applicationDocument["admin_server"]; !exists {
		t.Fatalf("ApplicationSummary missing admin_server: %s", encoded)
	}
	nodeDocument := document["nodes"].([]any)[0].(map[string]any)
	serviceDocument, ok := nodeDocument["services"].(map[string]any)
	if !ok {
		t.Fatalf("NodeSummary services = %#v, want one aggregate object", nodeDocument["services"])
	}
	if _, exists := serviceDocument["states"]; !exists {
		t.Fatalf("ServiceAggregate missing states: %s", encoded)
	}
	localRPC := nodeDocument["rpc"].(map[string]any)["local"].(map[string]any)
	if _, exists := localRPC["reconnects"]; exists {
		t.Fatalf("RPCSummary unexpectedly repeats reconnects: %s", encoded)
	}
	if _, exists := localRPC["consecutive_failures"]; exists {
		t.Fatalf("RPCSummary unexpectedly repeats consecutive_failures: %s", encoded)
	}
}
