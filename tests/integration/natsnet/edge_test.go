package natsnet_test

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/duanhf2012/origin/v3/errs"
	"github.com/duanhf2012/origin/v3/internal/natsnet"
	"github.com/nats-io/nats.go"
)

// TestInitialConnectCancellationDuringHandshake 验证 Context 可以打断不发送 INFO 的服务端。
func TestInitialConnectCancellationDuringHandshake(t *testing.T) {
	t.Parallel()

	// 假服务端只接受 TCP 并保持沉默，模拟协议握手或 TLS 黑洞。
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	defer listener.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		raw, acceptErr := listener.Accept()
		if acceptErr == nil {
			accepted <- raw
		}
	}()

	options := testOptions("integration.cancel", "nats://"+listener.Addr().String())
	options.ConnectTimeout = time.Second
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	conn, err := natsnet.Connect(ctx, options, nil)
	if conn != nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Connect() = %v, %v", conn, err)
	}
	select {
	case raw := <-accepted:
		raw.Close()
	case <-time.After(time.Second):
		t.Fatal("假服务端没有接受初始连接")
	}
}

// TestInvalidPublicArguments 验证所有 nil、空 Subject 和无效 Handler 快速失败。
func TestInvalidPublicArguments(t *testing.T) {
	t.Parallel()

	options := testOptions("integration.invalid", "nats://127.0.0.1:1")
	if conn, err := natsnet.Connect(nil, options, nil); conn != nil ||
		!errors.Is(err, errs.ErrInvalidArgument) {
		t.Fatalf("Connect(nil) = %v, %v", conn, err)
	}

	running := startServer(t, defaultServerOptions())
	conn := connectForTest(t, testOptions("integration.invalid.live", running.ClientURL()), nil)
	defer closeConn(t, conn)

	if err := conn.Publish("", nil); !errors.Is(err, errs.ErrInvalidArgument) {
		t.Fatalf("Publish(empty subject) error = %v", err)
	}
	if _, err := conn.Subscribe(
		context.Background(),
		"",
		natsnet.SubscriptionOptions{},
		func(natsnet.Message) {},
	); !errors.Is(err, errs.ErrInvalidArgument) {
		t.Fatalf("Subscribe(empty subject) error = %v", err)
	}
	if _, err := conn.Subscribe(
		context.Background(),
		"origin.integration.invalid",
		natsnet.SubscriptionOptions{},
		nil,
	); !errors.Is(err, errs.ErrInvalidArgument) {
		t.Fatalf("Subscribe(nil handler) error = %v", err)
	}
	if err := conn.Flush(nil); !errors.Is(err, errs.ErrInvalidArgument) {
		t.Fatalf("Flush(nil) error = %v", err)
	}
	if err := conn.Wait(nil); !errors.Is(err, errs.ErrInvalidArgument) {
		t.Fatalf("Wait(nil) error = %v", err)
	}
}

// TestInboundMessageLimit 验证超限消息不会进入 Handler，并通过异步事件报告。
func TestInboundMessageLimit(t *testing.T) {
	t.Parallel()

	running := startServer(t, defaultServerOptions())
	options := testOptions("integration.inbound-limit", running.ClientURL())
	options.MaxMessageSize = 32
	options.Reconnect.BufferSize = 64
	options.Subscription.PendingBytes = 64
	events := make(chan natsnet.Event, 8)
	conn := connectForTest(
		t,
		options,
		func(event natsnet.Event) { events <- event },
	)
	defer closeConn(t, conn)

	var handled atomic.Int64
	_, err := conn.Subscribe(
		context.Background(),
		"origin.integration.inbound-limit",
		natsnet.SubscriptionOptions{},
		func(natsnet.Message) { handled.Add(1) },
	)
	if err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}

	// 使用原生客户端绕过 natsnet 的本地发送上限，验证接收侧二次检查。
	publisher, err := nats.Connect(running.ClientURL())
	if err != nil {
		t.Fatalf("nats.Connect() error = %v", err)
	}
	defer publisher.Close()
	if err = publisher.Publish(
		"origin.integration.inbound-limit",
		make([]byte, 33),
	); err != nil {
		t.Fatalf("native Publish() error = %v", err)
	}
	if err = publisher.Flush(); err != nil {
		t.Fatalf("native Flush() error = %v", err)
	}

	waitFor(t, func() bool {
		select {
		case event := <-events:
			return event.Type == natsnet.EventAsyncError &&
				errors.Is(event.Err, errs.ErrTransportMessageTooLarge)
		default:
			return false
		}
	})
	if handled.Load() != 0 {
		t.Fatalf("超限消息进入 Handler：%d", handled.Load())
	}
}

