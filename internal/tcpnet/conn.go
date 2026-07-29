package tcpnet

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/duanhf2012/origin/v3/errs"
	"github.com/duanhf2012/origin/v3/internal/bufferpool"
	originlog "github.com/duanhf2012/origin/v3/log"
)

// Conn 表示一条具有独立读写循环和发送队列的 TCP 长度帧连接。
//
// Conn 可以由多个 goroutine 并发 Send、Close 和 Wait。Handler 回调只在
// ReadLoop 中按连接串行执行；已经关闭的 Conn 永远不会重新连接或恢复运行。
type Conn struct {
	raw     net.Conn
	options ConnectionOptions
	handler Handler
	logger  originlog.Logger

	localAddr  net.Addr
	remoteAddr net.Addr
	send       *sendQueue

	// writeParts 和 writeBuffers 只由唯一 WriteLoop 使用并按帧复位。
	//
	// 把 scatter/gather 描述符保存在连接对象中，可以避免每帧临时 net.Buffers
	// 的切片头和底层切片数组逃逸到堆。
	readHeader   [4]byte
	writeHeader  [4]byte
	writeParts   [2][]byte
	writeBuffers net.Buffers

	// closeOnce 使第一个关闭原因、队列关闭和 socket 关闭成为一次性状态提交。
	closeOnce sync.Once
	stateMu   sync.Mutex
	cause     error

	// writeDone 由唯一 WriteLoop 关闭；done 在 OnClose 和全部资源清理后关闭。
	writeDone chan struct{}
	done      chan struct{}
	// onDone 只由 Listener 注入，用于在 OnClose 后移除连接登记。
	onDone func(*Conn)

	// overloadLogged 保证同一连接最多记录一次队列过载，避免故障时形成日志风暴。
	overloadLogged atomic.Bool
}

// newConn 使用已经建立并完成 TCP 参数配置的 net.Conn 创建内部连接对象。
func newConn(
	raw net.Conn,
	options ConnectionOptions,
	handler Handler,
	onDone func(*Conn),
) *Conn {
	// 地址在 socket 关闭前保存，确保 OnClose、日志和调用方诊断仍可读取。
	return &Conn{
		raw:        raw,
		options:    options,
		handler:    handler,
		logger:     options.Logger,
		localAddr:  raw.LocalAddr(),
		remoteAddr: raw.RemoteAddr(),
		send:       newSendQueue(options.SendQueueFrames),
		writeDone:  make(chan struct{}),
		done:       make(chan struct{}),
		onDone:     onDone,
	}
}

// start 启动一条连接唯一的 WriteLoop 和 ReadLoop。
func (conn *Conn) start() {
	// 先启动 Writer，确保 OnOpen 中立即 Send 时已经存在消费者。
	go conn.writeLoop()
	go conn.readLoop()

	// 生命周期日志不在逐帧热路径执行。
	conn.logger.Info(
		"TCP 连接已建立",
		originlog.String("local_addr", addrString(conn.localAddr)),
		originlog.String("remote_addr", addrString(conn.remoteAddr)),
	)
}

// LocalAddr 返回连接建立时保存的本地地址。
func (conn *Conn) LocalAddr() net.Addr {
	// 地址对象在连接生命周期内保持只读，可以直接返回。
	return conn.localAddr
}

// RemoteAddr 返回连接建立时保存的远端地址。
func (conn *Conn) RemoteAddr() net.Addr {
	// 即使 socket 已经关闭，保存的地址仍可用于诊断。
	return conn.remoteAddr
}

// Done 返回连接全部读写循环和 Handler.OnClose 完成后关闭的 Channel。
//
// 上层连接管理器可以把它与重连 Context、心跳 Timer 放在同一个 select 中，不需要为
// Conn.Wait 额外创建 goroutine。Listener 注销紧随 Done 发布之后完成，不属于该信号的
// 对外保证；调用方不能关闭或向该 Channel 发送数据。
func (conn *Conn) Done() <-chan struct{} {
	if conn == nil {
		return nil
	}
	return conn.done
}

// Cause 返回连接已经完成后的首个关闭原因。
//
// Done 尚未关闭时返回 nil，避免上层把仍在运行的连接误判为 CodeTransportClosed。
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

