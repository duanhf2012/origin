package kcp

import (
	"context"
	"errors"
	"net"
	"sync"

	"github.com/duanhf2012/origin/v3/internal/kcpnet"
	"github.com/duanhf2012/origin/v3/service"
	"github.com/duanhf2012/origin/v3/sysmodule/network"
	"github.com/duanhf2012/origin/v3/sysmodule/network/internal/core"
)

// Server 是只能由 Service.AddModule 托管的 KCP 长度帧服务器。
type Server struct {
	service.Module

	address string
	options ServerOptions

	mu       sync.RWMutex
	runtime  *core.Runtime
	listener *kcpnet.Listener
}

// NewServer 校验并创建尚未绑定 Service 的 KCP Server Module。
func NewServer(address string, options ServerOptions) (*Server, error) {
	if err := validateAddress(address); err != nil {
		return nil, err
	}
	if err := validateServerOptions(options); err != nil {
		return nil, err
	}
	return &Server{address: address, options: options}, nil
}

// OnInit 冻结 Router 等构造期注册表。
func (server *Server) OnInit() error {
	if freezer, ok := server.options.Network.Handler.(network.Freezer); ok {
		return freezer.Freeze()
	}
	return nil
}

// OnStart 建立唯一 Runtime，完整应用 UDP/KCP 参数后启动 Listener。
func (server *Server) OnStart(ctx context.Context) error {
	runtime, err := core.NewRuntime(
		server.Service(),
		network.TransportKCP,
		server.options.Network,
		server.Logger(),
		false,
	)
	if err != nil {
		return err
	}
	handler := newRuntimeHandler(runtime)
	dialOptions := dialOptionsFromServer(server.options)
	listener, err := kcpnet.Listen(server.address, kcpnet.ListenOptions{
		MaxConnections: server.options.Network.MaxSessions,
		BlockCrypt:     server.options.BlockCrypt,
		FEC: kcpnet.FECOptions{
			DataShards:   server.options.FEC.DataShards,
			ParityShards: server.options.FEC.ParityShards,
		},
		DSCP:              server.options.DSCP,
		SocketReadBuffer:  server.options.SocketReadBuffer,
		SocketWriteBuffer: server.options.SocketWriteBuffer,
		Connection:        connectionOptions(runtime, dialOptions),
	}, handler)
	if err != nil {
		runtime.BeginStop()
		_ = runtime.Finalize(ctx)
		return err
	}
	server.mu.Lock()
	server.runtime = runtime
	server.listener = listener
	server.mu.Unlock()
	return nil
}

// OnStop 先停止 Session 准入，再关闭全部 KCP Session，最后交付 Service OnClose。
func (server *Server) OnStop(ctx context.Context) error {
	server.mu.RLock()
	runtime := server.runtime
	listener := server.listener
	server.mu.RUnlock()
	if runtime == nil {
		return nil
	}
	runtime.BeginStop()
	var closeErr error
	if listener != nil {
		closeErr = listener.Close(ctx)
	}
	finalizeErr := runtime.Finalize(ctx)
	return errors.Join(closeErr, finalizeErr)
}

// Addr 返回启动后真实 UDP 监听地址；尚未启动时返回 nil。
func (server *Server) Addr() net.Addr {
	if server == nil {
		return nil
	}
	server.mu.RLock()
	listener := server.listener
	server.mu.RUnlock()
	if listener == nil {
		return nil
	}
	return listener.Addr()
}

// Session 查询活动 Session。
func (server *Server) Session(id network.SessionID) (network.Session, bool) {
	if server == nil {
		return nil, false
	}
	server.mu.RLock()
	runtime := server.runtime
	server.mu.RUnlock()
	if runtime == nil {
		return nil, false
	}
	return runtime.Session(id)
}

// SessionCount 返回当前活动 Session 数。
func (server *Server) SessionCount() int {
	if server == nil {
		return 0
	}
	server.mu.RLock()
	runtime := server.runtime
	server.mu.RUnlock()
	if runtime == nil {
		return 0
	}
	return runtime.SessionCount()
}

// CloseSession 幂等发起指定 Session 关闭。
func (server *Server) CloseSession(id network.SessionID, cause error) bool {
	if server == nil {
		return false
	}
	server.mu.RLock()
	runtime := server.runtime
	server.mu.RUnlock()
	return runtime != nil && runtime.CloseSession(id, cause)
}

// Stats 返回 Server 当前固定统计快照。
func (server *Server) Stats() network.EndpointStats {
	if server == nil {
		return network.EndpointStats{}
	}
	server.mu.RLock()
	runtime := server.runtime
	listener := server.listener
	server.mu.RUnlock()
	if runtime == nil {
		return network.EndpointStats{}
	}
	stats := runtime.Stats()
	if listener != nil {
		stats.RejectedSessions += listener.RejectedConnections()
	}
	return stats
}

var _ service.IModule = (*Server)(nil)
