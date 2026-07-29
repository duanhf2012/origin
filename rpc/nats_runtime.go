package rpc

import (
	"context"
	"fmt"
	"sync"
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
	deadlines   *inboundDeadlines

	pending *natsPendingTable

	// Subject 在第一次见到 NodeID 时建立，后续 RPC 只读缓存字符串。
	subjectMu        sync.RWMutex
	requestSubjects  map[string]string
	responseSubjects map[string]string
	localRequest     string
	localResponse    string
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

// start 建立 Connection、校验 Server 上限并依次发布 Response/Request Subscription。
func (runtime *natsRuntime) start(engine *timerwheel.Engine) error {
	if runtime == nil || engine == nil {
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
	runtime.mu.Unlock()

	deadlines, err := newInboundDeadlines(engine)
	if err != nil {
		return err
	}
	options := runtime.connectionOptions()
	conn, err := natsnet.Connect(
		context.Background(),
		options,
		runtime.handleEvent,
	)
	if err != nil {
		deadlines.close(errs.ErrServiceStopped)
		return err
	}

	// Broker 必须能够承载业务上限加最坏 Origin 包络；否则启动后大包必然随机失败。
	requiredPayload := int64(
		runtime.config.MaxPayloadSize + natsMaximumEnvelopeSize,
	)
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
	runtime.conn = conn
	runtime.deadlines = deadlines
	runtime.mu.Unlock()

	subscriptionOptions := natsnet.SubscriptionOptions{
		PendingMessages: runtime.config.NATS.ReceiveQueueMessages,
	}
	// 先建立 Response Subscription，确保第一个 Request 发出前调用方已经具备响应路径。
	responseSub, err := conn.Subscribe(
		context.Background(),
		runtime.localResponse,
		subscriptionOptions,
		runtime.handleResponse,
	)
	if err != nil {
		runtime.close()
		return err
	}
	runtime.mu.Lock()
	runtime.responseSub = responseSub
	runtime.mu.Unlock()

	requestSub, err := conn.Subscribe(
		context.Background(),
		runtime.localRequest,
		subscriptionOptions,
		runtime.handleInbound,
	)
	if err != nil {
		runtime.close()
		return err
	}
	runtime.mu.Lock()
	runtime.requestSub = requestSub
	// EventClosed 与启动完成使用同一把锁线性化：若终态先发生，启动明确失败；若本处
	// 先发布 started，随后 EventClosed 会走 Node 级受控停机，不能形成“成功但已关闭”的 Node。
	if conn.Status() != natsnet.StatusConnected {
		runtime.mu.Unlock()
		runtime.close()
		return errs.ErrTransportUnavailable
	}
	runtime.started = true
	runtime.mu.Unlock()
	return nil
}

// connectionOptions 把稳定 RPC 配置映射到 M6 原生 Options。
func (runtime *natsRuntime) connectionOptions() natsnet.Options {
	config := runtime.config.NATS
	options := natsnet.DefaultOptions(
		"origin-rpc-"+config.Namespace+"-"+runtime.owner.nodeID,
		config.URLs...,
	)
	options.NoEcho = true
	options.MaxMessageSize =
		runtime.config.MaxPayloadSize + natsMaximumEnvelopeSize
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
	requestSub := runtime.requestSub
	runtime.mu.Unlock()
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
func (runtime *natsRuntime) close() {
	if runtime == nil {
		return
	}
	runtime.mu.Lock()
	if runtime.closed {
		runtime.mu.Unlock()
		return
	}
	runtime.closed = true
	runtime.stopping = true
	requestSub := runtime.requestSub
	responseSub := runtime.responseSub
	conn := runtime.conn
	deadlines := runtime.deadlines
	runtime.requestSub = nil
	runtime.responseSub = nil
	runtime.conn = nil
	runtime.deadlines = nil
	runtime.mu.Unlock()

	if requestSub != nil {
		requestSub.Close()
	}
	if responseSub != nil {
		responseSub.Close()
	}
	runtime.pending.failAll(errs.ErrTransportUnavailable)
	if conn != nil {
		conn.Close()
		_ = conn.Wait(context.Background())
	}
	if deadlines != nil {
		deadlines.close(errs.ErrServiceStopped)
	}
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
	runtime.mu.Lock()
	conn := runtime.conn
	closed := runtime.closed
	runtime.mu.Unlock()
	if closed || conn == nil || conn.Status() != natsnet.StatusConnected {
		return nil, errs.ErrTransportUnavailable
	}
	return conn, nil
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
		return
	}
	sourceNodeID := string(view.sourceNodeID)
	if !validSubjectToken(sourceNodeID) {
		return
	}
	if len(data)-view.payloadOffset > runtime.config.MaxPayloadSize {
		runtime.sendError(
			sourceNodeID,
			view.sourceSessionID,
			view.requestID,
			errs.ErrTransportMessageTooLarge,
		)
		return
	}
	if view.targetSessionID != runtime.owner.sessionID {
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
		cancel(err)
		runtime.sendError(
			sourceNodeID,
			view.sourceSessionID,
			view.requestID,
			err,
		)
		return
	}

	err = endpoint.target.DispatchAsync(func(targetCtx context.Context) {
		defer cancel(nil)
		defer deadlines.unbind(deadlineID)
		if cause := context.Cause(deadlineContext); cause != nil {
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
			runtime.sendError(
				sourceNodeID,
				view.sourceSessionID,
				view.requestID,
				cause,
			)
			return
		}
		runtime.sendResponse(
			sourceNodeID,
			view.sourceSessionID,
			view.requestID,
			response,
			dispatchErr,
		)
	})
	if err != nil {
		deadlines.unbind(deadlineID)
		cancel(err)
		runtime.sendError(
			sourceNodeID,
			view.sourceSessionID,
			view.requestID,
			err,
		)
	}
}

// handleNotify 校验目标并把只读 Message.Data 转移给 Service 任务；准入失败只在目标侧丢弃。
func (runtime *natsRuntime) handleNotify(data []byte) {
	view, err := parseNATSNotify(data)
	if err != nil ||
		view.targetSessionID != runtime.owner.sessionID ||
		len(data)-view.payloadOffset > runtime.config.MaxPayloadSize {
		return
	}
	endpoint, err := runtime.resolveInbound(
		string(view.serviceName),
		view.methodID,
	)
	if err != nil {
		return
	}
	_ = endpoint.target.DispatchAsync(func(targetCtx context.Context) {
		runtime.owner.dispatchNotify(
			targetCtx,
			endpoint,
			view.methodID,
			data[view.payloadOffset:],
		)
	})
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

// handleEvent 只处理连接终态；短暂断线保留 pending，重连后也不自动重放。
func (runtime *natsRuntime) handleEvent(event natsnet.Event) {
	if event.Type != natsnet.EventClosed {
		return
	}
	runtime.pending.failAll(errs.ErrTransportUnavailable)
	runtime.mu.Lock()
	unexpected := runtime.started && !runtime.stopping && !runtime.closed
	runtime.mu.Unlock()
	if !unexpected {
		return
	}
	runtime.owner.logger.Error(
		"NATS RPC Connection 已进入终态",
		originlog.Err(event.Err),
	)
	cause := event.Err
	if cause == nil {
		cause = errs.ErrTransportUnavailable
	}
	runtime.owner.reportTransportFailure(cause)
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
