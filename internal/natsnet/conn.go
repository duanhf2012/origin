package natsnet

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/duanhf2012/origin/v3/errs"
	originlog "github.com/duanhf2012/origin/v3/log"
	"github.com/nats-io/nats.go"
)

// Conn 表示一条由单个 Origin Node 持有并复用多个 Subject 的 Core NATS 连接。
//
// Conn 可以由多个 goroutine 并发 Publish、Subscribe、Flush、Drain、Close 和 Wait。
// Close 是立即关闭，Drain 是停止准入后有界排空；进入 Closed 后不会由包装层自行恢复。
type Conn struct {
	options Options
	logger  originlog.Logger
	handler EventHandler

	// raw 在初始 Connect 成功后只写一次，随后由官方客户端并发安全地管理。
	raw *nats.Conn

	// status 位于 Publish/Subscribe 热路径，使用原子值避免每次操作争用状态锁。
	status atomic.Uint32

	// stateMu 保护订阅登记、关闭意图和第一个有效终止原因。
	stateMu        sync.Mutex
	subscriptions  map[*nats.Subscription]*Subscription
	closeRequested bool
	drainRequested bool
	terminalSet    bool
	terminal       error

	// finishOnce 统一发布 EventClosed、关闭所有包装 Subscription 和完成 Wait。
	finishOnce sync.Once
	done       chan struct{}
}

// Connect 完成初始 NATS 连接并返回可用 Conn。
//
// 初始连接不会在后台无限重试；Context 取消会打断 TCP、TLS 和协议握手阶段。成功连接后的
// 自动重连由 ReconnectOptions 控制，且不会继续持有初始 Context。
func Connect(
	ctx context.Context,
	options Options,
	eventHandler EventHandler,
) (*Conn, error) {
	// nil Context 无法提供初始取消语义，必须在创建资源前拒绝。
	if ctx == nil {
		return nil, invalidArgument("natsnet: Connect Context 不能为空")
	}

	// 完整配置先于文件读取、socket 和 goroutine 创建进行校验。
	tlsEnabled, err := validateOptions(options)
	if err != nil {
		return nil, err
	}
	// 配置切片复制到 Conn 独占快照，避免调用方随后修改 URL 集合。
	options.URLs = append([]string(nil), options.URLs...)

	// 包装对象必须先于 nats.Connect 创建，使官方异步回调可以安全引用稳定地址。
	conn := &Conn{
		options:       options,
		logger:        options.Logger,
		handler:       eventHandler,
		subscriptions: make(map[*nats.Subscription]*Subscription),
		done:          make(chan struct{}),
	}
	conn.status.Store(uint32(StatusConnecting))

	// 初始 Dialer 只在 Connect 阶段传播 Context；defer 保证所有观察 goroutine 已退出。
	dialer := newInitialDialer(ctx, options.ConnectTimeout)
	defer dialer.finish()

	natsOptions, err := buildNATSOptions(options, tlsEnabled, conn, dialer)
	if err != nil {
		return nil, err
	}

	// 官方客户端接受逗号分隔的 Seed 列表，并负责随机选择、发现和后续重连。
	raw, connectErr := nats.Connect(strings.Join(options.URLs, ","), natsOptions...)
	if connectErr != nil {
		// Context 可能通过关闭握手 socket 使底层返回普通 I/O 错误，优先保留调用方取消语义。
		if ctxErr := ctx.Err(); ctxErr != nil {
			connectErr = ctxErr
		}
		mapped := mapError(redactCause(connectErr, options))
		conn.status.Store(uint32(StatusClosed))
		conn.logger.Error(
			"NATS 初始连接失败",
			originlog.String("server_url", safeURL(options.URLs[0])),
			originlog.Err(mapped),
		)
		return nil, mapped
	}

	// raw 赋值后 Connection 才能进入 Connected；初始成功事件由包装层明确发布一次。
	conn.raw = raw
	if conn.Status() != StatusClosed {
		conn.status.Store(uint32(StatusConnected))
		url := connectedURL(raw)
		conn.logger.Info(
			"NATS 初始连接成功",
			originlog.String("server_url", url),
			originlog.String("connection_name", options.Name),
		)
		conn.emit(Event{Type: EventConnected, URL: url})
	}
	return conn, nil
}

