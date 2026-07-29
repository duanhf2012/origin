package tcpnet_test

import (
	"context"
	"encoding/binary"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/duanhf2012/origin/v3/errs"
	"github.com/duanhf2012/origin/v3/internal/bufferpool"
	"github.com/duanhf2012/origin/v3/internal/tcpnet"
)

const integrationTimeout = 10 * time.Second

// echoHandler 把入站 Buffer 的唯一所有权直接转移给同一连接的发送队列。
type echoHandler struct {
	closed chan error
}

// OnOpen 不需要建立额外状态。
func (handler *echoHandler) OnOpen(*tcpnet.Conn) {}

// OnMessage 执行不复制 payload 的 Echo。
func (handler *echoHandler) OnMessage(
	conn *tcpnet.Conn,
	packet *bufferpool.Buffer,
) error {
	// Send 成功后由连接释放；失败时所有权仍在 Handler，必须当场释放。
	if err := conn.Send(packet); err != nil {
		packet.Release()
		return err
	}
	return nil
}

// OnClose 发布服务端连接终态，供测试等待所有连接退出。
func (handler *echoHandler) OnClose(_ *tcpnet.Conn, cause error) {
	handler.closed <- cause
}

// clientHandler 验证每条回包内容并负责释放入站 Buffer。
type clientHandler struct {
	mu       sync.Mutex
	expected map[uint32]struct{}
	received chan uint32
	closed   chan error
}

// OnOpen 不需要额外操作。
func (handler *clientHandler) OnOpen(*tcpnet.Conn) {}

// OnMessage 解码固定四字节序号，检查重复后释放所有权。
func (handler *clientHandler) OnMessage(
	_ *tcpnet.Conn,
	packet *bufferpool.Buffer,
) error {
	// 无论校验是否成功都在当前同步回调中归还 Buffer。
	data := packet.Bytes()
	if len(data) != 4 {
		packet.Release()
		return fmt.Errorf("回包长度=%d，期望=4", len(data))
	}
	sequence := binary.BigEndian.Uint32(data)
	packet.Release()

	// 每个客户端 Handler 只服务一条连接，但测试仍用锁保护状态，匹配公共接口约束。
	handler.mu.Lock()
	if _, ok := handler.expected[sequence]; !ok {
		handler.mu.Unlock()
		return fmt.Errorf("收到未知或重复序号=%d", sequence)
	}
	delete(handler.expected, sequence)
	handler.mu.Unlock()
	handler.received <- sequence
	return nil
}

// OnClose 发布客户端连接终态。
func (handler *clientHandler) OnClose(_ *tcpnet.Conn, cause error) {
	handler.closed <- cause
}

