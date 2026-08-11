package wsnet

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	gorillaws "github.com/gorilla/websocket"

	"github.com/duanhf2012/origin/v3/errs"
	"github.com/duanhf2012/origin/v3/internal/bufferpool"
	"github.com/duanhf2012/origin/v3/internal/messagequeue"
	originlog "github.com/duanhf2012/origin/v3/log"
)

const initialReadBufferSize = 256

// Conn 表示一条完成 HTTP Upgrade、具有单 Reader/Writer 和有界发送队列的 WebSocket 连接。
type Conn struct {
	raw     *gorillaws.Conn
	options ConnectionOptions
	handler Handler
	logger  originlog.Logger

	localAddr  net.Addr
	remoteAddr net.Addr
	send       *messagequeue.Queue[*bufferpool.Buffer]

	closeOnce sync.Once
	stateMu   sync.Mutex
	cause     error

	writeDone chan struct{}
	pingDone  chan struct{}
	closing   chan struct{}
	done      chan struct{}
	onDone    func(*Conn)

	overloadLogged atomic.Bool
	sentMessages   atomic.Uint64
	sentBytes      atomic.Uint64
}

func newConn(
	raw *gorillaws.Conn,
	options ConnectionOptions,
	handler Handler,
	onDone func(*Conn),
) *Conn {
	shared, err := messagequeue.New(
		options.SendQueueMessages,
		options.SendQueueBytes,
		options.SendBudget,
		func(buffer *bufferpool.Buffer) {
			if buffer != nil {
				buffer.Release()
			}
		},
	)
	if err != nil {
		panic("wsnet: 未校验的发送队列配置")
	}
	return &Conn{
		raw:        raw,
		options:    options,
		handler:    handler,
		logger:     options.Logger,
		localAddr:  raw.LocalAddr(),
		remoteAddr: raw.RemoteAddr(),
		send:       shared,
		writeDone:  make(chan struct{}),
		pingDone:   make(chan struct{}),
		closing:    make(chan struct{}),
		done:       make(chan struct{}),
		onDone:     onDone,
	}
}

func (conn *Conn) start() {
	conn.raw.SetReadLimit(int64(conn.options.MaxMessageSize))
	go conn.writeLoop()
	go conn.pingLoop()
	go conn.readLoop()
	conn.logger.Info(
		"WebSocket 连接已建立",
		originlog.String("local_addr", addrString(conn.localAddr)),
		originlog.String("remote_addr", addrString(conn.remoteAddr)),
	)
}

func (conn *Conn) LocalAddr() net.Addr  { return conn.localAddr }
func (conn *Conn) RemoteAddr() net.Addr { return conn.remoteAddr }

func (conn *Conn) Done() <-chan struct{} {
	if conn == nil {
		return nil
	}
	return conn.done
}

func (conn *Conn) Cause() error {
	if conn == nil {
		return errs.ErrTransportClosed
	}
	select {
	case <-conn.done:
		return conn.closeCause()
	default:
		return nil
	}
}

// Send 提交唯一 Payload；成功时接管 Buffer，失败时所有权仍属于调用方。
func (conn *Conn) Send(buffer *bufferpool.Buffer) error {
	if buffer == nil {
		return invalidArgument("wsnet: Send Buffer 不能为空")
	}
	payload := buffer.Bytes()
	if len(payload) > conn.options.MaxMessageSize {
		return errs.ErrTransportMessageTooLarge
	}
	if conn.options.MessageType == TextMessage && !utf8.Valid(payload) {
		return errs.NewMessage(errs.CodeTransportProtocol, "wsnet: Text Message 必须是有效 UTF-8")
	}
	changed, writable, err := conn.send.Enqueue(buffer, int64(buffer.Capacity()))
	if err == nil && changed {
		conn.notifyWritableChanged(writable)
	}
	if err != nil && errors.Is(err, errs.ErrTransportOverloaded) &&
		conn.overloadLogged.CompareAndSwap(false, true) {
		snapshot := conn.send.Snapshot()
		conn.logger.Warn(
			"WebSocket 发送队列过载",
			originlog.String("remote_addr", addrString(conn.remoteAddr)),
			originlog.Int("queued_messages", snapshot.Messages),
			originlog.Int64("queued_bytes", snapshot.Bytes),
		)
	}
	return err
}

