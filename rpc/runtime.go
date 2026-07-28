package rpc

import (
	"context"
	"fmt"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	"github.com/duanhf2012/origin/v3/errs"
	"github.com/duanhf2012/origin/v3/internal/bufferpool"
	originlog "github.com/duanhf2012/origin/v3/log"
	"github.com/duanhf2012/origin/v3/service"
)

// serviceEndpoint 是 Runtime 在 Node 装配冷路径建立的不可变本地路由记录。
type serviceEndpoint struct {
	serviceName string
	target      service.IService
	dispatcher  Dispatcher
}

// Runtime 管理一个 Node 内的本地 RPC 路由、共享 BufferPool 和调用提交。
//
// RegisterService 只允许 Node 在 Runtime 发布前调用。Freeze 后目录只读，因此 RPC 热路径
// 查找普通 Map 不需要锁；每个 Node 使用独立 Runtime，不存在跨 Node 隐式共享状态。
type Runtime struct {
	nodeID string
	pool   *bufferpool.Pool
	logger originlog.Logger

	mu        sync.Mutex
	endpoints map[string]serviceEndpoint
	frozen    atomic.Bool
	closed    atomic.Bool
}

// NewRuntime 创建尚未发布的 Node RPC Runtime。
func NewRuntime(
	nodeID string,
	pool *bufferpool.Pool,
	logger originlog.Logger,
) (*Runtime, error) {
	if nodeID == "" || pool == nil {
		return nil, errs.ErrInvalidArgument
	}
	return &Runtime{
		nodeID:    nodeID,
		pool:      pool,
		logger:    logger,
		endpoints: make(map[string]serviceEndpoint),
	}, nil
}

