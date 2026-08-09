package rpc

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/duanhf2012/origin/v3/errs"
	"github.com/duanhf2012/origin/v3/internal/natsnet"
	"github.com/duanhf2012/origin/v3/internal/timerwheel"
	originlog "github.com/duanhf2012/origin/v3/log"
)

const (
	// NATS Request 的最坏包络包含固定头、255 字节来源 NodeID 和 255 字节 ServiceName。
	natsMaximumEnvelopeSize = natsRequestFixedSize + 2*wireMaxNameSize
)

// natsRuntime 管理一个 Node 独占的 NATS Connection、两个 Subscription 和 pending。
//
// 服务发现仍是目标 Node/Service/SessionID 的唯一事实来源；本对象只缓存由 Namespace 和
// NodeID 推导出的 Subject 字符串，不建立第二份路由目录。
type natsRuntime struct {
	owner  *Runtime
	config Config

	mu          sync.Mutex
	started     bool
	stopping    bool
	closed      bool
	conn        *natsnet.Conn
	requestSub  *natsnet.Subscription
	responseSub *natsnet.Subscription
	systemSubs  []*natsnet.Subscription
	deadlines   *inboundDeadlines
	engine      *timerwheel.Engine

	// generation 只隔离当前进程内的 Transport 实例；Node SessionID 在整个进程生命周期
	// 保持不变。recoveryWake 容量为 1，用于合并重复 Closed 回调。
	generation          uint64
	activeGeneration    atomic.Uint64
	activeConnection    atomic.Pointer[natsConnectionView]
	reconnects          uint64
	consecutiveFailures uint64
	recoveryCancel      context.CancelFunc
	recoveryWake        chan struct{}
	recoveryDone        chan struct{}

	pending *natsPendingTable

	// Subject 在第一次见到 NodeID 时建立，后续 RPC 只读缓存字符串。
	subjectMu        sync.RWMutex
	requestSubjects  map[string]string
	responseSubjects map[string]string
	localRequest     string
	localResponse    string
}

type natsConnectionView struct {
	conn       *natsnet.Conn
	generation uint64
}

// newNATSRuntime 创建尚未连接 Broker、没有后台资源的 NATS Runtime。
func newNATSRuntime(owner *Runtime, config Config) *natsRuntime {
	return &natsRuntime{
		owner:            owner,
		config:           config,
		pending:          newNATSPendingTable(DefaultPendingPerNode),
		requestSubjects:  make(map[string]string),
		responseSubjects: make(map[string]string),
	}
}

// start 建立首个 Connection，并启动唯一外层恢复 owner。
func (runtime *natsRuntime) start(
	ctx context.Context,
	engine *timerwheel.Engine,
) error {
	if runtime == nil || ctx == nil || engine == nil {
		return errs.ErrInvalidArgument
	}
	runtime.mu.Lock()
	if runtime.started || runtime.stopping || runtime.closed {
		runtime.mu.Unlock()
		return errs.ErrInvalidArgument
	}
	if runtime.owner.sessionID == 0 ||
		!validSubjectToken(runtime.owner.nodeID) {
		runtime.mu.Unlock()
		return invalidRPCConfig("NATS RPC NodeID 必须是小写 kebab-case 且 SessionID 非零")
	}
	runtime.localRequest = natsRequestSubject(
		runtime.config.NATS.Namespace,
		runtime.owner.nodeID,
	)
	runtime.localResponse = natsResponseSubject(
		runtime.config.NATS.Namespace,
		runtime.owner.nodeID,
	)
	runtime.subjectMu.Lock()
	runtime.requestSubjects[runtime.owner.nodeID] = runtime.localRequest
	runtime.responseSubjects[runtime.owner.nodeID] = runtime.localResponse
	runtime.subjectMu.Unlock()
	recoveryCtx, recoveryCancel := context.WithCancel(context.Background())
	runtime.engine = engine
	runtime.generation = 1
	runtime.activeGeneration.Store(runtime.generation)
	runtime.recoveryCancel = recoveryCancel
	runtime.recoveryWake = make(chan struct{}, 1)
	runtime.recoveryDone = make(chan struct{})
	generation := runtime.generation
	runtime.mu.Unlock()

	delay := reconnectInitialDelay
	for {
		err := runtime.connectGeneration(ctx, generation)
		if err == nil {
			break
		}
		if natsnet.IsAuthenticationError(err) ||
			errs.IsCode(err, errs.CodeInvalidConfig) {
			recoveryCancel()
			return err
		}
		runtime.mu.Lock()
		runtime.consecutiveFailures++
		failures := runtime.consecutiveFailures
		runtime.mu.Unlock()
		runtime.owner.reportTransportEvent(TransportEvent{
			Kind:                TransportKindNATS,
			State:               TransportStateRecovering,
			ConsecutiveFailures: failures,
			ErrorCode:           errs.CodeTransportUnavailable,
			Cause:               err,
		})
		if !waitTransportBackoff(ctx, delay) {
			recoveryCancel()
			return contextError(context.Cause(ctx))
		}
		delay = nextTransportBackoff(delay)
		runtime.mu.Lock()
		runtime.generation++
		generation = runtime.generation
		runtime.activeGeneration.Store(generation)
		runtime.mu.Unlock()
	}
	runtime.mu.Lock()
	runtime.started = true
	runtime.consecutiveFailures = 0
	runtime.mu.Unlock()
	go runtime.recoveryLoop(recoveryCtx)
	return nil
}

