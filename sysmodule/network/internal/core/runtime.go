package core

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	"github.com/duanhf2012/origin/v3/errs"
	"github.com/duanhf2012/origin/v3/internal/bufferpool"
	"github.com/duanhf2012/origin/v3/internal/bytebudget"
	originlog "github.com/duanhf2012/origin/v3/log"
	"github.com/duanhf2012/origin/v3/service"
	public "github.com/duanhf2012/origin/v3/sysmodule/network"
)

const (
	initialDispatchRetry = time.Millisecond
	maxDispatchRetry     = 100 * time.Millisecond
)

// Runtime 管理一个 Server、Client 或 Dialer 的全部公共 Session 状态。
type Runtime struct {
	owner     service.IService
	transport public.Transport
	options   public.EndpointOptions
	logger    originlog.Logger
	pool      *bufferpool.Pool
	receive   *bytebudget.Budget
	send      *bytebudget.Budget

	ctx    context.Context
	cancel context.CancelFunc
	// sessionIDSource 固定为系统安全随机源；字段仅用于在不替换包级状态的情况下验证失败路径。
	sessionIDSource io.Reader

	mu           sync.Mutex
	sessions     map[public.SessionID]*Session
	stopping     bool
	pendingClose []*Session

	opened           atomic.Uint64
	closed           atomic.Uint64
	rejected         atomic.Uint64
	receiveOverload  atomic.Uint64
	sendOverload     atomic.Uint64
	protocolErrors   atomic.Uint64
	slowClientCloses atomic.Uint64
}