// Send 非阻塞地把 payload Buffer 提交给当前连接。
//
// 返回 nil 时 Buffer 所有权已经转移给 Conn；返回 error 时所有权仍属于调用方。
func (conn *Conn) Send(buffer *bufferpool.Buffer) error {
	// nil 与有效零长度 Buffer 语义不同，必须在读取 Buffer 前明确拒绝 nil。
	if buffer == nil {
		return invalidArgument("tcpnet: Send Buffer 不能为空")
	}
	// Bytes 同时验证 Buffer 尚未释放；释放后使用属于内部不变量错误并按 M2 规则 panic。
	payload := buffer.Bytes()
	payloadSize := len(payload)
	if payloadSize > conn.options.MaxMessageSize {
		return errs.ErrTransportMessageTooLarge
	}

	// 帧头直接写入值类型队列项，不拼接或复制完整 payload。
	item := sendItem{
		buffer:      buffer,
		payloadSize: payloadSize,
	}
	item.headerSize = uint8(
		encodeFrameLength(&item.header, payloadSize, conn.options.Frame),
	)

	// enqueue 是连接关闭状态、消息数额度和所有权转移的唯一原子边界。
	err := conn.send.enqueue(item)
	if err != nil &&
		errors.Is(err, errs.ErrTransportOverloaded) &&
		conn.overloadLogged.CompareAndSwap(false, true) {
		messages, _ := conn.send.snapshot()
		conn.logger.Warn(
			"TCP 发送队列过载",
			originlog.String("remote_addr", addrString(conn.remoteAddr)),
			originlog.Int("queued_messages", messages),
		)
	}
	return err
}

// Close 幂等地发起立即传输关闭，不等待当前调用 goroutine。
func (conn *Conn) Close() {
	// 主动关闭使用稳定 CodeTransportClosed；真正等待清理应调用 Wait。
	conn.initiateClose(errs.ErrTransportClosed)
}

// Wait 等待读写循环、OnClose 和 Listener 注销全部完成，并返回首个关闭原因。
func (conn *Conn) Wait(ctx context.Context) error {
	// nil Context 会使 select 访问 Done 时 panic，因此在等待前拒绝。
	if ctx == nil {
		return invalidArgument("tcpnet: Wait Context 不能为空")
	}

	// 已经完成时优先返回终态，避免 done 与 Context 同时就绪造成随机结果。
	select {
	case <-conn.done:
		return conn.closeCause()
	default:
	}

	// 等待本身可以取消，但 Context 结束不会把已经关闭的连接重新标记为运行。
	select {
	case <-conn.done:
		return conn.closeCause()
	case <-ctx.Done():
		return contextError(ctx.Err())
	}
}

// initiateClose 提交首个关闭原因，停止发送准入并打断底层阻塞 I/O。
func (conn *Conn) initiateClose(cause error) {
	// 所有关闭入口共用一次性边界，后续错误不能覆盖首个有效原因。
	conn.closeOnce.Do(func() {
		if cause == nil {
			cause = errs.ErrTransportClosed
		}
		conn.stateMu.Lock()
		conn.cause = cause
		conn.stateMu.Unlock()

		// 先关闭发送准入并释放尚未出队的 Buffer，再关闭 socket 唤醒读写循环。
		conn.send.close()
		if err := conn.raw.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			conn.logger.Warn(
				"关闭 TCP socket 失败",
				originlog.String("remote_addr", addrString(conn.remoteAddr)),
				originlog.Err(err),
			)
		}
	})
}

// closeCause 返回已经提交的稳定终态原因。
func (conn *Conn) closeCause() error {
	// cause 只写一次，但 Wait、ReadLoop 和日志可能并发读取，因此使用短锁。
	conn.stateMu.Lock()
	cause := conn.cause
	conn.stateMu.Unlock()
	if cause == nil {
		// 只有框架内部错误才能在 done 前缺失 cause，使用稳定关闭错误兜底。
		return errs.ErrTransportClosed
	}
	return cause
}