// connectGeneration 创建并发布一整组 Connection、Subscription 和入站 Deadline。
//
// 该函数只在首次启动 goroutine 或唯一恢复 owner 中调用，因此不会并行创建两代资源。
func (runtime *natsRuntime) connectGeneration(
	ctx context.Context,
	generation uint64,
) error {
	runtime.mu.Lock()
	engine := runtime.engine
	runtime.mu.Unlock()
	if ctx == nil || engine == nil {
		return errs.ErrInvalidArgument
	}
	deadlines, err := newInboundDeadlines(engine)
	if err != nil {
		return err
	}
	options := runtime.connectionOptions()
	conn, err := natsnet.Connect(
		ctx,
		options,
		func(event natsnet.Event) {
			runtime.handleGenerationEvent(generation, event)
		},
	)
	if err != nil {
		deadlines.close(errs.ErrServiceStopped)
		return err
	}

	// Broker 必须能够承载业务上限加最坏 Origin 包络；否则启动后大包必然随机失败。
	requiredPayload := int64(runtime.config.MaxPayloadSize + natsMaximumEnvelopeSize)
	if runtime.owner.system != nil && requiredPayload < MaxSystemMessageSize {
		requiredPayload = MaxSystemMessageSize
	}
	if conn.MaxPayload() < requiredPayload {
		conn.Close()
		_ = conn.Wait(context.Background())
		deadlines.close(errs.ErrServiceStopped)
		return invalidRPCConfig(fmt.Sprintf(
			"NATS Server max_payload=%d，小于 Origin RPC 所需 %d",
			conn.MaxPayload(),
			requiredPayload,
		))
	}

	runtime.mu.Lock()
	if runtime.stopping || runtime.closed || runtime.generation != generation {
		runtime.mu.Unlock()
		conn.Close()
		_ = conn.Wait(context.Background())
		deadlines.close(errs.ErrServiceStopped)
		return errs.ErrServiceStopped
	}
	runtime.conn = conn
	runtime.deadlines = deadlines
	runtime.mu.Unlock()

	subscriptionOptions := natsnet.SubscriptionOptions{
		PendingMessages: runtime.config.NATS.ReceiveQueueMessages,
	}
	// 先建立 Response Subscription，确保第一个 Request 发出前调用方已经具备响应路径。
	responseSub, err := conn.Subscribe(
		ctx,
		runtime.localResponse,
		subscriptionOptions,
		func(message natsnet.Message) {
			if runtime.generationCurrent(generation) {
				runtime.handleResponse(message)
			}
		},
	)
	if err != nil {
		runtime.discardGeneration(generation, conn, nil, nil, deadlines)
		return err
	}
	runtime.mu.Lock()
	runtime.responseSub = responseSub
	runtime.mu.Unlock()

	requestSub, err := conn.Subscribe(
		ctx,
		runtime.localRequest,
		subscriptionOptions,
		func(message natsnet.Message) {
			if runtime.generationCurrent(generation) {
				runtime.handleInbound(message)
			}
		},
	)
	if err != nil {
		runtime.discardGeneration(
			generation,
			conn,
			nil,
			responseSub,
			deadlines,
		)
		return err
	}
	runtime.mu.Lock()
	runtime.requestSub = requestSub
	runtime.mu.Unlock()
	var systemSubs []*natsnet.Subscription
	if runtime.owner.system != nil {
		systemSubs, err = runtime.owner.system.setupNATS(
			ctx,
			conn,
			runtime.config.NATS.Namespace,
			runtime.config.NATS.ReceiveQueueMessages,
		)
		if err != nil {
			runtime.discardGeneration(
				generation,
				conn,
				requestSub,
				responseSub,
				deadlines,
			)
			return err
		}
	}
	runtime.mu.Lock()
	runtime.systemSubs = systemSubs
	// Closed 与发布使用同一代次线性化：已经终止的连接不能被宣布为当前 Ready。
	if runtime.stopping || runtime.closed ||
		runtime.generation != generation ||
		conn.Status() != natsnet.StatusConnected {
		runtime.mu.Unlock()
		runtime.discardGeneration(
			generation,
			conn,
			requestSub,
			responseSub,
			deadlines,
		)
		return errs.ErrTransportUnavailable
	}
	runtime.activeConnection.Store(&natsConnectionView{
		conn:       conn,
		generation: generation,
	})
	runtime.mu.Unlock()
	runtime.owner.NotifyRoutesChanged()
	return nil
}

