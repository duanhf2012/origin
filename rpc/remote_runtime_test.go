package rpc

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net"
	"testing"
	"time"

	"github.com/duanhf2012/origin/v3/errs"
	"github.com/duanhf2012/origin/v3/internal/bufferpool"
	"github.com/duanhf2012/origin/v3/internal/timerwheel"
	originlog "github.com/duanhf2012/origin/v3/log"
)

func TestRemoteTargetCapacityMatchesPublishedNodeLimit(t *testing.T) {
	runtime := newPrepareTestRuntime(t, "gateway-1", TransportTCP, nil)
	targets := make([]ConnectionTarget, 8192)
	for index := range targets {
		targets[index] = ConnectionTarget{
			NodeID:    fmt.Sprintf("node-%04d", index),
			SessionID: uint64(index + 1),
			Address:   "127.0.0.1:26001",
		}
	}
	if err := runtime.ReconcileTargets(targets); err != nil {
		t.Fatalf("8192 targets error = %v", err)
	}
	targets = append(targets, ConnectionTarget{
		NodeID:    "node-overflow",
		SessionID: 8193,
		Address:   "127.0.0.1:26001",
	})
	if err := runtime.ReconcileTargets(targets); !errors.Is(
		err,
		errs.ErrTransportOverloaded,
	) {
		t.Fatalf("8193 targets error = %v", err)
	}
	if got := runtime.remote.listenOptions().MaxConnections; got != 8192 {
		t.Fatalf("listener max connections = %d", got)
	}
}

func TestRemoteTargetAddressLifecycle(t *testing.T) {
	pool := bufferpool.NewPool(bufferpool.Options{})
	runtime, err := NewRuntime("gateway-1", pool, originlog.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	config := DefaultConfig()
	config.TCP.Listen = "127.0.0.1:17001"
	config.TCP.Advertise = "127.0.0.1:17001"
	if err := runtime.Configure(&config); err != nil {
		t.Fatal(err)
	}
	if address, enabled := runtime.AdvertiseAddress(); !enabled ||
		address != config.TCP.Advertise {
		t.Fatalf("AdvertiseAddress() = %q, %v", address, enabled)
	}

	// 相同目标幂等；不同地址不能隐式替换当前目标。
	if err := runtime.AddTarget("player-1", 1, "127.0.0.1:17002"); err != nil {
		t.Fatal(err)
	}
	if err := runtime.AddTarget("player-1", 1, "127.0.0.1:17002"); err != nil {
		t.Fatalf("same AddTarget() error = %v", err)
	}
	if err := runtime.AddTarget("player-1", 1, "127.0.0.1:17003"); !errors.Is(
		err,
		errs.ErrTransportProtocol,
	) {
		t.Fatalf("replacement AddTarget() error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := runtime.RemoveTarget(
		ctx,
		"player-1",
		"127.0.0.1:17003",
	); err != nil {
		t.Fatalf("stale RemoveTarget() error = %v", err)
	}
	if err := runtime.AddTarget("player-1", 1, "127.0.0.1:17003"); !errors.Is(
		err,
		errs.ErrTransportProtocol,
	) {
		t.Fatalf("stale RemoveTarget removed current target: %v", err)
	}
	if err := runtime.RemoveTarget(
		ctx,
		"player-1",
		"127.0.0.1:17002",
	); err != nil {
		t.Fatalf("exact RemoveTarget() error = %v", err)
	}
	if err := runtime.AddTarget("player-1", 2, "127.0.0.1:17003"); err != nil {
		t.Fatalf("AddTarget after exact remove error = %v", err)
	}
	if err := runtime.Close(context.Background()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func TestRequestIDDoesNotWrap(t *testing.T) {
	runtime := &Runtime{}
	runtime.requestID.Store(math.MaxUint64 - 1)
	if id, err := runtime.nextRequestID(); err != nil || id != math.MaxUint64 {
		t.Fatalf("last nextRequestID() = %d, %v", id, err)
	}
	if id, err := runtime.nextRequestID(); id != 0 ||
		!errors.Is(err, errs.ErrTransportOverloaded) {
		t.Fatalf("wrapped nextRequestID() = %d, %v", id, err)
	}
}

func TestReconnectJitterBounds(t *testing.T) {
	state := uint64(1)
	base := time.Second
	for range 10_000 {
		delay := jitterDelay(base, &state)
		if delay < 800*time.Millisecond || delay > 1200*time.Millisecond {
			t.Fatalf("jitterDelay() = %v", delay)
		}
	}
}

// TestTCPListenerUnexpectedStopRecovers 验证 Listener 不是由 Runtime 正式 Stop 关闭时，
// 唯一恢复 owner 会在相同地址重建监听，并依次发布 Recovering 和 Ready。
func TestTCPListenerUnexpectedStopRecovers(t *testing.T) {
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("申请临时 TCP 端口: %v", err)
	}
	address := probe.Addr().String()
	if err := probe.Close(); err != nil {
		t.Fatalf("释放临时 TCP 端口: %v", err)
	}

	engine, err := timerwheel.New(timerwheel.DefaultOptions())
	if err != nil {
		t.Fatalf("timerwheel.New() error = %v", err)
	}
	if err := engine.Start(); err != nil {
		t.Fatalf("Engine.Start() error = %v", err)
	}
	defer engine.Close()

	runtime, err := NewRuntime(
		"gateway-1",
		bufferpool.NewPool(bufferpool.Options{}),
		originlog.NewNop(),
	)
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}
	config := DefaultConfig()
	config.TCP.Listen = address
	config.TCP.Advertise = address
	if err := runtime.Configure(&config); err != nil {
		t.Fatalf("Configure() error = %v", err)
	}
	events := make(chan TransportEvent, 8)
	if err := runtime.BindTransportObserver(func(event TransportEvent) {
		events <- event
	}); err != nil {
		t.Fatalf("BindTransportObserver() error = %v", err)
	}
	if err := runtime.Freeze(); err != nil {
		t.Fatalf("Freeze() error = %v", err)
	}
	if err := runtime.StartNetwork(context.Background(), engine); err != nil {
		t.Fatalf("StartNetwork() error = %v", err)
	}
	defer runtime.Close(context.Background())

	remote := runtime.remote
	remote.mu.Lock()
	first := remote.listener
	firstGeneration := remote.listenerGeneration
	remote.mu.Unlock()
	if first == nil {
		t.Fatal("StartNetwork() 未发布 TCP Listener")
	}
	if err := first.StopAccept(context.Background()); err != nil {
		t.Fatalf("意外 StopAccept() error = %v", err)
	}

	deadline := time.After(3 * time.Second)
	recovering := false
	ready := false
	for !ready {
		select {
		case event := <-events:
			if event.State == TransportStateRecovering {
				recovering = true
			}
			if recovering && event.State == TransportStateReady {
				ready = true
			}
		case <-deadline:
			t.Fatal("TCP Listener 没有在期限内完成恢复")
		}
	}

	remote.mu.Lock()
	current := remote.listener
	currentGeneration := remote.listenerGeneration
	remote.mu.Unlock()
	if current == nil || current == first {
		t.Fatal("恢复后仍持有旧 TCP Listener")
	}
	if currentGeneration <= firstGeneration {
		t.Fatalf(
			"恢复代次没有递增: before=%d after=%d",
			firstGeneration,
			currentGeneration,
		)
	}
}
