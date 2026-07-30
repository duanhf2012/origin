package rpc

import (
	"context"
	"errors"
	"sync"

	"github.com/duanhf2012/origin/v3/errs"
	"github.com/duanhf2012/origin/v3/service"
)

// Client 是生成强类型客户端复用的轻量值语义底座。
//
// 构造时只解析一次 owner 所属 Runtime；每次调用不再查询 Service Runtime 或做 any
// 断言。零值和构造失败值都可安全调用，并返回固定参数或未就绪错误。
type Client struct {
	owner       service.IService
	runtime     *Runtime
	target      Target
	contractID  ContractID
	fingerprint ContractFingerprint
	route       routeSpec
	prepared    preparedTarget
}

// NewGeneratedClient 创建供 origingen 生成代码保存的底层客户端。
func NewGeneratedClient(
	owner service.IService,
	target Target,
	contractID ContractID,
	fingerprint ContractFingerprint,
) Client {
	client := Client{
		owner:       owner,
		target:      target,
		contractID:  contractID,
		fingerprint: fingerprint,
	}
	bound := service.RuntimeOf(owner)
	if bound == nil {
		return client
	}
	// service.Runtime 的公共最小接口不因 RPC 扩张；只有 Node 内部实现该窄桥接。
	bridge, ok := bound.(interface{ RPC() any })
	if !ok {
		return client
	}
	client.runtime, _ = bridge.RPC().(*Runtime)
	return client
}

// AllocateRequest 为生成请求编码器取得准确 payload 和调用类型所需 headroom 的最终 Buffer。
func (client Client) AllocateRequest(size int, kind CallKind) (*Buffer, error) {
	if err := client.validate(); err != nil {
		return nil, err
	}
	return client.runtime.AllocateRequest(client.target, size, kind)
}

// PrepareNotify 在编码前选择并固定一次无响应调用目标。
func (client Client) PrepareNotify(
	ctx context.Context,
	methodID MethodID,
) (Client, error) {
	if ctx == nil || methodID == 0 {
		return Client{}, errs.ErrInvalidArgument
	}
	if err := client.validate(); err != nil {
		return Client{}, err
	}
	prepared, err := client.runtime.prepareNotify(ctx, client, methodID)
	if err != nil {
		return Client{}, err
	}
	client.prepared = prepared
	return client, nil
}

// Await 执行一次有响应本地调用，并在 owner 的原任务调用栈恢复后解码结果。
//
// request 在进入本函数后由本函数消费；无论提交、超时、解码或 panic 均不再归调用方释放。
func (client Client) Await(
	ctx context.Context,
	methodID MethodID,
	request *Buffer,
	decode func([]byte) error,
) error {
	if ctx == nil || request == nil || decode == nil {
		releaseBuffer(request)
		return errs.ErrInvalidArgument
	}
	if err := client.validate(); err != nil {
		request.Release()
		return err
	}

	call := newAwaitCall()
	started := false
	err := client.owner.Await(ctx, func(waitCtx context.Context) error {
		started = true
		handle, err := client.runtime.submit(
			waitCtx,
			client.owner,
			client.target,
			client.contractID,
			client.fingerprint,
			methodID,
			CallRequest,
			request,
			call.complete,
		)
		if err != nil {
			request.Release()
			return err
		}

		response, err := call.wait(waitCtx)
		handle.cancel(err)
		if response != nil {
			defer response.Release()
		}
		if err != nil {
			return err
		}
		return decode(response.Bytes())
	})
	if !started {
		request.Release()
	}
	return err
}

