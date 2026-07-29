package provider

import (
	"errors"
	"testing"
	"time"

	"github.com/duanhf2012/origin/v3/errs"
)

func TestConfigDecodeIsStrictAndIsolated(t *testing.T) {
	config, err := NewConfig(map[string]any{
		"ttl":    "15s",
		"nested": map[string]any{"enabled": true},
	})
	if err != nil {
		t.Fatalf("NewConfig() error = %v", err)
	}
	var decoded struct {
		TTL    string `json:"ttl"`
		Nested struct {
			Enabled bool `json:"enabled"`
		} `json:"nested"`
	}
	if err := config.Decode(&decoded); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if decoded.TTL != "15s" || !decoded.Nested.Enabled {
		t.Fatalf("decoded = %+v", decoded)
	}
	var incomplete struct {
		TTL string `json:"ttl"`
	}
	if err := config.Decode(&incomplete); !errs.IsCode(err, errs.CodeInvalidConfig) {
		t.Fatalf("未知字段 Decode() error = %v", err)
	}
}

func TestNormalizeSnapshotCopiesSortsAndRejectsDuplicates(t *testing.T) {
	first := validNode("game-2", 2)
	second := validNode("game-1", 1)
	second.Labels = map[string]string{"region": "cn-east"}
	second.Services = append(second.Services, Service{
		ServiceName: "ChatService",
		State:       ServiceStateRunning,
	})
	normalized, err := NormalizeSnapshot(Snapshot{
		Nodes: []Node{first, second},
	})
	if err != nil {
		t.Fatalf("NormalizeSnapshot() error = %v", err)
	}
	if normalized.Nodes[0].NodeID != "game-1" ||
		normalized.Nodes[0].Services[0].ServiceName != "ChatService" {
		t.Fatalf("规范排序错误: %+v", normalized)
	}
	second.Labels["region"] = "changed"
	second.Services[0].ServiceName = "Changed"
	if normalized.Nodes[0].Labels["region"] != "cn-east" ||
		normalized.Nodes[0].Services[0].ServiceName != "ChatService" {
		t.Fatal("规范快照仍引用调用方容器")
	}

	_, err = NormalizeSnapshot(Snapshot{
		Nodes: []Node{validNode("game-1", 1), validNode("game-1", 2)},
	})
	if !errs.IsCode(err, errs.CodeDiscoverySnapshotInvalid) {
		t.Fatalf("重复 NodeID error = %v", err)
	}
}

func TestNormalizeNodeRejectsInvalidTCPAddressWithoutPanicking(t *testing.T) {
	node := validNode("game-1", 1)
	node.Transport = TransportTCP
	for _, address := range []string{
		"localhost:not-a-port",
		"localhost:0",
		"localhost:65536",
		"a" + string(make([]byte, 255)) + ":7100",
	} {
		node.Address = address
		if _, err := NormalizeNode(node); !errs.IsCode(
			err,
			errs.CodeDiscoverySnapshotInvalid,
		) {
			t.Fatalf("NormalizeNode(%q) error = %v", address, err)
		}
	}
}

func TestHostDelegatesTTLStateAndSnapshot(t *testing.T) {
	var gotTTL time.Duration
	var gotSnapshot Snapshot
	var gotReport Report
	host := NewHost(
		func(ttl time.Duration) error {
			gotTTL = ttl
			return nil
		},
		func(snapshot Snapshot) error {
			gotSnapshot = snapshot
			return nil
		},
		func(report Report) {
			gotReport = report
		},
	)
	if err := host.SetTTL(15 * time.Second); err != nil {
		t.Fatalf("SetTTL() error = %v", err)
	}
	expected := Snapshot{Nodes: []Node{validNode("game-1", 1)}}
	if err := host.ReplaceSnapshot(expected); err != nil {
		t.Fatalf("ReplaceSnapshot() error = %v", err)
	}
	host.Report(Report{State: StateReady, Reconnects: 2})
	if gotTTL != 15*time.Second || len(gotSnapshot.Nodes) != 1 ||
		gotReport.State != StateReady || gotReport.Reconnects != 2 {
		t.Fatalf(
			"Host delegation = (%v, %+v, %+v)",
			gotTTL,
			gotSnapshot,
			gotReport,
		)
	}
	if err := (Host{}).SetTTL(time.Second); !errors.Is(err, errs.ErrInternal) {
		t.Fatalf("零值 Host SetTTL() error = %v", err)
	}
}

func validNode(nodeID string, sessionID uint64) Node {
	return Node{
		NodeID:    nodeID,
		SessionID: sessionID,
		Transport: TransportNATS,
		Services: []Service{{
			ServiceName: "PlayerService",
			State:       ServiceStateRunning,
		}},
	}
}
