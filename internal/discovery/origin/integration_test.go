package origin

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	publicprovider "github.com/duanhf2012/origin/v3/discovery/provider"
	"github.com/duanhf2012/origin/v3/discovery/providertest"
	"github.com/duanhf2012/origin/v3/errs"
	"github.com/duanhf2012/origin/v3/internal/bufferpool"
	"github.com/duanhf2012/origin/v3/internal/timerwheel"
	originlog "github.com/duanhf2012/origin/v3/log"
	"github.com/duanhf2012/origin/v3/rpc"
)

type hostRecorder struct {
	mu        sync.Mutex
	ttl       time.Duration
	snapshot  publicprovider.Snapshot
	history   []publicprovider.Snapshot
	report    publicprovider.Report
	snapshots chan struct{}
}

func newHostRecorder() *hostRecorder {
	return &hostRecorder{snapshots: make(chan struct{}, 32)}
}

func (recorder *hostRecorder) host() publicprovider.Host {
	return publicprovider.NewHost(
		func(ttl time.Duration) error {
			recorder.mu.Lock()
			defer recorder.mu.Unlock()
			if recorder.ttl != 0 && recorder.ttl != ttl {
				return errs.ErrInvalidConfig
			}
			recorder.ttl = ttl
			return nil
		},
		func(snapshot publicprovider.Snapshot) error {
			recorder.mu.Lock()
			recorder.snapshot = snapshot
			recorder.history = append(recorder.history, snapshot)
			recorder.mu.Unlock()
			select {
			case recorder.snapshots <- struct{}{}:
			default:
			}
			return nil
		},
		func(report publicprovider.Report) {
			recorder.mu.Lock()
			recorder.report = report
			recorder.mu.Unlock()
		},
	)
}

func (recorder *hostRecorder) waitForState(
	t *testing.T,
	state publicprovider.State,
) {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		recorder.mu.Lock()
		current := recorder.report.State
		recorder.mu.Unlock()
		if current == state {
			return
		}
		select {
		case <-ticker.C:
		case <-deadline.C:
			t.Fatalf("等待 Provider State %v 超时，当前 %v", state, current)
		}
	}
}

func (recorder *hostRecorder) historyMarker() int {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	return len(recorder.history)
}

