package core

import (
	"context"
	"errors"
	"net"
	"sync"
	"sync/atomic"

	"github.com/duanhf2012/origin/v3/errs"
	"github.com/duanhf2012/origin/v3/internal/bufferpool"
	public "github.com/duanhf2012/origin/v3/sysmodule/network"
)

// Session 实现公共 network.Session，并保存内部唯一 Buffer 发送能力。
type Session struct {
	id        public.SessionID
	runtime   *Runtime
	transport TransportConn
	ctx       context.Context
	cancel    context.CancelFunc
	done      chan struct{}

	stateMu        sync.Mutex
	closing        bool
	requestedCause error
	finalCause     error
	closeDelivered bool
	closeOnce      sync.Once

	receiveMessages int
	receiveBytes    int64

	writableMu        sync.Mutex
	writableLatest    bool
	writableDelivered bool
	writableTask      bool

	receivedMessages atomic.Uint64
	receivedBytes    atomic.Uint64
}

// ID 返回当前 Runtime 内稳定的非零标识。
func (session *Session) ID() public.SessionID { return session.id }

// Transport 返回当前 Runtime 冻结的底层传输类型。
func (session *Session) Transport() public.Transport { return session.runtime.transport }

// LocalAddr 返回底层连接建立时保存的本地地址。
func (session *Session) LocalAddr() net.Addr { return session.transport.LocalAddr() }

// RemoteAddr 返回底层连接建立时保存的远端地址。
func (session *Session) RemoteAddr() net.Addr { return session.transport.RemoteAddr() }

// Context 返回随 beginClosing 取消的连接 Context。
func (session *Session) Context() context.Context { return session.ctx }

// Done 返回 OnClose 完成并从 Runtime 注销后关闭的信号。
func (session *Session) Done() <-chan struct{} { return session.done }

// Send 安全复制 payload 后同步提交底层有界发送队列。
func (session *Session) Send(payload []byte) error {
	if session == nil || session.runtime == nil {
		return errs.ErrInvalidArgument
	}
	if len(payload) > session.runtime.options.MaxMessageSize {
		return errs.ErrTransportMessageTooLarge
	}
	buffer := session.runtime.pool.Acquire(len(payload))
	copy(buffer.Bytes(), payload)
	if err := session.sendOwned(buffer); err != nil {
		buffer.Release()
		return err
	}
	return nil
}

// sendOwned 提交已经由框架唯一拥有的最终 Buffer；失败仍由调用方释放。
func (session *Session) sendOwned(buffer *bufferpool.Buffer) error {
	if session == nil || buffer == nil || session.isClosing() {
		return errs.ErrTransportClosed
	}
	if len(buffer.Bytes()) > session.runtime.options.MaxMessageSize {
		return errs.ErrTransportMessageTooLarge
	}
	if err := session.transport.Send(buffer); err != nil {
		if errors.Is(err, errs.ErrTransportOverloaded) {
			session.runtime.sendOverload.Add(1)
		}
		return err
	}
	return nil
}

// Close 记录首个业务原因并幂等关闭底层连接。
func (session *Session) Close(cause error) {
	if session == nil || session.runtime == nil {
		return
	}
	session.stateMu.Lock()
	if !session.closing {
		session.closing = true
		if cause == nil {
			cause = errs.ErrTransportClosed
		}
		session.requestedCause = cause
		session.cancel()
	}
	session.stateMu.Unlock()
	session.transport.Close()
}

// Writable 返回底层发送队列瞬时状态。
func (session *Session) Writable() bool {
	return session != nil && !session.isClosing() && session.transport.Writable()
}

// Cause 在 Done 关闭后返回最终原因，运行期间返回 nil。
func (session *Session) Cause() error {
	if session == nil {
		return errs.ErrTransportClosed
	}
	select {
	case <-session.done:
		return session.closeCause()
	default:
		return nil
	}
}

// Stats 合并 Runtime 入站计数和具体传输发送快照。
func (session *Session) Stats() public.SessionStats {
	if session == nil {
		return public.SessionStats{}
	}
	session.stateMu.Lock()
	receiveMessages := session.receiveMessages
	receiveBytes := session.receiveBytes
	session.stateMu.Unlock()
	transport := session.transport.Stats()
	return public.SessionStats{
		ReceivedMessages:       session.receivedMessages.Load(),
		ReceivedBytes:          session.receivedBytes.Load(),
		SentMessages:           transport.SentMessages,
		SentBytes:              transport.SentBytes,
		ReceivePendingMessages: receiveMessages,
		ReceivePendingSize:     receiveBytes,
		SendQueueMessages:      transport.QueueMessages,
		SendQueueSize:          transport.QueueBytes,
		Writable:               transport.Writable && !session.isClosing(),
	}
}

// reserveReceive 在 Session 锁内预留消息数和 Buffer 容量。
func (session *Session) reserveReceive(charge int64) bool {
	session.stateMu.Lock()
	defer session.stateMu.Unlock()
	if session.closing || charge < 0 ||
		session.receiveMessages >= session.runtime.options.ReceivePendingMessages ||
		charge > session.runtime.options.ReceivePendingSize-session.receiveBytes {
		return false
	}
	session.receiveMessages++
	session.receiveBytes += charge
	return true
}

// releaseReceive 归还一个已经成功预留的消息和容量。
func (session *Session) releaseReceive(charge int64) {
	session.stateMu.Lock()
	session.receiveMessages--
	session.receiveBytes -= charge
	if session.receiveMessages < 0 || session.receiveBytes < 0 {
		session.stateMu.Unlock()
		panic("network core: 入站 Session 额度释放失配")
	}
	session.stateMu.Unlock()
}

// beginClosing 提交最终原因并取消 Session Context。
func (session *Session) beginClosing(transportCause error) error {
	session.stateMu.Lock()
	if !session.closing {
		session.closing = true
		session.cancel()
	}
	cause := session.requestedCause
	if cause == nil {
		cause = transportCause
	}
	if cause == nil {
		cause = errs.ErrTransportClosed
	}
	session.finalCause = cause
	session.stateMu.Unlock()
	return cause
}

// isClosing 返回已经关闭消息准入的瞬时状态。
func (session *Session) isClosing() bool {
	if session == nil {
		return true
	}
	session.stateMu.Lock()
	closing := session.closing
	session.stateMu.Unlock()
	return closing
}

// closeCause 返回已经提交的最终原因。
func (session *Session) closeCause() error {
	session.stateMu.Lock()
	cause := session.finalCause
	session.stateMu.Unlock()
	if cause == nil {
		return errs.ErrTransportClosed
	}
	return cause
}

// markCloseDelivered 使最终 OnClose 和 done 发布成为一次性状态提交。
func (session *Session) markCloseDelivered() bool {
	session.stateMu.Lock()
	if session.closeDelivered {
		session.stateMu.Unlock()
		return false
	}
	session.closeDelivered = true
	session.stateMu.Unlock()
	return true
}

var _ public.Session = (*Session)(nil)
