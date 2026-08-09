package diagnostics_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/duanhf2012/origin/v3/diagnostics"
	"github.com/duanhf2012/origin/v3/errs"
)

// staticSource 只用于编译期证明业务监控适配器可以依赖最小 Source，而不需要 Application。
type staticSource struct {
	snapshot diagnostics.Snapshot
}

func (source staticSource) Diagnostics() diagnostics.Snapshot {
	return source.snapshot
}

var _ diagnostics.Source = staticSource{}

// TestSnapshotJSONSchema 检查 JSON 消费方依赖的字段名、时间格式和带单位 Duration。
// 若 Duration 退回纳秒整数、字段改名或错误码丢失，本测试必须失败。
func TestSnapshotJSONSchema(t *testing.T) {
	collectedAt := time.Date(2026, 8, 1, 1, 2, 3, 456789000, time.FixedZone("CST", 8*60*60))
	snapshot := diagnostics.Snapshot{
		SchemaVersion: 2,
		CollectedAt:   collectedAt,
		StartedAt:     collectedAt.Add(-time.Minute),
		CollectCost:   diagnostics.Duration(1500 * time.Microsecond),
		Application: diagnostics.ApplicationSnapshot{
			Name:  "player",
			State: "running",
			DiagnosticsServer: diagnostics.ServerSnapshot{
				State:     "serving",
				Address:   "127.0.0.1:6061",
				ErrorCode: errs.CodeOK,
			},
		},
		Runtime: diagnostics.RuntimeSnapshot{
			Goroutines:   12,
			GOMAXPROCS:   8,
			GCPauseTotal: diagnostics.Duration(2 * time.Millisecond),
		},
		Nodes: []diagnostics.NodeSnapshot{{
			NodeID: "player-1",
			State:  "ready",
			Services: []diagnostics.ServiceSnapshot{{
				ServiceName: "PlayerService",
				State:       "running",
				ErrorCode:   errs.CodeOK,
				Timer: diagnostics.TimerSnapshot{
					LastReadyDelay: diagnostics.Duration(3 * time.Millisecond),
				},
			}},
		}},
	}

	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatalf("json.Marshal(snapshot) failed: %v", err)
	}

	var document map[string]any
	if err := json.Unmarshal(encoded, &document); err != nil {
		t.Fatalf("json.Unmarshal(snapshot) failed: %v", err)
	}
	if got := document["schema_version"]; got != float64(2) {
		t.Fatalf("schema_version = %#v, want 2", got)
	}
	if got := document["collected_at"]; got != "2026-08-01T01:02:03.456789+08:00" {
		t.Fatalf("collected_at = %#v", got)
	}
	if got := document["collect_cost"]; got != "1.5ms" {
		t.Fatalf("collect_cost = %#v, want 1.5ms", got)
	}

	runtimeSnapshot := document["runtime"].(map[string]any)
	if got := runtimeSnapshot["gc_pause_total"]; got != "2ms" {
		t.Fatalf("gc_pause_total = %#v, want 2ms", got)
	}
	nodeSnapshot := document["nodes"].([]any)[0].(map[string]any)
	serviceSnapshot := nodeSnapshot["services"].([]any)[0].(map[string]any)
	timerSnapshot := serviceSnapshot["timer"].(map[string]any)
	if got := timerSnapshot["last_ready_delay"]; got != "3ms" {
		t.Fatalf("last_ready_delay = %#v, want 3ms", got)
	}
}

// TestDurationJSONZeroAndNegative 固定零值和理论负值的 JSON 语义；诊断 DTO 不伪造单位。
func TestDurationJSONZeroAndNegative(t *testing.T) {
	tests := []struct {
		name  string
		value diagnostics.Duration
		want  string
	}{
		{name: "zero", value: 0, want: `"0s"`},
		{name: "negative", value: diagnostics.Duration(-time.Millisecond), want: `"-1ms"`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			encoded, err := json.Marshal(test.value)
			if err != nil {
				t.Fatalf("json.Marshal(Duration) failed: %v", err)
			}
			if string(encoded) != test.want {
				t.Fatalf("json = %s, want %s", encoded, test.want)
			}
		})
	}
}

// TestDurationJSONRoundTrip 固定管理工具读取诊断快照时的强类型解码语义。
func TestDurationJSONRoundTrip(t *testing.T) {
	var duration diagnostics.Duration
	if err := json.Unmarshal([]byte(`"1.25s"`), &duration); err != nil {
		t.Fatalf("json.Unmarshal(Duration) failed: %v", err)
	}
	if got, want := duration.Value(), 1250*time.Millisecond; got != want {
		t.Fatalf("Duration.Value() = %v, want %v", got, want)
	}
	if got, want := duration.String(), "1.25s"; got != want {
		t.Fatalf("Duration.String() = %q, want %q", got, want)
	}
}

// TestDurationJSONRejectsInvalidInput 防止无单位整数、非法单位和空接收者被静默接受。
func TestDurationJSONRejectsInvalidInput(t *testing.T) {
	tests := []struct {
		name string
		data string
	}{
		{name: "number", data: `1250`},
		{name: "invalid unit", data: `"1fortnight"`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var duration diagnostics.Duration
			if err := json.Unmarshal([]byte(test.data), &duration); err == nil {
				t.Fatalf("json.Unmarshal(%s) error = nil", test.data)
			}
		})
	}

	var duration *diagnostics.Duration
	if err := duration.UnmarshalJSON([]byte(`"1s"`)); err == nil {
		t.Fatalf("nil Duration.UnmarshalJSON() error = nil")
	}
}

// TestFullRPCRecoveryFieldsRemainCompatible 固定 Full v2 的重复恢复字段仍存在；Summary 会使用
// 独立窄 DTO，而不能通过删除既有字段破坏 v3.0 消费方。
func TestFullRPCRecoveryFieldsRemainCompatible(t *testing.T) {
	encoded, err := json.Marshal(diagnostics.RPCTransportSnapshot{
		Reconnects:          7,
		ConsecutiveFailures: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(encoded, &document); err != nil {
		t.Fatal(err)
	}
	if document["reconnects"] != float64(7) || document["consecutive_failures"] != float64(3) {
		t.Fatalf("Full RPC recovery JSON = %s", encoded)
	}
}
