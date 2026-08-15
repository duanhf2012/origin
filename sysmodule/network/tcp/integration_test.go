package tcp_test

import (
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"

	originlog "github.com/duanhf2012/origin/v3/log"
	"github.com/duanhf2012/origin/v3/node"
	"github.com/duanhf2012/origin/v3/service"
	"github.com/duanhf2012/origin/v3/sysmodule/network"
	"github.com/duanhf2012/origin/v3/sysmodule/network/tcp"
)

type loopbackService struct {
	service.Service
	server *tcp.Server
	client *tcp.Client
}

type serverOnlyService struct {
	service.Service
	server *tcp.Server
}

func (target *serverOnlyService) OnInit() error { return target.AddModule(target.server) }

type clientOnlyService struct {
	service.Service
	client *tcp.Client
}

func (target *clientOnlyService) OnInit() error { return target.AddModule(target.client) }

func (target *loopbackService) OnInit() error {
	if err := target.AddModule(target.server); err != nil {
		return err
	}
	return target.AddModule(target.client)
}

func reserveTCPAddress(t *testing.T) string {
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

func TestSameServiceTCPClientCallsOwnServer(t *testing.T) {
	for _, order := range []network.ByteOrder{network.BigEndian, network.LittleEndian} {
		order := order
		t.Run(map[network.ByteOrder]string{
			network.BigEndian:    "big_endian",
			network.LittleEndian: "little_endian",
		}[order], func(t *testing.T) {
			address := reserveTCPAddress(t)
			response := make(chan string, 1)
			var serverClosed atomic.Int32
			var clientClosed atomic.Int32
			serverSessionID := make(chan network.SessionID, 1)

			serverHandler := network.HandlerFuncs{
				Open: func(_ context.Context, session network.Session) error {
					serverSessionID <- session.ID()
					return nil
				},
				Message: func(_ context.Context, session network.Session, payload []byte) error {
					return session.Send(payload)
				},
				Close: func(context.Context, network.Session, error) {
					serverClosed.Add(1)
				},
			}
			clientHandler := network.HandlerFuncs{
				Open: func(_ context.Context, session network.Session) error {
					return session.Send([]byte("self-call"))
				},
				Message: func(_ context.Context, _ network.Session, payload []byte) error {
					response <- string(payload)
					return nil
				},
				Close: func(context.Context, network.Session, error) {
					clientClosed.Add(1)
				},
			}

			serverOptions := tcp.DefaultServerOptions(serverHandler)
			serverOptions.Frame.ByteOrder = order
			server, err := tcp.NewServer(address, serverOptions)
			if err != nil {
				t.Fatal(err)
			}
			clientOptions := tcp.DefaultClientOptions(clientHandler)
			clientOptions.Dial.Frame.ByteOrder = order
			client, err := tcp.NewClient(address, clientOptions)
			if err != nil {
				t.Fatal(err)
			}
			owner := &loopbackService{server: server, client: client}
			current, err := node.New(
				node.Config{ID: "tcp-self", Services: []string{"Loopback"}},
				[]node.ServiceBinding{{Name: "Loopback", Template: "Loopback", Service: owner}},
				originlog.NewNop(),
				node.Options{MaxTimersPerNode: 64, TimerLocation: time.UTC},
			)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = current.Rollback(context.Background()) })
			startCtx, cancelStart := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancelStart()
			if err := current.Start(startCtx); err != nil {
				t.Fatalf("Start error=%v", err)
			}
			select {
			case got := <-response:
				if got != "self-call" {
					t.Fatalf("response=%q", got)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("同 Service TCP 回环超时")
			}
			clientSession, ok := client.Session()
			if !ok || server.SessionCount() != 1 {
				t.Fatalf("sessions: client=%v server=%d", ok, server.SessionCount())
			}
			if clientSession.ID() == "" || clientSession.Transport() != network.TransportTCP ||
				clientSession.LocalAddr() == nil || clientSession.RemoteAddr() == nil ||
				clientSession.Context() == nil || !clientSession.Writable() ||
				clientSession.Cause() != nil {
				t.Fatalf("client session 外观无效：id=%q stats=%+v", clientSession.ID(), clientSession.Stats())
			}
			statsDeadline := time.Now().Add(5 * time.Second)
			for {
				stats := clientSession.Stats()
				if stats.SentMessages >= 1 && stats.ReceivedMessages >= 1 &&
					stats.SentBytes >= uint64(len("self-call")) &&
					stats.ReceivedBytes >= uint64(len("self-call")) {
					break
				}
				if time.Now().After(statsDeadline) {
					t.Fatalf("client session stats=%+v", stats)
				}
				time.Sleep(time.Millisecond)
			}
			var serverID network.SessionID
			select {
			case serverID = <-serverSessionID:
			case <-time.After(5 * time.Second):
				t.Fatal("等待 TCP Server SessionID 超时")
			}
			serverSession, exists := server.Session(serverID)
			if server.Addr() == nil || !exists || serverSession.ID() != serverID ||
				serverID == clientSession.ID() {
				t.Fatalf(
					"server 查询失败或跨 Runtime ID 碰撞：addr=%v server_id=%q client_id=%q exists=%v",
					server.Addr(), serverID, clientSession.ID(), exists,
				)
			}
			if stats := client.Stats(); stats.OpenedSessions != 1 || stats.ActiveSessions != 1 {
				t.Fatalf("client stats=%+v", stats)
			}
			if !server.CloseSession(serverID, nil) {
				t.Fatal("CloseSession 未找到活动 Session")
			}
			select {
			case <-clientSession.Done():
			case <-time.After(5 * time.Second):
				t.Fatal("CloseSession 后客户端未关闭")
			}
			deadline := time.Now().Add(5 * time.Second)
			for server.SessionCount() != 0 && time.Now().Before(deadline) {
				time.Sleep(time.Millisecond)
			}
			if server.SessionCount() != 0 {
				t.Fatal("Server Session 未完成关闭")
			}
			if clientSession.Cause() == nil || server.CloseSession(serverID, nil) {
				t.Fatalf("关闭终态异常：cause=%v server_count=%d", clientSession.Cause(), server.SessionCount())
			}
			stopCtx, cancelStop := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancelStop()
			if err := current.Stop(stopCtx); err != nil {
				t.Fatalf("Stop error=%v", err)
			}
			if serverClosed.Load() != 1 || clientClosed.Load() != 1 {
				t.Fatalf("close callbacks server=%d client=%d", serverClosed.Load(), clientClosed.Load())
			}
		})
	}
}

