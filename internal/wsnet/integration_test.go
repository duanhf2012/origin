package wsnet

import (
	"context"
	"crypto/tls"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	gorillaws "github.com/gorilla/websocket"

	"github.com/duanhf2012/origin/v3/errs"
	"github.com/duanhf2012/origin/v3/internal/bufferpool"
	"github.com/duanhf2012/origin/v3/internal/bytebudget"
	originlog "github.com/duanhf2012/origin/v3/log"
)

const wsTestTimeout = 5 * time.Second

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

func TestListenerDialBinaryRoundTripAndCleanup(t *testing.T) {
	serverPool, serverOptions := testConnectionOptions(t, BinaryMessage, 1024)
	clientPool, clientOptions := testConnectionOptions(t, BinaryMessage, 1024)
	serverHandler := newRecordingHandler()
	serverHandler.onMessage = func(conn *Conn, packet *bufferpool.Buffer) error {
		if err := conn.Send(packet); err != nil {
			packet.Release()
			return err
		}
		return nil
	}
	listener, err := Listen("127.0.0.1:0", ListenOptions{
		MaxConnections: 4, Path: "/ws", HandshakeTimeout: time.Second,
		Connection: serverOptions,
	}, serverHandler)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), wsTestTimeout)
	defer cancel()
	t.Cleanup(func() { _ = listener.Close(context.Background()) })

	clientHandler := newRecordingHandler()
	clientHandler.onOpen = func(conn *Conn) {
		packet := clientPool.Acquire(len("round-trip"))
		copy(packet.Bytes(), "round-trip")
		if err := conn.Send(packet); err != nil {
			packet.Release()
			t.Errorf("OnOpen Send error=%v", err)
		}
	}
	client, err := Dial(ctx, "ws://"+listener.Addr().String()+"/ws", DialOptions{
		HandshakeTimeout: time.Second,
		Connection:       clientOptions,
	}, clientHandler)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case payload := <-clientHandler.messages:
		if string(payload) != "round-trip" {
			t.Fatalf("payload=%q", payload)
		}
	case <-ctx.Done():
		t.Fatal("等待 WebSocket 回环消息超时")
	}
	for {
		stats := client.SendStats()
		if stats.SentMessages == 1 && stats.SentBytes == 10 {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatalf("client stats=%+v", stats)
		case <-time.After(time.Millisecond):
		}
	}
	client.Close()
	if err := client.Wait(ctx); !errors.Is(err, errs.ErrTransportClosed) {
		t.Fatalf("client Wait=%v", err)
	}
	if err := listener.Close(ctx); err != nil {
		t.Fatal(err)
	}
	assertPoolEmpty(t, serverPool)
	assertPoolEmpty(t, clientPool)
	if got := clientHandler.snapshot(); len(got) != 3 || got[0] != "open" ||
		got[1] != "message" || got[2] != "close" {
		t.Fatalf("client events=%v", got)
	}
}

func TestListenerDefaultOriginAndPathPolicy(t *testing.T) {
	_, options := testConnectionOptions(t, BinaryMessage, 1024)
	listener, err := Listen("127.0.0.1:0", ListenOptions{
		MaxConnections: 2, Path: "/socket", HandshakeTimeout: time.Second,
		Connection: options,
	}, newRecordingHandler())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), wsTestTimeout)
	defer cancel()
	defer listener.Close(ctx)
	url := "ws://" + listener.Addr().String()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+listener.Addr().String()+"/missing", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("wrong path status=%d", response.StatusCode)
	}

	header := http.Header{"Origin": []string{"https://cross-origin.example"}}
	raw, response, err := gorillaws.DefaultDialer.DialContext(ctx, url+"/socket", header)
	if raw != nil {
		_ = raw.Close()
	}
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}
	if err == nil || response == nil || response.StatusCode != http.StatusForbidden {
		t.Fatalf("cross origin=(response=%v err=%v)", response, err)
	}

	header.Set("Origin", "http://"+listener.Addr().String())
	raw, _, err = gorillaws.DefaultDialer.DialContext(ctx, url+"/socket", header)
	if err != nil {
		t.Fatalf("same origin dial=%v", err)
	}
	_ = raw.Close()
}

