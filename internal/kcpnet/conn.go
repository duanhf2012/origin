package kcpnet

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	kcplib "github.com/xtaci/kcp-go/v5"

	"github.com/duanhf2012/origin/v3/errs"
	"github.com/duanhf2012/origin/v3/internal/bufferpool"
	"github.com/duanhf2012/origin/v3/internal/lengthframe"
	"github.com/duanhf2012/origin/v3/internal/messagequeue"
	originlog "github.com/duanhf2012/origin/v3/log"
)

type sendItem struct {
	buffer     *bufferpool.Buffer
	closeAfter bool
}

// Conn 表示一条启用 Stream Mode、具有长度帧和有界发送队列的 KCP Session。
type Conn struct {
	raw     *kcplib.UDPSession
	options ConnectionOptions
	handler Handler
	logger  originlog.Logger

	localAddr  net.Addr
	remoteAddr net.Addr
	send       *messagequeue.Queue[sendItem]

	readHeader  [4]byte
	writeHeader [4]byte
	writeParts  [2][]byte

	closeOnce sync.Once
	stateMu   sync.Mutex
	cause     error

	writeDone chan struct{}
	done      chan struct{}
	onDone    func(*Conn)

	overloadLogged atomic.Bool
	sentMessages   atomic.Uint64
	sentBytes      atomic.Uint64
}

func newConn(
	raw *kcplib.UDPSession,
	options ConnectionOptions,
	handler Handler,
	onDone func(*Conn),
) *Conn {
	queue, err := messagequeue.New(
		options.SendQueueMessages,
		options.SendQueueBytes,
		options.SendBudget,
		func(item sendItem) {
			if item.buffer != nil {
				item.buffer.Release()
			}
		},
	)
	if err != nil {
		panic("kcpnet: 未校验的发送队列配置")
	}
	return &Conn{
		raw:        raw,
		options:    options,
		handler:    handler,
		logger:     options.Logger,
		localAddr:  raw.LocalAddr(),
		remoteAddr: raw.RemoteAddr(),
		send:       queue,
		writeDone:  make(chan struct{}),
		done:       make(chan struct{}),
		onDone:     onDone,
	}
}