func (recorder *hostRecorder) assertNodeNeverMissingSince(
	t *testing.T,
	marker int,
	nodeID string,
) {
	t.Helper()
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	for index := marker; index < len(recorder.history); index++ {
		found := false
		for _, node := range recorder.history[index].Nodes {
			if node.NodeID == nodeID {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf(
				"服务端恢复期间出现空洞快照: history[%d]=%+v",
				index,
				recorder.history[index],
			)
		}
	}
}

func (recorder *hostRecorder) waitForNode(
	t *testing.T,
	nodeID string,
	present bool,
) {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for {
		recorder.mu.Lock()
		found := false
		for _, node := range recorder.snapshot.Nodes {
			if node.NodeID == nodeID {
				found = true
				break
			}
		}
		recorder.mu.Unlock()
		if found == present {
			return
		}
		select {
		case <-recorder.snapshots:
		case <-deadline.C:
			t.Fatalf("等待 Node %q present=%v 超时", nodeID, present)
		}
	}
}

func TestOriginProviderEndToEndAndDuplicateSession(t *testing.T) {
	rawConfig, err := publicprovider.NewConfig(map[string]any{
		"ttl": "3s",
		"server": map[string]any{
			"node": "discovery-1",
		},
	})
	if err != nil {
		t.Fatalf("NewConfig() error = %v", err)
	}
	config, err := DecodeConfig(rawConfig)
	if err != nil {
		t.Fatalf("DecodeConfig() error = %v", err)
	}
	pool := bufferpool.NewPool(bufferpool.Options{TrackUsage: true})
	serverAddress := reserveAddress(t)
	serverRuntime := newOriginTestRuntime(t, "discovery-1", serverAddress, pool)
	server := NewService(config, pool, originlog.NewNop())
	if err := server.BindSystemRPC(serverRuntime); err != nil {
		t.Fatalf("BindSystemRPC() error = %v", err)
	}
	if err := serverRuntime.Freeze(); err != nil {
		t.Fatalf("server Runtime.Freeze() error = %v", err)
	}
	if err := serverRuntime.StartNetwork(context.Background(), newOriginTestEngine(t)); err != nil {
		t.Fatalf("server Runtime.StartNetwork() error = %v", err)
	}
	if err := server.PrepareDiscovery(context.Background()); err != nil {
		t.Fatalf("PrepareDiscovery() error = %v", err)
	}
	t.Cleanup(func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.CloseDiscovery(closeCtx); err != nil {
			t.Errorf("CloseDiscovery() error = %v", err)
		}
	})

	clientRuntime := newOriginTestRuntime(t, "game-client", reserveAddress(t), pool)
	if err := clientRuntime.Freeze(); err != nil {
		t.Fatalf("client Runtime.Freeze() error = %v", err)
	}
	if err := clientRuntime.StartNetwork(context.Background(), newOriginTestEngine(t)); err != nil {
		t.Fatalf("client Runtime.StartNetwork() error = %v", err)
	}
	factory := NewFactory(clientRuntime, rpc.SystemTarget{
		NodeID:  "discovery-1",
		Address: serverAddress,
	})
	firstRecorder := newHostRecorder()
	first, err := factory(publicprovider.Context{
		NodeID:    "game-1",
		SessionID: 11,
		Config:    rawConfig,
		Host:      firstRecorder.host(),
		Logger:    originlog.NewNop(),
	})
	if err != nil {
		t.Fatalf("first Factory() error = %v", err)
	}
	startCtx, cancelStart := context.WithTimeout(context.Background(), 5*time.Second)
	if err := first.Start(startCtx); err != nil {
		cancelStart()
		t.Fatalf("first Start() error = %v", err)
	}
	cancelStart()
	t.Cleanup(func() { _ = first.Close(context.Background()) })

	record := wireTestNode("game-1", 11)
	if err := first.Publish(context.Background(), record); err != nil {
		t.Fatalf("first Publish() error = %v", err)
	}

	observerRecorder := newHostRecorder()
	observer, err := factory(publicprovider.Context{
		NodeID:    "observer-1",
		SessionID: 22,
		Config:    rawConfig,
		Host:      observerRecorder.host(),
		Logger:    originlog.NewNop(),
	})
	if err != nil {
		t.Fatalf("observer Factory() error = %v", err)
	}
	observerCtx, cancelObserver := context.WithTimeout(context.Background(), 5*time.Second)
	if err := observer.Start(observerCtx); err != nil {
		cancelObserver()
		t.Fatalf("observer Start() error = %v", err)
	}
	cancelObserver()
	t.Cleanup(func() { _ = observer.Close(context.Background()) })
	observerRecorder.waitForNode(t, "game-1", true)

	duplicateRecorder := newHostRecorder()
	duplicate, err := factory(publicprovider.Context{
		NodeID:    "game-1",
		SessionID: 33,
		Config:    rawConfig,
		Host:      duplicateRecorder.host(),
		Logger:    originlog.NewNop(),
	})
	if err != nil {
		t.Fatalf("duplicate Factory() error = %v", err)
	}
	duplicateCtx, cancelDuplicate := context.WithTimeout(
		context.Background(),
		5*time.Second,
	)
	if err := duplicate.Start(duplicateCtx); err != nil {
		cancelDuplicate()
		t.Fatalf("duplicate Start() error = %v", err)
	}
	cancelDuplicate()
	t.Cleanup(func() { _ = duplicate.Close(context.Background()) })
	duplicateRecord := wireTestNode("game-1", 33)
	if err := duplicate.Publish(context.Background(), duplicateRecord); !errs.IsCode(err, errs.CodeDiscoveryDuplicateNode) {
		t.Fatalf("duplicate Publish() error = %v", err)
	}

	if err := first.Withdraw(context.Background()); err != nil {
		t.Fatalf("first Withdraw() error = %v", err)
	}
	observerRecorder.waitForNode(t, "game-1", false)

	if err := duplicate.Publish(context.Background(), duplicateRecord); err != nil {
		t.Fatalf("duplicate takeover Publish() error = %v", err)
	}
	observerRecorder.waitForNode(t, "game-1", true)

	if err := duplicate.Withdraw(context.Background()); err != nil {
		t.Fatalf("duplicate Withdraw() error = %v", err)
	}
	observerRecorder.waitForNode(t, "game-1", false)

	// 同一个真实 TCP 后端继续执行公开契约测试，确保 Origin 没有依赖私有测试入口。
	providertest.Run(t, providertest.Harness{
		Factory: factory,
		Config:  rawConfig,
		Timeout: 5 * time.Second,
	})
}

