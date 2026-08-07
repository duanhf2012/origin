package rpcfixture

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/duanhf2012/origin/v3/errs"
	"github.com/duanhf2012/origin/v3/rpc"
	"github.com/duanhf2012/origin/v3/service"
)

// TestV31GeneratedCallFromOrdinaryGoroutine 验证 CallXxx 不需要 Service Task：它在测试
// goroutine 原地阻塞、接受 nil Context，并在响应到达后把结果返回到同一调用栈。
func TestV31GeneratedCallFromOrdinaryGoroutine(t *testing.T) {
	fixture := newRPCFixture(t)
	client := NewPlayerRPCClient(
		fixture.caller,
		rpc.ToService("PlayerService"),
	)

	value, err := client.CallEchoName(nil, "ordinary")
	if err != nil || value != "ordinary-echo" {
		t.Fatalf("CallEchoName(nil) value=%q error=%v", value, err)
	}
}

// TestV31GeneratedCallOverTCP 验证普通 goroutine 的 CallXxx 复用正式 TCP 路由、Pending
// 和响应解码内核，而不要求先投递一个 CallerService Task。
func TestV31GeneratedCallOverTCP(t *testing.T) {
	fixture := newRemoteRPCFixture(t)
	// 先用既有 Await helper 等待发现和 TCP 会话发布，避免把启动传播时间混入 Call 断言。
	if value := awaitRemoteEcho(t, fixture, "warm"); value != "warm-echo" {
		t.Fatalf("TCP warm value = %q", value)
	}
	client := NewPlayerRPCClient(
		fixture.caller,
		rpc.ToServiceOnNode("player-1", "PlayerService"),
	)
	value, err := client.CallEchoName(nil, "tcp-call")
	if err != nil || value != "tcp-call-echo" {
		t.Fatalf("TCP CallEchoName() value=%q error=%v", value, err)
	}
}

// TestV31GeneratedCallOverNATS 验证相同 CallXxx 外观直接工作在 NATS；发现事实尚未传播
// 时只在测试冷路径重试，单次已发现但连接未就绪的等待仍由 Call 内部 Deadline 管理。
func TestV31GeneratedCallOverNATS(t *testing.T) {
	fixture := newNATSRPCPair(t, service.DefaultSchedulerConfig())
	client := NewPlayerRPCClient(
		fixture.caller,
		rpc.ToServiceOnNode("player-1", "PlayerService"),
	)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		value, err := client.CallEchoName(ctx, "nats-call")
		if err == nil {
			if value != "nats-call-echo" {
				t.Fatalf("NATS CallEchoName() value = %q", value)
			}
			return
		}
		if !errors.Is(err, errs.ErrRPCNoRoute) &&
			!errors.Is(err, errs.ErrTransportUnavailable) {
			t.Fatalf("NATS CallEchoName() error = %v", err)
		}
		select {
		case <-time.After(20 * time.Millisecond):
		case <-ctx.Done():
			t.Fatalf("NATS CallEchoName() readiness timeout: %v", err)
		}
	}
}

// TestV31ExplicitCallDeadlineOverridesDefault 验证显式长 Deadline 不会被默认值截断。默认
// 只有 30ms，但本次调用显式允许 500ms，并在目标阻塞超过默认值后仍能成功完成。
func TestV31ExplicitCallDeadlineOverridesDefault(t *testing.T) {
	config := service.DefaultSchedulerConfig()
	config.DefaultAwaitTimeout = 30 * time.Millisecond
	fixture := newRPCFixtureWithConfig(t, config)

	release := make(chan struct{})
	started := make(chan struct{}, 1)
	fixture.player.Wait = release
	fixture.player.WaitStarted = started

	go func() {
		<-started
		time.Sleep(80 * time.Millisecond)
		close(release)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	client := NewPlayerRPCClient(
		fixture.caller,
		rpc.ToService("PlayerService"),
	)
	result, _, err := client.CallGetPlayer(
		ctx,
		42,
		PlayerData{Name: "long"},
		nil,
	)
	if err != nil || result.ID != 42 || result.Name != "long" {
		t.Fatalf("CallGetPlayer(explicit) result=%+v error=%v", result, err)
	}
}

// TestV31AwaitAcceptsNilContext 验证 AwaitXxx 的执行身份来自 owner 当前 Service Task，
// nil 只表示让框架为这次公开调用建立新的默认预算。
func TestV31AwaitAcceptsNilContext(t *testing.T) {
	fixture := newRPCFixture(t)
	result := make(chan string, 1)
	errors := make(chan error, 1)
	if err := fixture.caller.DispatchAsync(func(context.Context) {
		value, callErr := NewPlayerRPCClient(
			fixture.caller,
			rpc.ToService("PlayerService"),
		).AwaitEchoName(nil, "managed")
		result <- value
		errors <- callErr
	}); err != nil {
		t.Fatalf("DispatchAsync() error = %v", err)
	}

	if err := <-errors; err != nil {
		t.Fatalf("AwaitEchoName(nil) error = %v", err)
	}
	if value := <-result; value != "managed-echo" {
		t.Fatalf("AwaitEchoName(nil) value = %q", value)
	}
}

// TestV31AsyncFromOrdinaryGoroutineReturnsToOwnerService 验证普通 goroutine 可以直接提交
// AsyncXxx，但 callback 始终进入 owner Service 的后续串行任务，而不是回到提交 goroutine。
func TestV31AsyncFromOrdinaryGoroutineReturnsToOwnerService(t *testing.T) {
	fixture := newRPCFixture(t)
	callbackDone := make(chan struct{})
	client := NewPlayerRPCClient(
		fixture.caller,
		rpc.ToService("PlayerService"),
	)
	err := client.AsyncEchoName(nil, "async", func(
		_ context.Context,
		value string,
		callErr error,
	) {
		if callErr != nil || value != "async-echo" {
			t.Errorf("AsyncEchoName callback value=%q error=%v", value, callErr)
		}
		// callback 若不在 owner Service Task 中，这个 Await 会返回 InvalidArgument。
		if awaitErr := fixture.caller.Await(nil, func(context.Context) error {
			return nil
		}); awaitErr != nil {
			t.Errorf("callback owner Await() error = %v", awaitErr)
		}
		close(callbackDone)
	})
	if err != nil {
		t.Fatalf("AsyncEchoName(nil) submit error = %v", err)
	}
	awaitSignal(t, callbackDone)
}

// TestV31NotifyAcceptsNilContext 验证无响应通知允许省略 Context；其返回边界仍只是目标
// 队列已接受，随后用同一目标 Service 的 FIFO 屏障确认业务通知已经执行。
func TestV31NotifyAcceptsNilContext(t *testing.T) {
	fixture := newRPCFixture(t)
	client := NewPlayerRPCClient(
		fixture.caller,
		rpc.ToService("PlayerService"),
	)
	if err := client.NotifyPlayerOnline(nil, 77); err != nil {
		t.Fatalf("NotifyPlayerOnline(nil) error = %v", err)
	}
	barrier := make(chan struct{})
	if err := fixture.player.DispatchAsync(func(context.Context) {
		close(barrier)
	}); err != nil {
		t.Fatalf("target barrier DispatchAsync() error = %v", err)
	}
	awaitSignal(t, barrier)
	if fixture.player.OnlineID != 77 {
		t.Fatalf("PlayerOnline ID = %d, want 77", fixture.player.OnlineID)
	}
}
