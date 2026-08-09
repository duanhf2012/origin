package diagnostics_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/duanhf2012/origin/v3/diagnostics"
)

// TestSummaryV2JSONContract keeps the default monitoring document deliberately
// low-cardinality: listener topology, per-transport RPC trees and service
// lifecycle noise belong exclusively to the detailed Full snapshot.
func TestSummaryV2JSONContract(t *testing.T) {
	collectedAt := time.Date(2026, 8, 9, 1, 2, 3, 0, time.UTC)
	summary := diagnostics.Summary{
		SchemaVersion: 2,
		CollectedAt:   collectedAt,
		StartedAt:     collectedAt.Add(-time.Minute),
		CollectCost:   diagnostics.Duration(2 * time.Millisecond),
		Application: diagnostics.ApplicationSummary{
			Name:  "game",
			State: "running",
			AdminServer: diagnostics.ListenerSummary{
				State: "serving",
			},
			Pprof: diagnostics.ListenerSummary{State: "stopped"},
		},
		Runtime: diagnostics.RuntimeSummary{
			Goroutines:            10,
			RunnableGoroutines:    2,
			GOMAXPROCS:            8,
			GoMemoryUsedBytes:     100,
			MemoryLimitConfigured: true,
			MemoryLimitBytes:      200,
			HeapGoalBytes:         75,
			HeapAllocBytes:        50,
			TotalAllocBytes:       300,
			GCCycles:              3,
			GCPauseTotal:          diagnostics.Duration(time.Millisecond),
			GCCPUSecondsTotal:     0.25,
			MutexWaitSecondsTotal: 0.5,
		},
		Nodes: []diagnostics.NodeSummary{{
			NodeID: "game-1",
			State:  "ready",
			RPC: diagnostics.RPCSummary{
				Pending: 3, PendingHighWater: 7,
				OutboundCompleted: 11, OutboundFailed: 12, OutboundTimeout: 13, OutboundRejected: 14,
				InboundCompleted: 21, InboundFailed: 22, InboundTimeout: 23, InboundRejected: 24,
				PayloadSentBytes: 31, PayloadReceivedBytes: 32,
			},
			Services: diagnostics.ServiceAggregate{
				Total:  1,
				States: diagnostics.ServiceStateAggregate{Running: 1},
				Execution: diagnostics.ExecutionAggregate{
					Accepted: 1, Ready: 2, Running: 3, Awaiting: 4,
					DispatchedTotal: 5, CompletedTotal: 6, RejectedTotal: 7, AwaitTimeoutTotal: 8, PanicTotal: 9,
				},
				Timer: diagnostics.TimerAggregate{
					Active: 10, DuePending: 11, Ready: 12, Running: 13,
					TriggeredTotal: 14, CompletedTotal: 15, RejectedTotal: 16, PanicTotal: 17,
					MaxReadyDelay: diagnostics.Duration(2 * time.Millisecond),
				},
				Event: diagnostics.EventAggregate{
					SyncNotifiedTotal: 18, AsyncNotifiedTotal: 19, HandlerFailureTotal: 20,
				},
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
	if document["schema_version"] != float64(2) {
		t.Fatalf("schema_version = %v, want 2", document["schema_version"])
	}

	applicationDocument := document["application"].(map[string]any)
	for _, listenerName := range []string{"admin_server", "pprof"} {
		listener := applicationDocument[listenerName].(map[string]any)
		if _, exists := listener["address"]; exists {
			t.Fatalf("Summary listener %q leaks address: %s", listenerName, encoded)
		}
		if _, exists := listener["state"]; !exists {
			t.Fatalf("Summary listener %q missing state: %s", listenerName, encoded)
		}
		if _, exists := listener["error_code"]; !exists {
			t.Fatalf("Summary listener %q missing error_code: %s", listenerName, encoded)
		}
	}

	runtimeDocument := document["runtime"].(map[string]any)
	for _, key := range []string{
		"goroutines", "runnable_goroutines", "gomaxprocs", "go_memory_used_bytes",
		"memory_limit_configured", "memory_limit_bytes", "heap_goal_bytes", "heap_alloc_bytes",
		"total_alloc_bytes", "gc_cycles", "gc_pause_total", "gc_cpu_seconds_total", "mutex_wait_seconds_total",
	} {
		if _, exists := runtimeDocument[key]; !exists {
			t.Fatalf("RuntimeSummary missing %q: %s", key, encoded)
		}
	}
	if _, exists := runtimeDocument["heap_objects"]; exists {
		t.Fatalf("Summary unexpectedly contains heap_objects: %s", encoded)
	}

	nodeDocument := document["nodes"].([]any)[0].(map[string]any)
	rpcDocument := nodeDocument["rpc"].(map[string]any)
	for _, forbidden := range []string{"local", "tcp", "nats", "outbound_accepted", "inbound_accepted"} {
		if _, exists := rpcDocument[forbidden]; exists {
			t.Fatalf("Summary RPC unexpectedly contains %q: %s", forbidden, encoded)
		}
	}
	for _, required := range []string{"pending", "pending_high_water", "outbound_completed", "inbound_completed"} {
		if _, exists := rpcDocument[required]; !exists {
			t.Fatalf("Summary RPC missing %q: %s", required, encoded)
		}
	}

	serviceDocument := nodeDocument["services"].(map[string]any)
	states := serviceDocument["states"].(map[string]any)
	for _, forbidden := range []string{"created", "initializing", "initialized", "starting", "stopping", "stopped"} {
		if _, exists := states[forbidden]; exists {
			t.Fatalf("Summary service states unexpectedly contain %q: %s", forbidden, encoded)
		}
	}
	for _, required := range []string{"running", "retired", "failed", "unknown"} {
		if _, exists := states[required]; !exists {
			t.Fatalf("Summary service states missing %q: %s", required, encoded)
		}
	}
}

// TestSummaryZeroJSONContract fixes the top-level zero-value wire shape.
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
	if got := string(document["nodes"]); got != `[]` {
		t.Fatalf("nodes = %s, want []", got)
	}
}

// TestFullSnapshotRemainsDetailedV2 locks the compatibility boundary: Summary
// may evolve independently, but Full keeps its old detailed values.
func TestFullSnapshotRemainsDetailedV2(t *testing.T) {
	full := diagnostics.Snapshot{
		SchemaVersion: 2,
		Application: diagnostics.ApplicationSnapshot{
			AdminServer: diagnostics.ServerSnapshot{State: "serving", Address: "127.0.0.1:6060"},
			Pprof:       diagnostics.ServerSnapshot{State: "serving", Address: "127.0.0.1:6061"},
		},
		Runtime: diagnostics.RuntimeSnapshot{HeapObjects: 9},
	}
	encoded, err := json.Marshal(full)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(encoded, &document); err != nil {
		t.Fatal(err)
	}
	if document["schema_version"] != float64(2) ||
		document["application"].(map[string]any)["admin_server"].(map[string]any)["address"] != "127.0.0.1:6060" ||
		document["runtime"].(map[string]any)["heap_objects"] != float64(9) {
		t.Fatalf("Full Snapshot v2 changed: %s", encoded)
	}
}
