// Package natsnet_test 使用真实 NATS Server 验证内部 natsnet 的协议和生命周期。
package natsnet_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/duanhf2012/origin/v3/errs"
	"github.com/duanhf2012/origin/v3/internal/natsnet"
	"github.com/nats-io/nats-server/v2/server"
)

const integrationTimeout = 5 * time.Second

// TestMessageDataCanOutliveHandler 锁定 M15 依赖的 nats.go 所有权边界：异步 Handler 返回后，
// Message.Data 仍由接收者持有，natsnet 不会池化或复用底层切片。
func TestMessageDataCanOutliveHandler(t *testing.T) {
	t.Parallel()

	running := startServer(t, defaultServerOptions())
	options := testOptions("integration.message-ownership", running.ClientURL())
	conn := connectForTest(t, options, nil)
	defer closeConn(t, conn)

	subject := "origin.integration.message-ownership"
	retained := make(chan []byte, 1)
	_, err := conn.Subscribe(
		context.Background(),
		subject,
		natsnet.SubscriptionOptions{},
		func(message natsnet.Message) {
			// 故意只转移 Slice Header，不复制 payload。
			select {
			case retained <- message.Data:
			default:
			}
		},
	)
	if err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}

	expected := bytes.Repeat([]byte{0x5a}, 1024)
	if err = conn.Publish(subject, expected); err != nil {
		t.Fatalf("first Publish() error = %v", err)
	}
	first := <-retained
	for index := 0; index < 1024; index++ {
		if err = conn.Publish(subject, bytes.Repeat([]byte{byte(index)}, 1024)); err != nil {
			t.Fatalf("noise Publish() error = %v", err)
		}
	}
	if err = conn.Flush(context.Background()); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	runtime.GC()
	if !bytes.Equal(first, expected) {
		t.Fatal("Handler 返回后 Message.Data 被复用或修改")
	}
}

// TestPublishSubscribeAndLifecycle 验证普通消息、空 payload、源切片复用、统计和立即关闭。
func TestPublishSubscribeAndLifecycle(t *testing.T) {
	t.Parallel()

	// 进程内真实 Server 使用随机端口，测试不依赖 Docker 或预装二进制。
	running := startServer(t, defaultServerOptions())
	options := testOptions("integration.publish", running.ClientURL())

	events := make(chan natsnet.Event, 8)
	conn, err := natsnet.Connect(
		context.Background(),
		options,
		func(event natsnet.Event) { events <- event },
	)
	if err != nil {
		t.Fatalf("Connect() error = %v", err)
	}

	// Connect 返回前必须已经发布一次明确的初始成功事件。
	select {
	case event := <-events:
		if event.Type != natsnet.EventConnected {
			t.Fatalf("首个事件 = %v", event.Type)
		}
	default:
		t.Fatal("缺少 EventConnected")
	}

	messages := make(chan []byte, 4)
	subscription, err := conn.Subscribe(
		context.Background(),
		"origin.integration.publish",
		natsnet.SubscriptionOptions{},
		func(message natsnet.Message) {
			messages <- append([]byte(nil), message.Data...)
		},
	)
	if err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}

	// nil payload 是合法消息，必须进入 Handler 而不是被过滤。
	if err = conn.Publish("origin.integration.publish", nil); err != nil {
		t.Fatalf("Publish(nil) error = %v", err)
	}
	assertMessage(t, messages, nil)

	// Publish 返回后立即修改源切片，接收端必须仍看到发布时的数据。
	payload := []byte("payload-before-reuse")
	expected := append([]byte(nil), payload...)
	if err = conn.Publish("origin.integration.publish", payload); err != nil {
		t.Fatalf("Publish(payload) error = %v", err)
	}
	for index := range payload {
		payload[index] = 'x'
	}
	assertMessage(t, messages, expected)

	stats := conn.Stats()
	if stats.OutMessages < 2 || stats.InMessages < 2 {
		t.Fatalf("Conn Stats 未累计消息：%+v", stats)
	}
	if subscription.Subject() != "origin.integration.publish" ||
		subscription.Queue() != "" {
		t.Fatalf("Subscription 外观错误：%q %q", subscription.Subject(), subscription.Queue())
	}

	// 单独关闭订阅后再关闭 Connection，两个动作都必须幂等。
	subscription.Close()
	subscription.Close()
	conn.Close()
	conn.Close()
	waitCtx, cancel := context.WithTimeout(context.Background(), integrationTimeout)
	defer cancel()
	if err = conn.Wait(waitCtx); !errors.Is(err, errs.ErrTransportClosed) {
		t.Fatalf("Wait() error = %v", err)
	}
}