// Publish 把 payload 发布到指定 Subject。
//
// nil 和零长度 payload 都合法。nats.go 在返回前接管或复制发送数据，因此返回后调用方可以
// 立即复用原切片；natsnet 不为了包装层所有权再复制一次 payload。
func (conn *Conn) Publish(subject string, payload []byte) error {
	// 空 Subject 没有可路由语义，直接按调用参数错误返回。
	if strings.TrimSpace(subject) == "" {
		return invalidArgument("natsnet: Publish Subject 不能为空")
	}
	if len(payload) > conn.options.MaxMessageSize {
		return errs.ErrTransportMessageTooLarge
	}
	if !conn.acceptingWork() {
		return errs.ErrTransportClosed
	}

	// 官方 Publish 负责连接锁、协议写缓冲和有界重连缓冲；包装层不增加第二套队列。
	err := conn.raw.Publish(subject, payload)
	return mapError(redactCause(err, conn.options))
}

// Subscribe 建立普通异步订阅或 Queue Group 订阅。
//
// 返回成功前会执行一次带 Deadline 的 Flush，保证 Server 已处理订阅命令。Handler 在
// nats.go 的订阅回调 goroutine 中执行，同一订阅保持顺序。
func (conn *Conn) Subscribe(
	ctx context.Context,
	subject string,
	options SubscriptionOptions,
	handler MessageHandler,
) (*Subscription, error) {
	// 参数和默认值在创建官方 Subscription 前全部确定。
	if ctx == nil {
		return nil, invalidArgument("natsnet: Subscribe Context 不能为空")
	}
	if strings.TrimSpace(subject) == "" {
		return nil, invalidArgument("natsnet: Subscribe Subject 不能为空")
	}
	if handler == nil {
		return nil, invalidArgument("natsnet: MessageHandler 不能为空")
	}
	resolved, err := validateSubscriptionOptions(
		conn.options.Subscription,
		options,
	)
	if err != nil {
		return nil, err
	}
	if !conn.acceptingWork() {
		return nil, errs.ErrTransportClosed
	}

	// nats.go 会在 Subscribe 返回前启动异步订阅 goroutine。远端消息理论上可能在包装对象
	// 登记和 Flush 屏障完成前到达，因此使用一次性就绪屏障保证：
	// 1. Subscribe 成功返回前不执行用户 Handler；
	// 2. 任一初始化步骤失败时，已经进入官方队列的消息只退出回调，不接触半初始化对象；
	// 3. 该屏障只在订阅创建阶段等待，不进入后续消息热路径，也不创建额外 goroutine。
	callbackReady := make(chan struct{})
	var activeSubscription *Subscription
	waitForActivation := true
	callback := func(message *nats.Msg) {
		// nats.go 保证同一异步订阅的 Handler 串行执行，因此只有第一条消息需要等待屏障；
		// 后续热路径只进行一次可预测布尔判断，不再读取已关闭 Channel。
		if waitForActivation {
			<-callbackReady
			waitForActivation = false
		}
		if activeSubscription != nil {
			activeSubscription.deliver(message)
		}
	}

	var raw *nats.Subscription
	if resolved.Queue == "" {
		raw, err = conn.raw.Subscribe(subject, callback)
	} else {
		raw, err = conn.raw.QueueSubscribe(subject, resolved.Queue, callback)
	}
	if err != nil {
		return nil, mapError(redactCause(err, conn.options))
	}

	// 只限制待回调消息数；字节额度固定为 -1，避免重复且难解释的双重配置。
	if err = raw.SetPendingLimits(resolved.PendingMessages, -1); err != nil {
		_ = raw.Unsubscribe()
		// 解除可能已经取得消息的官方回调；activeSubscription 仍为空，因此不会调用业务。
		close(callbackReady)
		return nil, mapError(redactCause(err, conn.options))
	}
	subscription := newSubscription(conn, raw, subject, resolved.Queue, handler)

	// Drain/Close 可能与订阅创建竞态；登记时再次检查准入，失败则逆序注销。
	if !conn.registerSubscription(raw, subscription) {
		subscription.Close()
		// 登记失败表示调用方不会取得 Subscription，丢弃屏障前已经到达的消息。
		close(callbackReady)
		return nil, errs.ErrTransportClosed
	}
	subscription.startMonitor()

	// Flush 是订阅创建的唯一自动屏障；普通 Publish 热路径不会增加网络往返。
	if err = conn.Flush(ctx); err != nil {
		subscription.Close()
		// Flush 失败时 Subscribe 对外失败，屏障前消息不能越过失败的 API 边界进入业务。
		close(callbackReady)
		return nil, err
	}

	// 包装对象、Pending 上限、所有权登记和 Server 屏障全部完成后，才一次性开放回调。
	activeSubscription = subscription
	close(callbackReady)
	return subscription, nil
}