// TestOriginProviderServerRestartPreservesSnapshotDuringWarming catches a new server epoch
// publishing an empty warming snapshot before surviving clients have re-registered.
func TestOriginProviderServerRestartPreservesSnapshotDuringWarming(t *testing.T) {
	rawConfig, err := publicprovider.NewConfig(map[string]any{
		"ttl": "3s",
		"server": map[string]any{
			"node": "discovery-1",
		},
	})
	if err != nil {
		t.Fatalf("NewConfig() error = %v", err)
	}
	config, err := DecodeConfig(rawConfig)
	if err != nil {
		t.Fatalf("DecodeConfig() error = %v", err)
	}
	pool := bufferpool.NewPool(bufferpool.Options{TrackUsage: true})
	serverAddress := reserveAddress(t)
	startServer := func() (*rpc.Runtime, *Service) {
		serverRuntime := newOriginTestRuntime(
			t,
			"discovery-1",
			serverAddress,
			pool,
		)
		server := NewService(config, pool, originlog.NewNop())
		if err := server.BindSystemRPC(serverRuntime); err != nil {
			t.Fatalf("BindSystemRPC() error = %v", err)
		}
		if err := serverRuntime.Freeze(); err != nil {
			t.Fatalf("server Runtime.Freeze() error = %v", err)
		}
		if err := serverRuntime.StartNetwork(
			context.Background(),
			newOriginTestEngine(t),
		); err != nil {
			t.Fatalf("server Runtime.StartNetwork() error = %v", err)
		}
		if err := server.PrepareDiscovery(context.Background()); err != nil {
			t.Fatalf("PrepareDiscovery() error = %v", err)
		}
		return serverRuntime, server
	}

	serverRuntime, server := startServer()
	clientRuntime := newOriginTestRuntime(
		t,
		"game-client",
		reserveAddress(t),
		pool,
	)
	if err := clientRuntime.Freeze(); err != nil {
		t.Fatalf("client Runtime.Freeze() error = %v", err)
	}
	if err := clientRuntime.StartNetwork(
		context.Background(),
		newOriginTestEngine(t),
	); err != nil {
		t.Fatalf("client Runtime.StartNetwork() error = %v", err)
	}
	factory := NewFactory(clientRuntime, rpc.SystemTarget{
		NodeID:  "discovery-1",
		Address: serverAddress,
	})

	publisherRecorder := newHostRecorder()
	publisher, err := factory(publicprovider.Context{
		NodeID:    "game-1",
		SessionID: 301,
		Config:    rawConfig,
		Host:      publisherRecorder.host(),
		Logger:    originlog.NewNop(),
	})
	if err != nil {
		t.Fatalf("publisher Factory() error = %v", err)
	}
	startCtx, cancelStart := context.WithTimeout(context.Background(), 5*time.Second)
	if err := publisher.Start(startCtx); err != nil {
		cancelStart()
		t.Fatalf("publisher Start() error = %v", err)
	}
	cancelStart()
	t.Cleanup(func() { _ = publisher.Close(context.Background()) })
	if err := publisher.Publish(
		context.Background(),
		wireTestNode("game-1", 301),
	); err != nil {
		t.Fatalf("publisher Publish() error = %v", err)
	}

	observerRecorder := newHostRecorder()
	observer, err := factory(publicprovider.Context{
		NodeID:    "observer-1",
		SessionID: 302,
		Config:    rawConfig,
		Host:      observerRecorder.host(),
		Logger:    originlog.NewNop(),
	})
	if err != nil {
		t.Fatalf("observer Factory() error = %v", err)
	}
	observerCtx, cancelObserver := context.WithTimeout(
		context.Background(),
		5*time.Second,
	)
	if err := observer.Start(observerCtx); err != nil {
		cancelObserver()
		t.Fatalf("observer Start() error = %v", err)
	}
	cancelObserver()
	t.Cleanup(func() { _ = observer.Close(context.Background()) })
	observerRecorder.waitForNode(t, "game-1", true)
	marker := observerRecorder.historyMarker()

	// 先停止权威 Actor，再关闭 Transport，模拟进程崩溃而不是正常 Withdraw。
	closeCtx, cancelClose := context.WithTimeout(context.Background(), 5*time.Second)
	if err := server.CloseDiscovery(closeCtx); err != nil {
		cancelClose()
		t.Fatalf("first CloseDiscovery() error = %v", err)
	}
	if err := serverRuntime.Close(closeCtx); err != nil {
		cancelClose()
		t.Fatalf("first Runtime.Close() error = %v", err)
	}
	cancelClose()
	observerRecorder.waitForState(t, publicprovider.StateRecovering)
	observerRecorder.waitForNode(t, "game-1", true)

	_, replacement := startServer()
	t.Cleanup(func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = replacement.CloseDiscovery(closeCtx)
	})
	observerRecorder.waitForState(t, publicprovider.StateReady)
	observerRecorder.waitForNode(t, "game-1", true)
	observerRecorder.assertNodeNeverMissingSince(t, marker, "game-1")
}

