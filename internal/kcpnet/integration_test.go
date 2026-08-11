package kcpnet

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	kcplib "github.com/xtaci/kcp-go/v5"

	"github.com/duanhf2012/origin/v3/errs"
	"github.com/duanhf2012/origin/v3/internal/bufferpool"
	"github.com/duanhf2012/origin/v3/internal/bytebudget"
	"github.com/duanhf2012/origin/v3/internal/lengthframe"
	originlog "github.com/duanhf2012/origin/v3/log"
)

const kcpTestTimeout = 5 * time.Second

type recordingHandler struct {
	opened    chan *Conn
	messages  chan []byte
	closed    chan error
	onOpen    func(*Conn)
	onMessage func(*Conn, *bufferpool.Buffer) error

	mu     sync.Mutex
	events []string
}

func newRecordingHandler() *recordingHandler {
	return &recordingHandler{
		opened:   make(chan *Conn, 2),
		messages: make(chan []byte, 4),
		closed:   make(chan error, 2),
	}
}

func (handler *recordingHandler) OnOpen(conn *Conn) {
	handler.record("open")
	if handler.onOpen != nil {
		handler.onOpen(conn)
	}
	select {
	case handler.opened <- conn:
	default:
	}
}

func (handler *recordingHandler) OnMessage(conn *Conn, packet *bufferpool.Buffer) error {
	handler.record("message")
	if handler.onMessage != nil {
		return handler.onMessage(conn, packet)
	}
	payload := append([]byte(nil), packet.Bytes()...)
	packet.Release()
	handler.messages <- payload
	return nil
}

func (handler *recordingHandler) OnClose(_ *Conn, cause error) {
	handler.record("close")
	handler.closed <- cause
}

func (handler *recordingHandler) record(event string) {
	handler.mu.Lock()
	handler.events = append(handler.events, event)
	handler.mu.Unlock()
}

func (handler *recordingHandler) snapshot() []string {
	handler.mu.Lock()
	defer handler.mu.Unlock()
	return append([]string(nil), handler.events...)
}

func TestListenerDialRoundTripFECEncryptionAndCleanup(t *testing.T) {
	tests := []struct {
		name  string
		order lengthframe.ByteOrder
		fec   FECOptions
		block bool
		data  string
	}{
		{name: "plain big endian", order: lengthframe.BigEndian, data: "round-trip"},
		{name: "empty message", order: lengthframe.LittleEndian},
		{name: "fec little endian", order: lengthframe.LittleEndian, fec: FECOptions{DataShards: 4, ParityShards: 2}, data: "round-trip"},
		{name: "encrypted and fec", order: lengthframe.BigEndian, fec: FECOptions{DataShards: 4, ParityShards: 2}, block: true, data: "round-trip"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var block kcplib.BlockCrypt
			if test.block {
				var err error
				block, err = kcplib.NewAESBlockCrypt([]byte("origin-kcp-key16"))
				if err != nil {
					t.Fatal(err)
				}
			}
			serverPool, serverConnection := testConnectionOptions(t, 1024, test.order)
			clientPool, clientConnection := testConnectionOptions(t, 1024, test.order)
			serverHandler := newRecordingHandler()
			serverHandler.onMessage = func(conn *Conn, packet *bufferpool.Buffer) error {
				if err := conn.Send(packet); err != nil {
					packet.Release()
					return err
				}
				return nil
			}
			listener, err := Listen("127.0.0.1:0", ListenOptions{
				MaxConnections:    4,
				BlockCrypt:        block,
				FEC:               test.fec,
				SocketReadBuffer:  256 * 1024,
				SocketWriteBuffer: 256 * 1024,
				Connection:        serverConnection,
			}, serverHandler)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), kcpTestTimeout)
			defer cancel()
			t.Cleanup(func() { _ = listener.Close(context.Background()) })

			clientHandler := newRecordingHandler()
			clientHandler.onOpen = func(conn *Conn) {
				packet := clientPool.Acquire(len(test.data))
				copy(packet.Bytes(), test.data)
				if err := conn.Send(packet); err != nil {
					packet.Release()
					t.Errorf("OnOpen Send error=%v", err)
				}
			}
			client, err := Dial(ctx, listener.Addr().String(), DialOptions{
				BlockCrypt:        block,
				FEC:               test.fec,
				SocketReadBuffer:  256 * 1024,
				SocketWriteBuffer: 256 * 1024,
				Connection:        clientConnection,
			}, clientHandler)
			if err != nil {
				t.Fatal(err)
			}
			if client.LocalAddr() == nil || client.RemoteAddr() == nil ||
				client.Done() == nil || client.Cause() != nil || !client.Writable() {
				t.Fatalf("client facade invalid: local=%v remote=%v cause=%v writable=%v",
					client.LocalAddr(), client.RemoteAddr(), client.Cause(), client.Writable())
			}
			if stats := client.SendStats(); stats.Closed || !stats.Writable {
				t.Fatalf("client send stats=%+v", stats)
			}
			select {
			case payload := <-clientHandler.messages:
				if string(payload) != test.data {
					t.Fatalf("payload=%q", payload)
				}
			case <-ctx.Done():
				t.Fatal("等待 KCP 回环消息超时")
			}
			client.Close()
			if err := client.Wait(ctx); !errors.Is(err, errs.ErrTransportClosed) {
				t.Fatalf("client Wait=%v", err)
			}
			if client.Cause() == nil || client.Writable() {
				t.Fatalf("client terminal cause=%v writable=%v", client.Cause(), client.Writable())
			}
			if err := listener.Close(ctx); err != nil {
				t.Fatal(err)
			}
			if listener.RejectedConnections() != 0 {
				t.Fatalf("unexpected rejected=%d", listener.RejectedConnections())
			}
			assertPoolEmpty(t, serverPool)
			assertPoolEmpty(t, clientPool)
			if got := clientHandler.snapshot(); len(got) != 3 || got[0] != "open" ||
				got[1] != "message" || got[2] != "close" {
				t.Fatalf("client events=%v", got)
			}
		})
	}
}

