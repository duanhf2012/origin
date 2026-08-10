package tcpnet

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/duanhf2012/origin/v3/errs"
	"github.com/duanhf2012/origin/v3/internal/bufferpool"
)

func TestListenAndDialRoundTrip(t *testing.T) {
	t.Parallel()

	// 服务端直接把收到的 Buffer 唯一所有权转移给发送队列，形成零 payload 拷贝 Echo。
	serverPool := bufferpool.NewPool(bufferpool.Options{TrackUsage: true})
	serverHandler := newRecordingHandler()
	serverHandler.onMessage = func(conn *Conn, packet *bufferpool.Buffer) error {
		if err := conn.Send(packet); err != nil {
			// Send 失败时所有权没有转移，Handler 必须自行释放。
			packet.Release()
			return err
		}
		return nil
	}
	listenOptions := DefaultListenOptions(serverPool)
	listenOptions.Connection.MaxMessageSize = 1024
	listener, err := Listen("127.0.0.1:0", listenOptions, serverHandler)
	if err != nil {
		t.Fatalf("Listen 失败：%v", err)
	}

	// 客户端使用独立 Pool，验证双端所有权不会相互污染。
	clientPool := bufferpool.NewPool(bufferpool.Options{TrackUsage: true})
	clientHandler := newRecordingHandler()
	dialOptions := DefaultConnectionOptions(clientPool)
	dialOptions.MaxMessageSize = 1024
	ctx, cancel := context.WithTimeout(context.Background(), testWaitTimeout)
	defer cancel()
	client, err := Dial(ctx, listener.Addr().String(), dialOptions, clientHandler)
	if err != nil {
		closeListener(t, listener)
		t.Fatalf("Dial 失败：%v", err)
	}

	// 连续发送普通消息和空消息，服务端回显顺序必须保持一致。
	for _, value := range []string{"hello", ""} {
		packet := acquireBytes(clientPool, value)
		if err := client.Send(packet); err != nil {
			packet.Release()
			t.Fatalf("Send(%q) 失败：%v", value, err)
		}
		assertMessage(t, clientHandler.messages, []byte(value))
	}
	if client.LocalAddr() == nil || client.RemoteAddr() == nil {
		t.Fatal("Dial 返回的 Conn 缺少地址")
	}

	// Listener.Close 同时关闭所属客户端服务端连接并等待注销完成。
	closeListener(t, listener)
	if err := waitConn(t, client); !errs.IsCode(err, errs.CodeTransportUnavailable) {
		// 客户端观察到对端关闭，因此归类为不可用而不是本地主动关闭。
		t.Fatalf("客户端终态=%v", err)
	}
	assertPoolEmpty(t, serverPool)
	assertPoolEmpty(t, clientPool)
}

// TestListenerStopAcceptKeepsAcceptedConnections 验证两阶段停止只关闭新连接准入。
func TestListenerStopAcceptKeepsAcceptedConnections(t *testing.T) {
	t.Parallel()

	serverPool := bufferpool.NewPool(bufferpool.Options{TrackUsage: true})
	serverHandler := newRecordingHandler()
	serverHandler.onMessage = func(conn *Conn, packet *bufferpool.Buffer) error {
		if err := conn.Send(packet); err != nil {
			packet.Release()
			return err
		}
		return nil
	}
	listener, err := Listen(
		"127.0.0.1:0",
		DefaultListenOptions(serverPool),
		serverHandler,
	)
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}

	clientPool := bufferpool.NewPool(bufferpool.Options{TrackUsage: true})
	clientHandler := newRecordingHandler()
	ctx, cancel := context.WithTimeout(context.Background(), testWaitTimeout)
	defer cancel()
	client, err := Dial(
		ctx,
		listener.Addr().String(),
		DefaultConnectionOptions(clientPool),
		clientHandler,
	)
	if err != nil {
		closeListener(t, listener)
		t.Fatalf("Dial() error = %v", err)
	}
	waitForConnCount(t, listener, 1)

	// StopAccept 返回时监听 socket 已经结束，但之前接受的连接仍归 Listener 所有。
	if err := listener.StopAccept(ctx); err != nil {
		t.Fatalf("StopAccept() error = %v", err)
	}
	if _, err := Dial(
		ctx,
		listener.Addr().String(),
		DefaultConnectionOptions(
			bufferpool.NewPool(bufferpool.Options{}),
		),
		newRecordingHandler(),
	); !errs.IsCode(err, errs.CodeTransportUnavailable) {
		t.Fatalf("StopAccept 后 Dial error = %v", err)
	}

	packet := acquireBytes(clientPool, "admitted")
	if err := client.Send(packet); err != nil {
		packet.Release()
		t.Fatalf("旧连接 Send() error = %v", err)
	}
	assertMessage(t, clientHandler.messages, []byte("admitted"))

	closeListener(t, listener)
	if err := waitConn(t, client); !errs.IsCode(err, errs.CodeTransportUnavailable) {
		t.Fatalf("旧连接最终状态 = %v", err)
	}
	assertPoolEmpty(t, serverPool)
	assertPoolEmpty(t, clientPool)
}

