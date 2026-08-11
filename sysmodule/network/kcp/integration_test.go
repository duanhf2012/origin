package kcp_test

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
	"github.com/duanhf2012/origin/v3/sysmodule/network/kcp"
)

type loopbackService struct {
	service.Service
	server *kcp.Server
	client *kcp.Client
}

func (target *loopbackService) OnInit() error {
	if err := target.AddModule(target.server); err != nil {
		return err
	}
	return target.AddModule(target.client)
}

type serverOnlyService struct {
	service.Service
	server *kcp.Server
}

func (target *serverOnlyService) OnInit() error { return target.AddModule(target.server) }

type clientOnlyService struct {
	service.Service
	client *kcp.Client
}

func (target *clientOnlyService) OnInit() error { return target.AddModule(target.client) }

func reserveUDPAddress(t *testing.T) string {
	t.Helper()
	packet, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := packet.LocalAddr().String()
	if err := packet.Close(); err != nil {
		t.Fatal(err)
	}
	return address
}

func TestSameServiceKCPClientCallsOwnServerForEveryFrame(t *testing.T) {
	for _, size := range []int{1, 2, 4} {
		for _, order := range []network.ByteOrder{network.BigEndian, network.LittleEndian} {
			size, order := size, order
			name := map[network.ByteOrder]string{
				network.BigEndian: "big", network.LittleEndian: "little",
			}[order]
			t.Run(name+"_frame_"+string(rune('0'+size)), func(t *testing.T) {
				address := reserveUDPAddress(t)
				response := make(chan string, 1)
				var serverSessionID atomic.Uint64
				serverHandler := network.HandlerFuncs{
					Open: func(_ context.Context, session network.Session) error {
						serverSessionID.Store(uint64(session.ID()))
						return nil
					},
					Message: func(_ context.Context, session network.Session, payload []byte) error {
						return session.Send(payload)
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
				}
				serverOptions := kcp.DefaultServerOptions(serverHandler)
				serverOptions.Frame = kcp.FrameOptions{LengthFieldSize: size, ByteOrder: order}
				serverOptions.Network.MaxMessageSize = 64
				server, err := kcp.NewServer(address, serverOptions)
				if err != nil {
					t.Fatal(err)
				}
				clientOptions := kcp.DefaultClientOptions(clientHandler)
				clientOptions.Dial.Frame = kcp.FrameOptions{LengthFieldSize: size, ByteOrder: order}
				clientOptions.Dial.Network.MaxMessageSize = 64
				client, err := kcp.NewClient(address, clientOptions)
				if err != nil {
					t.Fatal(err)
				}
				owner := &loopbackService{server: server, client: client}
				current, err := node.New(
					node.Config{ID: "kcp-self", Services: []string{"Loopback"}},
					[]node.ServiceBinding{{Name: "Loopback", Template: "Loopback", Service: owner}},
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
				if server.Addr() == nil {
					t.Fatal("Server 启动后 Addr 为空")
				}
				select {
				case got := <-response:
					if got != "self-call" {
						t.Fatalf("response=%q", got)
					}
				case <-ctx.Done():
					t.Fatal("同 Service KCP 回环超时")
				}
				if client.State().State != network.ClientConnected {
					t.Fatalf("client state=%+v", client.State())
				}
				clientSession, ok := client.Session()
				if !ok || clientSession.Transport() != network.TransportKCP ||
					clientSession.LocalAddr() == nil || clientSession.RemoteAddr() == nil {
					t.Fatalf("client session 无效：ok=%v session=%v", ok, clientSession)
				}
				deadline := time.Now().Add(5 * time.Second)
				for serverSessionID.Load() == 0 && time.Now().Before(deadline) {
					time.Sleep(time.Millisecond)
				}
				serverID := network.SessionID(serverSessionID.Load())
				if serverID == 0 || server.SessionCount() != 1 {
					t.Fatalf("server session id=%d count=%d", serverID, server.SessionCount())
				}
				serverSession, found := server.Session(serverID)
				if !found || serverSession.ID() != serverID || !serverSession.Writable() {
					t.Fatalf("server Session(%d)=%v,%v", serverID, serverSession, found)
				}
				if stats := clientSession.Stats(); stats.SentMessages == 0 || stats.ReceivedMessages == 0 {
					t.Fatalf("client session stats=%+v", stats)
				}
				if stats := client.Stats(); stats.ActiveSessions != 1 || stats.OpenedSessions != 1 {
					t.Fatalf("client stats=%+v", stats)
				}
				if stats := server.Stats(); stats.ActiveSessions != 1 || stats.OpenedSessions != 1 {
					t.Fatalf("server stats=%+v", stats)
				}
				if !server.CloseSession(serverID, nil) {
					t.Fatal("CloseSession 未找到活动 KCP Session")
				}
				// KCP 没有 TCP FIN 或标准 Close 帧；服务端本地关闭不能立即通知客户端。
				// 客户端生产环境依靠 ReadIdleTimeout/业务心跳发现失活，本测试显式关闭本地端以快速清理。
				clientSession.Close(nil)
				select {
				case <-clientSession.Done():
				case <-ctx.Done():
					t.Fatal("KCP Session 关闭超时")
				}
				if err := current.Stop(ctx); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}

func TestDialerCallsServerOwnedBySameService(t *testing.T) {
	address := reserveUDPAddress(t)
	serverHandler := network.HandlerFuncs{Message: func(
		_ context.Context,
		session network.Session,
		payload []byte,
	) error {
		return session.Send(payload)
	}}
	server, err := kcp.NewServer(address, kcp.DefaultServerOptions(serverHandler))
	if err != nil {
		t.Fatal(err)
	}
	owner := &serverOnlyService{server: server}
	current, err := node.New(
		node.Config{ID: "kcp-dialer", Services: []string{"Owner"}},
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
	dialer, err := kcp.NewDialer(address, kcp.DefaultDialOptions(network.HandlerFuncs{
		Open: func(_ context.Context, session network.Session) error {
			return session.Send([]byte("dial-once"))
		},
		Message: func(_ context.Context, _ network.Session, payload []byte) error {
			response <- string(payload)
			return nil
		},
	}))
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
		t.Fatal("KCP Dialer 回环超时")
	}
	session.Close(nil)
	select {
	case <-session.Done():
	case <-ctx.Done():
		t.Fatal("KCP Dialer Session 关闭超时")
	}
	if err := current.Stop(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestClientReconnectsAfterSilentPeerTimeout(t *testing.T) {
	address := reserveUDPAddress(t)
	response := make(chan string, 1)
	states := make(chan network.ClientStateSnapshot, 32)
	clientOptions := kcp.DefaultClientOptions(network.HandlerFuncs{
		Open: func(_ context.Context, session network.Session) error {
			return session.Send([]byte("reconnect"))
		},
		Message: func(_ context.Context, _ network.Session, payload []byte) error {
			response <- string(payload)
			return nil
		},
	})
	clientOptions.Dial.Network.ReadIdleTimeout = 80 * time.Millisecond
	clientOptions.Reconnect.Enabled = true
	clientOptions.Reconnect.MaxAttempts = 10
	clientOptions.Reconnect.InitialDelay = 20 * time.Millisecond
	clientOptions.Reconnect.MaxDelay = 40 * time.Millisecond
	clientOptions.Reconnect.Jitter = 0
	clientOptions.StateChange = func(_ context.Context, state network.ClientStateSnapshot) {
		select {
		case states <- state:
		default:
		}
	}
	client, err := kcp.NewClient(address, clientOptions)
	if err != nil {
		t.Fatal(err)
	}
	clientOwner := &clientOnlyService{client: client}
	clientNode, err := node.New(
		node.Config{ID: "kcp-reconnect-client", Services: []string{"Client"}},
		[]node.ServiceBinding{{Name: "Client", Template: "Client", Service: clientOwner}},
		originlog.NewNop(),
		node.Options{MaxTimersPerNode: 64, TimerLocation: time.UTC},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = clientNode.Rollback(context.Background()) })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := clientNode.Start(ctx); err != nil {
		t.Fatal(err)
	}

	// KCP 创建本地 UDP Session 时没有远端握手；只有读空闲到期后才能发现当前对端无响应。
	for {
		select {
		case state := <-states:
			if state.State == network.ClientReconnecting {
				goto startServer
			}
		case <-ctx.Done():
			t.Fatalf("未观察到 KCP 静默对端重连：state=%+v", client.State())
		}
	}

startServer:
	server, err := kcp.NewServer(address, kcp.DefaultServerOptions(network.HandlerFuncs{
		Message: func(_ context.Context, session network.Session, payload []byte) error {
			return session.Send(payload)
		},
	}))
	if err != nil {
		t.Fatal(err)
	}
	serverOwner := &serverOnlyService{server: server}
	serverNode, err := node.New(
		node.Config{ID: "kcp-reconnect-server", Services: []string{"Server"}},
		[]node.ServiceBinding{{Name: "Server", Template: "Server", Service: serverOwner}},
		originlog.NewNop(),
		node.Options{MaxTimersPerNode: 64, TimerLocation: time.UTC},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = serverNode.Rollback(context.Background()) })
	if err := serverNode.Start(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-response:
		if got != "reconnect" {
			t.Fatalf("response=%q", got)
		}
	case <-ctx.Done():
		t.Fatal("KCP Client 未在服务端启动后恢复通信")
	}
	if err := clientNode.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if err := serverNode.Stop(ctx); err != nil {
		t.Fatal(err)
	}
}
