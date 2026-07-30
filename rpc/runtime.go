package rpc

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	publicdiscovery "github.com/duanhf2012/origin/v3/discovery"
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
	public      bool
}

// Runtime 管理一个 Node 内的本地 RPC 路由、共享 BufferPool 和调用提交。
//
// RegisterService 只允许 Node 在 Runtime 发布前调用。Freeze 后目录只读，因此 RPC 热路径
// 查找普通 Map 不需要锁；每个 Node 使用独立 Runtime，不存在跨 Node 隐式共享状态。
type Runtime struct {
	nodeID    string
	sessionID uint64
	pool      *bufferpool.Pool
	logger    originlog.Logger

	mu        sync.Mutex
	endpoints map[string]serviceEndpoint
	frozen    atomic.Bool
	closed    atomic.Bool
	requestID atomic.Uint64
	// inboundReady 只在整个 Node 越过统一 OnStart 屏障后开放远端业务准入。
	inboundReady atomic.Bool
	// remoteResolver 由所属 Node 的不可变发现目录实现，Freeze 后保持只读。
	remoteResolver RemoteResolver
	// localLabels 在 Node 装配冷路径深复制一次，Freeze 后只读。
	localLabels      map[string]string
	localLabelsBound bool
	// transportObserver 把整体入站状态变化交给 Node。网络回调只发布常数大小快照，
	// 不在这里执行发现发布、Service Stop 或 Application Stop。
	transportObserver func(TransportEvent)

	// remote 在配置启用 TCP 时保存连接、监听和 Deadline 资源；未配置时保持 nil，
	// 本地调用热路径只需一次 nil 判断。
	remote *remoteRuntime
	// nats 在配置启用 NATS 时保存 Node 共享 Connection、两个 Subscription 和 pending。
	// TCP 与 NATS 直接使用不同字段，不在逐调用热路径引入接口分派。
	nats *natsRuntime
}

// RemoteRoute 是发现目录为一次精确远端 RPC 解析出的传输与进程会话目标。
type RemoteRoute struct {
	NodeID    string
	SessionID uint64
	Transport string
	Address   string
}

// RemoteCandidate 是 RPC Runtime 从一次固定发现快照读取的远端候选标量。
type RemoteCandidate struct {
	NodeID      string
	SessionID   uint64
	ServiceName string
	State       publicdiscovery.State
	Labels      map[string]string
	Transport   string
	Address     string
	ContractID  ContractID
	Fingerprint ContractFingerprint
}

// RemoteSnapshot 是一次 Prepare 全程复用的不可变远端候选视图。
type RemoteSnapshot interface {
	Len(serviceName string) int
	Candidate(serviceName string, index int) (RemoteCandidate, bool)
	Find(nodeID string, serviceName string) (RemoteCandidate, bool)
}

// RemoteResolver 是 RPC 对所属 Node 发现目录定义的最小热路径接口。
type RemoteResolver interface {
	ResolveRemote(
		nodeID string,
		serviceName string,
		contractID ContractID,
		fingerprint ContractFingerprint,
	) (RemoteRoute, error)
}

