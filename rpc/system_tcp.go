package rpc

import (
	"context"
	"sync"

	"github.com/duanhf2012/origin/v3/errs"
	"github.com/duanhf2012/origin/v3/internal/bufferpool"
	"github.com/duanhf2012/origin/v3/internal/tcpnet"
)

const systemTCPQueueFrames = 64

type tcpMuxPlane uint8

const (
	tcpMuxUnknown tcpMuxPlane = iota
	tcpMuxBusiness
	tcpMuxSystem
)

// tcpMuxHandler 在第一帧选择业务 ORP1 或保留系统平面。既有业务 Hello 的布局保持
// 不变；系统帧使用专属前缀，因此不会被当作业务 Session 或 RPC 请求处理。
type tcpMuxHandler struct {
	business *inboundHandler
	system   *systemRuntime

	mu    sync.Mutex
	state map[*tcpnet.Conn]tcpMuxConnection
}

type tcpMuxConnection struct {
	plane tcpMuxPlane
	peer  *systemTCPPeer
}

func newTCPMuxHandler(
	business *inboundHandler,
	system *systemRuntime,
) *tcpMuxHandler {
	return &tcpMuxHandler{
		business: business,
		system:   system,
		state:    make(map[*tcpnet.Conn]tcpMuxConnection),
	}
}

func (handler *tcpMuxHandler) OnOpen(conn *tcpnet.Conn) {
	handler.mu.Lock()
	handler.state[conn] = tcpMuxConnection{plane: tcpMuxUnknown}
	handler.mu.Unlock()
}

func (handler *tcpMuxHandler) OnMessage(
	conn *tcpnet.Conn,
	packet *bufferpool.Buffer,
) error {
	handler.mu.Lock()
	state, exists := handler.state[conn]
	handler.mu.Unlock()
	if !exists || packet == nil || len(packet.Bytes()) == 0 {
		if packet != nil {
			packet.Release()
		}
		return errs.ErrTransportProtocol
	}
	if state.plane == tcpMuxUnknown {
		switch packet.Bytes()[0] {
		case tcpWireVersion:
			state.plane = tcpMuxBusiness
			handler.business.OnOpen(conn)
		case systemTCPFramePrefix:
			if handler.system == nil {
				packet.Release()
				return errs.ErrTransportProtocol
			}
			systemHandler := handler.system.inboundHandler()
			if systemHandler == nil {
				packet.Release()
				return errs.ErrTransportProtocol
			}
			state.plane = tcpMuxSystem
			state.peer = newSystemTCPPeer(
				conn,
				handler.system.owner.pool,
				systemHandler,
			)
			systemHandler.OnSystemOpen(state.peer)
		default:
			packet.Release()
			return errs.ErrTransportProtocol
		}
		handler.mu.Lock()
		current, stillExists := handler.state[conn]
		if stillExists && current.plane == tcpMuxUnknown {
			handler.state[conn] = state
		}
		handler.mu.Unlock()
	}
	switch state.plane {
	case tcpMuxBusiness:
		return handler.business.OnMessage(conn, packet)
	case tcpMuxSystem:
		return handler.handleSystemMessage(state.peer, packet)
	default:
		packet.Release()
		return errs.ErrTransportProtocol
	}
}

func (handler *tcpMuxHandler) handleSystemMessage(
	peer *systemTCPPeer,
	packet *bufferpool.Buffer,
) error {
	defer packet.Release()
	data := packet.Bytes()
	if peer == nil || len(data) < 2 || data[0] != systemTCPFramePrefix ||
		len(data)-1 > MaxSystemMessageSize {
		return errs.ErrTransportProtocol
	}
	systemHandler := handler.system.inboundHandler()
	if systemHandler == nil {
		return errs.ErrServiceStopped
	}
	systemHandler.OnSystemMessage(peer, data[1:])
	return nil
}

func (handler *tcpMuxHandler) OnClose(conn *tcpnet.Conn, cause error) {
	handler.mu.Lock()
	state, exists := handler.state[conn]
	delete(handler.state, conn)
	handler.mu.Unlock()
	if !exists {
		return
	}
	switch state.plane {
	case tcpMuxBusiness:
		handler.business.OnClose(conn, cause)
	case tcpMuxSystem:
		if state.peer != nil {
			state.peer.closeWith(cause)
		}
	}
}