// generationCurrent 是 NATS 消息回调进入解析前的冷分支代次校验。
func (runtime *natsRuntime) generationCurrent(generation uint64) bool {
	// 一次原子读取替代逐消息互斥锁。Draining 期间仍保留当前代次，让已经接受的 Request
	// 和 Response 完成；正式 Close 或 Closed 外层重建才把活动代次切换掉。
	return generation != 0 && runtime.activeGeneration.Load() == generation
}

// discardGeneration 回收一次没有成功发布或已经被新代次取代的资源。
func (runtime *natsRuntime) discardGeneration(
	generation uint64,
	conn *natsnet.Conn,
	requestSub *natsnet.Subscription,
	responseSub *natsnet.Subscription,
	deadlines *inboundDeadlines,
) {
	if runtime.clearActiveConnection(generation, conn) {
		runtime.owner.NotifyRoutesChanged()
	}
	runtime.mu.Lock()
	var systemSubs []*natsnet.Subscription
	if runtime.generation == generation {
		if runtime.conn == conn {
			runtime.conn = nil
		}
		if runtime.requestSub == requestSub {
			runtime.requestSub = nil
		}
		if runtime.responseSub == responseSub {
			runtime.responseSub = nil
		}
		if runtime.deadlines == deadlines {
			runtime.deadlines = nil
		}
		if runtime.conn == nil {
			systemSubs = runtime.systemSubs
			runtime.systemSubs = nil
		}
	}
	runtime.mu.Unlock()
	if requestSub != nil {
		requestSub.Close()
	}
	if responseSub != nil {
		responseSub.Close()
	}
	for _, subscription := range systemSubs {
		subscription.Close()
	}
	if conn != nil {
		conn.Close()
		_ = conn.Wait(context.Background())
	}
	if deadlines != nil {
		deadlines.close(errs.ErrTransportUnavailable)
	}
	if runtime.owner.system != nil {
		runtime.owner.system.notifyNATSDisconnected(errs.ErrTransportUnavailable)
	}
}

func (runtime *natsRuntime) clearActiveConnection(
	generation uint64,
	conn *natsnet.Conn,
) bool {
	for {
		current := runtime.activeConnection.Load()
		if current == nil ||
			current.generation != generation ||
			(conn != nil && current.conn != conn) {
			return false
		}
		if runtime.activeConnection.CompareAndSwap(current, nil) {
			return true
		}
	}
}

// recoveryLoop 在非 Stop Closed 后持续重建整组 NATS 资源。
func (runtime *natsRuntime) recoveryLoop(ctx context.Context) {
	defer close(runtime.recoveryDone)
	for {
		select {
		case <-ctx.Done():
			return
		case <-runtime.recoveryWake:
		}

		// EventClosed 回调本身不能等待 natsnet.Conn，否则会等待自己的完成发布。唯一恢复
		// owner 在回调返回后统一拆除旧代资源，再开始下一代连接。
		runtime.mu.Lock()
		generation := runtime.generation
		conn := runtime.conn
		requestSub := runtime.requestSub
		responseSub := runtime.responseSub
		deadlines := runtime.deadlines
		runtime.mu.Unlock()
		runtime.discardGeneration(
			generation,
			conn,
			requestSub,
			responseSub,
			deadlines,
		)

		delay := reconnectInitialDelay
		for {
			if !waitTransportBackoff(ctx, delay) {
				return
			}

			// 每次尝试使用新代次，确保上一代迟到 Event/Message 无法覆盖恢复结果。
			runtime.mu.Lock()
			if runtime.stopping || runtime.closed {
				runtime.mu.Unlock()
				return
			}
			runtime.generation++
			generation := runtime.generation
			runtime.activeGeneration.Store(generation)
			runtime.mu.Unlock()

			err := runtime.connectGeneration(ctx, generation)
			if err != nil {
				runtime.mu.Lock()
				runtime.consecutiveFailures++
				failures := runtime.consecutiveFailures
				reconnects := runtime.reconnects
				runtime.mu.Unlock()
				runtime.owner.reportTransportEvent(TransportEvent{
					Kind:                TransportKindNATS,
					State:               TransportStateRecovering,
					Reconnects:          reconnects,
					ConsecutiveFailures: failures,
					ErrorCode:           errs.CodeTransportUnavailable,
					Cause:               err,
				})
				delay = nextTransportBackoff(delay)
				continue
			}

			runtime.mu.Lock()
			runtime.reconnects++
			runtime.consecutiveFailures = 0
			reconnects := runtime.reconnects
			runtime.mu.Unlock()
			runtime.owner.logger.Info(
				"NATS RPC Transport 已完成外层重建",
				originlog.Uint64("transport_generation", generation),
			)
			runtime.owner.reportTransportEvent(TransportEvent{
				Kind:       TransportKindNATS,
				State:      TransportStateReady,
				Reconnects: reconnects,
			})
			break
		}
	}
}