func TestListenerEnforcesConnectionLimit(t *testing.T) {
	t.Parallel()

	// 限制为一条活动连接，第二个 Accept 到的 socket 应立即关闭且不触发 OnOpen。
	pool := bufferpool.NewPool(bufferpool.Options{TrackUsage: true})
	handler := newRecordingHandler()
	options := DefaultListenOptions(pool)
	options.MaxConnections = 1
	options.Connection.MaxMessageSize = 64
	listener, err := Listen("127.0.0.1:0", options, handler)
	if err != nil {
		t.Fatalf("Listen 失败：%v", err)
	}

	first, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		closeListener(t, listener)
		t.Fatalf("第一个 Dial 失败：%v", err)
	}
	waitForConnCount(t, listener, 1)

	second, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		_ = first.Close()
		closeListener(t, listener)
		t.Fatalf("第二个 Dial 失败：%v", err)
	}
	// 内核握手可能成功，但服务端必须在 Accept 后立即关闭第二条连接。
	if err := second.SetReadDeadline(time.Now().Add(testWaitTimeout)); err != nil {
		t.Fatalf("设置第二连接 Deadline 失败：%v", err)
	}
	var one [1]byte
	if _, err := second.Read(one[:]); err == nil {
		t.Fatal("超过上限的第二条连接没有被关闭")
	}
	if rejected := listener.RejectedConnections(); rejected != 1 {
		t.Fatalf("RejectedConnections=%d want=1", rejected)
	}
	waitForConnCount(t, listener, 1)

	// 释放第一条连接后应重新允许一条连接进入。
	_ = second.Close()
	_ = first.Close()
	waitForConnCount(t, listener, 0)
	third, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		closeListener(t, listener)
		t.Fatalf("容量恢复后 Dial 失败：%v", err)
	}
	waitForConnCount(t, listener, 1)
	_ = third.Close()
	closeListener(t, listener)
	assertPoolEmpty(t, pool)
}

func TestListenerCloseContextDoesNotAbortCleanup(t *testing.T) {
	t.Parallel()

	// 阻塞 OnClose，验证第一次 Close 的 Context 只限制等待，不撤销已经开始的清理。
	pool := bufferpool.NewPool(bufferpool.Options{TrackUsage: true})
	handler := newRecordingHandler()
	releaseClose := make(chan struct{})
	handler.onClose = func(*Conn, error) {
		<-releaseClose
	}
	options := DefaultListenOptions(pool)
	options.Connection.MaxMessageSize = 64
	listener, err := Listen("127.0.0.1:0", options, handler)
	if err != nil {
		t.Fatalf("Listen 失败：%v", err)
	}
	peer, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		close(releaseClose)
		closeListener(t, listener)
		t.Fatalf("Dial 失败：%v", err)
	}
	waitForConnCount(t, listener, 1)

	// 短 Context 应返回 DeadlineExceeded，但 Listener 已不再接收新连接。
	shortContext, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if err := listener.Close(shortContext); !errs.IsCode(err, errs.CodeDeadlineExceeded) {
		t.Fatalf("短 Close error=%v", err)
	}
	if _, err := net.DialTimeout(
		"tcp",
		listener.Addr().String(),
		100*time.Millisecond,
	); err == nil {
		t.Fatal("Close 开始后 Listener 仍接受新连接")
	}

	// 放行 OnClose 后，再次 Close 应等待并返回正常终态。
	close(releaseClose)
	closeListener(t, listener)
	_ = peer.Close()
	assertPoolEmpty(t, pool)
}