type systemTCPPeer struct {
	pool *bufferpool.Pool

	mu      sync.Mutex
	conn    *tcpnet.Conn
	handler SystemHandler
	closed  bool
}

func newSystemTCPPeer(
	conn *tcpnet.Conn,
	pool *bufferpool.Pool,
	handler SystemHandler,
) *systemTCPPeer {
	return &systemTCPPeer{conn: conn, pool: pool, handler: handler}
}

func (peer *systemTCPPeer) Send(payload []byte) error {
	if peer == nil || len(payload) > MaxSystemMessageSize {
		return errs.ErrInvalidArgument
	}
	peer.mu.Lock()
	conn := peer.conn
	closed := peer.closed
	pool := peer.pool
	peer.mu.Unlock()
	if closed || conn == nil || pool == nil {
		return errs.ErrServiceStopped
	}
	buffer := pool.Acquire(len(payload) + 1)
	buffer.Bytes()[0] = systemTCPFramePrefix
	copy(buffer.Bytes()[1:], payload)
	if err := conn.Send(buffer); err != nil {
		buffer.Release()
		return err
	}
	return nil
}

func (peer *systemTCPPeer) Close() {
	if peer == nil {
		return
	}
	peer.mu.Lock()
	conn := peer.conn
	peer.mu.Unlock()
	if conn != nil {
		conn.Close()
	}
}

func (peer *systemTCPPeer) closeWith(cause error) {
	if peer == nil {
		return
	}
	peer.mu.Lock()
	if peer.closed {
		peer.mu.Unlock()
		return
	}
	peer.closed = true
	handler := peer.handler
	peer.mu.Unlock()
	if handler != nil {
		handler.OnSystemClose(peer, cause)
	}
}

func (system *systemRuntime) dialTCP(
	ctx context.Context,
	target SystemTarget,
	handler SystemHandler,
) (SystemPeer, error) {
	if system == nil || system.owner == nil || system.owner.remote == nil {
		return nil, errs.ErrTransportUnavailable
	}
	system.mu.Lock()
	closed := system.closed
	system.mu.Unlock()
	if closed {
		return nil, errs.ErrServiceStopped
	}
	peer := &systemTCPPeer{pool: system.owner.pool, handler: handler}
	adapter := &systemTCPClientHandler{peer: peer}
	options := system.owner.remote.connectionOptions()
	if options.MaxMessageSize < MaxSystemMessageSize+1 {
		options.MaxMessageSize = MaxSystemMessageSize + 1
	}
	options.SendQueueFrames = systemTCPQueueFrames
	conn, err := tcpnet.Dial(ctx, target.Address, options, adapter)
	if err != nil {
		return nil, err
	}
	peer.mu.Lock()
	peer.conn = conn
	peer.mu.Unlock()
	return peer, nil
}

type systemTCPClientHandler struct {
	peer *systemTCPPeer
}

func (handler *systemTCPClientHandler) OnOpen(conn *tcpnet.Conn) {
	if handler == nil || handler.peer == nil {
		return
	}
	handler.peer.mu.Lock()
	handler.peer.conn = conn
	handler.peer.mu.Unlock()
	if handler.peer.handler != nil {
		handler.peer.handler.OnSystemOpen(handler.peer)
	}
}

func (handler *systemTCPClientHandler) OnMessage(
	_ *tcpnet.Conn,
	packet *bufferpool.Buffer,
) error {
	defer packet.Release()
	if handler == nil || handler.peer == nil || len(packet.Bytes()) < 2 ||
		packet.Bytes()[0] != systemTCPFramePrefix ||
		len(packet.Bytes())-1 > MaxSystemMessageSize {
		return errs.ErrTransportProtocol
	}
	handler.peer.handler.OnSystemMessage(handler.peer, packet.Bytes()[1:])
	return nil
}

func (handler *systemTCPClientHandler) OnClose(_ *tcpnet.Conn, cause error) {
	if handler != nil && handler.peer != nil {
		handler.peer.closeWith(cause)
	}
}