// TestQueueGroupOnlyDeliversOnce 验证同一 Queue Group 中每条消息只交给一个成员。
func TestQueueGroupOnlyDeliversOnce(t *testing.T) {
	t.Parallel()

	running := startServer(t, defaultServerOptions())
	conn := connectForTest(t, testOptions("integration.queue", running.ClientURL()), nil)
	defer closeConn(t, conn)

	var received atomic.Int64
	handler := func(natsnet.Message) {
		received.Add(1)
	}
	for index := 0; index < 2; index++ {
		_, err := conn.Subscribe(
			context.Background(),
			"origin.integration.queue",
			natsnet.SubscriptionOptions{Queue: "workers"},
			handler,
		)
		if err != nil {
			t.Fatalf("Queue Subscribe %d error = %v", index, err)
		}
	}

	// 发布固定数量并 Flush，最终总回调数必须等于发布数而不是订阅数乘发布数。
	const messageCount = 50
	for index := 0; index < messageCount; index++ {
		if err := conn.Publish("origin.integration.queue", []byte("x")); err != nil {
			t.Fatalf("Publish %d error = %v", index, err)
		}
	}
	if err := conn.Flush(context.Background()); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	waitFor(t, func() bool { return received.Load() == messageCount })
	if got := received.Load(); got != messageCount {
		t.Fatalf("Queue Group received = %d, want %d", got, messageCount)
	}
}

// TestHandlerPanicDoesNotStopSubscription 验证 panic 只丢当前消息并报告内部异步事件。
func TestHandlerPanicDoesNotStopSubscription(t *testing.T) {
	t.Parallel()

	running := startServer(t, defaultServerOptions())
	events := make(chan natsnet.Event, 16)
	conn := connectForTest(
		t,
		testOptions("integration.panic", running.ClientURL()),
		func(event natsnet.Event) {
			// EventHandler 自身的初始 panic 也必须被包装层隔离。
			if event.Type == natsnet.EventConnected {
				panic("event panic")
			}
			events <- event
		},
	)
	defer closeConn(t, conn)

	delivered := make(chan struct{}, 1)
	_, err := conn.Subscribe(
		context.Background(),
		"origin.integration.panic",
		natsnet.SubscriptionOptions{},
		func(message natsnet.Message) {
			if bytes.Equal(message.Data, []byte("panic")) {
				panic("message panic")
			}
			delivered <- struct{}{}
		},
	)
	if err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}

	if err = conn.Publish("origin.integration.panic", []byte("panic")); err != nil {
		t.Fatalf("Publish panic message error = %v", err)
	}
	if err = conn.Publish("origin.integration.panic", []byte("continue")); err != nil {
		t.Fatalf("Publish continue message error = %v", err)
	}
	select {
	case <-delivered:
	case <-time.After(integrationTimeout):
		t.Fatal("Handler panic 后订阅没有继续处理消息")
	}

	// 异步事件必须携带稳定 CodeInternal，而不是让 panic 逃出 NATS goroutine。
	waitFor(t, func() bool {
		select {
		case event := <-events:
			return event.Type == natsnet.EventAsyncError &&
				errs.CodeOf(event.Err) == errs.CodeInternal
		default:
			return false
		}
	})
}