// connectionOptions 把稳定 RPC 配置映射到 M6 原生 Options。
func (runtime *natsRuntime) connectionOptions() natsnet.Options {
	config := runtime.config.NATS
	options := natsnet.DefaultOptions(
		"origin-rpc-"+config.Namespace+"-"+runtime.owner.nodeID,
		config.URLs...,
	)
	options.NoEcho = true
	options.IgnoreAuthErrorAbort = true
	options.MaxMessageSize = runtime.config.MaxPayloadSize + natsMaximumEnvelopeSize
	if runtime.owner.system != nil && options.MaxMessageSize < MaxSystemMessageSize {
		options.MaxMessageSize = MaxSystemMessageSize
	}
	options.Reconnect.MaxAttempts = -1
	options.Reconnect.BufferSize = -1
	options.Subscription.PendingMessages = config.ReceiveQueueMessages
	options.Auth = natsnet.AuthOptions{
		Username:        config.Auth.Username,
		Password:        config.Auth.Password,
		Token:           config.Auth.Token,
		CredentialsFile: config.Auth.CredentialsFile,
		NKeySeedFile:    config.Auth.NKeySeedFile,
	}
	options.TLS = natsnet.TLSOptions{
		Enabled:            config.TLS.Enabled,
		CAFile:             config.TLS.CAFile,
		CertFile:           config.TLS.CertFile,
		KeyFile:            config.TLS.KeyFile,
		ServerName:         config.TLS.ServerName,
		InsecureSkipVerify: config.TLS.InsecureSkipVerify,
	}
	options.Logger = runtime.owner.logger
	return options
}

// beginStop 只排空并关闭 Request Subscription，保留响应和出站调用能力。
func (runtime *natsRuntime) beginStop(ctx context.Context) error {
	if runtime == nil || ctx == nil {
		return errs.ErrInvalidArgument
	}
	runtime.mu.Lock()
	runtime.stopping = true
	cancelRecovery := runtime.recoveryCancel
	requestSub := runtime.requestSub
	runtime.mu.Unlock()
	if cancelRecovery != nil {
		cancelRecovery()
	}
	if requestSub == nil {
		return nil
	}
	if err := requestSub.Drain(ctx); err != nil {
		requestSub.Close()
		return err
	}
	return nil
}

// close 最终关闭 Subscription、Connection、入站 Deadline，并完成全部 pending。
func (runtime *natsRuntime) close(ctx context.Context) error {
	if runtime == nil {
		return nil
	}
	if ctx == nil {
		return errs.ErrInvalidArgument
	}
	runtime.mu.Lock()
	if runtime.closed {
		runtime.mu.Unlock()
		return nil
	}
	runtime.closed = true
	runtime.stopping = true
	runtime.activeGeneration.Store(0)
	hadActive := runtime.activeConnection.Swap(nil) != nil
	cancelRecovery := runtime.recoveryCancel
	recoveryDone := runtime.recoveryDone
	recoveryStarted := runtime.started
	requestSub := runtime.requestSub
	responseSub := runtime.responseSub
	systemSubs := runtime.systemSubs
	conn := runtime.conn
	deadlines := runtime.deadlines
	runtime.requestSub = nil
	runtime.responseSub = nil
	runtime.systemSubs = nil
	runtime.conn = nil
	runtime.deadlines = nil
	runtime.mu.Unlock()
	if hadActive {
		runtime.owner.NotifyRoutesChanged()
	}
	if cancelRecovery != nil {
		cancelRecovery()
	}

	if requestSub != nil {
		requestSub.Close()
	}
	if responseSub != nil {
		responseSub.Close()
	}
	for _, subscription := range systemSubs {
		subscription.Close()
	}
	runtime.pending.failAll(errs.ErrTransportUnavailable)
	var result error
	if conn != nil {
		conn.Close()
		result = conn.Wait(ctx)
		// 本地 Close 会让 natsnet 以 CodeTransportClosed 完成 Wait；这是预期终态，
		// 不能污染一次正常的 Node/Application 优雅停止结果。
		if errors.Is(result, errs.ErrTransportClosed) {
			result = nil
		}
	}
	if deadlines != nil {
		deadlines.close(errs.ErrServiceStopped)
	}
	if recoveryStarted && recoveryDone != nil {
		select {
		case <-recoveryDone:
		case <-ctx.Done():
			result = errors.Join(result, contextError(context.Cause(ctx)))
		}
	}
	return result
}

