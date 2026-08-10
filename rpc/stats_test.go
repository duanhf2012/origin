package rpc

import (
	"context"
	"errors"
	"testing"

	"github.com/duanhf2012/origin/v3/errs"
	"github.com/duanhf2012/origin/v3/internal/bufferpool"
	originlog "github.com/duanhf2012/origin/v3/log"
	"github.com/duanhf2012/origin/v3/service"
)

// statsTestOwner 让 Await 在当前测试调用栈中真实执行等待函数，避免用 mock 计数代替 Client 行为。
type statsTestOwner struct {
	service.Service
}

func (*statsTestOwner) Await(
	ctx context.Context,
	fn func(context.Context) error,
) error {
	return fn(ctx)
}

// statsTestTarget 同步执行目标任务，使测试能够精确观察解码前后的计数线性化点。
type statsTestTarget struct {
	service.Service
	reject error
}

func (target *statsTestTarget) DispatchAsync(fn func(context.Context)) error {
	if target.reject != nil {
		return target.reject
	}
	fn(context.Background())
	return nil
}

func newStatsTestClient(
	t *testing.T,
	dispatcher Dispatcher,
	target *statsTestTarget,
) (*Runtime, Client) {
	t.Helper()
	runtime, err := NewRuntime(
		"stats-1",
		bufferpool.NewPool(bufferpool.Options{TrackUsage: true}),
		originlog.NewNop(),
	)
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}
	if err := runtime.Configure(nil); err != nil {
		t.Fatalf("Configure(nil) error = %v", err)
	}
	if err := runtime.Freeze(); err != nil {
		t.Fatalf("Freeze() error = %v", err)
	}
	endpoint := serviceEndpoint{
		serviceName: "PlayerService",
		target:      target,
		dispatcher:  dispatcher,
		public:      true,
	}
	invocationCtx, finishInvocation := context.WithTimeout(
		context.Background(),
		service.DefaultAwaitTimeout,
	)
	client := Client{
		owner:       &statsTestOwner{},
		runtime:     runtime,
		target:      ToService("PlayerService"),
		contractID:  1,
		fingerprint: runtimeTestFingerprint,
		invocation: &clientInvocation{
			ctx:    invocationCtx,
			finish: finishInvocation,
		},
		prepared: preparedTarget{
			transport:   preparedLocal,
			serviceName: "PlayerService",
			methodID:    1,
			kind:        CallRequest,
			endpoint:    endpoint,
		},
	}
	return runtime, client
}