// TestMismatchedBlockCryptNeverDeliversPayload 验证错误密钥不会把不可认证的数据交给业务层。
func TestMismatchedBlockCryptNeverDeliversPayload(t *testing.T) {
	serverBlock, err := kcplib.NewAESBlockCrypt([]byte("0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	clientBlock, err := kcplib.NewAESBlockCrypt([]byte("fedcba9876543210"))
	if err != nil {
		t.Fatal(err)
	}
	serverPool, serverConnection := testConnectionOptions(t, 64, lengthframe.BigEndian)
	serverConnection.ReadTimeout = 80 * time.Millisecond
	serverHandler := newRecordingHandler()
	listener, err := Listen("127.0.0.1:0", ListenOptions{
		MaxConnections: 1,
		BlockCrypt:     serverBlock,
		Connection:     serverConnection,
	}, serverHandler)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), kcpTestTimeout)
	defer cancel()

	clientPool, clientConnection := testConnectionOptions(t, 64, lengthframe.BigEndian)
	clientConnection.ReadTimeout = 80 * time.Millisecond
	clientHandler := newRecordingHandler()
	clientHandler.onOpen = func(conn *Conn) {
		packet := clientPool.Acquire(len("secret"))
		copy(packet.Bytes(), "secret")
		if err := conn.Send(packet); err != nil {
			packet.Release()
		}
	}
	client, err := Dial(ctx, listener.Addr().String(), DialOptions{
		BlockCrypt: clientBlock,
		Connection: clientConnection,
	}, clientHandler)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case cause := <-clientHandler.closed:
		if !errors.Is(cause, errs.ErrDeadlineExceeded) {
			t.Fatalf("client close cause=%v", cause)
		}
	case <-ctx.Done():
		t.Fatal("等待错误 KCP 密钥读空闲关闭超时")
	}
	select {
	case payload := <-serverHandler.messages:
		t.Fatalf("错误密钥向服务端交付了 payload=%q", payload)
	default:
	}
	client.Close()
	_ = client.Wait(ctx)
	if err := listener.Close(ctx); err != nil {
		t.Fatal(err)
	}
	assertPoolEmpty(t, serverPool)
	assertPoolEmpty(t, clientPool)
}

func TestReadIdleTimeoutClosesSilentSession(t *testing.T) {
	_, serverConnection := testConnectionOptions(t, 64, lengthframe.BigEndian)
	serverConnection.ReadTimeout = 50 * time.Millisecond
	serverHandler := newRecordingHandler()
	listener, err := Listen("127.0.0.1:0", ListenOptions{
		MaxConnections: 1,
		Connection:     serverConnection,
	}, serverHandler)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), kcpTestTimeout)
	defer cancel()
	defer listener.Close(ctx)
	clientPool, clientConnection := testConnectionOptions(t, 64, lengthframe.BigEndian)
	clientHandler := newRecordingHandler()
	clientHandler.onOpen = func(conn *Conn) {
		packet := clientPool.Acquire(1)
		packet.Bytes()[0] = 1
		if err := conn.Send(packet); err != nil {
			packet.Release()
		}
	}
	client, err := Dial(ctx, listener.Addr().String(), DialOptions{
		Connection: clientConnection,
	}, clientHandler)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	select {
	case cause := <-serverHandler.closed:
		if !errors.Is(cause, errs.ErrDeadlineExceeded) {
			t.Fatalf("server close cause=%v", cause)
		}
	case <-ctx.Done():
		t.Fatal("等待 KCP 读空闲关闭超时")
	}
}

