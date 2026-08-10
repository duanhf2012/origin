package rpc

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/duanhf2012/origin/v3/errs"
	"github.com/duanhf2012/origin/v3/internal/bufferpool"
	"github.com/duanhf2012/origin/v3/internal/tcpnet"
)

// inboundHandler 处理一个 Node Listener 接受的全部 ORP1 连接。
//
// 同一 Conn 回调由 tcpnet 串行调用；connections 和 byNode 只协调不同连接之间的握手
// 唯一性。后连接绝不替换相同 NodeID 的现有会话。
type inboundHandler struct {
	remote *remoteRuntime

	mu          sync.Mutex
	connections map[*tcpnet.Conn]*inboundSession
	byNode      map[string]*inboundSession
}

// inboundSession 保存一条入站连接的握手身份和响应发送状态。
type inboundSession struct {
	handler *inboundHandler
	conn    *tcpnet.Conn

	sourceNodeID string
	ready        bool
	rejected     bool
	closed       atomic.Bool
}

// remoteDeadlineContext 为时间轮管理的线上超时补充标准 Context Deadline。
type remoteDeadlineContext struct {
	context.Context
	deadline time.Time
}

// Deadline 返回调用方传入并扣除网络耗时后的绝对截止时间。
func (ctx *remoteDeadlineContext) Deadline() (time.Time, bool) {
	if ctx == nil || ctx.deadline.IsZero() {
		return time.Time{}, false
	}
	return ctx.deadline, true
}

// Err 保持标准 Deadline Context 语义；时间轮到期虽然通过 CancelCause 唤醒，业务仍应看到
// context.DeadlineExceeded，而不是实现细节产生的 context.Canceled。
func (ctx *remoteDeadlineContext) Err() error {
	if ctx == nil {
		return nil
	}
	if context.Cause(ctx.Context) == errs.ErrDeadlineExceeded {
		return context.DeadlineExceeded
	}
	return ctx.Context.Err()
}

// newInboundHandler 创建空的连接身份表。
func newInboundHandler(remote *remoteRuntime) *inboundHandler {
	return &inboundHandler{
		remote:      remote,
		connections: make(map[*tcpnet.Conn]*inboundSession),
		byNode:      make(map[string]*inboundSession),
	}
}

// OnOpen 为连接登记尚未验证身份的会话。
func (handler *inboundHandler) OnOpen(conn *tcpnet.Conn) {
	session := &inboundSession{
		handler: handler,
		conn:    conn,
	}
	handler.mu.Lock()
	handler.connections[conn] = session
	handler.mu.Unlock()
}

// OnMessage 在第一帧完成握手，之后按 Kind 分派业务消息。
func (handler *inboundHandler) OnMessage(
	conn *tcpnet.Conn,
	packet *bufferpool.Buffer,
) error {
	handler.mu.Lock()
	session := handler.connections[conn]
	handler.mu.Unlock()
	if session == nil {
		packet.Release()
		return errs.ErrTransportProtocol
	}
	if !session.ready {
		return handler.handleHello(session, packet)
	}
	if session.rejected {
		packet.Release()
		return errs.ErrTransportProtocol
	}

	data := packet.Bytes()
	if len(data) == wireHeartbeatSize && data[0] == wireKindPing {
		packet.Release()
		return session.sendPong()
	}
	if len(data) == 0 {
		packet.Release()
		return errs.ErrTransportProtocol
	}
	switch data[0] {
	case wireKindRequest:
		return handler.handleRequest(session, packet)
	case wireKindNotify:
		return handler.handleNotify(packet)
	default:
		packet.Release()
		return errs.ErrTransportProtocol
	}
}

// OnClose 只删除仍对应当前连接的 NodeID 映射；已投递任务继续执行。
func (handler *inboundHandler) OnClose(
	conn *tcpnet.Conn,
	_ error,
) {
	handler.mu.Lock()
	session := handler.connections[conn]
	delete(handler.connections, conn)
	if session != nil {
		session.closed.Store(true)
		if current := handler.byNode[session.sourceNodeID]; current == session {
			delete(handler.byNode, session.sourceNodeID)
		}
	}
	handler.mu.Unlock()
}