// sendRequest 预占 Node 级 pending、发布 NATS Request，并在成功后释放请求 Buffer。
func (runtime *natsRuntime) sendRequest(
	targetNodeID string,
	targetSessionID uint64,
	serviceName string,
	methodID MethodID,
	remaining time.Duration,
	request *Buffer,
	complete func(*Buffer, error),
) (remoteRequestHandle, error) {
	if runtime == nil || request == nil {
		return remoteRequestHandle{}, errs.ErrInvalidArgument
	}
	conn, err := runtime.connectedConn()
	if err != nil {
		return remoteRequestHandle{}, err
	}
	return runtime.sendRequestWithConn(
		conn,
		targetNodeID,
		targetSessionID,
		serviceName,
		methodID,
		remaining,
		request,
		complete,
	)
}

func (runtime *natsRuntime) sendRequestWithConn(
	conn *natsnet.Conn,
	targetNodeID string,
	targetSessionID uint64,
	serviceName string,
	methodID MethodID,
	remaining time.Duration,
	request *Buffer,
	complete func(*Buffer, error),
) (remoteRequestHandle, error) {
	if runtime == nil || conn == nil || request == nil {
		return remoteRequestHandle{}, errs.ErrInvalidArgument
	}
	subject, err := runtime.requestSubject(targetNodeID)
	if err != nil {
		return remoteRequestHandle{}, err
	}
	requestID, err := runtime.owner.nextRequestID()
	if err != nil {
		return remoteRequestHandle{}, err
	}
	if err = runtime.pending.reserve(
		requestID,
		targetSessionID,
		complete,
	); err != nil {
		return remoteRequestHandle{}, err
	}
	if err = prependNATSRequest(
		request,
		requestID,
		methodID,
		remaining,
		runtime.owner.sessionID,
		targetSessionID,
		runtime.owner.nodeID,
		serviceName,
	); err != nil {
		runtime.pending.rollback(requestID)
		return remoteRequestHandle{}, err
	}
	if err = conn.Publish(subject, request.Bytes()); err != nil {
		runtime.pending.rollback(requestID)
		return remoteRequestHandle{}, normalizeRemoteClose(err)
	}

	// nats.go 在 Publish 返回前已经复制到协议写缓冲；成功后 Runtime 消费请求所有权。
	request.Release()
	return remoteRequestHandle{
		nats:      runtime,
		requestID: requestID,
		transport: preparedNATS,
	}, nil
}

// sendNotify 发布不建立 pending 的 NATS Notify，并在成功后释放请求 Buffer。
func (runtime *natsRuntime) sendNotify(
	targetNodeID string,
	targetSessionID uint64,
	serviceName string,
	methodID MethodID,
	request *Buffer,
) error {
	if runtime == nil || request == nil {
		return errs.ErrInvalidArgument
	}
	conn, err := runtime.connectedConn()
	if err != nil {
		return err
	}
	return runtime.sendNotifyWithConn(
		conn,
		targetNodeID,
		targetSessionID,
		serviceName,
		methodID,
		request,
	)
}

func (runtime *natsRuntime) sendNotifyWithConn(
	conn *natsnet.Conn,
	targetNodeID string,
	targetSessionID uint64,
	serviceName string,
	methodID MethodID,
	request *Buffer,
) error {
	if runtime == nil || conn == nil || request == nil {
		return errs.ErrInvalidArgument
	}
	subject, err := runtime.requestSubject(targetNodeID)
	if err != nil {
		return err
	}
	if err = prependNATSNotify(
		request,
		methodID,
		targetSessionID,
		serviceName,
	); err != nil {
		return err
	}
	if err = conn.Publish(subject, request.Bytes()); err != nil {
		return normalizeRemoteClose(err)
	}
	request.Release()
	return nil
}

// connectedConn 只允许当前确实连通 Broker 时提交新调用，重连期间不缓冲或重放。
func (runtime *natsRuntime) connectedConn() (*natsnet.Conn, error) {
	if runtime == nil {
		return nil, errs.ErrTransportUnavailable
	}
	view := runtime.activeConnection.Load()
	if view == nil ||
		view.conn == nil ||
		view.generation == 0 ||
		view.conn.Status() != natsnet.StatusConnected {
		return nil, errs.ErrTransportUnavailable
	}
	return view.conn, nil
}

func (runtime *natsRuntime) preparedConn(
	prepared *natsConnectionView,
) (*natsnet.Conn, error) {
	if runtime == nil || prepared == nil {
		return nil, errs.ErrTransportUnavailable
	}
	current := runtime.activeConnection.Load()
	if current != prepared ||
		current.conn == nil ||
		current.generation == 0 ||
		current.conn.Status() != natsnet.StatusConnected {
		return nil, errs.ErrTransportUnavailable
	}
	return current.conn, nil
}