// readLoop 管理 Handler 生命周期、读帧以及最终连接完成顺序。
func (conn *Conn) readLoop() {
	// runReadLoop 把所有正常错误和 panic 转换为终态原因。
	cause := conn.runReadLoop()
	conn.initiateClose(cause)

	// OnClose 必须等 Writer 释放活动项和全部队列 Buffer 后再执行。
	<-conn.writeDone
	finalCause := conn.closeCause()
	if err := conn.callOnClose(finalCause); err != nil {
		conn.logger.Error(
			"TCP Handler OnClose panic",
			originlog.String("remote_addr", addrString(conn.remoteAddr)),
			originlog.Err(err),
		)
	}

	// 先发布 Conn 完成，再通知 Listener 移除；Listener 只会在全部 Conn done 后完成。
	close(conn.done)
	if conn.onDone != nil {
		conn.onDone(conn)
	}
	conn.logger.Info(
		"TCP 连接已关闭",
		originlog.String("local_addr", addrString(conn.localAddr)),
		originlog.String("remote_addr", addrString(conn.remoteAddr)),
		originlog.Err(finalCause),
	)
}

// runReadLoop 顺序调用 Handler 并把每个完整长度帧交给其唯一所有者。
func (conn *Conn) runReadLoop() (result error) {
	// active 只保存尚未转移给 Handler 的 Buffer，供最外层 panic 路径兜底释放。
	var active *bufferpool.Buffer
	defer func() {
		// 内部读帧 panic 时，尚未转移的 Buffer 仍属于 ReadLoop。
		if active != nil {
			active.Release()
		}
		if value := recover(); value != nil {
			result = panicError("tcpnet ReadLoop", value)
			conn.logger.Error(
				"TCP ReadLoop panic",
				originlog.String("remote_addr", addrString(conn.remoteAddr)),
				originlog.Err(result),
			)
		}
	}()

	// OnOpen 在任何读操作之前执行；panic 被转换为连接内部错误。
	if err := conn.callOnOpen(); err != nil {
		return err
	}
	if conn.send.isClosed() {
		return conn.closeCause()
	}

	for {
		// 非零 ReadTimeout 在每帧开始前刷新，Handler 处理时间不计入下一帧空闲时间。
		if conn.options.ReadTimeout > 0 {
			deadline := time.Now().Add(conn.options.ReadTimeout)
			if err := conn.raw.SetReadDeadline(deadline); err != nil {
				return normalizeIOError(err)
			}
		}

		// 长度头固定复用连接内四字节数组，并按实际宽度完整读取。
		headerSize := conn.options.Frame.LengthFieldSize
		header := conn.readHeader[:headerSize]
		if _, err := io.ReadFull(conn.raw, header); err != nil {
			return normalizeIOError(err)
		}
		payloadLength := decodeFrameLength(header, conn.options.Frame)
		if payloadLength > uint64(conn.options.MaxMessageSize) {
			return errs.NewMessage(
				errs.CodeTransportMessageTooLarge,
				"tcpnet: 远端声明的 payload 超过 MaxMessageSize",
			)
		}

		// 校验通过后才申请最终 Buffer；零长度 Buffer 不分配底层字节数组。
		active = conn.options.Pool.Acquire(int(payloadLength))
		if payloadLength > 0 {
			if _, err := io.ReadFull(conn.raw, active.Bytes()); err != nil {
				active.Release()
				active = nil
				return normalizeIOError(err)
			}
		}

		// 调用前把所有权从 ReadLoop 转移给 Handler；此后由 Handler 保证最终释放。
		packet := active
		active = nil
		if err := conn.callOnMessage(packet); err != nil {
			return normalizeHandlerError(err)
		}
		// Handler 可以主动 Close 当前连接；回调返回后立即结束，不再尝试下一次 Read。
		if conn.send.isClosed() {
			return conn.closeCause()
		}
	}
}

// writeLoop 顺序写出队列帧，并保证活动 Buffer 在所有终态下释放一次。
func (conn *Conn) writeLoop() {
	// active 跨越单次写入，最外层 panic 恢复可以回收已经出队但尚未释放的 Buffer。
	var active *bufferpool.Buffer
	defer func() {
		// writeItem 若在系统调用或内部不变量处 panic，也要清除连接持有的 payload 切片。
		conn.clearWriteParts()
		if active != nil {
			active.Release()
		}
		if value := recover(); value != nil {
			cause := panicError("tcpnet WriteLoop", value)
			conn.logger.Error(
				"TCP WriteLoop panic",
				originlog.String("remote_addr", addrString(conn.remoteAddr)),
				originlog.Err(cause),
			)
			conn.initiateClose(cause)
		}
		close(conn.writeDone)
	}()

	for {
		// next 只有在队列关闭且清空后返回 false；普通空队列会等待合并唤醒信号。
		item, ok := conn.send.next()
		if !ok {
			return
		}
		active = item.buffer

		// 无论完整写入还是 I/O 失败，活动 Buffer 都在进入下一轮前由唯一 Writer 释放。
		err := conn.writeItem(item)
		active.Release()
		active = nil
		if err != nil {
			conn.initiateClose(err)
			return
		}
	}
}

