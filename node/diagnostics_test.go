package node

import (
	"context"
	"sync"
	"testing"

	"github.com/duanhf2012/origin/v3/errs"
	internaldiscovery "github.com/duanhf2012/origin/v3/internal/discovery"
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

// TestDiagnosticsConcurrentRetireResumeAndStop 验证冷路径快照不会与 Service 状态切换、
// Discovery 发布或最终停止争用可变对象；Race 模式下可直接检查所有权边界。
func TestDiagnosticsConcurrentRetireResumeAndStop(t *testing.T) {
	source := internaldiscovery.NewSource()
	var changes []string
	first := &retirementService{label: "First", changes: &changes}
	second := &retirementService{label: "Second", changes: &changes}
	current := newRetirementNode(t, source, first, second)

	stopReading := make(chan struct{})
	readerDone := make(chan struct{})
	var readers sync.WaitGroup
	for range 4 {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-stopReading:
					return
				default:
					snapshot := current.Diagnostics()
					if snapshot.NodeID != "retirement-node" || len(snapshot.Services) != 2 {
						t.Errorf("concurrent Diagnostics() = %+v", snapshot)
						return
					}
				}
			}
		}()
	}
	go func() {
		readers.Wait()
		close(readerDone)
	}()

	for range 10 {
		if err := current.Retire(context.Background()); err != nil {
			t.Fatalf("Retire() error = %v", err)
		}
		if err := current.Resume(context.Background()); err != nil {
			t.Fatalf("Resume() error = %v", err)
		}
	}
	if err := current.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	close(stopReading)
	<-readerDone
	if snapshot := current.Diagnostics(); snapshot.State != "stopped" {
		t.Fatalf("final Diagnostics() = %+v", snapshot)
	}
}