// handleHello 验证连接目标并原子裁决重复 NodeID。
func (handler *inboundHandler) handleHello(
	session *inboundSession,
	packet *bufferpool.Buffer,
) error {
	hello, err := parseHello(packet.Bytes())
	packet.Release()
	if err != nil {
		return err
	}

	status := errs.CodeOK
	handler.mu.Lock()
	if hello.targetNodeID != handler.remote.owner.nodeID ||
		hello.targetSessionID != handler.remote.owner.sessionID ||
		hello.sourceNodeID == handler.remote.owner.nodeID {
		status = errs.CodeTransportProtocol
	} else if current := handler.byNode[hello.sourceNodeID]; current != nil {
		status = errs.CodeTransportProtocol
	} else {
		session.sourceNodeID = hello.sourceNodeID
		session.ready = true
		handler.byNode[hello.sourceNodeID] = session
	}
	if status != errs.CodeOK {
		// 标记为已处理握手但拒绝业务帧；Ack 进入发送队列后由主动方关闭连接。
		session.ready = true
		session.rejected = true
	}
	handler.mu.Unlock()

	var services []wireServiceEntry
	if status == errs.CodeOK {
		services = handler.remote.publicCatalog()
	}
	ack, err := encodeHelloAck(
		handler.remote.owner.pool,
		status,
		services,
	)
	if err != nil {
		return err
	}
	if err := session.conn.Send(ack); err != nil {
		ack.Release()
		return err
	}
	return nil
}

// handleRequest 校验公开端点和超时，然后把业务 payload 唯一移交给 Service FIFO。
func (handler *inboundHandler) handleRequest(
	session *inboundSession,
	packet *bufferpool.Buffer,
) error {
	view, err := parseRequest(packet.Bytes())
	if err != nil {
		handler.remote.owner.recordInboundRejected(preparedTCP)
		packet.Release()
		return err
	}
	if len(packet.Bytes())-view.payloadOffset >
		handler.remote.config.MaxPayloadSize {
		handler.remote.owner.recordInboundRejected(preparedTCP)
		packet.Release()
		return errs.ErrTransportMessageTooLarge
	}
	endpoint, err := handler.remote.resolveInbound(
		string(view.serviceName),
		view.methodID,
	)
	if err != nil {
		handler.remote.owner.recordInboundRejected(preparedTCP)
		packet.Release()
		session.sendError(view.requestID, err)
		return nil
	}

	// 解析完成后丢弃协议头，Dispatcher 只借用原 Buffer 中的业务 payload。
	if !packet.DiscardPrefix(view.payloadOffset) {
		handler.remote.owner.recordInboundRejected(preparedTCP)
		packet.Release()
		return errs.ErrTransportProtocol
	}
	delay := view.remainingTimeout
	if delay <= 0 {
		handler.remote.owner.recordInboundRejected(preparedTCP)
		packet.Release()
		session.sendError(view.requestID, errs.ErrDeadlineExceeded)
		return nil
	}

	cancelContext, cancel := context.WithCancelCause(context.Background())
	deadlineContext := &remoteDeadlineContext{
		Context:  cancelContext,
		deadline: time.Now().Add(delay),
	}
	handler.remote.mu.Lock()
	deadlines := handler.remote.deadlines
	handler.remote.mu.Unlock()
	if deadlines == nil {
		handler.remote.owner.recordInboundRejected(preparedTCP)
		cancel(errs.ErrServiceStopped)
		packet.Release()
		session.sendError(view.requestID, errs.ErrServiceStopped)
		return nil
	}
	deadlineID, err := deadlines.bind(delay, cancel)
	if err != nil {
		handler.remote.owner.recordInboundRejected(preparedTCP)
		cancel(err)
		packet.Release()
		session.sendError(view.requestID, err)
		return nil
	}

	payloadBytes := len(packet.Bytes())
	err = endpoint.target.DispatchAsync(func(targetCtx context.Context) {
		defer packet.Release()
		defer cancel(nil)
		defer deadlines.unbind(deadlineID)

		// 已在 FIFO 等待阶段超时的请求不再执行业务方法。
		if cause := context.Cause(deadlineContext); cause != nil {
			handler.remote.owner.recordInboundFinished(preparedTCP, cause, 0)
			session.sendError(view.requestID, cause)
			return
		}
		dispatchContext := &rpcContext{
			execution: targetCtx,
			control:   deadlineContext,
			values:    deadlineContext,
		}
		response, dispatchErr := handler.remote.owner.dispatchRequest(
			dispatchContext,
			endpoint,
			view.methodID,
			packet.Bytes(),
			wireResponseFixedSize,
		)
		if cause := context.Cause(deadlineContext); cause != nil {
			releaseBuffer(response)
			handler.remote.owner.recordInboundFinished(preparedTCP, cause, 0)
			session.sendError(view.requestID, cause)
			return
		}
		responseBytes := 0
		if response != nil {
			responseBytes = len(response.Bytes())
		}
		handler.remote.owner.recordInboundFinished(
			preparedTCP,
			dispatchErr,
			responseBytes,
		)
		session.sendResponse(view.requestID, response, dispatchErr)
	})
	if err != nil {
		handler.remote.owner.recordInboundRejected(preparedTCP)
		deadlines.unbind(deadlineID)
		cancel(err)
		packet.Release()
		session.sendError(view.requestID, err)
		return nil
	}
	handler.remote.owner.recordInboundAccepted(preparedTCP, payloadBytes)
	return nil
}

