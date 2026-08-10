// Package service 定义 Origin 业务 Service 的最小生命周期和只读运行环境。
package service

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/duanhf2012/origin/v3/errs"
	originlog "github.com/duanhf2012/origin/v3/log"
)

// IService 是所有 Origin 业务 Service 必须满足的最小生命周期接口。
//
// 业务类型通常嵌入 Service，并只覆盖实际需要的生命周期方法。未导出的 baseService 方法
// 保证只有嵌入 Origin Service 的类型才能进入框架装配。
type IService interface {
	ITimer
	IServiceConfig

	Name() string
	OnInit() error
	OnStart(ctx context.Context) error
	OnStop(ctx context.Context) error

	DispatchAsync(fn func(context.Context)) error
	Await(ctx context.Context, fn func(context.Context) error) error
	SetDefaultAwaitTimeout(timeout time.Duration) error
	ExecutionStats() ExecutionStats
	GoSafe(fn func()) error
	RunSafe(fn func()) error
	AddModule(module IModule) error
	SubscribeEvent(eventID EventID, handler EventHandler) error
	NotifyEventAsync(event Event) error
	NotifyEventSync(ctx context.Context, event Event) error
	EventStats() EventStats
	Retire(ctx context.Context) error
	Resume(ctx context.Context) error
	GetNode() NodeRuntime

	baseService() *Service
}

// Service 为业务 Service 提供默认生命周期和只读运行环境查询。
//
// Service 应以值方式匿名嵌入业务结构体。它在绑定后不能复制，也不能被多个业务 Service
// 或 Node 共享。
type Service struct {
	// bindMu 只保护一次性 Runtime 绑定；正常运行查询不经过互斥锁。
	bindMu sync.Mutex
	// runtime 在 Node 完成实例装配后保持只读。
	runtime Runtime
	// defaultAwaitTimeout 只允许 OnInit 在调度器启动前写入，启动后保持只读。
	defaultAwaitTimeout time.Duration
	// scheduler 使用原子指针连接冷路径装配和并发业务热路径，避免每次查询 bindMu。
	scheduler atomic.Pointer[serviceScheduler]

	// modules 及其生命周期标记只在 Node 启停冷路径和同步 OnInit 注册栈中访问，
	// 复用 bindMu 串行化，不为每个 Service 额外分配管理器或集合。
	modules             []*moduleEntry
	moduleTarget        IService
	moduleInitActive    bool
	moduleSealed        bool
	moduleInitErr       error
	serviceStartEntered bool

	// events 在 OnInit 注册期惰性建立，封树后 Map 和监听器 Slice 均只读。
	events             map[EventID]*eventSlot
	eventListenerCount int
	eventSyncTotal     atomic.Uint64
	eventAsyncTotal    atomic.Uint64
	eventFailureTotal  atomic.Uint64
}

// OnInit 是不需要初始化逻辑时使用的默认空实现。
func (service *Service) OnInit() error {
	return nil
}

// OnStart 是不需要启动逻辑时使用的默认空实现。
func (service *Service) OnStart(context.Context) error {
	return nil
}

// OnStop 是不需要停止逻辑时使用的默认空实现。
func (service *Service) OnStop(context.Context) error {
	return nil
}

// Name 返回当前实例在所属 Node 内的实际 ServiceName。
func (service *Service) Name() string {
	// 未绑定的类型样本没有运行身份，返回空字符串比伪造类型名更明确。
	if service == nil || service.runtime == nil {
		return ""
	}
	return service.runtime.ServiceName()
}

// NodeID 返回当前 Service 所属 Node 的稳定 ID。
func (service *Service) NodeID() string {
	// 类型样本和装配失败对象尚不属于 Node，因此返回空字符串。
	if service == nil || service.runtime == nil {
		return ""
	}
	return service.runtime.NodeID()
}

// GetNode 返回当前 Service 所属 Node 的最小运行外观。
//
// 未绑定的类型模板返回 nil。NodeRuntime 独立于基础 Runtime，避免 service 包反向依赖具体
// node 包，也不强迫只提供基础调度能力的 Runtime 实现 Node 高级外观。
func (service *Service) GetNode() NodeRuntime {
	if service == nil || service.runtime == nil {
		return nil
	}
	current, ok := service.runtime.(NodeRuntime)
	if !ok {
		return nil
	}
	return current
}

// State 返回当前 Service 的无锁生命周期状态快照。
func (service *Service) State() State {
	// 未绑定对象仍处于 Created，便于零值 Service 在测试和 Setup 时安全查询。
	if service == nil || service.runtime == nil {
		return StateCreated
	}
	return service.runtime.State()
}

// Logger 返回已经绑定 NodeID 和 ServiceName 的结构化 Logger。
func (service *Service) Logger() originlog.Logger {
	// 未绑定对象不能访问 Application Logger，返回不会产生输出的安全零值。
	if service == nil || service.runtime == nil {
		return originlog.NewNop()
	}
	return service.runtime.Logger()
}