// TestSubscriptionDrainTimeout 验证阻塞 Handler 不会使 Drain 永久无法退出。
func TestSubscriptionDrainTimeout(t *testing.T) {
	t.Parallel()

	running := startServer(t, defaultServerOptions())
	options := testOptions("integration.drain-timeout", running.ClientURL())
	options.DefaultOperationTimeout = 30 * time.Millisecond
	conn := connectForTest(t, options, nil)
	defer closeConn(t, conn)

	started := make(chan struct{}, 1)
	release := make(chan struct{})
	subscription, err := conn.Subscribe(
		context.Background(),
		"origin.integration.drain-timeout",
		natsnet.SubscriptionOptions{},
		func(natsnet.Message) {
			started <- struct{}{}
			<-release
		},
	)
	if err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}
	if err = conn.Publish("origin.integration.drain-timeout", []byte("block")); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	select {
	case <-started:
	case <-time.After(integrationTimeout):
		t.Fatal("阻塞 Handler 未开始")
	}

	// 无 Deadline Context 使用默认 30ms，超时后强制注销 Subscription。
	err = subscription.Drain(context.Background())
	if !errors.Is(err, errs.ErrDeadlineExceeded) {
		t.Fatalf("Subscription Drain() error = %v", err)
	}
	close(release)
}

// TestConnectionDrainTimeout 验证整条连接排空超时后会强制关闭，
// 不会因为仍在执行的消息处理函数而让进程永久卡在退出阶段。
func TestConnectionDrainTimeout(t *testing.T) {
	t.Parallel()

	// 使用较短的默认操作超时，主动构造一个可重复触发的排空超时场景。
	running := startServer(t, defaultServerOptions())
	options := testOptions("integration.connection-drain-timeout", running.ClientURL())
	options.DefaultOperationTimeout = 30 * time.Millisecond
	options.DrainTimeout = time.Second
	conn := connectForTest(t, options, nil)

	// 处理函数收到消息后保持阻塞，模拟业务代码尚未从回调返回。
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	_, err := conn.Subscribe(
		context.Background(),
		"origin.integration.connection-drain-timeout",
		natsnet.SubscriptionOptions{},
		func(natsnet.Message) {
			started <- struct{}{}
			<-release
		},
	)
	if err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}
	if err = conn.Publish(
		"origin.integration.connection-drain-timeout",
		[]byte("block"),
	); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	select {
	case <-started:
	case <-time.After(integrationTimeout):
		t.Fatal("阻塞 Handler 未开始")
	}

	// 无 Deadline 的 Context 应采用配置中的 30ms 默认边界。
	// 超时后连接必须进入稳定关闭状态，Wait 也必须返回同一个终止原因。
	err = conn.Drain(context.Background())
	if !errors.Is(err, errs.ErrDeadlineExceeded) {
		t.Fatalf("Conn Drain() error = %v", err)
	}
	if conn.Status() != natsnet.StatusClosed {
		t.Fatalf("Status() = %v", conn.Status())
	}
	if err = conn.Wait(context.Background()); !errors.Is(err, errs.ErrDeadlineExceeded) {
		t.Fatalf("Conn Wait() error = %v", err)
	}

	// 连接关闭已经解除官方客户端对回调的等待；释放业务回调，避免测试留下协程。
	close(release)
}