func TestTextTypeMismatchAndInvalidUTF8(t *testing.T) {
	serverPool, serverOptions := testConnectionOptions(t, TextMessage, 64)
	serverHandler := newRecordingHandler()
	listener, err := Listen("127.0.0.1:0", ListenOptions{
		MaxConnections: 2, Path: "/ws", HandshakeTimeout: time.Second,
		Connection: serverOptions,
	}, serverHandler)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), wsTestTimeout)
	defer cancel()
	defer listener.Close(ctx)

	clientPool, clientOptions := testConnectionOptions(t, BinaryMessage, 64)
	clientHandler := newRecordingHandler()
	clientHandler.onOpen = func(conn *Conn) {
		packet := clientPool.Acquire(1)
		packet.Bytes()[0] = 1
		if err := conn.Send(packet); err != nil {
			packet.Release()
		}
	}
	client, err := Dial(ctx, "ws://"+listener.Addr().String()+"/ws", DialOptions{
		HandshakeTimeout: time.Second, Connection: clientOptions,
	}, clientHandler)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case cause := <-serverHandler.closed:
		if !errors.Is(cause, errs.ErrTransportProtocol) {
			t.Fatalf("mismatch cause=%v", cause)
		}
	case <-ctx.Done():
		t.Fatal("等待类型不匹配关闭超时")
	}
	client.Close()
	_ = client.Wait(ctx)

	_, textOptions := testConnectionOptions(t, TextMessage, 64)
	rawHandler := newRecordingHandler()
	textClient, err := Dial(ctx, "ws://"+listener.Addr().String()+"/ws", DialOptions{
		HandshakeTimeout: time.Second, Connection: textOptions,
	}, rawHandler)
	if err != nil {
		t.Fatal(err)
	}
	invalid := textOptions.Pool.Acquire(1)
	invalid.Bytes()[0] = 0xff
	if err := textClient.Send(invalid); !errors.Is(err, errs.ErrTransportProtocol) {
		t.Fatalf("invalid UTF-8 Send=%v", err)
	}
	invalid.Release()
	textClient.Close()
	_ = textClient.Wait(ctx)
	assertPoolEmpty(t, serverPool)
}

func TestPingPongTimeoutClosesSilentPeer(t *testing.T) {
	_, options := testConnectionOptions(t, BinaryMessage, 1024)
	options.PingInterval = 20 * time.Millisecond
	options.PongTimeout = 60 * time.Millisecond
	handler := newRecordingHandler()
	listener, err := Listen("127.0.0.1:0", ListenOptions{
		MaxConnections: 1, Path: "/ws", HandshakeTimeout: time.Second,
		Connection: options,
	}, handler)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), wsTestTimeout)
	defer cancel()
	defer listener.Close(ctx)
	raw, _, err := gorillaws.DefaultDialer.DialContext(
		ctx,
		"ws://"+listener.Addr().String()+"/ws",
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	select {
	case cause := <-handler.closed:
		if !errors.Is(cause, errs.ErrDeadlineExceeded) {
			t.Fatalf("silent peer cause=%v", cause)
		}
	case <-ctx.Done():
		t.Fatal("等待 Pong 超时关闭失败")
	}
}