// handleInbound 在 nats.go 的顺序回调 goroutine 中完成轻量解析和 Service 队列准入。
func (runtime *natsRuntime) handleInbound(message natsnet.Message) {
	if len(message.Data) == 0 {
		return
	}
	switch message.Data[0] {
	case natsPacketRequest:
		runtime.handleRequest(message.Data)
	case natsPacketNotify:
		runtime.handleNotify(message.Data)
	}
}

// handleRequest 校验会话和端点，并把只读 Message.Data 唯一转移给 Service 任务闭包。
func (runtime *natsRuntime) handleRequest(data []byte) {
	view, err := parseNATSRequest(data)
	if err != nil {
		runtime.owner.recordInboundRejected(preparedNATS)
		return
	}
	sourceNodeID := string(view.sourceNodeID)
	if !validSubjectToken(sourceNodeID) {
		runtime.owner.recordInboundRejected(preparedNATS)
		return
	}
	if len(data)-view.payloadOffset > runtime.config.MaxPayloadSize {
		runtime.owner.recordInboundRejected(preparedNATS)
		runtime.sendError(
			sourceNodeID,
			view.sourceSessionID,
			view.requestID,
			errs.ErrTransportMessageTooLarge,
		)
		return
	}
	if view.targetSessionID != runtime.owner.sessionID {
		runtime.owner.recordInboundRejected(preparedNATS)
		runtime.sendError(
			sourceNodeID,
			view.sourceSessionID,
			view.requestID,
			errs.ErrTransportUnavailable,
		)
		return
	}
	endpoint, err := runtime.resolveInbound(
		string(view.serviceName),
		view.methodID,
	)
	if err != nil {
		runtime.owner.recordInboundRejected(preparedNATS)
		runtime.sendError(
			sourceNodeID,
			view.sourceSessionID,
			view.requestID,
			err,
		)
		return
	}

	cancelContext, cancel := context.WithCancelCause(context.Background())
	deadlineContext := &remoteDeadlineContext{
		Context:  cancelContext,
		deadline: time.Now().Add(view.remainingTimeout),
	}
	runtime.mu.Lock()
	deadlines := runtime.deadlines
	runtime.mu.Unlock()
	if deadlines == nil {
		runtime.owner.recordInboundRejected(preparedNATS)
		cancel(errs.ErrServiceStopped)
		runtime.sendError(
			sourceNodeID,
			view.sourceSessionID,
			view.requestID,
			errs.ErrServiceStopped,
		)
		return
	}
	deadlineID, err := deadlines.bind(view.remainingTimeout, cancel)
	if err != nil {
		runtime.owner.recordInboundRejected(preparedNATS)
		cancel(err)
		runtime.sendError(
			sourceNodeID,
			view.sourceSessionID,
			view.requestID,
			err,
		)
		return
	}

	payloadBytes := len(data) - view.payloadOffset
	err = endpoint.target.DispatchAsync(func(targetCtx context.Context) {
		defer cancel(nil)
		defer deadlines.unbind(deadlineID)
		if cause := context.Cause(deadlineContext); cause != nil {
			runtime.owner.recordInboundFinished(preparedNATS, cause, 0)
			runtime.sendError(
				sourceNodeID,
				view.sourceSessionID,
				view.requestID,
				cause,
			)
			return
		}
		dispatchContext := &rpcContext{
			execution: targetCtx,
			control:   deadlineContext,
			values:    deadlineContext,
		}
		response, dispatchErr := runtime.owner.dispatchRequest(
			dispatchContext,
			endpoint,
			view.methodID,
			data[view.payloadOffset:],
			natsResponseFixedSize,
		)
		if cause := context.Cause(deadlineContext); cause != nil {
			releaseBuffer(response)
			runtime.owner.recordInboundFinished(preparedNATS, cause, 0)
			runtime.sendError(
				sourceNodeID,
				view.sourceSessionID,
				view.requestID,
				cause,
			)
			return
		}
		responseBytes := 0
		if response != nil {
			responseBytes = len(response.Bytes())
		}
		runtime.owner.recordInboundFinished(
			preparedNATS,
			dispatchErr,
			responseBytes,
		)
		runtime.sendResponse(
			sourceNodeID,
			view.sourceSessionID,
			view.requestID,
			response,
			dispatchErr,
		)
	})
	if err != nil {
		runtime.owner.recordInboundRejected(preparedNATS)
		deadlines.unbind(deadlineID)
		cancel(err)
		runtime.sendError(
			sourceNodeID,
			view.sourceSessionID,
			view.requestID,
			err,
		)
		return
	}
	runtime.owner.recordInboundAccepted(preparedNATS, payloadBytes)
}