// Async 预留调用方回调任务后提交有响应调用。
//
// 返回非 nil 时 callback 绝不执行；返回 nil 后 callback 必须在 owner 的串行执行上下文中
// 严格执行一次。decode 和 callback 都在取得 Service 执行权后运行。
func (client Client) Async(
	ctx context.Context,
	methodID MethodID,
	request *Buffer,
	decodeAndCallback func(context.Context, []byte, error),
) error {
	if ctx == nil || request == nil || decodeAndCallback == nil {
		releaseBuffer(request)
		return errs.ErrInvalidArgument
	}
	if err := client.validate(); err != nil {
		request.Release()
		return err
	}
	if cause := context.Cause(ctx); cause != nil {
		request.Release()
		return contextError(cause)
	}

	call := newAsyncCall()
	var handleMu sync.Mutex
	var handle remoteRequestHandle
	// completionStarted 只由同一个调用方完成任务按“wait 后 callback”的顺序访问。
	// 它用于识别 Context 已取消或 Service 已停止导致 Await 根本没有进入等待函数的路径。
	completionStarted := false
	if err := service.DispatchAsyncCompletion(
		client.owner,
		ctx,
		func(waitCtx context.Context) error {
			completionStarted = true
			// 提交方先发布 committed 再允许当前任务读取结果。通常当前调用仍占有唯一
			// Service 执行权，本等待不会抢跑；该门闩也覆盖框架外误用的并发调用。
			select {
			case <-call.committed:
				// 目标任务已经接受请求，可以开始等待真实响应。
			case <-call.aborted:
				return errAsyncAborted
			}
			// 提交结论必须先于 Context 取消生效。否则目标提交失败与取消同时发生时，
			// select 可能随机选择取消并错误调用业务 callback，破坏“返回 error 就绝不
			// 回调”的 API 契约。Runtime 的提交是有界非阻塞准入，不会长期占住该门闩。
			response, err := call.wait(waitCtx)
			handleMu.Lock()
			currentHandle := handle
			handleMu.Unlock()
			currentHandle.cancel(err)
			call.setCallbackResult(response, err)
			return err
		},
		func(callbackCtx context.Context, waitErr error) {
			// Await 可能因调用方已经取消或 Service 正在停止而不调用 wait。此时必须先
			// 放弃 localCall，确保已经到达或之后到达的响应都由 localCall 归还。
			if !completionStarted {
				call.abandon()
				handleMu.Lock()
				currentHandle := handle
				handleMu.Unlock()
				currentHandle.cancel(waitErr)
			}
			response, resultErr := call.callbackResult()
			if response != nil {
				defer response.Release()
			}
			if waitErr != nil {
				resultErr = waitErr
			}
			if resultErr == errAsyncAborted {
				return
			}
			var payload []byte
			if response != nil {
				payload = response.Bytes()
			}
			decodeAndCallback(callbackCtx, payload, resultErr)
		},
	); err != nil {
		request.Release()
		return err
	}

	submittedHandle, err := client.runtime.submit(
		ctx,
		client.owner,
		client.target,
		client.contractID,
		client.fingerprint,
		methodID,
		CallRequest,
		request,
		call.complete,
	)
	if err != nil {
		request.Release()
		call.abort()
		return err
	}
	handleMu.Lock()
	handle = submittedHandle
	handleMu.Unlock()
	call.commit()
	return nil
}

// Notify 只等待目标队列接受请求，不创建响应、Pending 或超时状态。
func (client Client) Notify(
	ctx context.Context,
	methodID MethodID,
	request *Buffer,
) error {
	if ctx == nil || request == nil {
		releaseBuffer(request)
		return errs.ErrInvalidArgument
	}
	if err := client.validate(); err != nil {
		request.Release()
		return err
	}
	if cause := context.Cause(ctx); cause != nil {
		request.Release()
		return contextError(cause)
	}
	if _, err := client.runtime.submit(
		ctx,
		client.owner,
		client.target,
		client.contractID,
		client.fingerprint,
		methodID,
		CallNotify,
		request,
		nil,
	); err != nil {
		request.Release()
		return err
	}
	return nil
}

// Broadcast 在 M11 当前本地目标范围执行通知投递。
//
// 同一 Node 内 ServiceName 唯一，因此本阶段与 Notify 共享一次编码和投递；后续服务发现
// 只扩展 Runtime 候选集合，不改变生成方法签名。
func (client Client) Broadcast(
	ctx context.Context,
	methodID MethodID,
	request *Buffer,
) error {
	return client.Notify(ctx, methodID, request)
}

// validate 检查构造冷路径是否建立了完整、可调用的客户端。
func (client Client) validate() error {
	if client.owner == nil || client.runtime == nil ||
		!client.target.valid() || client.contractID == 0 ||
		client.fingerprint == (ContractFingerprint{}) {
		return errs.ErrInvalidArgument
	}
	return nil
}

// localCall 是一次请求—响应本地调用的最小完成状态。
//
// M11 先保留未池化基线：每次调用只分配一个状态和一个完成 Channel，避免复杂的复用代次
// 与晚到响应 ABA。Benchmark 证明池化有稳定收益后才允许增加对象池。
type localCall struct {
	done      chan struct{}
	aborted   chan struct{}
	committed chan struct{}

	mu             sync.Mutex
	response       *Buffer
	err            error
	completed      bool
	abandoned      bool
	callbackBuffer *Buffer
	callbackErr    error
}

