package websocket_test

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	originlog "github.com/duanhf2012/origin/v3/log"
	"github.com/duanhf2012/origin/v3/node"
	"github.com/duanhf2012/origin/v3/service"
	"github.com/duanhf2012/origin/v3/sysmodule/network"
	"github.com/duanhf2012/origin/v3/sysmodule/network/websocket"
)

type loopbackService struct {
	service.Service
	server *websocket.Server
	client *websocket.Client
}

func (target *loopbackService) OnInit() error {
	if err := target.AddModule(target.server); err != nil {
		return err
	}
	return target.AddModule(target.client)
}

type serverOnlyService struct {
	service.Service
	server *websocket.Server
}

func (target *serverOnlyService) OnInit() error { return target.AddModule(target.server) }

type clientOnlyService struct {
	service.Service
	client *websocket.Client
}

func (target *clientOnlyService) OnInit() error { return target.AddModule(target.client) }

func reserveAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return address
}

func newTestNode(t *testing.T, id, name string, target service.IService) *node.Node {
	t.Helper()
	current, err := node.New(
		node.Config{ID: id, Services: []string{name}},
		[]node.ServiceBinding{{Name: name, Template: name, Service: target}},
		originlog.NewNop(),
		node.Options{MaxTimersPerNode: 64, TimerLocation: time.UTC},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = current.Rollback(context.Background()) })
	return current
}

func TestSameServiceWebSocketClientCallsOwnServer(t *testing.T) {
	for _, messageType := range []websocket.MessageType{websocket.BinaryMessage, websocket.TextMessage} {
		messageType := messageType
		t.Run(map[websocket.MessageType]string{
			websocket.BinaryMessage: "binary",
			websocket.TextMessage:   "text",
		}[messageType], func(t *testing.T) {
			address := reserveAddress(t)
			response := make(chan string, 1)
			var serverClosed atomic.Int32
			var clientClosed atomic.Int32
			var serverSessionID atomic.Uint64

			serverHandler := network.HandlerFuncs{
				Open: func(_ context.Context, session network.Session) error {
					serverSessionID.Store(uint64(session.ID()))
					return nil
				},
				Message: func(_ context.Context, session network.Session, payload []byte) error {
					return session.Send(payload)
				},
				Close: func(context.Context, network.Session, error) { serverClosed.Add(1) },
			}
			clientHandler := network.HandlerFuncs{
				Open: func(_ context.Context, session network.Session) error {
					return session.Send([]byte("self-call"))
				},
				Message: func(_ context.Context, _ network.Session, payload []byte) error {
					response <- string(payload)
					return nil
				},
				Close: func(context.Context, network.Session, error) { clientClosed.Add(1) },
			}
			serverOptions := websocket.DefaultServerOptions(serverHandler)
			serverOptions.MessageType = messageType
			server, err := websocket.NewServer(address, serverOptions)
			if err != nil {
				t.Fatal(err)
			}
			clientOptions := websocket.DefaultClientOptions(clientHandler)
			clientOptions.Dial.MessageType = messageType
			client, err := websocket.NewClient("ws://"+address+"/ws", clientOptions)
			if err != nil {
				t.Fatal(err)
			}
			owner := &loopbackService{server: server, client: client}
			current := newTestNode(t, "ws-self", "Loopback", owner)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := current.Start(ctx); err != nil {
				t.Fatalf("Start error=%v", err)
			}
			select {
			case got := <-response:
				if got != "self-call" {
					t.Fatalf("response=%q", got)
				}
			case <-ctx.Done():
				t.Fatal("同 Service WebSocket 回环超时")
			}

			clientSession, ok := client.Session()
			if !ok || clientSession.Transport() != network.TransportWebSocket ||
				clientSession.ID() == 0 || !clientSession.Writable() || clientSession.Cause() != nil {
				t.Fatalf("client Session 无效：ok=%v session=%v", ok, clientSession)
			}
			for {
				stats := clientSession.Stats()
				if stats.SentMessages >= 1 && stats.ReceivedMessages >= 1 {
					break
				}
				select {
				case <-ctx.Done():
					t.Fatalf("client Session stats=%+v", stats)
				case <-time.After(time.Millisecond):
				}
			}
			serverID := network.SessionID(serverSessionID.Load())
			serverSession, exists := server.Session(serverID)
			if server.Addr() == nil || !exists || serverSession.ID() != serverID ||
				server.SessionCount() != 1 {
				t.Fatalf("Server 查询失败：addr=%v id=%d exists=%v", server.Addr(), serverID, exists)
			}
			if stats := client.Stats(); stats.OpenedSessions != 1 || stats.ActiveSessions != 1 {
				t.Fatalf("client stats=%+v", stats)
			}
			if stats := server.Stats(); stats.OpenedSessions != 1 || stats.ActiveSessions != 1 {
				t.Fatalf("server stats=%+v", stats)
			}
			if !server.CloseSession(serverID, nil) {
				t.Fatal("CloseSession 未找到活动 Session")
			}
			select {
			case <-clientSession.Done():
			case <-ctx.Done():
				t.Fatal("CloseSession 后客户端未关闭")
			}
			for server.SessionCount() != 0 {
				select {
				case <-ctx.Done():
					t.Fatal("Server Session 未完成关闭")
				case <-time.After(time.Millisecond):
				}
			}
			if clientSession.Cause() == nil || server.CloseSession(serverID, nil) {
				t.Fatalf("关闭终态异常：cause=%v server_count=%d", clientSession.Cause(), server.SessionCount())
			}
			if err := current.Stop(ctx); err != nil {
				t.Fatalf("Stop error=%v", err)
			}
			if serverClosed.Load() != 1 || clientClosed.Load() != 1 {
				t.Fatalf("close callbacks server=%d client=%d", serverClosed.Load(), clientClosed.Load())
			}
		})
	}
}