// TestConnectionAndSubscriptionDrain 验证单订阅与整连接的有界排空。
func TestConnectionAndSubscriptionDrain(t *testing.T) {
	t.Parallel()

	running := startServer(t, defaultServerOptions())
	conn := connectForTest(t, testOptions("integration.drain", running.ClientURL()), nil)

	started := make(chan struct{}, 1)
	release := make(chan struct{})
	subscription, err := conn.Subscribe(
		context.Background(),
		"origin.integration.sub-drain",
		natsnet.SubscriptionOptions{},
		func(natsnet.Message) {
			started <- struct{}{}
			<-release
		},
	)
	if err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}
	if err = conn.Publish("origin.integration.sub-drain", []byte("wait")); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	select {
	case <-started:
	case <-time.After(integrationTimeout):
		t.Fatal("阻塞 Handler 未开始")
	}

	// Subscription Drain 必须等待正在执行的 Handler，而不是立即返回。
	drainResult := make(chan error, 1)
	go func() {
		drainResult <- subscription.Drain(context.Background())
	}()
	select {
	case err = <-drainResult:
		t.Fatalf("Subscription Drain 提前返回：%v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	select {
	case err = <-drainResult:
		if err != nil {
			t.Fatalf("Subscription Drain error = %v", err)
		}
	case <-time.After(integrationTimeout):
		t.Fatal("Subscription Drain 未完成")
	}

	// Connection Drain 应排空剩余订阅并以 nil 终态完成 Wait。
	received := make(chan struct{}, 1)
	_, err = conn.Subscribe(
		context.Background(),
		"origin.integration.conn-drain",
		natsnet.SubscriptionOptions{},
		func(natsnet.Message) { received <- struct{}{} },
	)
	if err != nil {
		t.Fatalf("second Subscribe() error = %v", err)
	}
	if err = conn.Publish("origin.integration.conn-drain", []byte("last")); err != nil {
		t.Fatalf("second Publish() error = %v", err)
	}
	drainCtx, cancel := context.WithTimeout(context.Background(), integrationTimeout)
	defer cancel()
	if err = conn.Drain(drainCtx); err != nil {
		t.Fatalf("Conn Drain() error = %v", err)
	}
	select {
	case <-received:
	default:
		t.Fatal("Connection Drain 丢失已接受消息")
	}
	if conn.Status() != natsnet.StatusClosed {
		t.Fatalf("Status() = %v", conn.Status())
	}
	if err = conn.Wait(context.Background()); err != nil {
		t.Fatalf("Drain 后 Wait() error = %v", err)
	}
	if err = conn.Publish("origin.integration.conn-drain", nil); !errors.Is(
		err,
		errs.ErrTransportClosed,
	) {
		t.Fatalf("Drain 后 Publish() error = %v", err)
	}
}

// TestAuthenticationAndMessageLimit 验证用户名密码、认证失败和本地消息上限。
func TestAuthenticationAndMessageLimit(t *testing.T) {
	t.Parallel()

	serverOptions := defaultServerOptions()
	serverOptions.Username = "origin"
	serverOptions.Password = "test-password"
	serverOptions.MaxPayload = 128
	running := startServer(t, serverOptions)

	options := testOptions("integration.auth", running.ClientURL())
	options.MaxMessageSize = 64
	options.Reconnect.BufferSize = 128
	options.Auth.Username = "origin"
	options.Auth.Password = "test-password"
	conn := connectForTest(t, options, nil)
	defer closeConn(t, conn)

	// RPC Adapter 依赖该冷路径信息在创建订阅前校验完整 Origin 包络上限。
	if got := conn.MaxPayload(); got != 128 {
		t.Fatalf("MaxPayload() = %d，期望 128", got)
	}

	if err := conn.Publish("origin.integration.limit", make([]byte, 65)); !errors.Is(
		err,
		errs.ErrTransportMessageTooLarge,
	) {
		t.Fatalf("超限 Publish() error = %v", err)
	}

	// 错误密码必须在初始 Connect 阶段直接失败，且映射为 TransportUnavailable。
	wrong := options
	wrong.Name = "integration.auth.wrong"
	wrong.Auth.Password = "wrong"
	if failedConn, err := natsnet.Connect(context.Background(), wrong, nil); failedConn != nil ||
		!errors.Is(err, errs.ErrTransportUnavailable) {
		t.Fatalf("错误密码 Connect() = %v, %v", failedConn, err)
	}
}