// RemoteSnapshotResolver 是发现目录为自动实例选择提供的可选只读扩展。
//
// 精确远端解析仍只要求 RemoteResolver，保持既有测试和窄实现兼容。
type RemoteSnapshotResolver interface {
	RemoteResolver
	Snapshot() RemoteSnapshot
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

// BindSessionID 在 Freeze 前绑定当前 Node 进程会话，供 TCP Hello/Ack 校验。
func (runtime *Runtime) BindSessionID(sessionID uint64) error {
	if runtime == nil || sessionID == 0 {
		return errs.ErrInvalidArgument
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.frozen.Load() || runtime.closed.Load() {
		return errs.ErrServiceNotReady
	}
	runtime.sessionID = sessionID
	return nil
}

// BindLocalLabels 在 Freeze 前冻结当前 Node 的本地候选标签。
func (runtime *Runtime) BindLocalLabels(labels map[string]string) error {
	if runtime == nil {
		return errs.ErrInvalidArgument
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.frozen.Load() || runtime.closed.Load() ||
		runtime.localLabelsBound {
		return errs.ErrServiceNotReady
	}
	runtime.localLabels = cloneRouteLabels(labels)
	runtime.localLabelsBound = true
	return nil
}

func cloneRouteLabels(source map[string]string) map[string]string {
	if len(source) == 0 {
		return nil
	}
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

// BindRemoteResolver 在 Freeze 前绑定当前 Node 唯一的服务发现解析器。
func (runtime *Runtime) BindRemoteResolver(resolver RemoteResolver) error {
	if runtime == nil || resolver == nil {
		return errs.ErrInvalidArgument
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.frozen.Load() || runtime.closed.Load() ||
		runtime.remoteResolver != nil {
		return errs.ErrServiceNotReady
	}
	runtime.remoteResolver = resolver
	return nil
}

// BindTransportObserver 在 Freeze 前绑定当前 Node 唯一的 Transport 状态观察者。
//
// Observer 必须快速返回；Runtime 可能从网络恢复 goroutine 中调用它。观察者只能更新状态、
// 撤销或重新发布发现，不能直接停止 Node 或 Application。
func (runtime *Runtime) BindTransportObserver(observer func(TransportEvent)) error {
	if runtime == nil || observer == nil {
		return errs.ErrInvalidArgument
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.frozen.Load() || runtime.closed.Load() ||
		runtime.transportObserver != nil {
		return errs.ErrServiceNotReady
	}
	runtime.transportObserver = observer
	return nil
}

// reportTransportEvent 在不持有 Runtime 状态锁时同步发布一次 Transport 状态变化。
//
// Transport 状态变化属于冷路径；同步调用可以保证发现撤销先于后续恢复尝试完成，不需要
// 再建立一条可能乱序或溢出的内部 Channel。
func (runtime *Runtime) reportTransportEvent(event TransportEvent) {
	if runtime == nil {
		return
	}
	runtime.mu.Lock()
	observer := runtime.transportObserver
	runtime.mu.Unlock()
	if observer != nil {
		observer(event)
	}
}

// OpenInbound 在整个 Node 的全部 OnStart 成功后开放远端业务请求准入。
func (runtime *Runtime) OpenInbound() error {
	if runtime == nil || !runtime.frozen.Load() || runtime.closed.Load() {
		return errs.ErrServiceNotReady
	}
	if !runtime.inboundReady.CompareAndSwap(false, true) {
		return errs.ErrInvalidArgument
	}
	return nil
}

// resolveRemote 使用当前不可变发现目录解析精确远端目标。
func (runtime *Runtime) resolveRemote(
	nodeID string,
	serviceName string,
	contractID ContractID,
	fingerprint ContractFingerprint,
) (RemoteRoute, error) {
	if runtime == nil || nodeID == "" || serviceName == "" ||
		contractID == 0 || fingerprint == (ContractFingerprint{}) {
		return RemoteRoute{}, errs.ErrInvalidArgument
	}
	resolver := runtime.remoteResolver
	if resolver == nil {
		return RemoteRoute{}, errs.ErrRPCNoRoute
	}
	route, err := resolver.ResolveRemote(
		nodeID,
		serviceName,
		contractID,
		fingerprint,
	)
	if err != nil {
		return RemoteRoute{}, err
	}
	if route.NodeID != nodeID || route.SessionID == 0 {
		return RemoteRoute{}, errs.ErrTransportUnavailable
	}
	switch route.Transport {
	case TransportTCP:
		if runtime.remote == nil || validateAdvertiseAddress(route.Address) != nil {
			return RemoteRoute{}, errs.ErrTransportUnavailable
		}
	case TransportNATS:
		if runtime.nats == nil || route.Address != "" {
			return RemoteRoute{}, errs.ErrTransportUnavailable
		}
	default:
		return RemoteRoute{}, errs.ErrTransportUnavailable
	}
	return route, nil
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
	return runtime.RegisterServiceVisibility(
		serviceName,
		target,
		dispatcher,
		true,
	)
}

// RegisterServiceVisibility 登记本地端点，并显式决定它是否进入远端握手目录。
//
// 私有 Node 或 `_ServiceName` 仍可被同 Node 客户端调用，但不会通过 TCP 暴露。
func (runtime *Runtime) RegisterServiceVisibility(
	serviceName string,
	target service.IService,
	dispatcher Dispatcher,
	public bool,
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
		public:      public,
	}
	return nil
}

// Configure 在 Freeze 前装配当前 Node 的远端 RPC 运行配置。
//
// nil 表示当前 Node 只提供本地 RPC。配置只允许写入一次，防止启动阶段发生地址或上限漂移。
func (runtime *Runtime) Configure(config *Config) error {
	if runtime == nil {
		return errs.ErrInvalidArgument
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.frozen.Load() || runtime.closed.Load() ||
		runtime.remote != nil || runtime.nats != nil {
		return errs.ErrInvalidArgument
	}
	if config == nil {
		return nil
	}
	if err := config.Validate(); err != nil {
		return err
	}
	switch config.Transport {
	case TransportTCP:
		runtime.remote = newRemoteRuntime(runtime, *config)
	case TransportNATS:
		runtime.nats = newNATSRuntime(runtime, *config)
	default:
		return errs.ErrInvalidArgument
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

// Close 使用调用方的总体停止 Context 永久关闭一次性 Runtime。
func (runtime *Runtime) Close(ctx context.Context) error {
	if runtime == nil {
		return nil
	}
	if ctx == nil {
		return errs.ErrInvalidArgument
	}
	result := runtime.BeginStop(ctx)
	runtime.closed.Store(true)
	if runtime.remote != nil {
		result = errors.Join(result, runtime.remote.closeTransport(ctx))
		runtime.remote.closeDeadlines()
	}
	if runtime.nats != nil {
		result = errors.Join(result, runtime.nats.close(ctx))
	}
	runtime.reportTransportEvent(TransportEvent{
		Kind:  runtime.transportKind(),
		State: TransportStateStopped,
	})
	return result
}

// transportKind 返回冻结配置对应的内部 Transport 类型。
func (runtime *Runtime) transportKind() TransportKind {
	switch {
	case runtime != nil && runtime.remote != nil:
		return TransportKindTCP
	case runtime != nil && runtime.nats != nil:
		return TransportKindNATS
	default:
		return TransportKindNone
	}
}

// TransportKind 返回当前 Runtime 的冻结传输类型。
//
// 该查询只用于 Node 初始化状态，不进入逐次 RPC 热路径。
func (runtime *Runtime) TransportKind() TransportKind {
	return runtime.transportKind()
}

// maxMessageSize 返回当前 Node 冻结的业务 payload 上限。
func (runtime *Runtime) maxMessageSize() int {
	if runtime != nil && runtime.remote != nil {
		return runtime.remote.config.MaxPayloadSize
	}
	if runtime != nil && runtime.nats != nil {
		return runtime.nats.config.MaxPayloadSize
	}
	return DefaultMaxPayloadSize
}

// AllocateRequest 为生成代码取得准确 payload，并只为远端目标保留对应调用头空间。
func (runtime *Runtime) AllocateRequest(
	target Target,
	size int,
	kind CallKind,
) (*Buffer, error) {
	if runtime == nil || runtime.pool == nil || size < 0 ||
		size > runtime.maxMessageSize() ||
		(kind != CallRequest && kind != CallNotify) ||
		!target.valid() {
		return nil, errs.ErrRPCEncodeFailed
	}

	// 同 Node 调用不承担网络头容量；只有显式选择其他 Node 时按准确 Kind 保留 headroom。
	if target.mode != targetServiceOnNode || target.nodeID == runtime.nodeID {
		return runtime.pool.Acquire(size), nil
	}
	headroom := 0
	switch {
	case runtime.remote != nil && kind == CallRequest:
		headroom = wireRequestFixedSize + len(target.serviceName)
	case runtime.remote != nil:
		headroom = wireNotifyFixedSize + len(target.serviceName)
	case runtime.nats != nil && kind == CallRequest:
		headroom = natsRequestFixedSize +
			len(runtime.nodeID) +
			len(target.serviceName)
	case runtime.nats != nil:
		headroom = natsNotifyFixedSize + len(target.serviceName)
	default:
		return nil, errs.ErrRPCNoRoute
	}
	if headroom > wireEnvelopeSize || !validWireName(target.serviceName) {
		return nil, errs.ErrRPCEncodeFailed
	}
	return runtime.pool.AcquireWithHeadroom(size, headroom), nil
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
	owner service.IService,
	target Target,
	contractID ContractID,
	fingerprint ContractFingerprint,
	methodID MethodID,
	kind CallKind,
	request *Buffer,
	complete func(*Buffer, error),
) (remoteRequestHandle, error) {
	if ctx == nil || owner == nil || request == nil || methodID == 0 ||
		contractID == 0 || fingerprint == (ContractFingerprint{}) ||
		(kind != CallRequest && kind != CallNotify) ||
		(kind == CallRequest && complete == nil) ||
		(kind == CallNotify && complete != nil) {
		return remoteRequestHandle{}, errs.ErrInvalidArgument
	}
	if !runtime.frozen.Load() {
		return remoteRequestHandle{}, errs.ErrServiceNotReady
	}
	if runtime.closed.Load() {
		return remoteRequestHandle{}, errs.ErrServiceStopped
	}

	// 指定其他 Node 时只走所选真实 Transport；同进程 Runtime 之间也不做指针短路。
	if target.mode == targetServiceOnNode && target.nodeID != runtime.nodeID {
		// 服务发现是远端调用的唯一事实来源；历史连接不能绕过当前可见快照。
		route, err := runtime.resolveRemote(
			target.nodeID,
			target.serviceName,
			contractID,
			fingerprint,
		)
		if err != nil {
			return remoteRequestHandle{}, err
		}
		switch route.Transport {
		case TransportTCP:
			session := runtime.remote.targetSession(target.nodeID, route.SessionID)
			if session == nil {
				return remoteRequestHandle{}, errs.ErrTransportUnavailable
			}
			if kind == CallNotify {
				err := session.sendNotify(
					target.serviceName,
					fingerprint,
					methodID,
					request,
				)
				return remoteRequestHandle{}, err
			}
			timeout, err := service.AwaitTimeoutOf(owner)
			if err != nil {
				return remoteRequestHandle{}, err
			}
			remaining, err := remoteRemainingTimeout(timeout, ctx)
			if err != nil {
				return remoteRequestHandle{}, err
			}
			return session.sendRequest(
				target.serviceName,
				fingerprint,
				methodID,
				remaining,
				request,
				complete,
			)
		case TransportNATS:
			if kind == CallNotify {
				err := runtime.nats.sendNotify(
					target.nodeID,
					route.SessionID,
					target.serviceName,
					methodID,
					request,
				)
				return remoteRequestHandle{}, err
			}
			timeout, err := service.AwaitTimeoutOf(owner)
			if err != nil {
				return remoteRequestHandle{}, err
			}
			remaining, err := remoteRemainingTimeout(timeout, ctx)
			if err != nil {
				return remoteRequestHandle{}, err
			}
			return runtime.nats.sendRequest(
				target.nodeID,
				route.SessionID,
				target.serviceName,
				methodID,
				remaining,
				request,
				complete,
			)
		default:
			return remoteRequestHandle{}, errs.ErrTransportUnavailable
		}
	}
	endpoint, err := runtime.resolve(target, contractID, fingerprint)
	if err != nil {
		return remoteRequestHandle{}, err
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
			0,
		)
		complete(response, dispatchErr)
	})
	return remoteRequestHandle{}, err
}

// dispatchRequest 执行请求—响应 Dispatcher，并取得其一次分配的最终响应。
func (runtime *Runtime) dispatchRequest(
	ctx context.Context,
	endpoint serviceEndpoint,
	methodID MethodID,
	request []byte,
	responseHeadroom int,
) (response *Buffer, result error) {
	writer := newResponseWriter(
		runtime.pool,
		runtime.maxMessageSize(),
		responseHeadroom,
	)
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