// Flush 等待 Server 处理当前连接在调用前写出的协议命令。
func (conn *Conn) Flush(ctx context.Context) error {
	// 官方 FlushWithContext 要求非 nil 且带 Deadline，包装层统一补默认值。
	if ctx == nil {
		return invalidArgument("natsnet: Flush Context 不能为空")
	}
	if !conn.acceptingWork() {
		return errs.ErrTransportClosed
	}
	operationCtx, cancel := boundedContext(ctx, conn.options.DefaultOperationTimeout)
	defer cancel()

	err := conn.raw.FlushWithContext(operationCtx)
	return mapError(redactCause(err, conn.options))
}

// Status 返回当前 Connection 生命周期状态快照。
func (conn *Conn) Status() Status {
	// Status 的底层值由包装层原子维护，不需要获取官方客户端内部锁。
	return Status(conn.status.Load())
}

// MaxPayload 返回当前 NATS Server 在 INFO 中公布的单消息 payload 上限。
//
// RPC Adapter 在创建 Subscription 前读取该值，确保“业务上限 + Origin 包络”不会在运行中
// 才被 Server 拒绝。nats.go 会在重连后原子更新 Server INFO，因此该读取可以并发使用。
func (conn *Conn) MaxPayload() int64 {
	if conn == nil || conn.raw == nil {
		return 0
	}
	return conn.raw.MaxPayload()
}

// Stats 返回官方客户端已经维护的累计统计，不增加热路径原子计数。
func (conn *Conn) Stats() ConnStats {
	// 初始连接失败不会返回 Conn；成功对象的 raw 在生命周期内始终非 nil。
	stats := conn.raw.Stats()
	return ConnStats{
		InMessages:  stats.InMsgs,
		OutMessages: stats.OutMsgs,
		InBytes:     stats.InBytes,
		OutBytes:    stats.OutBytes,
		Reconnects:  stats.Reconnects,
	}
}