// TestTokenAuthentication 验证公开配置中的 Token 模式能够直接接入
// 使用标准 NATS authorization 配置启动的服务端。
func TestTokenAuthentication(t *testing.T) {
	t.Parallel()

	// Token 认证与用户名密码认证互斥，因此单独启动服务端覆盖这一条真实握手路径。
	serverOptions := defaultServerOptions()
	serverOptions.Authorization = "origin-token"
	running := startServer(t, serverOptions)

	options := testOptions("integration.token-auth", running.ClientURL())
	options.Auth.Token = "origin-token"
	conn := connectForTest(t, options, nil)
	defer closeConn(t, conn)

	// 完成一次 Flush，确认不仅建立了 TCP 连接，也已经通过协议认证并可正常通信。
	if err := conn.Flush(context.Background()); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}

	// 错误 Token 必须在初始连接阶段返回传输不可用，不得进入后台无限重连。
	wrong := options
	wrong.Name = "integration.token-auth.wrong"
	wrong.Auth.Token = "wrong-token"
	if failedConn, err := natsnet.Connect(context.Background(), wrong, nil); failedConn != nil ||
		!errors.Is(err, errs.ErrTransportUnavailable) {
		t.Fatalf("错误 Token Connect() = %v, %v", failedConn, err)
	}
}

// TestReconnectAndAutomaticResubscribe 验证同端口重启后的事件、重连和自动重订阅。
func TestReconnectAndAutomaticResubscribe(t *testing.T) {
	// 该测试独占固定临时端口，不与其他并行 Server 生命周期交错。
	port := freePort(t)
	serverOptions := defaultServerOptions()
	serverOptions.Port = port
	first := startServerManual(t, serverOptions)

	options := testOptions(
		"integration.reconnect",
		fmt.Sprintf("nats://127.0.0.1:%d", port),
	)
	options.Reconnect.MaxAttempts = 200
	options.Reconnect.Wait = 10 * time.Millisecond
	options.Reconnect.Jitter = 0
	options.Reconnect.TLSJitter = 0
	events := make(chan natsnet.Event, 32)
	conn := connectForTest(
		t,
		options,
		func(event natsnet.Event) { events <- event },
	)
	defer closeConn(t, conn)

	messages := make(chan []byte, 1)
	_, err := conn.Subscribe(
		context.Background(),
		"origin.integration.reconnect",
		natsnet.SubscriptionOptions{},
		func(message natsnet.Message) {
			messages <- append([]byte(nil), message.Data...)
		},
	)
	if err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}

	// 停止 Server 后必须先看到断开，再由同端口新 Server 触发重连。
	first.Shutdown()
	first.WaitForShutdown()
	waitEvent(t, events, natsnet.EventDisconnected)

	second := startServerManual(t, serverOptions)
	defer shutdownServer(second)
	waitEvent(t, events, natsnet.EventReconnected)

	if err = conn.Publish("origin.integration.reconnect", []byte("restored")); err != nil {
		t.Fatalf("重连后 Publish() error = %v", err)
	}
	assertMessage(t, messages, []byte("restored"))
	if conn.Stats().Reconnects == 0 {
		t.Fatalf("Reconnects 未增加：%+v", conn.Stats())
	}
}

// TestReconnectExhaustedAndBufferOverload 验证有限重连终态和重连缓冲过载。
func TestReconnectExhaustedAndBufferOverload(t *testing.T) {
	// 第一条连接使用较长重连窗口，先验证断线期间的官方有界缓冲。
	port := freePort(t)
	serverOptions := defaultServerOptions()
	serverOptions.Port = port
	running := startServerManual(t, serverOptions)

	options := testOptions(
		"integration.buffer",
		fmt.Sprintf("nats://127.0.0.1:%d", port),
	)
	options.MaxMessageSize = 32
	options.Reconnect.BufferSize = 64
	options.Reconnect.MaxAttempts = 100
	options.Reconnect.Wait = 200 * time.Millisecond
	events := make(chan natsnet.Event, 16)
	conn := connectForTest(
		t,
		options,
		func(event natsnet.Event) { events <- event },
	)

	running.Shutdown()
	running.WaitForShutdown()
	waitEvent(t, events, natsnet.EventDisconnected)

	var overloaded bool
	for index := 0; index < 10; index++ {
		err := conn.Publish("origin.integration.buffer", make([]byte, 32))
		if errors.Is(err, errs.ErrTransportOverloaded) {
			overloaded = true
			break
		}
	}
	if !overloaded {
		t.Fatal("Reconnect Buffer 达到边界后没有返回过载")
	}
	conn.Close()
	if err := conn.Wait(context.Background()); !errors.Is(err, errs.ErrTransportClosed) {
		t.Fatalf("Close 后 Wait() error = %v", err)
	}

	// 第二条连接使用极少重连次数，验证耗尽后进入不可恢复终态。
	port = freePort(t)
	serverOptions.Port = port
	running = startServerManual(t, serverOptions)
	exhaustedOptions := testOptions(
		"integration.exhausted",
		fmt.Sprintf("nats://127.0.0.1:%d", port),
	)
	exhaustedOptions.Reconnect.MaxAttempts = 1
	exhaustedOptions.Reconnect.Wait = 10 * time.Millisecond
	exhaustedOptions.Reconnect.Jitter = 0
	exhausted := connectForTest(t, exhaustedOptions, nil)
	running.Shutdown()
	running.WaitForShutdown()

	waitCtx, cancel := context.WithTimeout(context.Background(), integrationTimeout)
	defer cancel()
	if err := exhausted.Wait(waitCtx); !errors.Is(err, errs.ErrTransportUnavailable) {
		t.Fatalf("重连耗尽 Wait() error = %v", err)
	}
}