// TestListenerRejectsSessionBeyondCapacity 验证 KCP Listener 在活动连接达到硬上限后拒绝新 Session，
// 同时保持已准入连接可继续通信。KCP 没有握手，因此拒绝结果以服务端统计为准。
func TestListenerRejectsSessionBeyondCapacity(t *testing.T) {
	serverPool, serverConnection := testConnectionOptions(t, 64, lengthframe.BigEndian)
	serverHandler := newRecordingHandler()
	serverHandler.onMessage = func(conn *Conn, packet *bufferpool.Buffer) error {
		if err := conn.Send(packet); err != nil {
			packet.Release()
			return err
		}
		return nil
	}
	listener, err := Listen("127.0.0.1:0", ListenOptions{
		MaxConnections: 1,
		Connection:     serverConnection,
	}, serverHandler)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), kcpTestTimeout)
	defer cancel()

	firstPool, firstConnection := testConnectionOptions(t, 64, lengthframe.BigEndian)
	firstHandler := newRecordingHandler()
	firstHandler.onOpen = func(conn *Conn) {
		packet := firstPool.Acquire(len("first"))
		copy(packet.Bytes(), "first")
		if err := conn.Send(packet); err != nil {
			packet.Release()
		}
	}
	first, err := Dial(ctx, listener.Addr().String(), DialOptions{Connection: firstConnection}, firstHandler)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case payload := <-firstHandler.messages:
		if string(payload) != "first" {
			t.Fatalf("first payload=%q", payload)
		}
	case <-ctx.Done():
		t.Fatal("等待首条 KCP Session 回环超时")
	}

	secondPool, secondConnection := testConnectionOptions(t, 64, lengthframe.BigEndian)
	secondHandler := newRecordingHandler()
	secondHandler.onOpen = func(conn *Conn) {
		packet := secondPool.Acquire(len("second"))
		copy(packet.Bytes(), "second")
		if err := conn.Send(packet); err != nil {
			packet.Release()
		}
	}
	second, err := Dial(ctx, listener.Addr().String(), DialOptions{Connection: secondConnection}, secondHandler)
	if err != nil {
		t.Fatal(err)
	}
	for listener.RejectedConnections() == 0 {
		select {
		case <-time.After(time.Millisecond):
		case <-ctx.Done():
			t.Fatal("KCP Listener 未记录超容量拒绝")
		}
	}
	second.Close()
	_ = second.Wait(ctx)
	first.Close()
	_ = first.Wait(ctx)
	if err := listener.Close(ctx); err != nil {
		t.Fatal(err)
	}
	assertPoolEmpty(t, serverPool)
	assertPoolEmpty(t, firstPool)
	assertPoolEmpty(t, secondPool)
}

// TestListenerCloseReportsLocalCloseToSessions 防止共享 UDP socket 的关闭错误抢先覆盖本地主动停止语义。
func TestListenerCloseReportsLocalCloseToSessions(t *testing.T) {
	serverPool, serverConnection := testConnectionOptions(t, 64, lengthframe.BigEndian)
	serverHandler := newRecordingHandler()
	listener, err := Listen("127.0.0.1:0", ListenOptions{
		MaxConnections: 1,
		Connection:     serverConnection,
	}, serverHandler)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), kcpTestTimeout)
	defer cancel()

	clientPool, clientConnection := testConnectionOptions(t, 64, lengthframe.BigEndian)
	clientHandler := newRecordingHandler()
	clientHandler.onOpen = func(conn *Conn) {
		packet := clientPool.Acquire(1)
		packet.Bytes()[0] = 1
		if err := conn.Send(packet); err != nil {
			packet.Release()
		}
	}
	client, err := Dial(ctx, listener.Addr().String(), DialOptions{Connection: clientConnection}, clientHandler)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-serverHandler.messages:
	case <-ctx.Done():
		t.Fatal("等待 KCP Server 准入 Session 超时")
	}
	if err := listener.Close(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case cause := <-serverHandler.closed:
		if !errors.Is(cause, errs.ErrTransportClosed) {
			t.Fatalf("server close cause=%v", cause)
		}
	case <-ctx.Done():
		t.Fatal("等待 KCP Server Session 关闭超时")
	}
	client.Close()
	_ = client.Wait(ctx)
	assertPoolEmpty(t, serverPool)
	assertPoolEmpty(t, clientPool)
}

func testConnectionOptions(
	t testing.TB,
	maxMessageSize int,
	order lengthframe.ByteOrder,
) (*bufferpool.Pool, ConnectionOptions) {
	t.Helper()
	pool := bufferpool.NewPool(bufferpool.Options{TrackUsage: true})
	budget, err := bytebudget.New(1024 * 1024)
	if err != nil {
		t.Fatal(err)
	}
	return pool, ConnectionOptions{
		Pool:   pool,
		Logger: originlog.NewNop(),
		Frame: FrameOptions{
			LengthFieldSize: 4,
			ByteOrder:       order,
		},
		Protocol: ProtocolOptions{
			MTU: 1400, SendWindow: 128, ReceiveWindow: 128,
			NoDelay: NoDelayOptions{
				Enabled: true, Interval: 10 * time.Millisecond, FastResend: 2,
				DisableCongestionControl: true,
			},
		},
		MaxMessageSize: maxMessageSize, SendQueueMessages: 8,
		SendQueueBytes: 1024 * 1024, SendBudget: budget,
		ReadTimeout: time.Second, WriteTimeout: time.Second, SlowClientTimeout: time.Second,
	}
}

func assertPoolEmpty(t testing.TB, pool *bufferpool.Pool) {
	t.Helper()
	if stats := pool.Stats(); stats.InUseBuffers != 0 || stats.InUseCapacityBytes != 0 {
		t.Fatalf("Pool 未配平：%+v", stats)
	}
}