// Drain 停止新 Publish/Subscribe，排空现有 Subscription 和 Publish 后关闭连接。
func (conn *Conn) Drain(ctx context.Context) error {
	// nil Context 无法形成排空退出条件，必须明确拒绝。
	if ctx == nil {
		return invalidArgument("natsnet: Drain Context 不能为空")
	}

	// 第一个调用提交 Drain 状态；重复调用只等待同一个最终结果。
	conn.stateMu.Lock()
	if conn.closeRequested {
		result := conn.terminalResultLocked()
		conn.stateMu.Unlock()
		return result
	}
	first := !conn.drainRequested
	if first {
		conn.drainRequested = true
		conn.status.Store(uint32(StatusDraining))
	}
	conn.stateMu.Unlock()

	if first {
		// nats.Drain 立即启动官方排空 goroutine；真正完成由 ClosedHandler 发布。
		if err := conn.raw.Drain(); err != nil {
			mapped := mapError(redactCause(err, conn.options))
			conn.setTerminal(mapped)
			conn.raw.Close()
			<-conn.done
			return mapped
		}
	}

	// Wrapper Deadline 与官方 DrainTimeout 同时生效，采用更早的边界。
	limit := minDuration(
		conn.options.DefaultOperationTimeout,
		conn.options.DrainTimeout,
	)
	operationCtx, cancel := boundedContext(ctx, limit)
	defer cancel()

	select {
	case <-conn.done:
		return conn.terminalResult()
	case <-operationCtx.Done():
		mapped := mapError(operationCtx.Err())
		conn.setTerminal(mapped)
		// Context 超时后强制 Close，避免官方 Drain goroutine永久占用连接资源。
		conn.raw.Close()
		<-conn.done
		return mapped
	}
}

// Close 幂等地立即关闭连接，不等待 Pending 消息或本地写缓冲排空。
func (conn *Conn) Close() {
	// 关闭意图和稳定终态原因在调用官方 Close 前提交，使新工作立即被拒绝。
	conn.stateMu.Lock()
	if !conn.closeRequested {
		conn.closeRequested = true
		if !conn.terminalSet {
			conn.terminalSet = true
			conn.terminal = errs.ErrTransportClosed
		}
		conn.status.Store(uint32(StatusClosed))
	}
	conn.stateMu.Unlock()
	conn.raw.Close()
}

// Wait 等待 EventClosed、全部包装 Subscription 关闭以及最终资源清理完成。
func (conn *Conn) Wait(ctx context.Context) error {
	// nil Context 会使 select 访问 Done 时 panic，因此在等待前拒绝。
	if ctx == nil {
		return invalidArgument("natsnet: Wait Context 不能为空")
	}

	// 已完成时优先返回确定终态，避免 done 与 Context 同时就绪造成随机结果。
	select {
	case <-conn.done:
		return conn.terminalResult()
	default:
	}
	select {
	case <-conn.done:
		return conn.terminalResult()
	case <-ctx.Done():
		return mapError(ctx.Err())
	}
}

// acceptingWork 报告当前是否接受新的 Publish、Subscribe 和 Flush。
func (conn *Conn) acceptingWork() bool {
	// Reconnecting 期间 Publish 可以进入官方有界重连缓冲，仍属于允许准入状态。
	switch conn.Status() {
	case StatusConnected, StatusReconnecting:
		return true
	default:
		return false
	}
}

// registerSubscription 在同一状态锁边界内复核准入并登记最终所有权。
func (conn *Conn) registerSubscription(
	raw *nats.Subscription,
	subscription *Subscription,
) bool {
	conn.stateMu.Lock()
	defer conn.stateMu.Unlock()

	// Drain 或 Close 已经提交后不能让竞态创建的新 Subscription 逃出 Connection 管理。
	if conn.closeRequested || conn.drainRequested {
		return false
	}
	conn.subscriptions[raw] = subscription
	return true
}

// unregisterSubscription 移除已经单独关闭或完成 Drain 的订阅登记。
func (conn *Conn) unregisterSubscription(raw *nats.Subscription) {
	// map 只在短锁内修改，不能跨用户 Handler 或官方客户端调用持有。
	conn.stateMu.Lock()
	delete(conn.subscriptions, raw)
	conn.stateMu.Unlock()
}

// subscriptionFor 返回异步错误关联的包装 Subscription。
func (conn *Conn) subscriptionFor(raw *nats.Subscription) *Subscription {
	// ErrorHandler 可能在订阅登记之前或移除之后到达，查不到时返回 nil 即可。
	conn.stateMu.Lock()
	subscription := conn.subscriptions[raw]
	conn.stateMu.Unlock()
	return subscription
}