func TestDialerCallsServerOwnedBySameService(t *testing.T) {
	address := reserveTCPAddress(t)
	serverHandler := network.HandlerFuncs{Message: func(
		_ context.Context,
		session network.Session,
		payload []byte,
	) error {
		return session.Send(payload)
	}}
	server, err := tcp.NewServer(address, tcp.DefaultServerOptions(serverHandler))
	if err != nil {
		t.Fatal(err)
	}
	owner := &serverOnlyService{server: server}
	current, err := node.New(
		node.Config{ID: "tcp-dialer", Services: []string{"Owner"}},
		[]node.ServiceBinding{{Name: "Owner", Template: "Owner", Service: owner}},
		originlog.NewNop(),
		node.Options{MaxTimersPerNode: 64, TimerLocation: time.UTC},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = current.Rollback(context.Background()) })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := current.Start(ctx); err != nil {
		t.Fatal(err)
	}
	response := make(chan string, 1)
	dialOptions := tcp.DefaultDialOptions(network.HandlerFuncs{
		Open: func(_ context.Context, session network.Session) error {
			return session.Send([]byte("dial-once"))
		},
		Message: func(_ context.Context, _ network.Session, payload []byte) error {
			response <- string(payload)
			return nil
		},
	})
	dialer, err := tcp.NewDialer(address, dialOptions)
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

func TestClientReconnectsAfterInitialDialFailure(t *testing.T) {
	address := reserveTCPAddress(t)
	clientOptions := tcp.DefaultClientOptions(network.HandlerFuncs{})
	clientOptions.Reconnect.Enabled = true
	clientOptions.Reconnect.MaxAttempts = 10
	clientOptions.Reconnect.InitialDelay = 50 * time.Millisecond
	clientOptions.Reconnect.MaxDelay = 100 * time.Millisecond
	clientOptions.Reconnect.Jitter = 0
	client, err := tcp.NewClient(address, clientOptions)
	if err != nil {
		t.Fatal(err)
	}
	owner := &clientOnlyService{client: client}
	current, err := node.New(
		node.Config{ID: "tcp-reconnect", Services: []string{"Client"}},
		[]node.ServiceBinding{{Name: "Client", Template: "Client", Service: owner}},
		originlog.NewNop(),
		node.Options{MaxTimersPerNode: 64, TimerLocation: time.UTC},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = current.Rollback(context.Background()) })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := current.Start(ctx); err != nil {
		t.Fatalf("开启重连后初次拨号失败不应阻止启动：%v", err)
	}

	listener, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatal(err)
	}
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr == nil {
			accepted <- conn
		}
	}()
	var raw net.Conn
	select {
	case raw = <-accepted:
	case <-ctx.Done():
		t.Fatal("Client 未在重试中建立 TCP 连接")
	}
	defer raw.Close()
	defer listener.Close()
	for {
		if state := client.State(); state.State == network.ClientConnected {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatalf("Client 未进入 Connected，最终状态=%+v", client.State())
		case <-time.After(5 * time.Millisecond):
		}
	}
	if err := current.Stop(ctx); err != nil {
		t.Fatal(err)
	}
}
