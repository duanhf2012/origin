package tcpnet

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	originlog "github.com/duanhf2012/origin/v3/log"
)

const (
	// Accept 临时错误从 5ms 开始指数退避，避免故障时忙循环。
	initialAcceptBackoff = 5 * time.Millisecond
	// 单次退避最多 1s，使持续临时故障仍可响应关闭。
	maxAcceptBackoff = time.Second
)

// Listener 管理一个 TCP 监听 socket、AcceptLoop 和其接受的全部 Conn。
type Listener struct {
	raw     net.Listener
	addr    net.Addr
	options ListenOptions
	handler Handler
	logger  originlog.Logger

	mu             sync.Mutex
	conns          map[*Conn]struct{}
	acceptStopping bool
	closing        bool
	acceptStopped  bool
	cause          error

	acceptOnce sync.Once
	closeOnce  sync.Once
	doneOnce   sync.Once
	closingCh  chan struct{}
	acceptDone chan struct{}
	done       chan struct{}

	// limitLogged 对连接上限告警限频，成功接受新连接后允许下一次告警。
	limitLogged atomic.Bool
}

// Listen 绑定 TCP 地址并启动 AcceptLoop。
func Listen(address string, options ListenOptions, handler Handler) (*Listener, error) {
	// 参数和 Options 在绑定端口前全部校验，避免留下半初始化 Listener。
	if strings.TrimSpace(address) == "" {
		return nil, invalidArgument("tcpnet: Listen 地址不能为空")
	}
	if handler == nil {
		return nil, invalidArgument("tcpnet: Listen Handler 不能为空")
	}
	if err := validateListenOptions(options); err != nil {
		return nil, err
	}

	// net.ListenConfig 保留未来受控 socket 参数入口，但 M5 不提前增加 Control 抽象。
	var listenConfig net.ListenConfig
	raw, err := listenConfig.Listen(context.Background(), "tcp", address)
	if err != nil {
		options.Connection.Logger.Error(
			"TCP Listen 失败",
			originlog.String("address", address),
			originlog.Err(err),
		)
		return nil, transportUnavailable(err)
	}

	// 所有字段完成初始化后才启动唯一 AcceptLoop。
	listener := &Listener{
		raw:        raw,
		addr:       raw.Addr(),
		options:    options,
		handler:    handler,
		logger:     options.Connection.Logger,
		conns:      make(map[*Conn]struct{}),
		closingCh:  make(chan struct{}),
		acceptDone: make(chan struct{}),
		done:       make(chan struct{}),
	}
	go listener.acceptLoop()
	listener.logger.Info(
		"TCP Listener 已启动",
		originlog.String("address", addrString(listener.addr)),
	)
	return listener, nil
}

// Addr 返回 Listener 绑定成功时保存的实际地址。
func (listener *Listener) Addr() net.Addr {
	// 使用 :0 监听时，该地址包含系统选择的真实端口。
	return listener.addr
}

// AcceptDone 返回只在 AcceptLoop 已经完全退出后关闭的只读信号。
//
// 上层 Transport 可以监听该信号区分“监听仍可用”和“监听已经永久终止”，但不能据此
// 推断已有 Conn 已经关闭；完整资源退出仍以 Close 返回为准。
func (listener *Listener) AcceptDone() <-chan struct{} {
	if listener == nil {
		closed := make(chan struct{})
		close(closed)
		return closed
	}
	return listener.acceptDone
}

// Cause 返回 Listener 首个永久终止原因；主动 StopAccept/Close 的正常终态返回 nil。
func (listener *Listener) Cause() error {
	if listener == nil {
		return nil
	}
	return listener.closeCause()
}

// StopAccept 幂等地停止新连接准入并等待 AcceptLoop 退出。
//
// 已经接受并登记的 Conn 继续正常收发，仍由 Listener 最终持有。该能力供上层优雅停止：
// 先阻止新 RPC 连接，再排空已经进入 Service 的任务，最后调用 Close 关闭旧连接。
func (listener *Listener) StopAccept(ctx context.Context) error {
	if ctx == nil {
		return invalidArgument("tcpnet: Listener.StopAccept Context 不能为空")
	}
	listener.initiateStopAccept(nil)
	select {
	case <-listener.acceptDone:
		return listener.closeCause()
	default:
	}
	select {
	case <-listener.acceptDone:
		return listener.closeCause()
	case <-ctx.Done():
		return contextError(ctx.Err())
	}
}