// RegisterService 登记一个 Node 内已经完成 Runtime 绑定的 Service。
//
// dispatcher 可以为 nil，表示 Service 存在但不公开 RPC。保留这类记录可以把“无路由”
// 和“找到 Service 但契约不匹配”稳定地区分开。
func (runtime *Runtime) RegisterService(
	serviceName string,
	target service.IService,
	dispatcher Dispatcher,
) error {
	if runtime == nil || serviceName == "" || target == nil {
		return errs.ErrInvalidArgument
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.frozen.Load() || runtime.closed.Load() {
		return errs.ErrServiceNotReady
	}
	if _, exists := runtime.endpoints[serviceName]; exists {
		return errs.ErrInvalidArgument
	}
	runtime.endpoints[serviceName] = serviceEndpoint{
		serviceName: serviceName,
		target:      target,
		dispatcher:  dispatcher,
	}
	return nil
}

// Freeze 结束 Node RPC 目录装配并允许生成客户端执行调用。
func (runtime *Runtime) Freeze() error {
	if runtime == nil {
		return errs.ErrInvalidArgument
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.closed.Load() {
		return errs.ErrServiceStopped
	}
	runtime.frozen.Store(true)
	return nil
}

// Close 永久关闭一次性 Runtime，并拒绝之后的新调用。
func (runtime *Runtime) Close() {
	if runtime == nil {
		return
	}
	runtime.closed.Store(true)
}

// AllocateRequest 为生成代码取得一次准确大小的请求 Buffer。
func (runtime *Runtime) AllocateRequest(size int) (*Buffer, error) {
	if runtime == nil || runtime.pool == nil || size < 0 ||
		size > DefaultMaxMessageSize {
		return nil, errs.ErrRPCEncodeFailed
	}
	return runtime.pool.Acquire(size), nil
}

// resolve 把逻辑 Target 解析为当前 Node 内唯一端点，并校验完整契约。
func (runtime *Runtime) resolve(
	target Target,
	contractID ContractID,
	fingerprint ContractFingerprint,
) (serviceEndpoint, error) {
	if runtime == nil || !target.valid() || contractID == 0 ||
		fingerprint == (ContractFingerprint{}) {
		return serviceEndpoint{}, errs.ErrInvalidArgument
	}
	if !runtime.frozen.Load() {
		return serviceEndpoint{}, errs.ErrServiceNotReady
	}
	if runtime.closed.Load() {
		return serviceEndpoint{}, errs.ErrServiceStopped
	}
	if target.mode == targetServiceOnNode && target.nodeID != runtime.nodeID {
		return serviceEndpoint{}, errs.ErrRPCNoRoute
	}
	endpoint, exists := runtime.endpoints[target.serviceName]
	if !exists {
		return serviceEndpoint{}, errs.ErrRPCNoRoute
	}
	if endpoint.dispatcher == nil ||
		endpoint.dispatcher.ContractID() != contractID ||
		endpoint.dispatcher.Fingerprint() != fingerprint {
		return serviceEndpoint{}, errs.ErrRPCContractMismatch
	}
	return endpoint, nil
}

// submit 把已经编码的请求唯一所有权移交给目标 Service FIFO。
//
// 提交失败时所有权仍属于调用方；提交成功后目标任务无论正常、错误或 panic 都会释放请求。
func (runtime *Runtime) submit(
	ctx context.Context,
	target Target,
	contractID ContractID,
	fingerprint ContractFingerprint,
	methodID MethodID,
	kind CallKind,
	request *Buffer,
	complete func(*Buffer, error),
) error {
	if ctx == nil || request == nil || methodID == 0 ||
		(kind != CallRequest && kind != CallNotify) ||
		(kind == CallRequest && complete == nil) ||
		(kind == CallNotify && complete != nil) {
		return errs.ErrInvalidArgument
	}
	endpoint, err := runtime.resolve(target, contractID, fingerprint)
	if err != nil {
		return err
	}

	// caller Context 只提供业务值；目标任务自身的生命周期和执行令牌由目标 Scheduler
	// 创建。WithoutCancel 防止 Notify 在准入成功后又被调用方撤回。
	control := ctx
	if kind == CallNotify {
		control = context.WithoutCancel(ctx)
	}
	values := context.WithoutCancel(ctx)
	err = endpoint.target.DispatchAsync(func(targetCtx context.Context) {
		// Dispatcher 只能在本任务期间借用请求字节；释放动作覆盖所有退出路径。
		defer request.Release()
		dispatchCtx := &rpcContext{
			execution: targetCtx,
			control:   control,
			values:    values,
		}

		if kind == CallNotify {
			runtime.dispatchNotify(
				dispatchCtx,
				endpoint,
				methodID,
				request.Bytes(),
			)
			return
		}

		response, dispatchErr := runtime.dispatchRequest(
			dispatchCtx,
			endpoint,
			methodID,
			request.Bytes(),
		)
		complete(response, dispatchErr)
	})
	return err
}

// dispatchRequest 执行请求—响应 Dispatcher，并取得其一次分配的最终响应。
func (runtime *Runtime) dispatchRequest(
	ctx context.Context,
	endpoint serviceEndpoint,
	methodID MethodID,
	request []byte,
) (response *Buffer, result error) {
	writer := newResponseWriter(runtime.pool, DefaultMaxMessageSize)
	defer func() {
		if value := recover(); value != nil {
			writer.release()
			response = nil
			result = errs.ErrRPCExecutionPanic
			runtime.logger.ErrorStack(
				"rpc method panic",
				originlog.String("service_name", endpoint.serviceName),
				originlog.Uint64("method_id", uint64(methodID)),
				originlog.String("panic", fmt.Sprint(value)),
				originlog.String("panic_stack", string(debug.Stack())),
			)
		}
	}()

	writer, err := endpoint.dispatcher.Dispatch(
		ctx,
		methodID,
		CallRequest,
		request,
		writer,
	)
	if err != nil {
		writer.release()
		// 本地调用也只跨 RPC 边界传递稳定错误码，不泄露同一 Go error 指针。
		return nil, errs.New(errs.CodeOf(err))
	}
	response = writer.take()
	if response == nil {
		return nil, errs.ErrRPCEncodeFailed
	}
	return response, nil
}

// dispatchNotify 执行通知 Dispatcher；业务 error 被主动放弃，panic 只在目标侧诊断。
func (runtime *Runtime) dispatchNotify(
	ctx context.Context,
	endpoint serviceEndpoint,
	methodID MethodID,
	request []byte,
) {
	defer func() {
		if value := recover(); value != nil {
			runtime.logger.ErrorStack(
				"rpc notify panic",
				originlog.String("service_name", endpoint.serviceName),
				originlog.Uint64("method_id", uint64(methodID)),
				originlog.String("panic", fmt.Sprint(value)),
				originlog.String("panic_stack", string(debug.Stack())),
			)
		}
	}()
	_, err := endpoint.dispatcher.Dispatch(
		ctx,
		methodID,
		CallNotify,
		request,
		ResponseWriter{},
	)
	if err != nil {
		runtime.logger.Error(
			"rpc notify failed",
			originlog.String("service_name", endpoint.serviceName),
			originlog.Uint64("method_id", uint64(methodID)),
			originlog.Err(err),
		)
	}
}

// rpcContext 组合目标 Service 执行 Context 与调用方保留的只读业务值。
type rpcContext struct {
	execution context.Context
	control   context.Context
	values    context.Context
}

func (ctx *rpcContext) Deadline() (time.Time, bool) { return ctx.control.Deadline() }
func (ctx *rpcContext) Done() <-chan struct{}       { return ctx.control.Done() }
func (ctx *rpcContext) Err() error                  { return ctx.control.Err() }
func (ctx *rpcContext) Value(key any) any {
	if value := ctx.execution.Value(key); value != nil {
		return value
	}
	return ctx.values.Value(key)
}