// TestAwaitStatsCompleteAfterDecode 防止把 Dispatcher 返回或响应到达误记为 Await 完成。
func TestAwaitStatsCompleteAfterDecode(t *testing.T) {
	runtime, client := newStatsTestClient(
		t,
		&runtimeTestDispatcher{},
		&statsTestTarget{},
	)
	request := runtime.pool.Acquire(4)
	err := client.Await(context.Background(), 1, request, func(payload []byte) error {
		if len(payload) != 1 || payload[0] != 7 {
			t.Fatalf("response payload = %v", payload)
		}
		stats := runtime.Stats().Local
		if stats.OutboundAccepted != 1 || stats.Pending != 1 ||
			stats.OutboundCompleted != 0 {
			t.Fatalf("stats during decode = %+v", stats)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Await() error = %v", err)
	}

	stats := runtime.Stats().Local
	if stats.Pending != 0 || stats.PendingHighWater != 1 ||
		stats.OutboundAccepted != 1 || stats.OutboundCompleted != 1 ||
		stats.OutboundFailed != 0 || stats.OutboundTimeout != 0 ||
		stats.OutboundRejected != 0 || stats.PayloadSentBytes != 5 ||
		stats.PayloadReceivedBytes != 5 {
		t.Fatalf("final outbound stats = %+v", stats)
	}
	if stats.InboundAccepted != 1 || stats.InboundCompleted != 1 ||
		stats.InboundFailed != 0 || stats.InboundTimeout != 0 ||
		stats.InboundRejected != 0 {
		t.Fatalf("final inbound stats = %+v", stats)
	}
}

// TestAwaitStatsClassifyDecodeFailureAndTimeout 区分调用方解码失败和目标 Deadline 终态。
func TestAwaitStatsClassifyDecodeFailureAndTimeout(t *testing.T) {
	t.Run("decode failure", func(t *testing.T) {
		runtime, client := newStatsTestClient(
			t,
			&runtimeTestDispatcher{},
			&statsTestTarget{},
		)
		request := runtime.pool.Acquire(2)
		err := client.Await(context.Background(), 1, request, func([]byte) error {
			return errs.ErrRPCResponseDecodeFailed
		})
		if !errors.Is(err, errs.ErrRPCResponseDecodeFailed) {
			t.Fatalf("Await() error = %v", err)
		}
		stats := runtime.Stats().Local
		if stats.OutboundFailed != 1 || stats.OutboundCompleted != 0 || stats.Pending != 0 {
			t.Fatalf("decode failure stats = %+v", stats)
		}
	})

	t.Run("deadline", func(t *testing.T) {
		runtime, client := newStatsTestClient(
			t,
			&runtimeTestDispatcher{fail: errs.ErrDeadlineExceeded},
			&statsTestTarget{},
		)
		request := runtime.pool.Acquire(2)
		err := client.Await(context.Background(), 1, request, func([]byte) error {
			t.Fatal("deadline response must not be decoded")
			return nil
		})
		if !errors.Is(err, errs.ErrDeadlineExceeded) {
			t.Fatalf("Await() error = %v", err)
		}
		stats := runtime.Stats().Local
		if stats.OutboundTimeout != 1 || stats.OutboundCompleted != 0 || stats.Pending != 0 {
			t.Fatalf("deadline stats = %+v", stats)
		}
		if stats.InboundTimeout != 1 || stats.InboundCompleted != 0 {
			t.Fatalf("inbound deadline stats = %+v", stats)
		}
	})
}

// TestAwaitStatsRejected 防止 Service 队列拒绝被误记为已接受或遗留 pending。
func TestAwaitStatsRejected(t *testing.T) {
	runtime, client := newStatsTestClient(
		t,
		&runtimeTestDispatcher{},
		&statsTestTarget{reject: errs.ErrServiceQueueFull},
	)
	request := runtime.pool.Acquire(2)
	err := client.Await(context.Background(), 1, request, func([]byte) error { return nil })
	if !errors.Is(err, errs.ErrServiceQueueFull) {
		t.Fatalf("Await() error = %v", err)
	}
	stats := runtime.Stats().Local
	if stats.OutboundRejected != 1 || stats.InboundRejected != 1 ||
		stats.OutboundAccepted != 0 || stats.Pending != 0 {
		t.Fatalf("rejected stats = %+v", stats)
	}
}

func TestAwaitRejectsSubmissionWithoutPreparedInvocation(t *testing.T) {
	runtime, client := newStatsTestClient(
		t,
		&runtimeTestDispatcher{},
		&statsTestTarget{},
	)
	// 丢弃白盒夹具预建的预算，模拟跳过 PrepareAwait 直接进入底层提交阶段。
	client.FinishInvocation()
	client.invocation = nil
	request := runtime.pool.Acquire(2)
	if err := client.Await(
		context.Background(),
		1,
		request,
		func([]byte) error { return nil },
	); !errors.Is(err, errs.ErrInvalidArgument) {
		t.Fatalf("Await() error = %v, want invalid argument", err)
	}
	if stats := runtime.pool.Stats(); stats.InUseBuffers != 0 {
		t.Fatalf("buffer stats after rejected direct Await = %+v", stats)
	}
}

// TestStatsMapTransportRecovery 证明恢复累计值写入固定类别且不会污染其他 Transport。
func TestStatsMapTransportRecovery(t *testing.T) {
	runtime, err := NewRuntime(
		"stats-1",
		bufferpool.NewPool(bufferpool.Options{}),
		originlog.NewNop(),
	)
	if err != nil {
		t.Fatal(err)
	}
	runtime.reportTransportEvent(TransportEvent{
		Kind:                TransportKindNATS,
		State:               TransportStateRecovering,
		Reconnects:          7,
		ConsecutiveFailures: 3,
	})
	stats := runtime.Stats()
	if stats.NATS.Reconnects != 7 || stats.NATS.ConsecutiveFailures != 3 {
		t.Fatalf("NATS stats = %+v", stats.NATS)
	}
	if stats.Local.Reconnects != 0 || stats.TCP.Reconnects != 0 {
		t.Fatalf("unrelated stats = %+v", stats)
	}
}