func (conn *Conn) Writable() bool {
	return conn != nil && conn.send.Snapshot().Writable
}

// SendQueueStats 是当前连接发送队列的固定诊断快照。
type SendQueueStats struct {
	SentMessages uint64
	SentBytes    uint64
	Messages     int
	Bytes        int64
	HighMessages int
	HighBytes    int64
	Writable     bool
	Closed       bool
}

func (conn *Conn) SendStats() SendQueueStats {
	if conn == nil {
		return SendQueueStats{Closed: true}
	}
	snapshot := conn.send.Snapshot()
	return SendQueueStats{
		SentMessages: conn.sentMessages.Load(),
		SentBytes:    conn.sentBytes.Load(),
		Messages:     snapshot.Messages,
		Bytes:        snapshot.Bytes,
		HighMessages: snapshot.HighMessages,
		HighBytes:    snapshot.HighBytes,
		Writable:     snapshot.Writable,
		Closed:       snapshot.Closed,
	}
}

func (conn *Conn) Close() {
	if conn != nil {
		conn.initiateClose(errs.ErrTransportClosed)
	}
}

func (conn *Conn) Wait(ctx context.Context) error {
	if conn == nil || ctx == nil {
		return invalidArgument("wsnet: Wait 的 Conn 和 Context 不能为空")
	}
	select {
	case <-conn.done:
		return conn.closeCause()
	default:
	}
	select {
	case <-conn.done:
		return conn.closeCause()
	case <-ctx.Done():
		return contextError(ctx.Err())
	}
}

func (conn *Conn) initiateClose(cause error) {
	conn.closeOnce.Do(func() {
		if cause == nil {
			cause = errs.ErrTransportClosed
		}
		conn.stateMu.Lock()
		conn.cause = cause
		conn.stateMu.Unlock()
		close(conn.closing)
		conn.send.Close()

		// WriteControl 可与 Reader/Writer 并发；只发送标准关闭码，不把内部错误文本暴露给远端。
		deadline := time.Now().Add(min(conn.options.WriteTimeout, time.Second))
		_ = conn.raw.WriteControl(
			gorillaws.CloseMessage,
			gorillaws.FormatCloseMessage(closeCode(cause), ""),
			deadline,
		)
		if err := conn.raw.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			conn.logger.Warn(
				"关闭 WebSocket 失败",
				originlog.String("remote_addr", addrString(conn.remoteAddr)),
				originlog.Err(err),
			)
		}
	})
}

func closeCode(cause error) int {
	switch {
	case errors.Is(cause, errs.ErrTransportMessageTooLarge):
		return gorillaws.CloseMessageTooBig
	case errors.Is(cause, errs.ErrTransportProtocol):
		return gorillaws.CloseProtocolError
	case errors.Is(cause, errs.ErrTransportOverloaded):
		return gorillaws.CloseTryAgainLater
	default:
		if marker, ok := cause.(interface{ SlowClient() bool }); ok && marker.SlowClient() {
			return gorillaws.CloseTryAgainLater
		}
		return gorillaws.CloseNormalClosure
	}
}

func (conn *Conn) closeCause() error {
	conn.stateMu.Lock()
	cause := conn.cause
	conn.stateMu.Unlock()
	if cause == nil {
		return errs.ErrTransportClosed
	}
	return cause
}

func (conn *Conn) readLoop() {
	cause := conn.runReadLoop()
	conn.initiateClose(cause)
	<-conn.writeDone
	<-conn.pingDone
	finalCause := conn.closeCause()
	if err := conn.callOnClose(finalCause); err != nil {
		conn.logger.Error(
			"WebSocket Handler OnClose panic",
			originlog.String("remote_addr", addrString(conn.remoteAddr)),
			originlog.Err(err),
		)
	}
	close(conn.done)
	if conn.onDone != nil {
		conn.onDone(conn)
	}
	conn.logger.Info(
		"WebSocket 连接已关闭",
		originlog.String("local_addr", addrString(conn.localAddr)),
		originlog.String("remote_addr", addrString(conn.remoteAddr)),
		originlog.Err(finalCause),
	)
}

