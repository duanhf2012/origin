package rpc

import (
	"context"
	"errors"
	"math"
	"sync"
	"time"

	"github.com/duanhf2012/origin/v3/errs"
	"github.com/duanhf2012/origin/v3/internal/bufferpool"
	"github.com/duanhf2012/origin/v3/internal/tcpnet"
)

// remoteRequestHandle 允许 Await/Async 在调用方超时或取消时立即移除 pending。
//
// 零值用于本地调用和 Notify；cancel 保持幂等，晚到远端 Response 会被会话安全丢弃。
type remoteRequestHandle struct {
	session   *outboundSession
	nats      *natsRuntime
	requestID uint64
}

// cancel 从仍存活的会话中删除当前调用，并提交调用方已经确定的终态。
func (handle remoteRequestHandle) cancel(cause error) {
	if handle.requestID == 0 {
		return
	}
	if handle.session != nil {
		handle.session.cancelPending(handle.requestID, cause)
		return
	}
	if handle.nats != nil {
		handle.nats.pending.cancel(handle.requestID, cause)
	}
}

// pendingCall 是一条出站连接上尚未返回的最小请求状态。
type pendingCall struct {
	complete func(*Buffer, error)
}

// outboundSession 同时实现一条主动连接的握手处理和响应关联。
//
// pending 没有跨重连迁移：连接断开即失败当前会话全部调用，新连接只接收之后的新请求。
type outboundSession struct {
	remote          *remoteRuntime
	targetNodeID    string
	targetSessionID uint64

	mu        sync.Mutex
	conn      *tcpnet.Conn
	ready     bool
	closed    bool
	catalog   map[string]ContractFingerprint
	pending   map[uint64]pendingCall
	handshake chan error
	once      sync.Once
}

// newOutboundSession 创建单次 Dial 使用的一次性会话。
func newOutboundSession(
	remote *remoteRuntime,
	targetNodeID string,
	targetSessionID uint64,
) *outboundSession {
	return &outboundSession{
		remote:          remote,
		targetNodeID:    targetNodeID,
		targetSessionID: targetSessionID,
		pending:         make(map[uint64]pendingCall),
		handshake:       make(chan error, 1),
	}
}

// OnOpen 在读取任何远端帧前发送 ORP1 Hello。
func (session *outboundSession) OnOpen(conn *tcpnet.Conn) {
	session.mu.Lock()
	session.conn = conn
	session.mu.Unlock()

	hello, err := encodeHello(
		session.remote.owner.pool,
		session.remote.owner.nodeID,
		session.targetNodeID,
		session.targetSessionID,
	)
	if err != nil {
		session.finishHandshake(err)
		conn.Close()
		return
	}
	if err := conn.Send(hello); err != nil {
		hello.Release()
		session.finishHandshake(err)
		conn.Close()
	}
}

// OnMessage 在握手前只接受 Ack，握手后只接受 Response 或 Pong。
func (session *outboundSession) OnMessage(
	conn *tcpnet.Conn,
	packet *bufferpool.Buffer,
) error {
	data := packet.Bytes()

	session.mu.Lock()
	ready := session.ready
	session.mu.Unlock()
	if !ready {
		ack, err := parseHelloAck(data)
		packet.Release()
		if err != nil {
			session.finishHandshake(err)
			return err
		}
		if ack.statusCode != errs.CodeOK {
			err = errs.New(ack.statusCode)
			session.finishHandshake(err)
			return err
		}

		// 目录在冷路径转换为只读 Map；热路径一次查询即可验证 Service 契约。
		catalog := make(map[string]ContractFingerprint, len(ack.services))
		for _, service := range ack.services {
			catalog[service.name] = service.fingerprint
		}
		session.mu.Lock()
		if session.closed {
			session.mu.Unlock()
			err = errs.ErrTransportClosed
			session.finishHandshake(err)
			return err
		}
		session.catalog = catalog
		session.ready = true
		session.mu.Unlock()
		session.finishHandshake(nil)
		return nil
	}

	if len(data) == wireHeartbeatSize && data[0] == wireKindPong {
		packet.Release()
		return nil
	}
	response, err := parseResponse(data)
	if err != nil {
		packet.Release()
		return err
	}
	if len(data)-response.payloadOffset > session.remote.config.MaxPayloadSize {
		packet.Release()
		return errs.ErrTransportMessageTooLarge
	}
	if !packet.DiscardPrefix(response.payloadOffset) {
		packet.Release()
		return errs.ErrTransportProtocol
	}
	session.completePending(response.requestID, packet, response.errorCode)
	return nil
}