// handleDisconnected 处理官方客户端断开回调。
func (conn *Conn) handleDisconnected(raw *nats.Conn, cause error) {
	// 主动 Drain/Close 期间的中间断开不重复发布误导性的自动重连状态。
	if conn.Status() == StatusDraining || conn.Status() == StatusClosed {
		return
	}
	conn.status.Store(uint32(StatusReconnecting))
	mapped := mapError(redactCause(cause, conn.options))
	url := connectedURL(raw)
	conn.logger.Warn(
		"NATS 连接已断开",
		originlog.String("server_url", url),
		originlog.Err(mapped),
	)
	conn.emit(Event{
		Type: EventDisconnected,
		URL:  url,
		Err:  mapped,
	})
}

// handleReconnected 处理官方客户端成功重连回调。
func (conn *Conn) handleReconnected(raw *nats.Conn) {
	// 与 Drain/Close 竞态的迟到回调不能把终态重新标记为 Connected。
	if conn.Status() == StatusDraining || conn.Status() == StatusClosed {
		return
	}
	conn.status.Store(uint32(StatusConnected))
	url := connectedURL(raw)
	conn.logger.Info(
		"NATS 连接已恢复",
		originlog.String("server_url", url),
	)
	conn.emit(Event{Type: EventReconnected, URL: url})
}

// handleLameDuck 转发 Server 优雅退出提示，不自行改变 Origin Node 状态。
func (conn *Conn) handleLameDuck(raw *nats.Conn) {
	// Lame Duck 是基础设施提示，由后续 Node 层决定是否退休或主动迁移。
	url := connectedURL(raw)
	conn.logger.Warn(
		"NATS Server 进入 Lame Duck",
		originlog.String("server_url", url),
	)
	conn.emit(Event{Type: EventLameDuck, URL: url})
}

// handleAsyncError 转换慢消费者、权限和其他官方异步错误。
func (conn *Conn) handleAsyncError(
	raw *nats.Conn,
	rawSubscription *nats.Subscription,
	cause error,
) {
	// 异步 cause 先脱敏再映射；Event 和日志不得出现凭据。
	mapped := mapError(redactCause(cause, conn.options))
	subject := ""
	var subscription *Subscription
	if rawSubscription != nil {
		subject = rawSubscription.Subject
		subscription = conn.subscriptionFor(rawSubscription)
	}

	// 慢消费者只为同一订阅记录首次告警，避免持续过载形成日志风暴。
	shouldLog := true
	if errors.Is(cause, nats.ErrSlowConsumer) &&
		subscription != nil &&
		!subscription.markOverloadLogged() {
		shouldLog = false
	}
	if shouldLog {
		fields := []originlog.Field{
			originlog.String("server_url", connectedURL(raw)),
			originlog.String("subject", subject),
			originlog.Err(mapped),
		}
		if subscription != nil {
			stats := subscription.Stats()
			fields = append(
				fields,
				originlog.Int("pending_messages", stats.PendingMessages),
				originlog.Int("dropped_messages", stats.DroppedMessages),
			)
		}
		conn.logger.Warn("NATS 异步错误", fields...)
	}
	conn.emit(Event{
		Type:    EventAsyncError,
		URL:     connectedURL(raw),
		Subject: subject,
		Err:     mapped,
	})
}

// reportHandlerError 报告包装层 Handler panic 或入站消息越界。
func (conn *Conn) reportHandlerError(subject string, cause error) {
	// 包装层错误不经过 nats.go ErrorHandler，直接复用统一日志和事件外观。
	conn.logger.Error(
		"NATS 消息 Handler 异常",
		originlog.String("server_url", connectedURL(conn.raw)),
		originlog.String("subject", subject),
		originlog.Err(cause),
	)
	conn.emit(Event{
		Type:    EventAsyncError,
		URL:     connectedURL(conn.raw),
		Subject: subject,
		Err:     cause,
	})
}