// Close 幂等地停止 Accept、关闭所属 Conn 并等待全部资源退出。
func (listener *Listener) Close(ctx context.Context) error {
	// nil Context 在执行破坏性关闭动作前拒绝，调用方可以修正后重试。
	if ctx == nil {
		return invalidArgument("tcpnet: Listener.Close Context 不能为空")
	}

	// 第一次调用提交关闭，后续调用只等待同一个终态。
	listener.initiateClose(nil)
	select {
	case <-listener.done:
		return listener.closeCause()
	default:
	}

	// Context 只限制当前等待者，不中断已经开始的资源清理。
	select {
	case <-listener.done:
		return listener.closeCause()
	case <-ctx.Done():
		return contextError(ctx.Err())
	}
}

// acceptLoop 接受连接、应用 TCP 参数并登记 Conn。
func (listener *Listener) acceptLoop() {
	// 无论正常关闭还是永久 Accept 错误，都必须发布 Accept 已停止。
	defer listener.finishAccept()

	var backoff time.Duration
	var temporaryLogged bool
	for {
		raw, err := listener.raw.Accept()
		if err != nil {
			// 主动关闭 Listener 会以 net.ErrClosed 唤醒 Accept，不记录误导性错误。
			if listener.isClosing() || errors.Is(err, net.ErrClosed) {
				return
			}

			// 临时错误在同一 AcceptLoop 内有界退避，成功接受后重置。
			if isTemporary(err) {
				if backoff == 0 {
					backoff = initialAcceptBackoff
				} else {
					backoff *= 2
					if backoff > maxAcceptBackoff {
						backoff = maxAcceptBackoff
					}
				}
				if !temporaryLogged {
					listener.logger.Warn(
						"TCP Accept 临时失败",
						originlog.String("address", addrString(listener.addr)),
						originlog.Err(err),
					)
					temporaryLogged = true
				}
				if !listener.waitBackoff(backoff) {
					return
				}
				continue
			}

			// 永久错误关闭 Listener 和已有连接，并保留首个失败原因。
			cause := transportUnavailable(err)
			listener.logger.Error(
				"TCP Accept 永久失败",
				originlog.String("address", addrString(listener.addr)),
				originlog.Err(cause),
			)
			listener.initiateClose(cause)
			return
		}
		backoff = 0
		temporaryLogged = false

		// 达到上限或 Listener 正在关闭时立即拒绝新 socket，不创建 Conn goroutine。
		if !listener.hasCapacity() {
			_ = raw.Close()
			if !listener.isClosing() &&
				listener.limitLogged.CompareAndSwap(false, true) {
				listener.logger.Warn(
					"TCP Listener 连接数达到上限",
					originlog.String("address", addrString(listener.addr)),
					originlog.Int("max_connections", listener.options.MaxConnections),
				)
			}
			continue
		}

		// socket 参数设置失败只清理当前连接，不影响仍然有效的 Listener。
		if err := configureTCP(raw, listener.options.Connection); err != nil {
			_ = raw.Close()
			listener.logger.Warn(
				"配置入站 TCP 连接失败",
				originlog.String("address", addrString(listener.addr)),
				originlog.Err(err),
			)
			continue
		}

		// 构造后再次在锁内检查关闭和容量，消除 Close/OnClose 并发窗口。
		conn := newConn(
			raw,
			listener.options.Connection,
			listener.handler,
			listener.removeConn,
		)
		if !listener.registerConn(conn) {
			// 尚未启动 goroutine，直接关闭队列和 socket 即可回收全部本地资源。
			conn.Close()
			continue
		}
		listener.limitLogged.Store(false)
		conn.start()
	}
}

// hasCapacity 在创建较大的发送队列前执行一次保守容量检查。
func (listener *Listener) hasCapacity() bool {
	// AcceptLoop 是唯一新增连接者，锁内长度足以避免正常路径的无意义队列分配。
	listener.mu.Lock()
	allowed := !listener.acceptStopping &&
		len(listener.conns) < listener.options.MaxConnections
	listener.mu.Unlock()
	return allowed
}

// registerConn 把已经完成 socket 配置的 Conn 纳入 Listener 生命周期。
func (listener *Listener) registerConn(conn *Conn) bool {
	// Close 可能发生在预检查之后，因此最终准入必须再次位于同一锁边界。
	listener.mu.Lock()
	defer listener.mu.Unlock()
	if listener.acceptStopping ||
		len(listener.conns) >= listener.options.MaxConnections {
		return false
	}
	listener.conns[conn] = struct{}{}
	return true
}