// handleNotify 校验目标并把只读 Message.Data 转移给 Service 任务；准入失败只在目标侧丢弃。
func (runtime *natsRuntime) handleNotify(data []byte) {
	view, err := parseNATSNotify(data)
	if err != nil ||
		view.targetSessionID != runtime.owner.sessionID ||
		len(data)-view.payloadOffset > runtime.config.MaxPayloadSize {
		runtime.owner.recordInboundRejected(preparedNATS)
		return
	}
	endpoint, err := runtime.resolveInbound(
		string(view.serviceName),
		view.methodID,
	)
	if err != nil {
		runtime.owner.recordInboundRejected(preparedNATS)
		return
	}
	payloadBytes := len(data) - view.payloadOffset
	err = endpoint.target.DispatchAsync(func(targetCtx context.Context) {
		runtime.owner.dispatchNotify(
			targetCtx,
			endpoint,
			view.methodID,
			data[view.payloadOffset:],
		)
		runtime.owner.recordInboundNotify(preparedNATS, payloadBytes)
	})
	if err != nil {
		runtime.owner.recordInboundRejected(preparedNATS)
	}
}

// resolveInbound 只允许仍接收入站、已 Ready 且公开的精确 Service。
func (runtime *natsRuntime) resolveInbound(
	serviceName string,
	methodID MethodID,
) (serviceEndpoint, error) {
	runtime.mu.Lock()
	stopping := runtime.stopping
	closed := runtime.closed
	runtime.mu.Unlock()
	if stopping || closed {
		return serviceEndpoint{}, errs.ErrServiceStopping
	}
	return runtime.owner.resolveInbound(serviceName, methodID)
}

// sendError 构造空框架错误响应；Publish 失败不关闭共享 Connection，也不重试。
func (runtime *natsRuntime) sendError(
	targetNodeID string,
	targetSessionID uint64,
	requestID uint64,
	cause error,
) {
	buffer := runtime.owner.pool.AcquireWithHeadroom(0, natsResponseFixedSize)
	runtime.sendResponse(
		targetNodeID,
		targetSessionID,
		requestID,
		buffer,
		cause,
	)
}

// sendResponse 发布成功业务响应或空错误响应，并最终释放 Buffer。
func (runtime *natsRuntime) sendResponse(
	targetNodeID string,
	targetSessionID uint64,
	requestID uint64,
	response *Buffer,
	cause error,
) {
	if response == nil {
		response = runtime.owner.pool.AcquireWithHeadroom(
			0,
			natsResponseFixedSize,
		)
	}
	defer response.Release()
	code := errs.CodeOf(cause)
	if cause == nil {
		code = errs.CodeOK
	}
	if err := prependNATSResponse(
		response,
		requestID,
		code,
		runtime.owner.sessionID,
		targetSessionID,
	); err != nil {
		return
	}
	subject, err := runtime.responseSubject(targetNodeID)
	if err != nil {
		return
	}
	runtime.mu.Lock()
	conn := runtime.conn
	closed := runtime.closed
	runtime.mu.Unlock()
	if conn == nil || closed {
		return
	}
	_ = conn.Publish(subject, response.Bytes())
}

// handleResponse 校验双向会话，成功 payload 只复制一次到业务独占 Buffer。
func (runtime *natsRuntime) handleResponse(message natsnet.Message) {
	view, err := parseNATSResponse(message.Data)
	if err != nil ||
		len(message.Data)-view.payloadOffset > runtime.config.MaxPayloadSize {
		return
	}
	call, exists := runtime.pending.take(
		view.requestID,
		view.sourceSessionID,
		view.targetSessionID,
		runtime.owner.sessionID,
	)
	if !exists {
		return
	}
	if view.errorCode != errs.CodeOK {
		call.complete(nil, errs.New(view.errorCode))
		return
	}
	payload := message.Data[view.payloadOffset:]
	response := runtime.owner.pool.Acquire(len(payload))
	copy(response.Bytes(), payload)
	call.complete(response, nil)
}

// handleEvent 保留给同包测试和诊断注入；正式连接回调始终携带创建时的 generation。
func (runtime *natsRuntime) handleEvent(event natsnet.Event) {
	runtime.mu.Lock()
	generation := runtime.generation
	runtime.mu.Unlock()
	runtime.handleGenerationEvent(generation, event)
}