func TestWebSocketDialerCallsServerOwnedBySameService(t *testing.T) {
	address := reserveAddress(t)
	server, err := websocket.NewServer(address, websocket.DefaultServerOptions(network.HandlerFuncs{
		Message: func(_ context.Context, session network.Session, payload []byte) error {
			return session.Send(payload)
		},
	}))
	if err != nil {
		t.Fatal(err)
	}
	owner := &serverOnlyService{server: server}
	current := newTestNode(t, "ws-dialer", "Owner", owner)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := current.Start(ctx); err != nil {
		t.Fatal(err)
	}
	response := make(chan string, 1)
	dialer, err := websocket.NewDialer(
		"ws://"+address+"/ws",
		websocket.DefaultDialOptions(network.HandlerFuncs{
			Open: func(_ context.Context, session network.Session) error {
				return session.Send([]byte("dial-once"))
			},
			Message: func(_ context.Context, _ network.Session, payload []byte) error {
				response <- string(payload)
				return nil
			},
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	session, err := dialer.Dial(ctx, owner)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-response:
		if got != "dial-once" {
			t.Fatalf("response=%q", got)
		}
	case <-ctx.Done():
		t.Fatal("Dialer 回环超时")
	}
	session.Close(nil)
	select {
	case <-session.Done():
	case <-ctx.Done():
		t.Fatal("Dialer Session 关闭超时")
	}
	if err := current.Stop(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestWebSocketClientReconnectsAfterInitialDialFailure(t *testing.T) {
	address := reserveAddress(t)
	clientOptions := websocket.DefaultClientOptions(network.HandlerFuncs{})
	clientOptions.Reconnect.Enabled = true
	clientOptions.Reconnect.MaxAttempts = 20
	clientOptions.Reconnect.InitialDelay = 25 * time.Millisecond
	clientOptions.Reconnect.MaxDelay = 50 * time.Millisecond
	clientOptions.Reconnect.Jitter = 0
	client, err := websocket.NewClient("ws://"+address+"/ws", clientOptions)
	if err != nil {
		t.Fatal(err)
	}
	clientOwner := &clientOnlyService{client: client}
	clientNode := newTestNode(t, "ws-reconnect-client", "Client", clientOwner)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := clientNode.Start(ctx); err != nil {
		t.Fatalf("开启重连后初次拨号失败不应阻止启动：%v", err)
	}

	server, err := websocket.NewServer(
		address,
		websocket.DefaultServerOptions(network.HandlerFuncs{}),
	)
	if err != nil {
		t.Fatal(err)
	}
	serverNode := newTestNode(t, "ws-reconnect-server", "Server", &serverOnlyService{server: server})
	if err := serverNode.Start(ctx); err != nil {
		t.Fatal(err)
	}
	for client.State().State != network.ClientConnected {
		select {
		case <-ctx.Done():
			t.Fatalf("Client 未进入 Connected，最终状态=%+v", client.State())
		case <-time.After(5 * time.Millisecond):
		}
	}
	if err := clientNode.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if err := serverNode.Stop(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestWSSServerClientAndDialer(t *testing.T) {
	// 复用 httptest 生成的自签名证书，只用于验证公共 TLS Options 接线。
	source := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	certificate := source.TLS.Certificates[0]
	source.Close()
	serverTLS := &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{certificate},
	}
	clientTLS := &tls.Config{
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: true, // 仅测试自签名证书；生产代码必须使用 RootCAs。
	}

	address := reserveAddress(t)
	serverOptions := websocket.DefaultServerOptions(network.HandlerFuncs{
		Message: func(_ context.Context, session network.Session, payload []byte) error {
			return session.Send(payload)
		},
	})
	serverOptions.TLSConfig = serverTLS
	server, err := websocket.NewServer(address, serverOptions)
	if err != nil {
		t.Fatal(err)
	}
	serverOwner := &serverOnlyService{server: server}
	serverNode := newTestNode(t, "wss-server", "Server", serverOwner)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := serverNode.Start(ctx); err != nil {
		t.Fatal(err)
	}

	clientResponse := make(chan string, 1)
	clientOptions := websocket.DefaultClientOptions(network.HandlerFuncs{
		Open: func(_ context.Context, session network.Session) error {
			return session.Send([]byte("wss-client"))
		},
		Message: func(_ context.Context, _ network.Session, payload []byte) error {
			clientResponse <- string(payload)
			return nil
		},
	})
	clientOptions.Dial.TLSConfig = clientTLS
	client, err := websocket.NewClient("wss://"+address+"/ws", clientOptions)
	if err != nil {
		t.Fatal(err)
	}
	clientNode := newTestNode(t, "wss-client", "Client", &clientOnlyService{client: client})
	if err := clientNode.Start(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-clientResponse:
		if got != "wss-client" {
			t.Fatalf("WSS Client response=%q", got)
		}
	case <-ctx.Done():
		t.Fatal("WSS Client 回环超时")
	}
	if err := clientNode.Stop(ctx); err != nil {
		t.Fatal(err)
	}

	dialResponse := make(chan string, 1)
	dialOptions := websocket.DefaultDialOptions(network.HandlerFuncs{
		Open: func(_ context.Context, session network.Session) error {
			return session.Send([]byte("wss-dialer"))
		},
		Message: func(_ context.Context, _ network.Session, payload []byte) error {
			dialResponse <- string(payload)
			return nil
		},
	})
	dialOptions.TLSConfig = clientTLS
	dialer, err := websocket.NewDialer("wss://"+address+"/ws", dialOptions)
	if err != nil {
		t.Fatal(err)
	}
	session, err := dialer.Dial(ctx, serverOwner)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-dialResponse:
		if got != "wss-dialer" {
			t.Fatalf("WSS Dialer response=%q", got)
		}
	case <-ctx.Done():
		t.Fatal("WSS Dialer 回环超时")
	}
	session.Close(nil)
	select {
	case <-session.Done():
	case <-ctx.Done():
		t.Fatal("WSS Dialer Session 关闭超时")
	}
	if err := serverNode.Stop(ctx); err != nil {
		t.Fatal(err)
	}
}