// removeConn 在 Conn 的 OnClose 和 done 发布后移除登记。
func (listener *Listener) removeConn(conn *Conn) {
	// 删除和 Listener 完成条件在同一锁内判断，避免错误的全空窗口。
	listener.mu.Lock()
	delete(listener.conns, conn)
	listener.maybeFinishLocked()
	listener.mu.Unlock()
}

// initiateClose 提交 Listener 关闭，并关闭当前登记的全部 Conn。
func (listener *Listener) initiateClose(cause error) {
	// closeOnce 统一正常 Close 和 Accept 永久失败路径；StopAccept 先执行过也不影响本阶段。
	listener.closeOnce.Do(func() {
		listener.mu.Lock()
		listener.closing = true
		if listener.cause == nil {
			listener.cause = cause
		}
		conns := make([]*Conn, 0, len(listener.conns))
		for conn := range listener.conns {
			conns = append(conns, conn)
		}
		listener.mu.Unlock()

		// 先保证监听 socket 已停止，再关闭已登记连接；所有破坏性动作都在锁外执行。
		listener.initiateStopAccept(cause)
		for _, conn := range conns {
			conn.Close()
		}
		listener.mu.Lock()
		listener.maybeFinishLocked()
		listener.mu.Unlock()
	})
}

// initiateStopAccept 提交监听阶段关闭；后续完整 Close 仍可单独关闭既有连接。
func (listener *Listener) initiateStopAccept(cause error) {
	listener.acceptOnce.Do(func() {
		listener.mu.Lock()
		listener.acceptStopping = true
		if listener.cause == nil {
			listener.cause = cause
		}
		close(listener.closingCh)
		listener.mu.Unlock()

		// 关闭监听 socket用于打断 Accept；不触碰已经登记的 Conn。
		if err := listener.raw.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			wrapped := transportUnavailable(err)
			listener.recordCause(wrapped)
			listener.logger.Warn(
				"关闭 TCP Listener socket 失败",
				originlog.String("address", addrString(listener.addr)),
				originlog.Err(err),
			)
		}
	})
}

// finishAccept 发布 AcceptLoop 已经退出，并尝试完成 Listener。
func (listener *Listener) finishAccept() {
	// 只有 AcceptLoop 写 acceptStopped；acceptDone 只表示监听阶段结束，不等待既有 Conn。
	listener.mu.Lock()
	listener.acceptStopped = true
	close(listener.acceptDone)
	listener.maybeFinishLocked()
	listener.mu.Unlock()
}

// maybeFinishLocked 在关闭、Accept 停止且连接集合为空时恰好关闭 done。
func (listener *Listener) maybeFinishLocked() {
	// 调用方必须持有 mu；doneOnce 防止 Accept 和最后 Conn 同时重复关闭 channel。
	if listener.closing && listener.acceptStopped && len(listener.conns) == 0 {
		listener.doneOnce.Do(func() {
			close(listener.done)
		})
	}
}

// recordCause 只在正常关闭尚未记录错误时保存 Listener 本地关闭失败。
func (listener *Listener) recordCause(cause error) {
	// Accept 永久错误作为首因优先；正常 Close 的 nil 可以被真实关闭错误补充。
	listener.mu.Lock()
	if listener.cause == nil {
		listener.cause = cause
	}
	listener.mu.Unlock()
}

// closeCause 返回 Listener 自身的终态；正常主动关闭返回 nil。
func (listener *Listener) closeCause() error {
	// Conn 的正常 CodeTransportClosed 不上升为 Listener.Close 错误。
	listener.mu.Lock()
	cause := listener.cause
	listener.mu.Unlock()
	return cause
}

// isClosing 报告 Listener 是否已经停止新连接准入。
func (listener *Listener) isClosing() bool {
	// StopAccept 已经足以把 net.ErrClosed 归类为预期关闭。
	listener.mu.Lock()
	closing := listener.acceptStopping
	listener.mu.Unlock()
	return closing
}

// waitBackoff 在临时 Accept 错误后等待，关闭信号可以立即中断等待。
func (listener *Listener) waitBackoff(delay time.Duration) bool {
	// 使用可停止 Timer，避免 time.Sleep 让 Listener.Close 最多延迟一秒。
	timer := time.NewTimer(delay)
	defer func() {
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
	}()

	select {
	case <-timer.C:
		return true
	case <-listener.closingCh:
		return false
	}
}

// isTemporary 兼容 net.Listener 暴露的临时 Accept 错误分类。
func isTemporary(err error) bool {
	// Temporary 已不建议用于新协议设计，但 net.Listener.Accept 仍以该接口表达可重试错误。
	var temporary interface {
		Temporary() bool
	}
	return errors.As(err, &temporary) && temporary.Temporary()
}
