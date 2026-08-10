package node

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	publicprovider "github.com/duanhf2012/origin/v3/discovery/provider"
	"github.com/duanhf2012/origin/v3/errs"
	internaldiscovery "github.com/duanhf2012/origin/v3/internal/discovery"
	originlog "github.com/duanhf2012/origin/v3/log"
	"github.com/duanhf2012/origin/v3/service"
)

func TestDiscoveryProviderFactoryPanicIsIsolated(t *testing.T) {
	local := &lifecycleService{label: "LocalService", events: &[]string{}}
	_, err := New(
		Config{ID: "factory-panic", Services: []string{"LocalService"}},
		[]ServiceBinding{{
			Name:     "LocalService",
			Template: "lifecycleService",
			Service:  local,
		}},
		originlog.NewNop(),
		Options{
			MaxTimersPerNode: 64,
			TimerLocation:    time.UTC,
			DiscoveryKind:    "panic",
			DiscoveryFactory: func(publicprovider.Context) (publicprovider.Provider, error) {
				panic("factory panic")
			},
		},
	)
	if !errors.Is(err, errs.ErrDiscoveryUnavailable) {
		t.Fatalf("New() error = %v", err)
	}
}

type providerRuntimeFixture struct {
	context  publicprovider.Context
	snapshot *publicprovider.Snapshot
}