func TestConcurrentLoopbackConnections(t *testing.T) {
	// 服务端使用一个共享 Pool 和 Handler，验证不同连接可以并发进入同一适配器。
	serverPool := bufferpool.NewPool(bufferpool.Options{TrackUsage: true})
	serverHandler := &echoHandler{closed: make(chan error, 16)}
	listenOptions := tcpnet.DefaultListenOptions(serverPool)
	listenOptions.MaxConnections = 32
	listenOptions.Connection.MaxMessageSize = 1024
	listenOptions.Connection.SendQueueFrames = 256
	listener, err := tcpnet.Listen(
		"127.0.0.1:0",
		listenOptions,
		serverHandler,
	)
	if err != nil {
		t.Fatalf("Listen 失败：%v", err)
	}
	defer closeListener(t, listener)

	const (
		clientCount       = 8
		messagesPerClient = 100
	)
	clients := make([]*tcpnet.Conn, clientCount)
	clientPools := make([]*bufferpool.Pool, clientCount)
	clientHandlers := make([]*clientHandler, clientCount)

	// 逐个建立真实 TCP 连接；每个客户端拥有独立 Pool，便于精确定位泄漏来源。
	for clientIndex := 0; clientIndex < clientCount; clientIndex++ {
		pool := bufferpool.NewPool(bufferpool.Options{TrackUsage: true})
		handler := &clientHandler{
			expected: make(map[uint32]struct{}, messagesPerClient),
			received: make(chan uint32, messagesPerClient),
			closed:   make(chan error, 1),
		}
		options := tcpnet.DefaultConnectionOptions(pool)
		options.MaxMessageSize = 1024
		options.SendQueueFrames = 256
		ctx, cancel := context.WithTimeout(context.Background(), integrationTimeout)
		conn, dialErr := tcpnet.Dial(
			ctx,
			listener.Addr().String(),
			options,
			handler,
		)
		cancel()
		if dialErr != nil {
			t.Fatalf("客户端 %d Dial 失败：%v", clientIndex, dialErr)
		}
		clients[clientIndex] = conn
		clientPools[clientIndex] = pool
		clientHandlers[clientIndex] = handler
	}

	// 多个 goroutine 并发提交各自序号，覆盖真实 socket 下的队列和 Writer 串行化。
	var sendWait sync.WaitGroup
	sendErrors := make(chan error, clientCount)
	sendWait.Add(clientCount)
	for clientIndex := 0; clientIndex < clientCount; clientIndex++ {
		go func(clientIndex int) {
			defer sendWait.Done()
			pool := clientPools[clientIndex]
			handler := clientHandlers[clientIndex]
			conn := clients[clientIndex]
			for messageIndex := 0; messageIndex < messagesPerClient; messageIndex++ {
				sequence := uint32(clientIndex*messagesPerClient + messageIndex)
				handler.mu.Lock()
				handler.expected[sequence] = struct{}{}
				handler.mu.Unlock()

				packet := pool.Acquire(4)
				binary.BigEndian.PutUint32(packet.Bytes(), sequence)
				if sendErr := conn.Send(packet); sendErr != nil {
					// Send 失败未转移所有权，测试负责释放并报告。
					packet.Release()
					sendErrors <- fmt.Errorf(
						"客户端 %d 序号 %d Send：%w",
						clientIndex,
						sequence,
						sendErr,
					)
					return
				}
			}
		}(clientIndex)
	}
	sendWait.Wait()
	close(sendErrors)
	for sendErr := range sendErrors {
		t.Error(sendErr)
	}
	if t.Failed() {
		return
	}

	// 等待每条连接收齐全部回包，验证连接内消息无丢失、重复或乱序破坏。
	deadline := time.After(integrationTimeout)
	for clientIndex, handler := range clientHandlers {
		for received := 0; received < messagesPerClient; received++ {
			select {
			case <-handler.received:
			case <-deadline:
				t.Fatalf(
					"等待客户端 %d 回包超时，已收到=%d",
					clientIndex,
					received,
				)
			}
		}
		handler.mu.Lock()
		remaining := len(handler.expected)
		handler.mu.Unlock()
		if remaining != 0 {
			t.Fatalf("客户端 %d 仍缺少 %d 条回包", clientIndex, remaining)
		}
	}

	// 客户端主动关闭并等待各自 Writer/ReadLoop 清理，服务端随后观察到远端断开。
	for _, conn := range clients {
		conn.Close()
		if err := waitConn(conn); !errs.IsCode(err, errs.CodeTransportClosed) {
			t.Fatalf("客户端 Close 终态=%v", err)
		}
	}
	for closed := 0; closed < clientCount; closed++ {
		select {
		case cause := <-serverHandler.closed:
			if !errs.IsCode(cause, errs.CodeTransportUnavailable) {
				t.Errorf("服务端连接终态=%v", cause)
			}
		case <-time.After(integrationTimeout):
			t.Fatal("等待服务端连接关闭超时")
		}
	}

	// 所有连接完成后，服务端和每个客户端 Pool 都必须归零。
	assertPoolEmpty(t, serverPool)
	for index, pool := range clientPools {
		if stats := pool.Stats(); stats.InUseBuffers != 0 ||
			stats.InUseCapacityBytes != 0 {
			t.Fatalf("客户端 %d Pool 未归零：%+v", index, stats)
		}
	}
}

// waitConn 使用统一 Deadline 等待完整连接清理。
func waitConn(conn *tcpnet.Conn) error {
	// 集成测试不接受永久等待，因此每次调用都创建独立 Context。
	ctx, cancel := context.WithTimeout(context.Background(), integrationTimeout)
	defer cancel()
	return conn.Wait(ctx)
}

// closeListener 幂等关闭 Listener；defer 和显式调用都可安全使用。
func closeListener(t *testing.T, listener *tcpnet.Listener) {
	t.Helper()

	// Listener.Close 自带幂等状态，本辅助函数只统一测试 Deadline 和失败格式。
	ctx, cancel := context.WithTimeout(context.Background(), integrationTimeout)
	defer cancel()
	if err := listener.Close(ctx); err != nil {
		t.Fatalf("Listener.Close 失败：%v", err)
	}
}

// assertPoolEmpty 验证开启统计的 Pool 已无活跃所有权。
func assertPoolEmpty(t *testing.T, pool *bufferpool.Pool) {
	t.Helper()

	// 同时检查数量与容量，避免某类统计错误被另一项掩盖。
	stats := pool.Stats()
	if !stats.Enabled ||
		stats.InUseBuffers != 0 ||
		stats.InUseCapacityBytes != 0 {
		t.Fatalf("Pool 未归零：%+v", stats)
	}
}
