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
	originlog "github.com/duanhf2012/origin/v3/log"
)

type hostRecorder struct {
	mu        sync.Mutex
	ttl       time.Duration
	snapshot  publicprovider.Snapshot
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
	address := reserveAddress(t)
	rawConfig, err := publicprovider.NewConfig(map[string]any{
		"ttl": "3s",
		"server": map[string]any{
			"node":    "discovery-1",
			"listen":  address,
			"address": address,
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
	server := NewService(config, pool, originlog.NewNop())
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

	factory := NewFactory(pool)
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

func TestDecodeConfigDefaultsAndValidation(t *testing.T) {
	config, err := publicprovider.NewConfig(map[string]any{
		"server": map[string]any{
			"node":    "discovery-1",
			"listen":  "127.0.0.1:7100",
			"address": "127.0.0.1:7100",
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
			"node":    "discovery-1",
			"listen":  "127.0.0.1:7100",
			"address": "0.0.0.0:7100",
		},
	})
	if _, err := DecodeConfig(invalid); !errs.IsCode(err, errs.CodeInvalidConfig) {
		t.Fatalf("invalid DecodeConfig() error = %v", err)
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