func (conn *Conn) start() {
	go conn.writeLoop()
	go conn.readLoop()
	conn.logger.Info(
		"KCP 连接已建立",
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

// Send 非阻塞提交唯一 Payload；成功时接管 Buffer，失败时所有权仍属于调用方。
func (conn *Conn) Send(buffer *bufferpool.Buffer) error {
	return conn.sendBuffer(buffer, false)
}

// SendAndClose 原子提交最后一帧，并在完整写出后关闭连接。
func (conn *Conn) SendAndClose(buffer *bufferpool.Buffer) error {
	return conn.sendBuffer(buffer, true)
}

func (conn *Conn) sendBuffer(buffer *bufferpool.Buffer, final bool) error {
	if conn == nil || buffer == nil {
		return invalidArgument("kcpnet: Send 的 Conn 和 Buffer 不能为空")
	}
	payload := buffer.Bytes()
	if len(payload) > conn.options.MaxMessageSize {
		return errs.ErrTransportMessageTooLarge
	}
	item := sendItem{buffer: buffer, closeAfter: final}
	var changed, writable bool
	var err error
	if final {
		changed, writable, err = conn.send.EnqueueFinal(item, int64(buffer.Capacity()))
	} else {
		changed, writable, err = conn.send.Enqueue(item, int64(buffer.Capacity()))
	}
	if err == nil && changed {
		conn.notifyWritableChanged(writable)
	}
	if err != nil && errors.Is(err, errs.ErrTransportOverloaded) &&
		conn.overloadLogged.CompareAndSwap(false, true) {
		snapshot := conn.send.Snapshot()
		conn.logger.Warn(
			"KCP 发送队列过载",
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
		return invalidArgument("kcpnet: Wait 的 Conn 和 Context 不能为空")
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
		conn.send.Close()
		if err := conn.raw.Close(); err != nil && !errors.Is(err, net.ErrClosed) &&
			!errors.Is(err, io.ErrClosedPipe) {
			conn.logger.Warn(
				"关闭 KCP Session 失败",
				originlog.String("remote_addr", addrString(conn.remoteAddr)),
				originlog.Err(err),
			)
		}
	})
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
	finalCause := conn.closeCause()
	if err := conn.callOnClose(finalCause); err != nil {
		conn.logger.Error(
			"KCP Handler OnClose panic",
			originlog.String("remote_addr", addrString(conn.remoteAddr)),
			originlog.Err(err),
		)
	}
	close(conn.done)
	if conn.onDone != nil {
		conn.onDone(conn)
	}
	conn.logger.Info(
		"KCP 连接已关闭",
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
			result = panicError("kcpnet ReadLoop", value)
			conn.logger.Error(
				"KCP ReadLoop panic",
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
	for {
		if err := conn.raw.SetReadDeadline(time.Now().Add(conn.options.ReadTimeout)); err != nil {
			return normalizeIOError(err)
		}
		headerSize := conn.options.Frame.LengthFieldSize
		header := conn.readHeader[:headerSize]
		if _, err := io.ReadFull(conn.raw, header); err != nil {
			return normalizeIOError(err)
		}
		payloadLength := lengthframe.Decode(header, lengthframe.Options{
			Size:      headerSize,
			ByteOrder: conn.options.Frame.ByteOrder,
		})
		if payloadLength > uint64(conn.options.MaxMessageSize) {
			return errs.NewMessage(
				errs.CodeTransportMessageTooLarge,
				"kcpnet: 远端声明的 payload 超过 MaxMessageSize",
			)
		}
		active = conn.options.Pool.Acquire(int(payloadLength))
		if payloadLength > 0 {
			if _, err := io.ReadFull(conn.raw, active.Bytes()); err != nil {
				active.Release()
				active = nil
				return normalizeIOError(err)
			}
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

func (conn *Conn) writeLoop() {
	var active messagequeue.Entry[sendItem]
	hasActive := false
	defer func() {
		conn.clearWriteParts()
		if hasActive {
			conn.send.Release(&active)
		}
		if value := recover(); value != nil {
			cause := panicError("kcpnet WriteLoop", value)
			conn.logger.Error(
				"KCP WriteLoop panic",
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
		payloadSize := len(entry.Value.buffer.Bytes())
		err := conn.writeMessage(entry.Value.buffer.Bytes())
		closeAfter := entry.Value.closeAfter
		conn.send.Release(&active)
		hasActive = false
		if err != nil {
			conn.initiateClose(err)
			return
		}
		conn.sentMessages.Add(1)
		conn.sentBytes.Add(uint64(payloadSize))
		if closeAfter {
			conn.initiateClose(errs.ErrTransportClosed)
			return
		}
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
	headerSize := lengthframe.Encode(&conn.writeHeader, len(payload), lengthframe.Options{
		Size:      conn.options.Frame.LengthFieldSize,
		ByteOrder: conn.options.Frame.ByteOrder,
	})
	conn.writeParts[0] = conn.writeHeader[:headerSize]
	conn.writeParts[1] = payload
	written, err := conn.raw.WriteBuffers(conn.writeParts[:])
	conn.clearWriteParts()
	if err != nil {
		return normalizeIOError(err)
	}
	if written != headerSize+len(payload) {
		return normalizeIOError(io.ErrShortWrite)
	}
	return nil
}

func (conn *Conn) clearWriteParts() {
	conn.writeParts[0] = nil
	conn.writeParts[1] = nil
}

func (conn *Conn) callOnOpen() (err error) {
	defer func() {
		if value := recover(); value != nil {
			err = panicError("kcpnet Handler.OnOpen", value)
		}
	}()
	conn.handler.OnOpen(conn)
	return nil
}

func (conn *Conn) callOnMessage(packet *bufferpool.Buffer) (err error) {
	defer func() {
		if value := recover(); value != nil {
			err = panicError("kcpnet Handler.OnMessage", value)
		}
	}()
	return conn.handler.OnMessage(conn, packet)
}

func (conn *Conn) callOnClose(cause error) (err error) {
	defer func() {
		if value := recover(); value != nil {
			err = panicError("kcpnet Handler.OnClose", value)
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
			err = panicError("kcpnet Handler.OnWritableChanged", value)
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