// writeItem 使用一个 WriteDeadline 完整写出长度头和 payload。
func (conn *Conn) writeItem(item sendItem) error {
	// 每帧开始前刷新绝对 Deadline，保证队列等待时间不消耗实际写入预算。
	if err := conn.raw.SetWriteDeadline(
		time.Now().Add(conn.options.WriteTimeout),
	); err != nil {
		return normalizeIOError(err)
	}

	// Buffer 有效长度必须与入队记账一致，否则继续发送会破坏帧边界。
	payload := item.buffer.Bytes()
	if len(payload) != item.payloadSize {
		panic("tcpnet: 发送 Buffer 长度与队列记账不一致")
	}

	// net.Buffers 在支持的平台使用 scatter/gather。描述符复用连接内固定数组；
	// WriteTo 会消费自己的切片头，因此每帧开始前都重新指向完整的两个部分。
	copy(conn.writeHeader[:item.headerSize], item.header[:item.headerSize])
	conn.writeParts[0] = conn.writeHeader[:item.headerSize]
	conn.writeParts[1] = payload
	conn.writeBuffers = conn.writeParts[:]
	written, err := conn.writeBuffers.WriteTo(conn.raw)
	// 系统调用返回后立即断开对 Buffer 字节的引用，使释放后的底层数组可以安全复用。
	conn.clearWriteParts()
	if err != nil {
		return normalizeIOError(err)
	}
	expected := int64(item.headerSize) + int64(item.payloadSize)
	if written != expected {
		return normalizeIOError(io.ErrShortWrite)
	}
	return nil
}

// clearWriteParts 清除 Writer 暂存的 scatter/gather 切片引用。
func (conn *Conn) clearWriteParts() {
	// 该方法只由唯一 WriteLoop 调用，不需要锁；显式清空避免延长 payload 生命周期。
	conn.writeParts[0] = nil
	conn.writeParts[1] = nil
	conn.writeBuffers = nil
}

// callOnOpen 在不持有 tcpnet 锁的情况下调用 Handler，并隔离 panic。
func (conn *Conn) callOnOpen() (err error) {
	// defer 只覆盖用户/Adapter 回调，不吞掉外层框架代码 panic。
	defer func() {
		if value := recover(); value != nil {
			err = panicError("tcpnet Handler.OnOpen", value)
			conn.logger.Error(
				"TCP Handler OnOpen panic",
				originlog.String("remote_addr", addrString(conn.remoteAddr)),
				originlog.Err(err),
			)
		}
	}()
	conn.handler.OnOpen(conn)
	return nil
}

// callOnMessage 在不持有 tcpnet 锁的情况下调用 Handler，并隔离 panic。
func (conn *Conn) callOnMessage(packet *bufferpool.Buffer) (err error) {
	// packet 所有权已经转移；Handler 自己必须在 panic 保护边界内安排最终释放。
	defer func() {
		if value := recover(); value != nil {
			err = panicError("tcpnet Handler.OnMessage", value)
			conn.logger.Error(
				"TCP Handler OnMessage panic",
				originlog.String("remote_addr", addrString(conn.remoteAddr)),
				originlog.Err(err),
			)
		}
	}()
	return conn.handler.OnMessage(conn, packet)
}

// callOnClose 调用最后一个 Handler 事件；panic 只记录，不改变已提交关闭原因。
func (conn *Conn) callOnClose(cause error) (err error) {
	// OnClose 已处于终态，恢复只用于保证 done 和 Listener 注销仍能完成。
	defer func() {
		if value := recover(); value != nil {
			err = panicError("tcpnet Handler.OnClose", value)
		}
	}()
	conn.handler.OnClose(conn, cause)
	return nil
}

// addrString 安全地把可能为空的 net.Addr 转换为日志字段。
func addrString(address net.Addr) string {
	// 测试替身和部分初始化错误可能没有地址，使用空字符串而不是 panic。
	if address == nil {
		return ""
	}
	return address.String()
}
