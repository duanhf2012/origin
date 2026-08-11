package kcpnet

import (
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"sync/atomic"

	kcplib "github.com/xtaci/kcp-go/v5"

	originlog "github.com/duanhf2012/origin/v3/log"
)

// Listener 管理一个 UDP socket、KCP AcceptLoop 及其接受的全部 Conn。
type Listener struct {
	raw     *kcplib.Listener
	addr    net.Addr
	options ListenOptions
	handler Handler
	logger  originlog.Logger

	mu            sync.Mutex
	conns         map[*Conn]struct{}
	closing       bool
	acceptStopped bool
	cause         error
	closeOnce     sync.Once
	doneOnce      sync.Once
	done          chan struct{}
	rejected      atomic.Uint64
}

// Listen 绑定 UDP 地址、应用 socket 参数并启动 KCP AcceptLoop。
func Listen(address string, options ListenOptions, handler Handler) (*Listener, error) {
	if strings.TrimSpace(address) == "" {
		return nil, invalidArgument("kcpnet: Listen 地址不能为空")
	}
	if handler == nil {
		return nil, invalidArgument("kcpnet: Listen Handler 不能为空")
	}
	if err := validateListenOptions(options); err != nil {
		return nil, err
	}
	raw, err := kcplib.ListenWithOptions(
		address,
		options.BlockCrypt,
		options.FEC.DataShards,
		options.FEC.ParityShards,
	)
	if err != nil {
		return nil, transportUnavailable(err)
	}
	if err := configureListenerSocket(raw, options); err != nil {
		_ = raw.Close()
		return nil, err
	}
	listener := &Listener{
		raw:     raw,
		addr:    raw.Addr(),
		options: options,
		handler: handler,
		logger:  options.Connection.Logger,
		conns:   make(map[*Conn]struct{}, min(options.MaxConnections, 128)),
		done:    make(chan struct{}),
	}
	go listener.acceptLoop()
	listener.logger.Info(
		"KCP Listener 已启动",
		originlog.String("address", addrString(listener.addr)),
	)
	return listener, nil
}

func configureListenerSocket(raw *kcplib.Listener, options ListenOptions) error {
	if options.DSCP > 0 {
		if err := raw.SetDSCP(options.DSCP); err != nil {
			return transportUnavailable(err)
		}
	}
	if options.SocketReadBuffer > 0 {
		if err := raw.SetReadBuffer(options.SocketReadBuffer); err != nil {
			return transportUnavailable(err)
		}
	}
	if options.SocketWriteBuffer > 0 {
		if err := raw.SetWriteBuffer(options.SocketWriteBuffer); err != nil {
			return transportUnavailable(err)
		}
	}
	return nil
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
		return invalidArgument("kcpnet: Listener.Close 参数不能为空")
	}
	listener.initiateClose(nil)
	select {
	case <-listener.done:
		return listener.closeCause()
	case <-ctx.Done():
		return contextError(ctx.Err())
	}
}

func (listener *Listener) acceptLoop() {
	defer func() {
		listener.mu.Lock()
		listener.acceptStopped = true
		listener.maybeFinishLocked()
		listener.mu.Unlock()
	}()
	for {
		raw, err := listener.raw.AcceptKCP()
		if err != nil {
			if listener.isClosing() {
				return
			}
			listener.initiateClose(transportUnavailable(err))
			return
		}
		if !listener.hasCapacity() {
			if !listener.isClosing() {
				listener.rejected.Add(1)
			}
			_ = raw.Close()
			continue
		}
		if err := configureSession(raw, listener.options.Connection.Protocol); err != nil {
			listener.rejected.Add(1)
			_ = raw.Close()
			continue
		}
		conn := newConn(raw, listener.options.Connection, listener.handler, listener.removeConn)
		if !listener.registerConn(conn) {
			if !listener.isClosing() {
				listener.rejected.Add(1)
			}
			conn.Close()
			continue
		}
		conn.start()
	}
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

		// kcp-go 的 Listener 与全部 Session 共享 UDP socket。先给每条 Conn 提交稳定的本地主动关闭
		// 原因，再关闭 Listener；否则 socket 错误可能抢先被 Session 记录成 TransportUnavailable。
		for _, conn := range conns {
			conn.Close()
		}
		if err := listener.raw.Close(); err != nil && !errors.Is(err, net.ErrClosed) &&
			!errors.Is(err, io.ErrClosedPipe) {
			listener.recordCause(transportUnavailable(err))
		}
		listener.mu.Lock()
		listener.maybeFinishLocked()
		listener.mu.Unlock()
	})
}

func (listener *Listener) maybeFinishLocked() {
	if listener.closing && listener.acceptStopped && len(listener.conns) == 0 {
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

func (listener *Listener) isClosing() bool {
	listener.mu.Lock()
	closing := listener.closing
	listener.mu.Unlock()
	return closing
}
