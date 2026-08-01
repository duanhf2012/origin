package node

import (
	"context"
	"testing"

	"github.com/duanhf2012/origin/v3/errs"
)

// TestDiagnosticsPreservesNodeAndServiceOrder 防止聚合时使用 Map 遍历打乱声明顺序，
// 同时验证现有健康、生命周期和叶子统计被复制为独立 DTO。
func TestDiagnosticsPreservesNodeAndServiceOrder(t *testing.T) {
	events := make([]string, 0, 8)
	first := &lifecycleService{label: "PlayerService", events: &events}
	second := &lifecycleService{label: "SceneService", events: &events}
	current := newTestNode(t, first, second)

	created := current.Diagnostics()
	if created.NodeID != "game-1" || created.State != "created" {
		t.Fatalf("created diagnostics = %+v", created)
	}
	if len(created.Services) != 2 ||
		created.Services[0].ServiceName != "PlayerService" ||
		created.Services[1].ServiceName != "SceneService" {
		t.Fatalf("created services = %+v", created.Services)
	}

	if err := current.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	running := current.Diagnostics()
	if running.State != "ready" || !running.Health.Liveness ||
		!running.Health.Readiness || running.Health.ErrorCode != errs.CodeOK {
		t.Fatalf("running diagnostics = %+v", running)
	}
	for _, snapshot := range running.Services {
		if snapshot.State != "running" || snapshot.EnteredAt.IsZero() ||
			snapshot.ErrorCode != errs.CodeOK {
			t.Fatalf("service diagnostics = %+v", snapshot)
		}
	}
	if running.RPC != mapRPCStats(current.rpcRuntime.Stats()) {
		t.Fatalf("RPC diagnostics = %+v", running.RPC)
	}

	if err := current.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	stopped := current.Diagnostics()
	if stopped.State != "stopped" || stopped.Services[0].State != "stopped" {
		t.Fatalf("stopped diagnostics = %+v", stopped)
	}
}

// TestNilNodeDiagnostics 返回可解释失败 DTO，而不是在监控冷路径 panic。
func TestNilNodeDiagnostics(t *testing.T) {
	var current *Node
	snapshot := current.Diagnostics()
	if snapshot.State != "failed" || snapshot.Health.ErrorCode != errs.CodeInternal {
		t.Fatalf("nil diagnostics = %+v", snapshot)
	}
}