// TestPendingLimitReportsSlowConsumer 验证 Pending 上限、Dropped 统计和异步过载事件。
func TestPendingLimitReportsSlowConsumer(t *testing.T) {
	t.Parallel()

	running := startServer(t, defaultServerOptions())
	options := testOptions("integration.pending", running.ClientURL())
	options.MaxMessageSize = 64
	options.Reconnect.BufferSize = 128
	events := make(chan natsnet.Event, 128)
	conn := connectForTest(
		t,
		options,
		func(event natsnet.Event) { events <- event },
	)
	defer closeConn(t, conn)

	started := make(chan struct{}, 1)
	release := make(chan struct{})
	subscription, err := conn.Subscribe(
		context.Background(),
		"origin.integration.pending",
		natsnet.SubscriptionOptions{
			PendingMessages: 1,
		},
		func(natsnet.Message) {
			select {
			case started <- struct{}{}:
			default:
			}
			<-release
		},
	)
	if err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}

	// 第一条消息占住回调，后续突发超过一条 Pending 上限并触发官方 Slow Consumer。
	if err = conn.Publish("origin.integration.pending", []byte("first")); err != nil {
		t.Fatalf("first Publish() error = %v", err)
	}
	select {
	case <-started:
	case <-time.After(integrationTimeout):
		t.Fatal("阻塞 Handler 未开始")
	}
	for index := 0; index < 100; index++ {
		if err = conn.Publish("origin.integration.pending", []byte("queued")); err != nil {
			t.Fatalf("Publish %d error = %v", index, err)
		}
	}
	if err = conn.Flush(context.Background()); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}

	waitFor(t, func() bool {
		select {
		case event := <-events:
			return event.Type == natsnet.EventAsyncError &&
				errors.Is(event.Err, errs.ErrTransportOverloaded)
		default:
			return false
		}
	})
	if stats := subscription.Stats(); stats.DroppedMessages == 0 {
		t.Fatalf("DroppedMessages 未增加：%+v", stats)
	}
	close(release)
}

// TestExternalThreeNodeCluster 使用环境变量指定的已安装三节点集群验证跨节点路由。
func TestExternalThreeNodeCluster(t *testing.T) {
	urls := externalURLs(t)
	username := os.Getenv("ORIGIN_NATS_USERNAME")
	password := os.Getenv("ORIGIN_NATS_PASSWORD")

	// 订阅端固定连接第一个节点，发布端固定连接最后一个节点，避免同连接回环掩盖路由问题。
	subscriberOptions := testOptions("integration.external.subscriber", urls[0])
	subscriberOptions.NoRandomize = true
	subscriberOptions.Auth.Username = username
	subscriberOptions.Auth.Password = password
	subscriber := connectForTest(t, subscriberOptions, nil)
	defer closeConn(t, subscriber)

	publisherOptions := testOptions(
		"integration.external.publisher",
		urls[len(urls)-1],
	)
	publisherOptions.NoRandomize = true
	publisherOptions.Auth.Username = username
	publisherOptions.Auth.Password = password
	publisher := connectForTest(t, publisherOptions, nil)
	defer closeConn(t, publisher)

	subject := "origin.m6.external." + strconv.FormatInt(time.Now().UnixNano(), 10)
	messages := make(chan []byte, 1)
	_, err := subscriber.Subscribe(
		context.Background(),
		subject,
		natsnet.SubscriptionOptions{},
		func(message natsnet.Message) {
			messages <- append([]byte(nil), message.Data...)
		},
	)
	if err != nil {
		t.Fatalf("external Subscribe() error = %v", err)
	}
	if err = publisher.Publish(subject, []byte("external-cluster")); err != nil {
		t.Fatalf("external Publish() error = %v", err)
	}
	if err = publisher.Flush(context.Background()); err != nil {
		t.Fatalf("external Flush() error = %v", err)
	}
	assertMessage(t, messages, []byte("external-cluster"))
}