// LookupLocalService 只查询当前 Service 所属 Node 中具有实际名称 name 的本地 Service。
//
// 它不读取服务发现目录、不查询其他 Node，也不发起网络或 RPC。
func (service *Service) LookupLocalService(name string) (IService, bool) {
	// 未绑定实例没有所属 Node；空名称也不具有有效查询语义。
	if service == nil || service.runtime == nil || name == "" {
		return nil, false
	}
	return service.runtime.LookupLocalService(name)
}

// DispatchAsync 把新的根任务异步投递到当前 Service 的串行执行上下文。
//
// 成功只表示任务已被有界队列接收；函数不会等待任务执行完成。
func (service *Service) DispatchAsync(fn func(context.Context)) error {
	// 先校验基础对象和函数，确保无效调用不读取未绑定的 Runtime。
	if service == nil || fn == nil {
		return errs.ErrInvalidArgument
	}

	// 公开生命周期先于内部状态发布。该检查既给未启动调用稳定错误，也防止
	// PrepareScheduler 已创建但 Node 尚未发布 Running 时提前接收业务任务。
	if err := service.acceptanceError(); err != nil {
		return err
	}
	scheduler := service.scheduler.Load()
	if scheduler == nil {
		return errs.ErrServiceNotReady
	}
	return scheduler.dispatch(fn)
}

// Await 暂时释放当前 Service 执行权，在原任务 goroutine 中等待 fn 返回，然后恢复原调用栈。
func (service *Service) Await(
	ctx context.Context,
	fn func(context.Context) error,
) error {
	// Context 只控制取消、Deadline 和 Value；nil 表示为本次 Await 建立新的默认预算。
	if service == nil || fn == nil {
		return errs.ErrInvalidArgument
	}
	scheduler := service.scheduler.Load()
	if scheduler == nil {
		return errs.ErrServiceNotReady
	}

	// RPC 的 Prepare 与最终响应等待会共享一个已经冻结的 operationContext；通用 Await
	// 则在本方法内创建并清理一次性预算，保证每次公开调用只有一个 Deadline。
	if preparedOperationContext(ctx, scheduler) != nil {
		return scheduler.await(ctx, fn)
	}
	operationCtx, finish, err := PrepareAwaitContext(service, ctx)
	if err != nil {
		return err
	}
	defer finish()
	return scheduler.await(operationCtx, fn)
}

// SetDefaultAwaitTimeout 设置当前 Service 覆盖 Node 默认值的 Await 超时。
//
// 该方法只允许由当前运行实例在 OnInit 中调用；启动后热路径只读取冻结结果。
func (service *Service) SetDefaultAwaitTimeout(timeout time.Duration) error {
	// 正时长是唯一有效覆盖值；零值表示没有 Service 级覆盖，不能通过本方法显式设置。
	if service == nil || timeout <= 0 {
		return errs.ErrInvalidArgument
	}

	// bindMu 只覆盖初始化冷路径，使并发误用无法在 Scheduler 发布后修改冻结配置。
	service.bindMu.Lock()
	defer service.bindMu.Unlock()
	if service.runtime == nil ||
		service.runtime.State() != StateInitializing ||
		service.scheduler.Load() != nil {
		return errs.ErrInvalidArgument
	}
	service.defaultAwaitTimeout = timeout
	return nil
}

// ExecutionStats 返回当前 Service 调度器的一致执行统计快照。
func (service *Service) ExecutionStats() ExecutionStats {
	// 未绑定或尚未启动的 Service 没有调度数据，返回结构体零值便于诊断路径安全调用。
	if service == nil {
		return ExecutionStats{}
	}
	scheduler := service.scheduler.Load()
	if scheduler == nil {
		return ExecutionStats{}
	}
	return scheduler.statsSnapshot()
}

// Failure 返回当前 Service 在运行期被隔离时记录的第一个根因。
//
// 正常、停止中或正常停止完成的 Service 返回 nil；失败根因会保留到一次性 Service 对象
// 被释放。该错误只供本地诊断，不应直接通过 RPC 发送。
func (service *Service) Failure() error {
	if service == nil || service.runtime == nil {
		return nil
	}
	return service.runtime.Failure()
}

// acceptanceError 把公开 Service 生命周期映射为稳定的调度准入错误。
func (service *Service) acceptanceError() error {
	switch service.State() {
	case StateRunning, StateRetired:
		return nil
	case StateStopping:
		return errs.ErrServiceStopping
	case StateStopped:
		return errs.ErrServiceStopped
	case StateFailed:
		return errs.ErrServiceFailed
	default:
		return errs.ErrServiceNotReady
	}
}

// baseService 返回嵌入对象，供 BindRuntime 完成唯一所有权绑定。
func (service *Service) baseService() *Service {
	return service
}