// handleGenerationEvent 把当前代连接事件转换为整体 Transport 状态。
func (runtime *natsRuntime) handleGenerationEvent(
	generation uint64,
	event natsnet.Event,
) {
	runtime.mu.Lock()
	current := runtime.generation == generation &&
		!runtime.stopping &&
		!runtime.closed
	started := runtime.started
	if !current {
		runtime.mu.Unlock()
		return
	}
	reconnects := runtime.reconnects
	failures := runtime.consecutiveFailures
	runtime.mu.Unlock()

	switch event.Type {
	case natsnet.EventDisconnected:
		// 不缓存、不重放已经在途的调用；新调用由 connectedConn 快速失败。
		runtime.mu.Lock()
		conn := runtime.conn
		runtime.mu.Unlock()
		if runtime.clearActiveConnection(generation, conn) {
			runtime.owner.NotifyRoutesChanged()
		}
		runtime.pending.failCurrent(errs.ErrTransportUnavailable)
		if runtime.owner.system != nil {
			runtime.owner.system.notifyNATSDisconnected(errs.ErrTransportUnavailable)
		}
		runtime.mu.Lock()
		runtime.consecutiveFailures++
		failures = runtime.consecutiveFailures
		reconnects = runtime.reconnects
		runtime.mu.Unlock()
		runtime.owner.reportTransportEvent(TransportEvent{
			Kind:                TransportKindNATS,
			State:               TransportStateRecovering,
			Reconnects:          reconnects,
			ConsecutiveFailures: failures,
			ErrorCode:           errs.CodeTransportUnavailable,
			Cause:               event.Err,
		})
	case natsnet.EventReconnected:
		runtime.mu.Lock()
		conn := runtime.conn
		runtime.reconnects++
		runtime.consecutiveFailures = 0
		reconnects = runtime.reconnects
		runtime.mu.Unlock()
		if conn != nil && conn.Status() == natsnet.StatusConnected {
			runtime.activeConnection.Store(&natsConnectionView{
				conn:       conn,
				generation: generation,
			})
			runtime.owner.NotifyRoutesChanged()
		}
		runtime.owner.reportTransportEvent(TransportEvent{
			Kind:       TransportKindNATS,
			State:      TransportStateReady,
			Reconnects: reconnects,
		})
	case natsnet.EventClosed:
		runtime.mu.Lock()
		conn := runtime.conn
		runtime.mu.Unlock()
		if runtime.clearActiveConnection(generation, conn) {
			runtime.owner.NotifyRoutesChanged()
		}
		runtime.pending.failCurrent(errs.ErrTransportUnavailable)
		if runtime.owner.system != nil {
			runtime.owner.system.notifyNATSDisconnected(errs.ErrTransportUnavailable)
		}
		if !started {
			return
		}
		runtime.activeGeneration.CompareAndSwap(generation, 0)
		runtime.mu.Lock()
		runtime.consecutiveFailures++
		failures = runtime.consecutiveFailures
		reconnects = runtime.reconnects
		wake := runtime.recoveryWake
		runtime.mu.Unlock()
		cause := event.Err
		if cause == nil {
			cause = errs.ErrTransportUnavailable
		}
		runtime.owner.logger.Error(
			"NATS RPC Connection 已进入终态，开始外层重建",
			originlog.Uint64("transport_generation", generation),
			originlog.Err(cause),
		)
		runtime.owner.reportTransportEvent(TransportEvent{
			Kind:                TransportKindNATS,
			State:               TransportStateRecovering,
			Reconnects:          reconnects,
			ConsecutiveFailures: failures,
			ErrorCode:           errs.CodeTransportUnavailable,
			Cause:               cause,
		})
		if wake != nil {
			select {
			case wake <- struct{}{}:
			default:
			}
		}
	default:
		// Connected、LameDuck 和单次异步错误不改变整体 RPC 就绪状态。
		return
	}
}

// requestSubject 返回缓存的目标 Request Subject。
func (runtime *natsRuntime) requestSubject(nodeID string) (string, error) {
	return runtime.cachedSubject(nodeID, true)
}

// responseSubject 返回缓存的目标 Response Subject。
func (runtime *natsRuntime) responseSubject(nodeID string) (string, error) {
	return runtime.cachedSubject(nodeID, false)
}

// cachedSubject 只在首次遇到 NodeID 时拼接 Subject，后续热路径读取冻结字符串。
func (runtime *natsRuntime) cachedSubject(
	nodeID string,
	request bool,
) (string, error) {
	if !validSubjectToken(nodeID) {
		return "", errs.ErrInvalidArgument
	}
	runtime.subjectMu.RLock()
	cache := runtime.responseSubjects
	if request {
		cache = runtime.requestSubjects
	}
	subject := cache[nodeID]
	runtime.subjectMu.RUnlock()
	if subject != "" {
		return subject, nil
	}

	runtime.subjectMu.Lock()
	cache = runtime.responseSubjects
	if request {
		cache = runtime.requestSubjects
	}
	subject = cache[nodeID]
	if subject == "" {
		if request {
			subject = natsRequestSubject(
				runtime.config.NATS.Namespace,
				nodeID,
			)
		} else {
			subject = natsResponseSubject(
				runtime.config.NATS.Namespace,
				nodeID,
			)
		}
		cache[nodeID] = subject
	}
	runtime.subjectMu.Unlock()
	return subject, nil
}

// natsRequestSubject 创建稳定简短的 Node 请求 Subject。
func natsRequestSubject(namespace, nodeID string) string {
	return "orpc." + namespace + ".req." + nodeID
}

// natsResponseSubject 创建稳定简短的 Node 响应 Subject。
func natsResponseSubject(namespace, nodeID string) string {
	return "orpc." + namespace + ".resp." + nodeID
}