// TestExternalClusterReconnect 在外部测试程序运行期间由测试操作者停止第一个 NATS 节点。
func TestExternalClusterReconnect(t *testing.T) {
	if os.Getenv("ORIGIN_NATS_RECONNECT_TEST") != "1" {
		t.Skip("设置 ORIGIN_NATS_RECONNECT_TEST=1 后执行外部故障恢复测试")
	}
	urls := externalURLs(t)
	// 只配置第一个 Seed，保证初始连接目标确定；后续可用节点必须来自 NATS 集群发现。
	options := testOptions("integration.external.reconnect", urls[0])
	options.NoRandomize = true
	options.Auth.Username = os.Getenv("ORIGIN_NATS_USERNAME")
	options.Auth.Password = os.Getenv("ORIGIN_NATS_PASSWORD")
	options.Reconnect.Wait = 100 * time.Millisecond
	options.Reconnect.Jitter = 0

	events := make(chan natsnet.Event, 64)
	conn := connectForTest(
		t,
		options,
		func(event natsnet.Event) {
			// 详细记录外部故障测试的低频事件，便于区分 Lame Duck、断开和重连顺序。
			t.Logf(
				"external event type=%d url=%s error=%v",
				event.Type,
				event.URL,
				event.Err,
			)
			events <- event
		},
	)
	defer closeConn(t, conn)

	// 在外部停止节点前确认 Connection 确实位于第一个 Seed，避免测试停止了无关容器。
	connected := waitEvent(t, events, natsnet.EventConnected)
	expectedAddress := strings.TrimPrefix(urls[0], "nats://")
	if !strings.Contains(connected.URL, expectedAddress) {
		t.Fatalf("初始连接 URL = %q, want %q", connected.URL, expectedAddress)
	}
	t.Logf("EXTERNAL_RECONNECT_READY connected=%s", connected.URL)

	// 外部操作者应在 READY 后停止第一个 URL 对应节点；客户端必须发现断开并连接其他节点。
	waitEvent(t, events, natsnet.EventDisconnected)
	waitEvent(t, events, natsnet.EventReconnected)

	subject := "origin.m6.external.reconnect." +
		strconv.FormatInt(time.Now().UnixNano(), 10)
	received := make(chan struct{}, 1)
	_, err := conn.Subscribe(
		context.Background(),
		subject,
		natsnet.SubscriptionOptions{},
		func(natsnet.Message) { received <- struct{}{} },
	)
	if err != nil {
		t.Fatalf("reconnect Subscribe() error = %v", err)
	}
	if err = conn.Publish(subject, []byte("after-reconnect")); err != nil {
		t.Fatalf("reconnect Publish() error = %v", err)
	}
	select {
	case <-received:
	case <-time.After(20 * time.Second):
		t.Fatal("重连后未收到消息")
	}
}

// defaultServerOptions 返回测试使用的最小真实 NATS Server 配置。
func defaultServerOptions() *server.Options {
	// 随机端口、关闭日志和信号处理，确保测试实例彼此隔离且可完整回收。
	return &server.Options{
		Host:   "127.0.0.1",
		Port:   -1,
		NoLog:  true,
		NoSigs: true,
	}
}

// startServer 启动随机端口 Server，并把关闭注册到当前测试。
func startServer(t *testing.T, options *server.Options) *server.Server {
	t.Helper()

	running := startServerManual(t, options)
	t.Cleanup(func() {
		shutdownServer(running)
	})
	return running
}

