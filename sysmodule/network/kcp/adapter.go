package kcp

import (
	"net"
	"sync"

	"github.com/duanhf2012/origin/v3/internal/bufferpool"
	"github.com/duanhf2012/origin/v3/internal/kcpnet"
	"github.com/duanhf2012/origin/v3/sysmodule/network/internal/core"
)

// transportConn 只适配 kcpnet 已有所有权和队列语义，不增加第二条发送队列。
type transportConn struct{ conn *kcpnet.Conn }

func (conn transportConn) LocalAddr() net.Addr  { return conn.conn.LocalAddr() }
func (conn transportConn) RemoteAddr() net.Addr { return conn.conn.RemoteAddr() }
func (conn transportConn) Send(buffer *bufferpool.Buffer) error {
	return conn.conn.Send(buffer)
}
func (conn transportConn) SendAndClose(buffer *bufferpool.Buffer) error {
	return conn.conn.SendAndClose(buffer)
}
func (conn transportConn) Close()         { conn.conn.Close() }
func (conn transportConn) Writable() bool { return conn.conn.Writable() }
func (conn transportConn) Stats() core.TransportStats {
	stats := conn.conn.SendStats()
	return core.TransportStats{
		SentMessages:  stats.SentMessages,
		SentBytes:     stats.SentBytes,
		QueueMessages: stats.Messages,
		QueueBytes:    stats.Bytes,
		Writable:      stats.Writable,
	}
}

// runtimeHandler 把并发 kcpnet 回调收束到一个公共 network Runtime。
type runtimeHandler struct {
	runtime *core.Runtime
	opened  func(*core.Session, *kcpnet.Conn)
	closed  func(*core.Session, *kcpnet.Conn, error)

	mu       sync.RWMutex
	sessions map[*kcpnet.Conn]*core.Session
}

func newRuntimeHandler(runtime *core.Runtime) *runtimeHandler {
	return &runtimeHandler{
		runtime:  runtime,
		sessions: make(map[*kcpnet.Conn]*core.Session),
	}
}

func (handler *runtimeHandler) OnOpen(conn *kcpnet.Conn) {
	session, err := handler.runtime.NewSession(transportConn{conn: conn})
	if err != nil {
		conn.Close()
		return
	}
	handler.mu.Lock()
	handler.sessions[conn] = session
	handler.mu.Unlock()
	if err := handler.runtime.Open(session); err != nil {
		conn.Close()
		return
	}
	if handler.opened != nil {
		handler.opened(session, conn)
	}
}

func (handler *runtimeHandler) OnMessage(
	conn *kcpnet.Conn,
	packet *bufferpool.Buffer,
) error {
	handler.mu.RLock()
	session := handler.sessions[conn]
	handler.mu.RUnlock()
	if session == nil {
		packet.Release()
		return nil
	}
	return handler.runtime.Message(session, packet)
}

func (handler *runtimeHandler) OnWritableChanged(conn *kcpnet.Conn, writable bool) {
	handler.mu.RLock()
	session := handler.sessions[conn]
	handler.mu.RUnlock()
	if session != nil {
		handler.runtime.Writable(session, writable)
	}
}

func (handler *runtimeHandler) OnClose(conn *kcpnet.Conn, cause error) {
	handler.mu.Lock()
	session := handler.sessions[conn]
	delete(handler.sessions, conn)
	handler.mu.Unlock()
	if session != nil {
		handler.runtime.CloseTransport(session, cause)
		if handler.closed != nil {
			handler.closed(session, conn, cause)
		}
	}
}

var (
	_ kcpnet.Handler         = (*runtimeHandler)(nil)
	_ kcpnet.WritableHandler = (*runtimeHandler)(nil)
	_ core.TransportConn     = transportConn{}
)
