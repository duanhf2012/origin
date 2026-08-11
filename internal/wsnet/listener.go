package wsnet

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	gorillaws "github.com/gorilla/websocket"

	originlog "github.com/duanhf2012/origin/v3/log"
)

// Listener 在一个专用 HTTP Server 上完成 Upgrade，并管理全部已经接管的 WebSocket Conn。
type Listener struct {
	address string
	addr    net.Addr
	options ListenOptions
	handler Handler
	logger  originlog.Logger

	raw    net.Listener
	server *http.Server

	mu           sync.Mutex
	conns        map[*Conn]struct{}
	closing      bool
	serveStopped bool
	cause        error
	closeOnce    sync.Once
	doneOnce     sync.Once
	done         chan struct{}
	rejected     atomic.Uint64
}

// Listen 绑定地址并启动专用 HTTP Upgrade Server。
func Listen(address string, options ListenOptions, handler Handler) (*Listener, error) {
	if strings.TrimSpace(address) == "" {
		return nil, invalidArgument("wsnet: Listen 地址不能为空")
	}
	if handler == nil {
		return nil, invalidArgument("wsnet: Listen Handler 不能为空")
	}
	if options.MaxConnections <= 0 || options.Path == "" || options.Path[0] != '/' ||
		options.HandshakeTimeout <= 0 {
		return nil, invalidConfig("wsnet: Listener 配置无效")
	}
	if err := validateConnectionOptions(options.Connection); err != nil {
		return nil, err
	}
	options.Subprotocols = append([]string(nil), options.Subprotocols...)
	options.ResponseHeader = cloneHeader(options.ResponseHeader)
	options.TLSConfig = cloneTLSConfig(options.TLSConfig)

	raw, err := net.Listen("tcp", address)
	if err != nil {
		return nil, transportUnavailable(err)
	}
	listener := &Listener{
		address: address,
		addr:    raw.Addr(),
		options: options,
		handler: handler,
		logger:  options.Connection.Logger,
		raw:     raw,
		conns:   make(map[*Conn]struct{}, options.MaxConnections),
		done:    make(chan struct{}),
	}
	listener.server = &http.Server{
		Handler:           http.HandlerFunc(listener.serveHTTP),
		ReadHeaderTimeout: options.HandshakeTimeout,
	}
	serveListener := raw
	if options.TLSConfig != nil {
		config := options.TLSConfig.Clone()
		config.NextProtos = []string{"http/1.1"}
		serveListener = tls.NewListener(raw, config)
	}
	go listener.serve(serveListener)
	return listener, nil
}

func (listener *Listener) Addr() net.Addr {
	if listener == nil {
		return nil
	}
	return listener.addr
}

func (listener *Listener) RejectedConnections() uint64 {
	if listener == nil {
		return 0
	}
	return listener.rejected.Load()
}

func (listener *Listener) Close(ctx context.Context) error {
	if listener == nil || ctx == nil {
		return invalidArgument("wsnet: Listener.Close 参数不能为空")
	}
	listener.initiateClose(nil)
	select {
	case <-listener.done:
		return listener.closeCause()
	case <-ctx.Done():
		return contextError(ctx.Err())
	}
}

func (listener *Listener) serveHTTP(response http.ResponseWriter, request *http.Request) {
	if request.URL.Path != listener.options.Path {
		http.NotFound(response, request)
		return
	}
	if !listener.hasCapacity() {
		listener.rejected.Add(1)
		http.Error(response, "websocket capacity reached", http.StatusServiceUnavailable)
		return
	}

	upgrader := gorillaws.Upgrader{
		HandshakeTimeout: listener.options.HandshakeTimeout,
		Subprotocols:     listener.options.Subprotocols,
		CheckOrigin:      listener.options.CheckOrigin,
	}
	raw, err := upgrader.Upgrade(response, request, listener.options.ResponseHeader)
	if err != nil {
		return
	}
	conn := newConn(raw, listener.options.Connection, listener.handler, listener.removeConn)
	if !listener.registerConn(conn) {
		listener.rejected.Add(1)
		_ = raw.WriteControl(
			gorillaws.CloseMessage,
			gorillaws.FormatCloseMessage(gorillaws.CloseTryAgainLater, ""),
			deadlineFromNow(listener.options.Connection.WriteTimeout),
		)
		_ = raw.Close()
		return
	}
	conn.start()
}

func (listener *Listener) serve(raw net.Listener) {
	err := listener.server.Serve(raw)
	if err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, net.ErrClosed) {
		listener.initiateClose(transportUnavailable(err))
	}
	listener.mu.Lock()
	listener.serveStopped = true
	listener.maybeFinishLocked()
	listener.mu.Unlock()
}

func (listener *Listener) hasCapacity() bool {
	listener.mu.Lock()
	allowed := !listener.closing && len(listener.conns) < listener.options.MaxConnections
	listener.mu.Unlock()
	return allowed
}

func (listener *Listener) registerConn(conn *Conn) bool {
	listener.mu.Lock()
	defer listener.mu.Unlock()
	if listener.closing || len(listener.conns) >= listener.options.MaxConnections {
		return false
	}
	listener.conns[conn] = struct{}{}
	return true
}

func (listener *Listener) removeConn(conn *Conn) {
	listener.mu.Lock()
	delete(listener.conns, conn)
	listener.maybeFinishLocked()
	listener.mu.Unlock()
}

func (listener *Listener) initiateClose(cause error) {
	listener.closeOnce.Do(func() {
		listener.mu.Lock()
		listener.closing = true
		listener.cause = cause
		conns := make([]*Conn, 0, len(listener.conns))
		for conn := range listener.conns {
			conns = append(conns, conn)
		}
		listener.mu.Unlock()

		if err := listener.server.Close(); err != nil && !errors.Is(err, http.ErrServerClosed) &&
			!errors.Is(err, net.ErrClosed) {
			listener.recordCause(transportUnavailable(err))
		}
		for _, conn := range conns {
			conn.Close()
		}
		listener.mu.Lock()
		listener.maybeFinishLocked()
		listener.mu.Unlock()
	})
}

func (listener *Listener) maybeFinishLocked() {
	if listener.closing && listener.serveStopped && len(listener.conns) == 0 {
		listener.doneOnce.Do(func() { close(listener.done) })
	}
}

func (listener *Listener) recordCause(cause error) {
	listener.mu.Lock()
	if listener.cause == nil {
		listener.cause = cause
	}
	listener.mu.Unlock()
}

func (listener *Listener) closeCause() error {
	listener.mu.Lock()
	cause := listener.cause
	listener.mu.Unlock()
	return cause
}

func deadlineFromNow(timeout time.Duration) time.Time {
	return time.Now().Add(min(timeout, time.Second))
}
