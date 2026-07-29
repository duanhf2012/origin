package rpc

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/duanhf2012/origin/v3/errs"
	"github.com/duanhf2012/origin/v3/internal/bufferpool"
	originlog "github.com/duanhf2012/origin/v3/log"
)

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
	runtime.Close()
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