func (conn *Conn) runReadLoop() (result error) {
	var active *bufferpool.Buffer
	defer func() {
		if active != nil {
			active.Release()
		}
		if value := recover(); value != nil {
			result = panicError("wsnet ReadLoop", value)
			conn.logger.Error(
				"WebSocket ReadLoop panic",
				originlog.String("remote_addr", addrString(conn.remoteAddr)),
				originlog.Err(result),
			)
		}
	}()
	if err := conn.callOnOpen(); err != nil {
		return err
	}
	if conn.send.IsClosed() {
		return conn.closeCause()
	}

	lastData := time.Now()
	lastPong := lastData
	conn.raw.SetPongHandler(func(string) error {
		lastPong = time.Now()
		return conn.refreshReadDeadline(lastData, lastPong)
	})
	if err := conn.refreshReadDeadline(lastData, lastPong); err != nil {
		return normalizeIOError(err)
	}

	for {
		messageType, reader, err := conn.raw.NextReader()
		if err != nil {
			return normalizeIOError(err)
		}
		if messageType != conn.gorillaMessageType() {
			return errs.NewMessage(errs.CodeTransportProtocol, "wsnet: 收到未配置的数据消息类型")
		}
		active, err = conn.readMessage(reader)
		if err != nil {
			return normalizeIOError(err)
		}
		if conn.options.MessageType == TextMessage && !utf8.Valid(active.Bytes()) {
			return errs.NewMessage(errs.CodeTransportProtocol, "wsnet: 收到无效 UTF-8 Text Message")
		}
		lastData = time.Now()
		if err := conn.refreshReadDeadline(lastData, lastPong); err != nil {
			return normalizeIOError(err)
		}

		packet := active
		active = nil
		if err := conn.callOnMessage(packet); err != nil {
			return normalizeHandlerError(err)
		}
		if conn.send.IsClosed() {
			return conn.closeCause()
		}
	}
}

// readMessage 以 256 B 起步按需增长池化 Buffer，避免为每条小消息预留 MaxMessageSize。
func (conn *Conn) readMessage(reader io.Reader) (*bufferpool.Buffer, error) {
	visible := min(initialReadBufferSize, conn.options.MaxMessageSize)
	buffer := conn.options.Pool.Acquire(visible)
	used := 0
	emptyReads := 0
	for {
		if used == len(buffer.Bytes()) {
			var probe [1]byte
			n, err := reader.Read(probe[:])
			if n > 0 {
				if used >= conn.options.MaxMessageSize {
					buffer.Release()
					return nil, errs.ErrTransportMessageTooLarge
				}
				nextSize := min(conn.options.MaxMessageSize, max(used+1, used*2))
				next := conn.options.Pool.Acquire(nextSize)
				copy(next.Bytes(), buffer.Bytes()[:used])
				next.Bytes()[used] = probe[0]
				used++
				buffer.Release()
				buffer = next
			}
			if err != nil {
				if errors.Is(err, io.EOF) {
					if !buffer.Resize(used) {
						panic("wsnet: 无法收缩已完整读取的 Buffer")
					}
					return buffer, nil
				}
				buffer.Release()
				return nil, err
			}
			if n > 0 {
				continue
			}
		}

		n, err := reader.Read(buffer.Bytes()[used:])
		used += n
		if n == 0 && err == nil {
			emptyReads++
			if emptyReads >= 100 {
				buffer.Release()
				return nil, io.ErrNoProgress
			}
		} else {
			emptyReads = 0
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				if used == 0 {
					buffer.Release()
					return conn.options.Pool.Acquire(0), nil
				}
				if !buffer.Resize(used) {
					panic("wsnet: 无法收缩已完整读取的 Buffer")
				}
				return buffer, nil
			}
			buffer.Release()
			return nil, err
		}
	}
}

func (conn *Conn) refreshReadDeadline(lastData, lastPong time.Time) error {
	var deadline time.Time
	if conn.options.ReadTimeout > 0 {
		deadline = lastData.Add(conn.options.ReadTimeout)
	}
	if conn.options.PongTimeout > 0 {
		pongDeadline := lastPong.Add(conn.options.PongTimeout)
		if deadline.IsZero() || pongDeadline.Before(deadline) {
			deadline = pongDeadline
		}
	}
	return conn.raw.SetReadDeadline(deadline)
}

