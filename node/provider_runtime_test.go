package node

import (
	"context"
	"testing"
	"time"

	publicprovider "github.com/duanhf2012/origin/v3/discovery/provider"
	originlog "github.com/duanhf2012/origin/v3/log"
	"github.com/duanhf2012/origin/v3/service"
)

type providerRuntimeFixture struct {
	context publicprovider.Context
}

func (fixture *providerRuntimeFixture) Start(context.Context) error {
	if err := fixture.context.Host.SetTTL(3 * time.Second); err != nil {
		return err
	}
	if err := fixture.context.Host.ReplaceSnapshot(publicprovider.Snapshot{
		Nodes: []publicprovider.Node{{
			NodeID:    "remote-1",
			SessionID: 77,
			Transport: publicprovider.TransportNATS,
			Services: []publicprovider.Service{{
				ServiceName: "RemoteService",
				State:       publicprovider.ServiceStateRunning,
			}},
		}},
	}); err != nil {
		return err
	}
	fixture.context.Host.Report(publicprovider.Report{State: publicprovider.StateReady})
	// 同值重复设置即使发生在首次 Host 使用之后也必须保持幂等。
	return fixture.context.Host.SetTTL(3 * time.Second)
}

func (*providerRuntimeFixture) Publish(context.Context, publicprovider.Node) error {
	return nil
}

func (*providerRuntimeFixture) Withdraw(context.Context) error { return nil }
func (*providerRuntimeFixture) Close(context.Context) error    { return nil }

func TestProviderRuntimeReportsRecoveryImmediatelyAndExpiresSnapshotAtTTL(
	t *testing.T,
) {
	events := make([]string, 0, 2)
	local := &lifecycleService{label: "LocalService", events: &events}
	var fixture *providerRuntimeFixture
	current, err := New(
		Config{
			ID:        "local-1",
			Scheduler: service.DefaultSchedulerConfig(),
			Services:  []string{"LocalService"},
		},
		[]ServiceBinding{{
			Name:     "LocalService",
			Template: "lifecycleService",
			Service:  local,
		}},
		originlog.NewNop(),
		Options{
			MaxTimersPerNode: 3_000_000,
			TimerLocation:    time.Local,
			DiscoveryKind:    "fixture",
			DiscoveryFactory: func(
				context publicprovider.Context,
			) (publicprovider.Provider, error) {
				fixture = &providerRuntimeFixture{context: context}
				return fixture, nil
			},
		},
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := current.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() {
		if current.State() == StateReady {
			_ = current.Stop(context.Background())
		}
	})
	if _, exists := local.FindDiscoveredService("remote-1", "RemoteService"); !exists {
		t.Fatal("首次权威快照没有进入 Node Directory")
	}

	// 生产下限仍是 3s；测试只缩短运行时已冻结值，以快速验证公共到期所有权。
	current.discoveryProvider.mu.Lock()
	current.discoveryProvider.ttl = 30 * time.Millisecond
	current.discoveryProvider.lastSnapshot = time.Now()
	current.discoveryProvider.mu.Unlock()
	fixture.context.Host.Report(publicprovider.Report{
		State:     publicprovider.StateRecovering,
		ErrorCode: 5001,
	})
	if current.HealthStatus().Readiness {
		t.Fatal("Recovering 没有立即清除 Node Readiness")
	}

	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	for {
		if _, exists := local.FindDiscoveredService("remote-1", "RemoteService"); !exists {
			break
		}
		select {
		case <-deadline.C:
			t.Fatal("一个 TTL 后旧快照仍未清空")
		case <-time.After(time.Millisecond):
		}
	}
}

var _ publicprovider.Provider = (*providerRuntimeFixture)(nil)