func TestDecodeConfigDefaultsAndValidation(t *testing.T) {
	config, err := publicprovider.NewConfig(map[string]any{
		"server": map[string]any{
			"node": "discovery-1",
		},
	})
	if err != nil {
		t.Fatalf("NewConfig() error = %v", err)
	}
	decoded, err := DecodeConfig(config)
	if err != nil {
		t.Fatalf("DecodeConfig() error = %v", err)
	}
	if decoded.TTL != 15*time.Second {
		t.Fatalf("default TTL = %v", decoded.TTL)
	}

	invalid, _ := publicprovider.NewConfig(map[string]any{
		"ttl": "2s",
		"server": map[string]any{
			"node": "discovery-1",
		},
	})
	if _, err := DecodeConfig(invalid); !errs.IsCode(err, errs.CodeInvalidConfig) {
		t.Fatalf("invalid DecodeConfig() error = %v", err)
	}
	legacy, _ := publicprovider.NewConfig(map[string]any{
		"server": map[string]any{
			"node":   "discovery-1",
			"listen": "127.0.0.1:7100",
		},
	})
	if _, err := DecodeConfig(legacy); !errs.IsCode(err, errs.CodeInvalidConfig) {
		t.Fatalf("legacy listen DecodeConfig() error = %v", err)
	}
}

func reserveAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve address: %v", err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatalf("close reserved listener: %v", err)
	}
	return address
}

func newOriginTestRuntime(
	t *testing.T,
	nodeID, address string,
	pool *bufferpool.Pool,
) *rpc.Runtime {
	t.Helper()
	runtime, err := rpc.NewRuntime(nodeID, pool, originlog.NewNop())
	if err != nil {
		t.Fatalf("rpc.NewRuntime() error = %v", err)
	}
	config := rpc.DefaultConfig()
	config.TCP.Listen = address
	config.TCP.Advertise = address
	if err := runtime.Configure(&config); err != nil {
		t.Fatalf("Runtime.Configure() error = %v", err)
	}
	if err := runtime.EnableSystem(); err != nil {
		t.Fatalf("Runtime.EnableSystem() error = %v", err)
	}
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	return runtime
}

func newOriginTestEngine(t *testing.T) *timerwheel.Engine {
	t.Helper()
	engine, err := timerwheel.New(timerwheel.DefaultOptions())
	if err != nil {
		t.Fatalf("timerwheel.New() error = %v", err)
	}
	if err := engine.Start(); err != nil {
		t.Fatalf("Engine.Start() error = %v", err)
	}
	t.Cleanup(func() { _ = engine.Close() })
	return engine
}