func (fixture *providerRuntimeFixture) Start(context.Context) error {
	if err := fixture.context.Host.SetTTL(3 * time.Second); err != nil {
		return err
	}
	snapshot := publicprovider.Snapshot{
		Nodes: []publicprovider.Node{{
			NodeID:    "remote-1",
			SessionID: 77,
			Transport: publicprovider.TransportNATS,
			Services: []publicprovider.Service{{
				ServiceName: "RemoteService",
				State:       publicprovider.ServiceStateRunning,
			}},
		}},
	}
	if fixture.snapshot != nil {
		snapshot = *fixture.snapshot
	}
	if err := fixture.context.Host.ReplaceSnapshot(snapshot); err != nil {
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
	if status := current.DiscoveryStatus(); status.Synchronized {
		t.Fatalf("TTL 到期后仍报告已同步: %+v", status)
	}

	// 错误顺序的第三方 Provider 不能只靠 Ready 恢复可用性；必须先给出新的权威快照。
	fixture.context.Host.Report(publicprovider.Report{State: publicprovider.StateReady})
	if current.HealthStatus().Readiness {
		t.Fatal("TTL 到期后未提交新快照却恢复了 Readiness")
	}
	if status := current.DiscoveryStatus(); status.Synchronized {
		t.Fatalf("过早 Ready 恢复了同步状态: %+v", status)
	}
	if err := fixture.context.Host.ReplaceSnapshot(publicprovider.Snapshot{
		Nodes: []publicprovider.Node{{
			NodeID:    "remote-1",
			SessionID: 78,
			Transport: publicprovider.TransportNATS,
			Services: []publicprovider.Service{{
				ServiceName: "RemoteService",
				State:       publicprovider.ServiceStateRunning,
			}},
		}},
	}); err != nil {
		t.Fatalf("ReplaceSnapshot() error = %v", err)
	}
	if current.HealthStatus().Readiness {
		t.Fatal("新快照后未重新报告 Ready 就恢复了 Readiness")
	}
	fixture.context.Host.Report(publicprovider.Report{State: publicprovider.StateReady})
	if !current.HealthStatus().Readiness {
		t.Fatal("新权威快照和 Ready 后没有恢复 Readiness")
	}
	if instance, exists := local.FindDiscoveredService(
		"remote-1",
		"RemoteService",
	); !exists || instance.SessionID != 78 {
		t.Fatalf("恢复后的权威实例 = (%+v, %v)", instance, exists)
	}
}

func TestProviderRuntimeSnapshotAndExpiryCommitAtomically(t *testing.T) {
	t.Parallel()

	directory, err := newDiscoveryRuntime("local-1", internaldiscovery.Filter{})
	if err != nil {
		t.Fatalf("newDiscoveryRuntime() error = %v", err)
	}
	current := &Node{
		id:        "local-1",
		discovery: directory,
		logger:    originlog.NewNop(),
	}
	directory.bindNode(current)
	runtime := &providerRuntime{
		node:          current,
		kind:          "fixture",
		ttl:           time.Second,
		ttlConfigured: true,
		state:         DiscoveryRecovering,
		synchronized:  true,
	}
	runtime.publishStatusLocked()

	snapshot := publicprovider.Snapshot{Nodes: []publicprovider.Node{{
		NodeID:    "remote-1",
		SessionID: 88,
		Transport: publicprovider.TransportNATS,
		Services: []publicprovider.Service{{
			ServiceName: "RemoteService",
			State:       publicprovider.ServiceStateRunning,
		}},
	}}}
	for iteration := 0; iteration < 100; iteration++ {
		// 每轮先构造一份已过期的旧权威快照，再让新快照与过期清空并发竞争。
		runtime.applyMu.Lock()
		runtime.mu.Lock()
		runtime.state = DiscoveryRecovering
		runtime.synchronized = true
		runtime.expiredSnapshot = false
		runtime.lastSnapshot = time.Now().Add(-2 * runtime.ttl)
		runtime.mu.Unlock()
		if err := directory.apply(internaldiscovery.RawSnapshot{}); err != nil {
			runtime.applyMu.Unlock()
			t.Fatalf("reset discovery snapshot error = %v", err)
		}
		runtime.applyMu.Unlock()

		now := time.Now()
		start := make(chan struct{})
		var wait sync.WaitGroup
		wait.Add(2)
		go func() {
			defer wait.Done()
			<-start
			if replaceErr := runtime.replaceSnapshot(snapshot); replaceErr != nil {
				t.Errorf("replaceSnapshot() error = %v", replaceErr)
			}
		}()
		go func() {
			defer wait.Done()
			<-start
			runtime.expireSnapshot(now)
		}()
		close(start)
		wait.Wait()

		runtime.mu.Lock()
		synchronized := runtime.synchronized
		runtime.mu.Unlock()
		instance, exists := directory.findPublic("remote-1", "RemoteService")
		if !synchronized || !exists || instance.SessionID != 88 {
			t.Fatalf(
				"iteration %d: synchronized=%v, instance=(%+v, %v)",
				iteration,
				synchronized,
				instance,
				exists,
			)
		}
	}
}

var _ publicprovider.Provider = (*providerRuntimeFixture)(nil)

// TestProviderRuntimeAppliesCommonDiscoveryFilter 验证 Provider 只负责传入标签和完整快照，
// allow_discovery 在公共 Node Directory 层生效，因而不依赖 Origin 或 etcd 后端。
func TestProviderRuntimeAppliesCommonDiscoveryFilter(t *testing.T) {
	services := []string{"RoomService"}
	labels := map[string][]string{"game_type": {"battle"}}
	filter, err := internaldiscovery.CompileFilter(true, []internaldiscovery.Rule{{
		Services:   &services,
		NodeLabels: &labels,
	}})
	if err != nil {
		t.Fatalf("CompileFilter() error = %v", err)
	}

	snapshot := publicprovider.Snapshot{Nodes: []publicprovider.Node{
		{
			NodeID:    "battle-room-1",
			SessionID: 11,
			Labels:    map[string]string{"game_type": "battle"},
			Transport: publicprovider.TransportNATS,
			Services: []publicprovider.Service{{
				ServiceName: "RoomService",
				State:       publicprovider.ServiceStateRunning,
			}},
		},
		{
			NodeID:    "card-room-1",
			SessionID: 12,
			Labels:    map[string]string{"game_type": "card"},
			Transport: publicprovider.TransportNATS,
			Services: []publicprovider.Service{{
				ServiceName: "RoomService",
				State:       publicprovider.ServiceStateRunning,
			}},
		},
	}}

	local := &lifecycleService{label: "LocalService", events: &[]string{}}
	current, err := New(
		Config{
			ID:              "gateway-1",
			DiscoveryFilter: filter,
			Scheduler:       service.DefaultSchedulerConfig(),
			Services:        []string{"LocalService"},
		},
		[]ServiceBinding{{
			Name:     "LocalService",
			Template: "lifecycleService",
			Service:  local,
		}},
		originlog.NewNop(),
		Options{
			MaxTimersPerNode: 64,
			TimerLocation:    time.UTC,
			DiscoveryKind:    "fixture",
			DiscoveryFactory: func(
				context publicprovider.Context,
			) (publicprovider.Provider, error) {
				return &providerRuntimeFixture{
					context:  context,
					snapshot: &snapshot,
				}, nil
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

	if _, exists := local.FindDiscoveredService(
		"battle-room-1",
		"RoomService",
	); !exists {
		t.Fatal("game_type=battle 的 RoomService 没有通过公共筛选")
	}
	if _, exists := local.FindDiscoveredService(
		"card-room-1",
		"RoomService",
	); exists {
		t.Fatal("game_type=card 的 RoomService 错误通过公共筛选")
	}
}