// NewRuntime 创建一个尚无 Session、由 Module 明确拥有的网络 Runtime。
func NewRuntime(
	owner service.IService,
	transport public.Transport,
	options public.EndpointOptions,
	logger originlog.Logger,
	trackBuffers bool,
) (*Runtime, error) {
	// 所有公共配置和 Service 所有者在创建任何 Buffer/Session 前完成校验。
	if owner == nil || transport < public.TransportTCP || transport > public.TransportKCP {
		return nil, errs.ErrInvalidArgument
	}
	if err := options.Validate(); err != nil {
		return nil, err
	}
	receive, err := bytebudget.New(options.ReceivePendingTotalSize)
	if err != nil {
		return nil, errs.Wrap(errs.CodeInvalidConfig, err)
	}
	send, err := bytebudget.New(options.SendQueueTotalSize)
	if err != nil {
		return nil, errs.Wrap(errs.CodeInvalidConfig, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Runtime{
		owner:     owner,
		transport: transport,
		options:   options,
		logger:    logger,
		pool: bufferpool.NewPool(bufferpool.Options{
			TrackUsage: trackBuffers,
		}),
		receive:         receive,
		send:            send,
		ctx:             ctx,
		cancel:          cancel,
		sessionIDSource: rand.Reader,
		sessions:        make(map[public.SessionID]*Session),
	}, nil
}

// Pool 返回当前 Runtime 唯一拥有的内部 Buffer Pool。
func (runtime *Runtime) Pool() *bufferpool.Pool {
	if runtime == nil {
		return nil
	}
	return runtime.pool
}

// SendBudget 返回具体传输全部 Session 共享的发送总容量。
func (runtime *Runtime) SendBudget() *bytebudget.Budget {
	if runtime == nil {
		return nil
	}
	return runtime.send
}

// Logger 返回具体传输共享的结构化 Logger。
func (runtime *Runtime) Logger() originlog.Logger {
	if runtime == nil {
		return originlog.NewNop()
	}
	return runtime.logger
}

// NewSession 登记一条已经建立但尚未执行 OnOpen 的底层连接。
func (runtime *Runtime) NewSession(conn TransportConn) (*Session, error) {
	if runtime == nil || conn == nil {
		return nil, errs.ErrInvalidArgument
	}
	// 先快速拒绝已经停止或达到容量的端点，避免过载连接继续消耗系统随机源。
	runtime.mu.Lock()
	if runtime.stopping || len(runtime.sessions) >= runtime.options.MaxSessions {
		runtime.mu.Unlock()
		runtime.rejected.Add(1)
		return nil, errs.ErrTransportOverloaded
	}
	runtime.mu.Unlock()

	for attempt := 0; attempt < maxSessionIDGenerationAttempts; attempt++ {
		id, err := newSessionID(runtime.sessionIDSource)
		if err != nil {
			return nil, errs.Wrap(
				errs.CodeInternal,
				fmt.Errorf("network SessionID 生成失败: %w", err),
			)
		}

		runtime.mu.Lock()
		// 生成期间停止或其他连接可能改变容量，登记前必须在线性化锁内重新检查。
		if runtime.stopping || len(runtime.sessions) >= runtime.options.MaxSessions {
			runtime.mu.Unlock()
			runtime.rejected.Add(1)
			return nil, errs.ErrTransportOverloaded
		}
		if _, exists := runtime.sessions[id]; exists {
			runtime.mu.Unlock()
			continue
		}
		sessionContext, cancel := context.WithCancel(runtime.ctx)
		session := &Session{
			id:                id,
			runtime:           runtime,
			transport:         conn,
			ctx:               sessionContext,
			cancel:            cancel,
			done:              make(chan struct{}),
			writableDelivered: true,
			writableLatest:    true,
		}
		runtime.sessions[id] = session
		runtime.mu.Unlock()
		runtime.opened.Add(1)
		return session, nil
	}
	return nil, errs.NewMessage(errs.CodeInternal, "network SessionID 连续碰撞")
}

// Open 在读取首条消息前把 OnOpen 同步交付所属 Service。
func (runtime *Runtime) Open(session *Session) error {
	if runtime == nil || session == nil || session.runtime != runtime {
		return errs.ErrInvalidArgument
	}
	err := runtime.dispatchAndWait(func(ctx context.Context) error {
		return runtime.callHandler("OnOpen", func() error {
			return runtime.options.Handler.OnOpen(ctx, session)
		})
	})
	if err != nil {
		session.Close(err)
	}
	return err
}

// Message 接管入站 Buffer，预留 Session/Module 额度并直接提交一个 Service Task。
func (runtime *Runtime) Message(session *Session, buffer *bufferpool.Buffer) error {
	if runtime == nil || session == nil || buffer == nil || session.runtime != runtime {
		if buffer != nil {
			buffer.Release()
		}
		return errs.ErrInvalidArgument
	}
	payloadLength := len(buffer.Bytes())
	charge := int64(buffer.Capacity())
	if payloadLength > runtime.options.MaxMessageSize || !session.reserveReceive(charge) {
		buffer.Release()
		runtime.receiveOverload.Add(1)
		return errs.ErrTransportOverloaded
	}
	if !runtime.receive.TryAcquire(charge) {
		session.releaseReceive(charge)
		buffer.Release()
		runtime.receiveOverload.Add(1)
		return errs.ErrTransportOverloaded
	}

	// 任务无论执行、跳过、错误还是 panic 都通过 defer 归还唯一 Buffer 和两层额度。
	err := runtime.owner.DispatchAsync(func(ctx context.Context) {
		defer func() {
			buffer.Release()
			runtime.receive.Release(charge)
			session.releaseReceive(charge)
		}()
		if session.isClosing() {
			return
		}
		session.receivedMessages.Add(1)
		session.receivedBytes.Add(uint64(payloadLength))
		if handlerErr := runtime.callHandler("OnMessage", func() error {
			return runtime.options.Handler.OnMessage(ctx, session, buffer.Bytes())
		}); handlerErr != nil {
			session.Close(handlerErr)
		}
	})
	if err != nil {
		runtime.receive.Release(charge)
		session.releaseReceive(charge)
		buffer.Release()
		runtime.receiveOverload.Add(1)
		return err
	}
	return nil
}

// Writable 合并同一 Session 的高低水位通知并投递最新状态。
func (runtime *Runtime) Writable(session *Session, writable bool) {
	if runtime == nil || session == nil || session.runtime != runtime || session.isClosing() {
		return
	}
	session.writableMu.Lock()
	session.writableLatest = writable
	if session.writableTask {
		session.writableMu.Unlock()
		return
	}
	session.writableTask = true
	session.writableMu.Unlock()

	err := runtime.dispatchWritable(session)
	if err == nil {
		return
	}
	if errors.Is(err, errs.ErrServiceQueueFull) || errors.Is(err, errs.ErrServiceNotReady) {
		go runtime.retryWritable(session)
		return
	}
	runtime.clearWritableTask(session)
}

func (runtime *Runtime) dispatchWritable(session *Session) error {
	return runtime.owner.DispatchAsync(func(ctx context.Context) {
		for {
			session.writableMu.Lock()
			if session.isClosing() || session.writableDelivered == session.writableLatest {
				session.writableTask = false
				session.writableMu.Unlock()
				return
			}
			current := session.writableLatest
			session.writableDelivered = current
			session.writableMu.Unlock()
			if err := runtime.callHandler("OnWritableChanged", func() error {
				runtime.options.Handler.OnWritableChanged(ctx, session, current)
				return nil
			}); err != nil {
				runtime.clearWritableTask(session)
				session.Close(err)
				return
			}
		}
	})
}

func (runtime *Runtime) retryWritable(session *Session) {
	backoff := initialDispatchRetry
	for {
		if session.isClosing() || !runtime.waitRetry(backoff) {
			runtime.clearWritableTask(session)
			return
		}
		err := runtime.dispatchWritable(session)
		if err == nil {
			return
		}
		if !errors.Is(err, errs.ErrServiceQueueFull) &&
			!errors.Is(err, errs.ErrServiceNotReady) {
			runtime.clearWritableTask(session)
			return
		}
		backoff = min(backoff*2, maxDispatchRetry)
	}
}

func (*Runtime) clearWritableTask(session *Session) {
	session.writableMu.Lock()
	session.writableTask = false
	session.writableMu.Unlock()
}

// CloseTransport 在底层 Reader/Writer 已经停止后交付最终 OnClose。
//
// 正常运行时本方法阻塞到底层 Close Task 在 Service 中完成，维持事件顺序并让连接数保持有界；
// Module 停止 finalizer 阶段则保存到 pendingClose，由 Finalize 在独占 Service 上下文执行。
func (runtime *Runtime) CloseTransport(session *Session, transportCause error) {
	if runtime == nil || session == nil || session.runtime != runtime {
		return
	}
	session.closeOnce.Do(func() {
		cause := session.beginClosing(transportCause)

		runtime.mu.Lock()
		if runtime.stopping {
			runtime.pendingClose = append(runtime.pendingClose, session)
			runtime.mu.Unlock()
			return
		}
		runtime.mu.Unlock()

		// Service Running 时对瞬时 QueueFull 进行有上限退避；连接仍登记在底层 Listener，
		// 因此等待 goroutine 和 Session 数不会突破 MaxSessions。
		backoff := initialDispatchRetry
		for {
			done := make(chan struct{})
			err := runtime.owner.DispatchAsync(func(ctx context.Context) {
				runtime.completeClose(ctx, session, cause)
				close(done)
			})
			if err == nil {
				select {
				case <-done:
				case <-runtime.ctx.Done():
				}
				return
			}
			if !errors.Is(err, errs.ErrServiceQueueFull) &&
				!errors.Is(err, errs.ErrServiceNotReady) {
				runtime.mu.Lock()
				runtime.pendingClose = append(runtime.pendingClose, session)
				runtime.mu.Unlock()
				return
			}
			if !runtime.waitRetry(backoff) {
				runtime.mu.Lock()
				runtime.pendingClose = append(runtime.pendingClose, session)
				runtime.mu.Unlock()
				return
			}
			backoff = min(backoff*2, maxDispatchRetry)
		}
	})
}

// BeginStop 关闭新 Session 准入，并让后续底层 Close 转入 finalizer 队列。
func (runtime *Runtime) BeginStop() {
	if runtime == nil {
		return
	}
	runtime.mu.Lock()
	runtime.stopping = true
	runtime.mu.Unlock()
}

// Finalize 在 Module OnStop 的独占 Service finalizer 上下文完成剩余 Close。
func (runtime *Runtime) Finalize(ctx context.Context) error {
	if runtime == nil || ctx == nil {
		return errs.ErrInvalidArgument
	}
	defer runtime.cancel()
	runtime.mu.Lock()
	runtime.stopping = true
	pending := make([]*Session, 0, len(runtime.sessions))
	for _, session := range runtime.sessions {
		pending = append(pending, session)
	}
	runtime.pendingClose = nil
	runtime.mu.Unlock()

	for _, session := range pending {
		if err := ctx.Err(); err != nil {
			return errs.Wrap(errs.CodeGracefulShutdownTimeout, err)
		}
		session.transport.Close()
		session.closeOnce.Do(func() {
			session.beginClosing(errs.ErrTransportClosed)
		})
		runtime.completeClose(ctx, session, session.closeCause())
	}
	return nil
}

// Session 按 ID 查询当前尚未完成最终 Close 的 Session。
func (runtime *Runtime) Session(id public.SessionID) (public.Session, bool) {
	if runtime == nil || id == "" {
		return nil, false
	}
	runtime.mu.Lock()
	session, ok := runtime.sessions[id]
	runtime.mu.Unlock()
	return session, ok
}

// SessionCount 返回当前尚未完成最终 Close 的 Session 数。
func (runtime *Runtime) SessionCount() int {
	if runtime == nil {
		return 0
	}
	runtime.mu.Lock()
	count := len(runtime.sessions)
	runtime.mu.Unlock()
	return count
}

// CancelIfIdle 释放一次性 Dialer 在最后一条正常 Close 已交付后的 Runtime Context。
//
// 托管 Server/Client 不调用本方法，它们统一由 Module Finalize 收口。
func (runtime *Runtime) CancelIfIdle() bool {
	if runtime == nil {
		return false
	}
	runtime.mu.Lock()
	idle := !runtime.stopping && len(runtime.sessions) == 0 && len(runtime.pendingClose) == 0
	runtime.mu.Unlock()
	if idle {
		runtime.cancel()
	}
	return idle
}

// CloseSession 按 ID 发起关闭；不存在或已完成时返回 false。
func (runtime *Runtime) CloseSession(id public.SessionID, cause error) bool {
	target, ok := runtime.Session(id)
	if !ok {
		return false
	}
	target.Close(cause)
	return true
}

// Stats 返回端点容量和累计连接的固定快照。
func (runtime *Runtime) Stats() public.EndpointStats {
	if runtime == nil {
		return public.EndpointStats{}
	}
	receive := runtime.receive.Snapshot()
	send := runtime.send.Snapshot()
	return public.EndpointStats{
		ActiveSessions:              runtime.SessionCount(),
		OpenedSessions:              runtime.opened.Load(),
		ClosedSessions:              runtime.closed.Load(),
		RejectedSessions:            runtime.rejected.Load(),
		ReceivePendingSize:          receive.Used,
		ReceivePendingHighWatermark: receive.HighWatermark,
		SendQueueSize:               send.Used,
		SendQueueHighWatermark:      send.HighWatermark,
		ReceiveOverloads:            runtime.receiveOverload.Load(),
		SendOverloads:               runtime.sendOverload.Load(),
		SlowClientCloses:            runtime.slowClientCloses.Load(),
		ProtocolErrors:              runtime.protocolErrors.Load(),
	}
}

// dispatchAndWait 在 Service 刚启动的短窗口重试生命周期任务，并等待 Handler 同步结果。
func (runtime *Runtime) dispatchAndWait(callback func(context.Context) error) error {
	backoff := initialDispatchRetry
	for {
		result := make(chan error, 1)
		err := runtime.owner.DispatchAsync(func(ctx context.Context) {
			result <- callback(ctx)
		})
		if err == nil {
			select {
			case callbackErr := <-result:
				return callbackErr
			case <-runtime.ctx.Done():
				return errs.ErrTransportClosed
			}
		}
		if !errors.Is(err, errs.ErrServiceNotReady) &&
			!errors.Is(err, errs.ErrServiceQueueFull) {
			return err
		}
		if !runtime.waitRetry(backoff) {
			return errs.ErrTransportClosed
		}
		backoff = min(backoff*2, maxDispatchRetry)
	}
}

// waitRetry 使用 Runtime 取消信号打断短退避。
func (runtime *Runtime) waitRetry(delay time.Duration) bool {
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
	case <-runtime.ctx.Done():
		return false
	}
}

// completeClose 在 Service 串行或 finalizer 独占上下文完成一次最终回调和注销。
func (runtime *Runtime) completeClose(
	ctx context.Context,
	session *Session,
	cause error,
) {
	if session == nil || !session.markCloseDelivered() {
		return
	}
	if errors.Is(cause, errs.ErrTransportProtocol) ||
		errors.Is(cause, errs.ErrTransportMessageTooLarge) {
		runtime.protocolErrors.Add(1)
	}
	if marker, ok := cause.(interface{ SlowClient() bool }); ok && marker.SlowClient() {
		runtime.slowClientCloses.Add(1)
	}
	if err := runtime.callHandler("OnClose", func() error {
		runtime.options.Handler.OnClose(ctx, session, cause)
		return nil
	}); err != nil {
		runtime.logger.Error(
			"network Handler OnClose panic",
			originlog.String("session_id", string(session.id)),
			originlog.Err(err),
		)
	}
	runtime.mu.Lock()
	delete(runtime.sessions, session.id)
	runtime.mu.Unlock()
	runtime.closed.Add(1)
	close(session.done)
}

// callHandler 只恢复当前网络回调 panic，不让一个 Session 破坏 Service Runner。
func (runtime *Runtime) callHandler(phase string, callback func() error) (result error) {
	defer func() {
		if value := recover(); value != nil {
			result = errs.Wrap(
				errs.CodeInternal,
				fmt.Errorf("network Handler %s panic: %v", phase, value),
			)
			runtime.logger.ErrorStack(
				"network Handler panic",
				originlog.String("phase", phase),
				originlog.String("panic", fmt.Sprint(value)),
				originlog.String("panic_stack", string(debug.Stack())),
			)
		}
	}()
	return callback()
}