// OnClose 发布握手失败并使当前会话所有 pending 以传输不可用结束。
func (session *outboundSession) OnClose(_ *tcpnet.Conn, cause error) {
	if cause == nil {
		cause = errs.ErrTransportUnavailable
	}
	session.finishHandshake(cause)
	session.failAllPending(errs.ErrTransportUnavailable)
}

// finishHandshake 只发布一次握手终态。
func (session *outboundSession) finishHandshake(err error) {
	session.once.Do(func() {
		session.handshake <- err
		close(session.handshake)
	})
}

// waitHandshake 等待 ORP1 Ack，并使用独立握手上限避免无效对端长期占用连接。
func (session *outboundSession) waitHandshake(ctx context.Context) error {
	timer := time.NewTimer(DefaultHandshakeTimeout)
	defer func() {
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
	}()
	select {
	case err := <-session.handshake:
		return err
	case <-timer.C:
		return errs.ErrDeadlineExceeded
	case <-ctx.Done():
		return contextError(context.Cause(ctx))
	}
}

// sendRequest 校验握手目录、登记 pending，并把原 Buffer 所有权交给 TCP。
func (session *outboundSession) sendRequest(
	serviceName string,
	fingerprint ContractFingerprint,
	methodID MethodID,
	remaining time.Duration,
	request *Buffer,
	complete func(*Buffer, error),
) (remoteRequestHandle, error) {
	if remaining <= 0 {
		return remoteRequestHandle{}, errs.ErrDeadlineExceeded
	}
	requestID, err := session.remote.owner.nextRequestID()
	if err != nil {
		return remoteRequestHandle{}, err
	}

	session.mu.Lock()
	if session.closed || !session.ready || session.conn == nil {
		session.mu.Unlock()
		return remoteRequestHandle{}, errs.ErrRPCNoRoute
	}
	remoteFingerprint, exists := session.catalog[serviceName]
	if !exists {
		session.mu.Unlock()
		return remoteRequestHandle{}, errs.ErrRPCNoRoute
	}
	if remoteFingerprint != fingerprint {
		session.mu.Unlock()
		return remoteRequestHandle{}, errs.ErrRPCContractMismatch
	}
	if len(session.pending) >= DefaultPendingPerSession {
		session.mu.Unlock()
		return remoteRequestHandle{}, errs.ErrTransportOverloaded
	}
	conn := session.conn
	session.pending[requestID] = pendingCall{complete: complete}
	session.mu.Unlock()

	// 生成器已经按服务名保留准确 headroom；这里只写入字段，不复制业务 payload。
	if err := prependRequest(
		request,
		requestID,
		methodID,
		remaining,
		serviceName,
	); err != nil {
		session.removePending(requestID)
		return remoteRequestHandle{}, err
	}
	if err := conn.Send(request); err != nil {
		session.removePending(requestID)
		return remoteRequestHandle{}, normalizeRemoteClose(err)
	}
	return remoteRequestHandle{
		session:   session,
		requestID: requestID,
	}, nil
}

// sendNotify 校验契约后发送不建立 pending 的短帧。
func (session *outboundSession) sendNotify(
	serviceName string,
	fingerprint ContractFingerprint,
	methodID MethodID,
	request *Buffer,
) error {
	session.mu.Lock()
	if session.closed || !session.ready || session.conn == nil {
		session.mu.Unlock()
		return errs.ErrRPCNoRoute
	}
	remoteFingerprint, exists := session.catalog[serviceName]
	if !exists {
		session.mu.Unlock()
		return errs.ErrRPCNoRoute
	}
	if remoteFingerprint != fingerprint {
		session.mu.Unlock()
		return errs.ErrRPCContractMismatch
	}
	conn := session.conn
	session.mu.Unlock()

	if err := prependNotify(request, methodID, serviceName); err != nil {
		return err
	}
	if err := conn.Send(request); err != nil {
		return normalizeRemoteClose(err)
	}
	return nil
}