// handleClosed 提交 Connection 最终状态并完成全部 Wait。
func (conn *Conn) handleClosed(raw *nats.Conn) {
	// 官方客户端和主动 Close 可能从不同路径触发关闭，所有终态动作只能执行一次。
	conn.finishOnce.Do(func() {
		conn.stateMu.Lock()
		if !conn.terminalSet {
			conn.terminalSet = true
			switch {
			case conn.drainRequested:
				// 正常 Drain 的终态是成功，不制造 TransportClosed。
				conn.terminal = nil
			case conn.closeRequested:
				conn.terminal = errs.ErrTransportClosed
			default:
				lastError := redactCause(raw.LastError(), conn.options)
				if lastError == nil {
					conn.terminal = errs.ErrTransportUnavailable
				} else {
					conn.terminal = mapError(lastError)
				}
			}
		}
		terminal := conn.terminal
		subscriptions := make([]*Subscription, 0, len(conn.subscriptions))
		for _, subscription := range conn.subscriptions {
			subscriptions = append(subscriptions, subscription)
		}
		clear(conn.subscriptions)
		conn.status.Store(uint32(StatusClosed))
		conn.stateMu.Unlock()

		// Connection 是全部 Subscription 的最终所有者；先完成包装订阅，再发布关闭事件。
		for _, subscription := range subscriptions {
			subscription.finish()
		}
		url := connectedURL(raw)
		conn.logger.Info(
			"NATS 连接已关闭",
			originlog.String("server_url", url),
			originlog.Err(terminal),
		)
		conn.emit(Event{
			Type: EventClosed,
			URL:  url,
			Err:  terminal,
		})
		close(conn.done)
	})
}

// emit 安全调用 EventHandler，不让业务 panic 破坏 nats.go 回调调度器。
func (conn *Conn) emit(event Event) {
	// nil Handler 是设计允许的零成本路径。
	if conn.handler == nil {
		return
	}
	defer func() {
		if value := recover(); value != nil {
			cause := panicError("natsnet EventHandler", value)
			conn.logger.Error(
				"NATS EventHandler panic",
				originlog.String("server_url", event.URL),
				originlog.Err(cause),
			)
		}
	}()
	conn.handler(event)
}

// setTerminal 只保存第一个有效终止原因。
func (conn *Conn) setTerminal(cause error) {
	conn.stateMu.Lock()
	if !conn.terminalSet {
		conn.terminalSet = true
		conn.terminal = cause
	}
	conn.stateMu.Unlock()
}

// terminalResult 返回已经提交的最终结果。
func (conn *Conn) terminalResult() error {
	conn.stateMu.Lock()
	result := conn.terminalResultLocked()
	conn.stateMu.Unlock()
	return result
}

// terminalResultLocked 在持有 stateMu 时返回最终结果。
func (conn *Conn) terminalResultLocked() error {
	// 运行中的连接尚无终态；该分支只可能由内部竞态触发，使用 Closed 作为安全兜底。
	if !conn.terminalSet {
		return errs.ErrTransportClosed
	}
	return conn.terminal
}

// connectedURL 返回官方客户端已经脱敏并再次移除 Query 的 Server 地址。
func connectedURL(raw *nats.Conn) string {
	// 回调极端竞态中 raw 可能为空，空字符串比泄露原始配置更安全。
	if raw == nil {
		return ""
	}
	return safeURL(raw.ConnectedUrlRedacted())
}

// boundedContext 保留更早的调用方 Deadline，否则增加固定超时。
func boundedContext(parent context.Context, limit time.Duration) (context.Context, context.CancelFunc) {
	// WithTimeout 会自动保留父 Context 更早的 Deadline 和取消状态。
	return context.WithTimeout(parent, limit)
}

// minDuration 返回两个正 Duration 中较小者。
func minDuration(left, right time.Duration) time.Duration {
	// 两个值都已经通过 Options 校验为正数，不需要处理无界零值。
	if left < right {
		return left
	}
	return right
}