func TestListenAndDialValidateArguments(t *testing.T) {
	t.Parallel()

	// 公共入口必须在创建 socket 前拒绝空参数和已取消连接。
	pool := bufferpool.NewPool(bufferpool.Options{})
	listenOptions := DefaultListenOptions(pool)
	handler := newRecordingHandler()
	if _, err := Listen("", listenOptions, handler); !errs.IsCode(err, errs.CodeInvalidArgument) {
		t.Fatalf("Listen 空地址 error=%v", err)
	}
	if _, err := Listen("127.0.0.1:0", listenOptions, nil); !errs.IsCode(err, errs.CodeInvalidArgument) {
		t.Fatalf("Listen nil Handler error=%v", err)
	}
	invalidListenOptions := listenOptions
	invalidListenOptions.MaxConnections = 0
	if _, err := Listen(
		"127.0.0.1:0",
		invalidListenOptions,
		handler,
	); !errs.IsCode(err, errs.CodeInvalidConfig) {
		t.Fatalf("Listen 非法 Options error=%v", err)
	}
	if _, err := Listen(
		"127.0.0.1:not-a-port",
		listenOptions,
		handler,
	); !errs.IsCode(err, errs.CodeTransportUnavailable) {
		t.Fatalf("Listen 非法网络地址 error=%v", err)
	}

	dialOptions := DefaultConnectionOptions(pool)
	if _, err := Dial(nil, "127.0.0.1:1", dialOptions, handler); !errs.IsCode(err, errs.CodeInvalidArgument) {
		t.Fatalf("Dial nil Context error=%v", err)
	}
	if _, err := Dial(context.Background(), "", dialOptions, handler); !errs.IsCode(err, errs.CodeInvalidArgument) {
		t.Fatalf("Dial 空地址 error=%v", err)
	}
	if _, err := Dial(context.Background(), "127.0.0.1:1", dialOptions, nil); !errs.IsCode(err, errs.CodeInvalidArgument) {
		t.Fatalf("Dial nil Handler error=%v", err)
	}
	invalidDialOptions := dialOptions
	invalidDialOptions.SendQueueFrames = 0
	if _, err := Dial(
		context.Background(),
		"127.0.0.1:1",
		invalidDialOptions,
		handler,
	); !errs.IsCode(err, errs.CodeInvalidConfig) {
		t.Fatalf("Dial 非法 Options error=%v", err)
	}
	if _, err := Dial(
		context.Background(),
		"127.0.0.1:not-a-port",
		dialOptions,
		handler,
	); !errs.IsCode(err, errs.CodeTransportUnavailable) {
		t.Fatalf("Dial 非法网络地址 error=%v", err)
	}

	// 使用已经取消的 Context，避免依赖固定端口是否确实无人监听。
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Dial(canceled, "127.0.0.1:1", dialOptions, handler); !errs.IsCode(err, errs.CodeCanceled) {
		t.Fatalf("取消 Dial error=%v", err)
	}
}

func TestListenerCloseIsIdempotentWithoutConnections(t *testing.T) {
	t.Parallel()

	// 无连接 Listener 也必须正确停止 AcceptLoop，重复关闭返回 nil。
	pool := bufferpool.NewPool(bufferpool.Options{TrackUsage: true})
	options := DefaultListenOptions(pool)
	options.Connection.MaxMessageSize = 64
	listener, err := Listen("127.0.0.1:0", options, newRecordingHandler())
	if err != nil {
		t.Fatalf("Listen 失败：%v", err)
	}
	if err := listener.Close(nil); !errs.IsCode(err, errs.CodeInvalidArgument) {
		t.Fatalf("Listener.Close(nil) error=%v", err)
	}
	closeListener(t, listener)
	closeListener(t, listener)
	assertPoolEmpty(t, pool)
}

func TestListenerConcurrentClose(t *testing.T) {
	t.Parallel()

	// 多个 goroutine 同时 Close 只允许一次状态提交和一次底层 socket 关闭。
	pool := bufferpool.NewPool(bufferpool.Options{TrackUsage: true})
	options := DefaultListenOptions(pool)
	options.Connection.MaxMessageSize = 64
	listener, err := Listen("127.0.0.1:0", options, newRecordingHandler())
	if err != nil {
		t.Fatalf("Listen 失败：%v", err)
	}

	const workers = 16
	var wait sync.WaitGroup
	results := make(chan error, workers)
	wait.Add(workers)
	for worker := 0; worker < workers; worker++ {
		go func() {
			defer wait.Done()
			ctx, cancel := context.WithTimeout(context.Background(), testWaitTimeout)
			defer cancel()
			results <- listener.Close(ctx)
		}()
	}
	wait.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Errorf("并发 Close error=%v", err)
		}
	}
	assertPoolEmpty(t, pool)
}

// waitForConnCount 等待 Listener 内部连接集合到达期望值。
func waitForConnCount(t *testing.T, listener *Listener, want int) {
	t.Helper()

	// 短轮询只存在于测试，不进入生产路径；Deadline 防止错误时无限等待。
	deadline := time.Now().Add(testWaitTimeout)
	for {
		listener.mu.Lock()
		got := len(listener.conns)
		listener.mu.Unlock()
		if got == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("Listener 连接数=%d，期望=%d", got, want)
		}
		time.Sleep(time.Millisecond)
	}
}

// closeListener 使用统一有界 Context 关闭并等待 Listener。
func closeListener(t *testing.T, listener *Listener) {
	t.Helper()

	// Listener 正常主动关闭应返回 nil；所有连接清理包含在该等待中。
	ctx, cancel := context.WithTimeout(context.Background(), testWaitTimeout)
	defer cancel()
	if err := listener.Close(ctx); err != nil {
		t.Fatalf("Listener.Close 失败：%v", err)
	}
}