func (conn *Conn) writeLoop() {
	var active messagequeue.Entry[*bufferpool.Buffer]
	hasActive := false
	defer func() {
		if hasActive {
			conn.send.Release(&active)
		}
		if value := recover(); value != nil {
			cause := panicError("wsnet WriteLoop", value)
			conn.logger.Error(
				"WebSocket WriteLoop panic",
				originlog.String("remote_addr", addrString(conn.remoteAddr)),
				originlog.Err(cause),
			)
			conn.initiateClose(cause)
		}
		close(conn.writeDone)
	}()

	for {
		entry, ok, changed, writable := conn.send.Next()
		if !ok {
			return
		}
		if changed {
			conn.notifyWritableChanged(writable)
		}
		active = entry
		hasActive = true
		payloadSize := len(entry.Value.Bytes())
		err := conn.writeMessage(entry.Value.Bytes())
		conn.send.Release(&active)
		hasActive = false
		if err != nil {
			conn.initiateClose(err)
			return
		}
		conn.sentMessages.Add(1)
		conn.sentBytes.Add(uint64(payloadSize))
		if conn.send.IsSlow(conn.options.SlowClientTimeout) {
			conn.initiateClose(slowClientError{})
			return
		}
	}
}

func (conn *Conn) writeMessage(payload []byte) error {
	if err := conn.raw.SetWriteDeadline(time.Now().Add(conn.options.WriteTimeout)); err != nil {
		return normalizeIOError(err)
	}
	writer, err := conn.raw.NextWriter(conn.gorillaMessageType())
	if err != nil {
		return normalizeIOError(err)
	}
	written, writeErr := writer.Write(payload)
	if writeErr == nil && written != len(payload) {
		writeErr = io.ErrShortWrite
	}
	closeErr := writer.Close()
	return normalizeIOError(errors.Join(writeErr, closeErr))
}

func (conn *Conn) pingLoop() {
	defer close(conn.pingDone)
	if conn.options.PingInterval == 0 {
		return
	}
	ticker := time.NewTicker(conn.options.PingInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			deadline := time.Now().Add(min(conn.options.WriteTimeout, conn.options.PingInterval))
			if err := conn.raw.WriteControl(gorillaws.PingMessage, nil, deadline); err != nil {
				conn.initiateClose(normalizeIOError(err))
				return
			}
		case <-conn.closing:
			return
		}
	}
}

func (conn *Conn) gorillaMessageType() int {
	if conn.options.MessageType == TextMessage {
		return gorillaws.TextMessage
	}
	return gorillaws.BinaryMessage
}

func (conn *Conn) callOnOpen() (err error) {
	defer func() {
		if value := recover(); value != nil {
			err = panicError("wsnet Handler.OnOpen", value)
		}
	}()
	conn.handler.OnOpen(conn)
	return nil
}

func (conn *Conn) callOnMessage(packet *bufferpool.Buffer) (err error) {
	defer func() {
		if value := recover(); value != nil {
			err = panicError("wsnet Handler.OnMessage", value)
		}
	}()
	return conn.handler.OnMessage(conn, packet)
}

func (conn *Conn) callOnClose(cause error) (err error) {
	defer func() {
		if value := recover(); value != nil {
			err = panicError("wsnet Handler.OnClose", value)
		}
	}()
	conn.handler.OnClose(conn, cause)
	return nil
}

func (conn *Conn) notifyWritableChanged(writable bool) {
	handler, ok := conn.handler.(WritableHandler)
	if !ok {
		return
	}
	if err := conn.callOnWritableChanged(handler, writable); err != nil {
		conn.initiateClose(err)
	}
}

func (conn *Conn) callOnWritableChanged(handler WritableHandler, writable bool) (err error) {
	defer func() {
		if value := recover(); value != nil {
			err = panicError("wsnet Handler.OnWritableChanged", value)
		}
	}()
	handler.OnWritableChanged(conn, writable)
	return nil
}

func addrString(address net.Addr) string {
	if address == nil {
		return ""
	}
	return address.String()
}