// startServerManual 启动需要由调用测试明确控制停止时机的 Server。
func startServerManual(t *testing.T, options *server.Options) *server.Server {
	t.Helper()

	running, err := server.NewServer(options)
	if err != nil {
		t.Fatalf("server.NewServer() error = %v", err)
	}
	running.Start()
	if !running.ReadyForConnections(integrationTimeout) {
		running.Shutdown()
		t.Fatal("NATS Server 未在期限内就绪")
	}
	return running
}

// shutdownServer 幂等停止测试 Server 并等待内部 goroutine 退出。
func shutdownServer(running *server.Server) {
	// Shutdown 和 WaitForShutdown 是进程内 Server 的完整资源回收边界。
	if running == nil {
		return
	}
	running.Shutdown()
	running.WaitForShutdown()
}

// testOptions 缩短测试等待，同时保留与生产默认值一致的容量边界。
func testOptions(name string, urls ...string) natsnet.Options {
	// 每个测试使用独占 Name，便于失败时在 Server 连接快照中定位。
	options := natsnet.DefaultOptions(name, urls...)
	options.ConnectTimeout = time.Second
	options.DefaultOperationTimeout = integrationTimeout
	options.DrainTimeout = integrationTimeout
	options.PingInterval = 100 * time.Millisecond
	options.Reconnect.Wait = 20 * time.Millisecond
	options.Reconnect.Jitter = 5 * time.Millisecond
	options.Reconnect.TLSJitter = 5 * time.Millisecond
	return options
}

// connectForTest 建立连接，并在失败时立即终止当前测试。
func connectForTest(
	t *testing.T,
	options natsnet.Options,
	handler natsnet.EventHandler,
) *natsnet.Conn {
	t.Helper()

	conn, err := natsnet.Connect(context.Background(), options, handler)
	if err != nil {
		t.Fatalf("natsnet.Connect() error = %v", err)
	}
	return conn
}

// closeConn 立即关闭连接并验证 Wait 能够完成。
func closeConn(t *testing.T, conn *natsnet.Conn) {
	t.Helper()

	if conn == nil || conn.Status() == natsnet.StatusClosed {
		return
	}
	conn.Close()
	waitCtx, cancel := context.WithTimeout(context.Background(), integrationTimeout)
	defer cancel()
	if err := conn.Wait(waitCtx); !errors.Is(err, errs.ErrTransportClosed) {
		t.Errorf("Close 后 Wait() error = %v", err)
	}
}

// assertMessage 在固定期限内读取并比较一条消息。
func assertMessage(t *testing.T, messages <-chan []byte, expected []byte) {
	t.Helper()

	select {
	case actual := <-messages:
		if !bytes.Equal(actual, expected) {
			t.Fatalf("message = %q, want %q", actual, expected)
		}
	case <-time.After(integrationTimeout):
		t.Fatalf("未收到消息 %q", expected)
	}
}

// waitFor 在固定期限内轮询最终一致的异步条件。
func waitFor(t *testing.T, condition func() bool) {
	t.Helper()

	deadline := time.Now().Add(integrationTimeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("异步条件未在期限内成立")
}

// waitEvent 跳过无关事件并等待指定生命周期事件。
func waitEvent(
	t *testing.T,
	events <-chan natsnet.Event,
	expected natsnet.EventType,
) natsnet.Event {
	t.Helper()

	timeout := time.NewTimer(20 * time.Second)
	defer timeout.Stop()
	for {
		select {
		case event := <-events:
			if event.Type == expected {
				return event
			}
		case <-timeout.C:
			t.Fatalf("未收到事件 %v", expected)
		}
	}
}

// freePort 短暂绑定本机随机端口并返回端口号，供同端口重启测试使用。
func freePort(t *testing.T) int {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen() error = %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err = listener.Close(); err != nil {
		t.Fatalf("Listener Close() error = %v", err)
	}
	return port
}

// externalURLs 读取逗号分隔的外部集群地址；未配置时跳过测试。
func externalURLs(t *testing.T) []string {
	t.Helper()

	raw := os.Getenv("ORIGIN_NATS_URLS")
	if raw == "" {
		t.Skip("未设置 ORIGIN_NATS_URLS，跳过外部三节点集群测试")
	}
	parts := strings.Split(raw, ",")
	urls := parts[:0]
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			urls = append(urls, trimmed)
		}
	}
	if len(urls) < 2 {
		t.Fatal("ORIGIN_NATS_URLS 至少需要两个地址")
	}
	return urls
}