// sendPing 发送一个不建立业务状态的存活探测。
func (session *outboundSession) sendPing() error {
	ping, err := encodeHeartbeat(session.remote.owner.pool, wireKindPing)
	if err != nil {
		return err
	}
	session.mu.Lock()
	conn := session.conn
	ready := session.ready && !session.closed
	session.mu.Unlock()
	if !ready || conn == nil {
		ping.Release()
		return errs.ErrTransportClosed
	}
	if err := conn.Send(ping); err != nil {
		ping.Release()
		return err
	}
	return nil
}

// close 主动关闭本会话；OnClose 负责统一完成 pending。
func (session *outboundSession) close() {
	session.mu.Lock()
	conn := session.conn
	session.mu.Unlock()
	if conn != nil {
		conn.Close()
	}
}

// completePending 把远端终态唯一移交给对应调用。
func (session *outboundSession) completePending(
	requestID uint64,
	response *Buffer,
	code errs.Code,
) {
	session.mu.Lock()
	pending, exists := session.pending[requestID]
	if exists {
		delete(session.pending, requestID)
	}
	session.mu.Unlock()
	if !exists {
		// 调用方已经超时或取消时，晚到响应不再污染新会话，也不视为协议错误。
		response.Release()
		return
	}
	if code != errs.CodeOK {
		response.Release()
		pending.complete(nil, errs.New(code))
		return
	}
	pending.complete(response, nil)
}

// cancelPending 在线性化点删除调用，并使用调用方终态唤醒仍在等待的 localCall。
func (session *outboundSession) cancelPending(requestID uint64, cause error) {
	session.mu.Lock()
	pending, exists := session.pending[requestID]
	if exists {
		delete(session.pending, requestID)
	}
	session.mu.Unlock()
	if exists && cause != nil {
		pending.complete(nil, cause)
	}
}

// removePending 仅用于发送尚未成功时回滚登记，不触发完成回调。
func (session *outboundSession) removePending(requestID uint64) {
	session.mu.Lock()
	delete(session.pending, requestID)
	session.mu.Unlock()
}

// failAllPending 关闭会话并在锁外完成所有调用，避免回调重入 session 锁。
func (session *outboundSession) failAllPending(cause error) {
	session.mu.Lock()
	if session.closed {
		session.mu.Unlock()
		return
	}
	session.closed = true
	session.ready = false
	pending := make([]pendingCall, 0, len(session.pending))
	for _, call := range session.pending {
		pending = append(pending, call)
	}
	clear(session.pending)
	session.mu.Unlock()
	for _, call := range pending {
		call.complete(nil, cause)
	}
}

// Runtime 使用单调递增且不回绕的 RequestID，避免晚到响应命中新调用。
func (runtime *Runtime) nextRequestID() (uint64, error) {
	for {
		current := runtime.requestID.Load()
		if current == math.MaxUint64 {
			return 0, errs.ErrTransportOverloaded
		}
		if runtime.requestID.CompareAndSwap(current, current+1) {
			return current + 1, nil
		}
	}
}

// remoteRemainingTimeout 计算线上唯一 Deadline 的剩余纳秒。
func remoteRemainingTimeout(
	ownerTimeout time.Duration,
	ctx context.Context,
) (time.Duration, error) {
	if ctx == nil || ownerTimeout <= 0 {
		return 0, errs.ErrInvalidArgument
	}
	if cause := context.Cause(ctx); cause != nil {
		return 0, contextError(cause)
	}
	if deadline, exists := ctx.Deadline(); exists {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return 0, errs.ErrDeadlineExceeded
		}
		return remaining, nil
	}
	return ownerTimeout, nil
}

// normalizeRemoteClose 把底层不同关闭原因稳定折叠为“本次传输不可用”。
func normalizeRemoteClose(err error) error {
	if err == nil || errors.Is(err, errs.ErrTransportClosed) {
		return errs.ErrTransportUnavailable
	}
	return errs.New(errs.CodeOf(err))
}