// errAsyncAborted 只在框架内部通知预留任务抑制 callback，不暴露为 Async 返回值。
var errAsyncAborted = errs.NewMessage(errs.CodeCanceled, "rpc async submit aborted")

// newAwaitCall 创建只需要完成信号的一次性 Await 状态。
func newAwaitCall() *localCall {
	return &localCall{
		done: make(chan struct{}),
	}
}

// newAsyncCall 额外创建提交和中止门闩，锁定“返回错误绝不回调”的提交边界。
func newAsyncCall() *localCall {
	return &localCall{
		done:      make(chan struct{}),
		aborted:   make(chan struct{}),
		committed: make(chan struct{}),
	}
}

// commit 发布目标已经接受请求；该门闩每次调用只能关闭一次。
func (call *localCall) commit() {
	close(call.committed)
}

// complete 提交唯一终态；调用方已经放弃时立即释放晚到响应。
func (call *localCall) complete(response *Buffer, err error) {
	call.mu.Lock()
	if call.completed {
		call.mu.Unlock()
		releaseBuffer(response)
		panic("rpc: localCall 重复完成")
	}
	call.completed = true
	if call.abandoned {
		call.mu.Unlock()
		releaseBuffer(response)
		close(call.done)
		return
	}
	call.response = response
	call.err = err
	call.mu.Unlock()
	close(call.done)
}

// wait 等待完成或 Context 取消，并在线性化点决定响应所有权。
func (call *localCall) wait(ctx context.Context) (*Buffer, error) {
	select {
	case <-call.done:
		return call.take()
	case <-ctx.Done():
		call.mu.Lock()
		if call.completed {
			response, err := call.takeLocked()
			call.mu.Unlock()
			return response, err
		}
		call.abandoned = true
		call.mu.Unlock()
		return nil, contextError(context.Cause(ctx))
	}
}

// take 在完成信号关闭后安全取得唯一响应结果。
func (call *localCall) take() (*Buffer, error) {
	call.mu.Lock()
	defer call.mu.Unlock()
	return call.takeLocked()
}

// takeLocked 在持锁条件下转移响应所有权，并清除 localCall 对结果的引用。
func (call *localCall) takeLocked() (*Buffer, error) {
	response := call.response
	err := call.err
	call.response = nil
	call.err = nil
	return response, err
}

// abort 发布目标提交失败，并让已预留的完成任务静默退出。
func (call *localCall) abort() {
	call.mu.Lock()
	if !call.abandoned && call.aborted != nil {
		call.abandoned = true
		close(call.aborted)
	}
	call.mu.Unlock()
}

// abandon 在线性化点放弃读取响应，并归还已经先于放弃到达的 Buffer。
//
// 响应尚未到达时，complete 会观察 abandoned 并负责归还；响应已经到达时，本函数取得
// 并清空唯一所有权。两条路径都不会关闭提交门闩，也不会触发业务 callback。
func (call *localCall) abandon() {
	call.mu.Lock()
	call.abandoned = true
	response := call.response
	call.response = nil
	call.err = nil
	call.mu.Unlock()
	releaseBuffer(response)
}

// setCallbackResult 暂存已经由完成任务取得、等待恢复执行权后消费的响应。
func (call *localCall) setCallbackResult(response *Buffer, err error) {
	call.mu.Lock()
	call.callbackBuffer = response
	call.callbackErr = err
	call.mu.Unlock()
}

// callbackResult 把暂存结果唯一转移给已恢复 Service 执行权的回调阶段。
func (call *localCall) callbackResult() (*Buffer, error) {
	call.mu.Lock()
	defer call.mu.Unlock()
	response := call.callbackBuffer
	err := call.callbackErr
	call.callbackBuffer = nil
	call.callbackErr = nil
	return response, err
}

// contextError 把标准 Context 终态映射为 Origin 稳定错误码。
func contextError(cause error) error {
	if errors.Is(cause, context.DeadlineExceeded) {
		return errs.ErrDeadlineExceeded
	}
	return errs.ErrCanceled
}

// releaseBuffer 允许所有参数校验错误路径安全释放可能为空的请求或响应。
func releaseBuffer(buffer *Buffer) {
	if buffer != nil {
		buffer.Release()
	}
}