func TestMessageLimitAndConnectionAdmission(t *testing.T) {
	_, options := testConnectionOptions(t, BinaryMessage, 64)
	handler := newRecordingHandler()
	listener, err := Listen("127.0.0.1:0", ListenOptions{
		MaxConnections: 1, Path: "/ws", HandshakeTimeout: time.Second,
		Connection: options,
	}, handler)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), wsTestTimeout)
	defer cancel()
	defer listener.Close(ctx)
	url := "ws://" + listener.Addr().String() + "/ws"
	first, _, err := gorillaws.DefaultDialer.DialContext(ctx, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	select {
	case <-handler.opened:
	case <-ctx.Done():
		t.Fatal("第一条连接未完成 OnOpen")
	}
	second, response, err := gorillaws.DefaultDialer.DialContext(ctx, url, nil)
	if second != nil {
		_ = second.Close()
	}
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}
	if err == nil || response == nil || response.StatusCode != http.StatusServiceUnavailable ||
		listener.RejectedConnections() != 1 {
		t.Fatalf("capacity=(response=%v err=%v rejected=%d)", response, err, listener.RejectedConnections())
	}

	if err := first.WriteMessage(gorillaws.BinaryMessage, make([]byte, 65)); err != nil {
		t.Fatal(err)
	}
	select {
	case cause := <-handler.closed:
		if !errors.Is(cause, errs.ErrTransportMessageTooLarge) {
			t.Fatalf("oversize cause=%v", cause)
		}
	case <-ctx.Done():
		t.Fatal("等待超大消息关闭超时")
	}
}

func TestTLSHeaderAndSubprotocol(t *testing.T) {
	source := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	certificate := source.TLS.Certificates[0]
	source.Close()
	serverTLS := &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12}
	clientTLS := &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12} // 仅测试自签名证书。

	_, serverOptions := testConnectionOptions(t, BinaryMessage, 1024)
	headerSeen := make(chan string, 1)
	listener, err := Listen("127.0.0.1:0", ListenOptions{
		MaxConnections: 1, Path: "/ws", HandshakeTimeout: time.Second,
		CheckOrigin: func(request *http.Request) bool {
			headerSeen <- request.Header.Get("X-Origin-Test")
			return true
		},
		Subprotocols: []string{"origin.v1"}, TLSConfig: serverTLS,
		Connection: serverOptions,
	}, newRecordingHandler())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), wsTestTimeout)
	defer cancel()
	defer listener.Close(ctx)
	_, clientOptions := testConnectionOptions(t, BinaryMessage, 1024)
	conn, err := Dial(ctx, "wss://"+listener.Addr().String()+"/ws", DialOptions{
		HandshakeTimeout: time.Second,
		Header:           http.Header{"X-Origin-Test": []string{"present"}},
		Subprotocols:     []string{"origin.v1"},
		TLSConfig:        clientTLS,
		Connection:       clientOptions,
	}, newRecordingHandler())
	if err != nil {
		t.Fatal(err)
	}
	if got := conn.raw.Subprotocol(); got != "origin.v1" {
		t.Fatalf("subprotocol=%q", got)
	}
	select {
	case got := <-headerSeen:
		if got != "present" {
			t.Fatalf("header=%q", got)
		}
	case <-ctx.Done():
		t.Fatal("未观察到握手 Header")
	}
	conn.Close()
	_ = conn.Wait(ctx)
}

func testConnectionOptions(
	t testing.TB,
	messageType MessageType,
	maxMessageSize int,
) (*bufferpool.Pool, ConnectionOptions) {
	t.Helper()
	pool := bufferpool.NewPool(bufferpool.Options{TrackUsage: true})
	budget, err := bytebudget.New(1024 * 1024)
	if err != nil {
		t.Fatal(err)
	}
	return pool, ConnectionOptions{
		Pool: pool, Logger: originlog.NewNop(), MessageType: messageType,
		MaxMessageSize: maxMessageSize, SendQueueMessages: 8,
		SendQueueBytes: 1024 * 1024, SendBudget: budget,
		WriteTimeout: time.Second, SlowClientTimeout: time.Second,
	}
}

func assertPoolEmpty(t testing.TB, pool *bufferpool.Pool) {
	t.Helper()
	if stats := pool.Stats(); stats.InUseBuffers != 0 || stats.InUseCapacityBytes != 0 {
		t.Fatalf("Pool 未配平：%+v", stats)
	}
}