// handleNotify 把无响应消息投递给目标 Service；准入失败只在目标侧放弃。
func (handler *inboundHandler) handleNotify(
	packet *bufferpool.Buffer,
) error {
	view, err := parseNotify(packet.Bytes())
	if err != nil {
		handler.remote.owner.recordInboundRejected(preparedTCP)
		packet.Release()
		return err
	}
	if len(packet.Bytes())-view.payloadOffset >
		handler.remote.config.MaxPayloadSize {
		handler.remote.owner.recordInboundRejected(preparedTCP)
		packet.Release()
		return errs.ErrTransportMessageTooLarge
	}
	endpoint, err := handler.remote.resolveInbound(
		string(view.serviceName),
		view.methodID,
	)
	if err != nil {
		handler.remote.owner.recordInboundRejected(preparedTCP)
		packet.Release()
		return nil
	}
	if !packet.DiscardPrefix(view.payloadOffset) {
		handler.remote.owner.recordInboundRejected(preparedTCP)
		packet.Release()
		return errs.ErrTransportProtocol
	}
	payloadBytes := len(packet.Bytes())
	err = endpoint.target.DispatchAsync(func(targetCtx context.Context) {
		defer packet.Release()
		handler.remote.owner.dispatchNotify(
			targetCtx,
			endpoint,
			view.methodID,
			packet.Bytes(),
		)
		handler.remote.owner.recordInboundNotify(preparedTCP, payloadBytes)
	})
	if err != nil {
		handler.remote.owner.recordInboundRejected(preparedTCP)
		packet.Release()
	}
	return nil
}

// resolveInbound 只允许公开且具有 Dispatcher 的精确 Service。
func (remote *remoteRuntime) resolveInbound(
	serviceName string,
	methodID MethodID,
) (serviceEndpoint, error) {
	remote.mu.Lock()
	stopping := remote.stopping
	remote.mu.Unlock()
	if stopping {
		return serviceEndpoint{}, errs.ErrServiceStopping
	}
	return remote.owner.resolveInbound(serviceName, methodID)
}

// resolveInbound 实现 TCP/NATS 共用的 Service Ready、可见性和 Dispatcher 校验。
func (runtime *Runtime) resolveInbound(
	serviceName string,
	methodID MethodID,
) (serviceEndpoint, error) {
	if runtime == nil || serviceName == "" || methodID == 0 {
		return serviceEndpoint{}, errs.ErrInvalidArgument
	}
	if runtime.closed.Load() {
		return serviceEndpoint{}, errs.ErrServiceStopping
	}
	if !runtime.inboundReady.Load() {
		return serviceEndpoint{}, errs.ErrServiceNotReady
	}
	endpoint, exists := runtime.endpoints[serviceName]
	if !exists || !endpoint.public || endpoint.dispatcher == nil {
		return serviceEndpoint{}, errs.ErrRPCNoRoute
	}
	if methodID == 0 {
		return serviceEndpoint{}, errs.ErrRPCMethodNotFound
	}
	return endpoint, nil
}

// sendPong 回应对端存活探测。
func (session *inboundSession) sendPong() error {
	pong, err := encodeHeartbeat(
		session.handler.remote.owner.pool,
		wireKindPong,
	)
	if err != nil {
		return err
	}
	if err := session.conn.Send(pong); err != nil {
		pong.Release()
		return err
	}
	return nil
}

// sendError 发送不含业务 payload 的稳定错误码响应。
func (session *inboundSession) sendError(requestID uint64, cause error) {
	response := session.handler.remote.owner.pool.AcquireWithHeadroom(
		0,
		wireResponseFixedSize,
	)
	session.sendResponse(requestID, response, cause)
}

// sendResponse 给成功响应或空错误响应原地补头并转移给 TCP。
//
// 调用方连接已经断开时直接释放响应；已接受业务任务不会因此被取消或重新执行。
func (session *inboundSession) sendResponse(
	requestID uint64,
	response *Buffer,
	cause error,
) {
	if response == nil {
		response = session.handler.remote.owner.pool.AcquireWithHeadroom(
			0,
			wireResponseFixedSize,
		)
	}
	if session.closed.Load() {
		response.Release()
		return
	}
	code := errs.CodeOK
	if cause != nil {
		code = errs.CodeOf(cause)
		// 错误响应不能泄露部分业务 payload。
		if len(response.Bytes()) != 0 {
			response.Release()
			response = session.handler.remote.owner.pool.AcquireWithHeadroom(
				0,
				wireResponseFixedSize,
			)
		}
	}
	if err := prependResponse(response, requestID, code); err != nil {
		response.Release()
		session.conn.Close()
		return
	}
	if err := session.conn.Send(response); err != nil {
		response.Release()
		// 响应队列过载意味着连接无法维持确定关联，关闭后由调用端统一失败 pending。
		session.conn.Close()
	}
}
