package service

import (
	"context"
	"errors"
	"fmt"
	goruntime "runtime"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	"github.com/duanhf2012/origin/v3/errs"
	"github.com/duanhf2012/origin/v3/internal/container/ringqueue"
	"github.com/duanhf2012/origin/v3/internal/timerwheel"
	originlog "github.com/duanhf2012/origin/v3/log"
)

const (
	// initialReadyCapacity 沿用小容量起步策略，避免按每个 Service 的峰值上限预分配。
	initialReadyCapacity = 64
	// deadlineDrainBatch 限制一次 Deadline 通知处理的 ID 数量，避免长时间独占调度短锁。
	deadlineDrainBatch = 256
)

// schedulerState 表示 ServiceScheduler 的一次性内部生命周期。
type schedulerState uint8

const (
	schedulerCreated schedulerState = iota
	schedulerPrepared
	schedulerRunning
	schedulerDraining
	schedulerFinalizing
	schedulerFailed
	schedulerStopped
)

// schedulerInvariantError 只表示框架内部状态已经无法继续证明安全。
//
// 业务 panic 仍由单任务边界恢复；只有该私有类型可以把当前 Service 隔离为 Failed。
type schedulerInvariantError struct {
	message string
}

// schedulerFailureSnapshot 让故障边界不依赖可能已经损坏或遗留锁的 Scheduler 状态。
//
// 该对象只在首个内部不变量故障时分配一次，不进入正常任务热路径。
type schedulerFailureSnapshot struct {
	cause error
}

func (failure schedulerInvariantError) Error() string {
	return failure.message
}

// panicInvariant 把不应出现的内部状态交给 Runner/Watcher 最外层故障边界。
func panicInvariant(message string) {
	panic(schedulerInvariantError{message: message})
}

// taskState 表示一个根任务在 Scheduler 内唯一有效的位置。
type taskState uint8

const (
	taskReady taskState = iota
	taskRunning
	taskWaiting
	taskRecoveryReady
	taskCompleted
)

// serviceTaskKind 区分同一 Ready 队列中的业务投递和 Timer 回调。
//
// 两类任务共用执行槽、Await 和停止排空规则，只在调用入口与完成后的资源处理上不同。
type serviceTaskKind uint8

const (
	taskKindDispatch serviceTaskKind = iota
	taskKindTimer
	taskKindDiscovery
	taskKindEvent
)

// taskContextKey 是不可从 service 包外构造的 Context 私有键。
type taskContextKey struct{}

// taskContext 在普通 Context 语义之外携带当前 Origin 根任务的执行令牌。
//
// 它不记录 goroutine ID；Scheduler 通过 Task 状态和当前执行槽验证调用位置。调用方仍必须
// 遵守“Task Context 不交给其他 goroutine 调用执行权 API”的契约。
type taskContext struct {
	context.Context
	scheduler *serviceScheduler
	task      atomic.Pointer[serviceTask]
}

// Value 优先返回私有且不会复用的执行令牌，再委托父 Context 查询业务值。
func (taskContext *taskContext) Value(key any) any {
	if _, matched := key.(taskContextKey); matched {
		return taskContext
	}
	return taskContext.Context.Value(key)
}

// serviceTask 保存一个根任务从 Ready、Running、Waiting 到完成的全部状态。
//
// serviceTask 会在完整清零后进入 Scheduler 私有对象池；context 指向的 taskContext 不池化，
// 因此被业务错误保留的旧 Context 永远不会因 Task 复用获得新任务执行权。
type serviceTask struct {
	scheduler  *serviceScheduler
	context    *taskContext
	fn         func(context.Context)
	kind       serviceTaskKind
	timer      *businessTimer
	eventOwner *Service
	eventSlot  *eventSlot
	event      Event
	// timerGeneration 固定 Timer Task 建立时的代次，使 Pause/Resume/Cancel 后留在
	// Ready 队列中的旧任务变成可识别的无害墓碑。
	timerGeneration uint64
	state           taskState
	// pooled 只在 Task 已完整清零且位于 sync.Pool 中时为 true。
	pooled bool

	awaitGeneration uint64
	awaitContext    context.Context
	awaitInput      context.Context
	awaitCancel     context.CancelCauseFunc
	awaitDeadlineID timerwheel.DeadlineID
	awaitDeadlineAt time.Time
	awaitHandoff    chan struct{}
	awaitError      error
	awaitPanic      any
	awaitPanicStack []byte
	// syncEventDepth 只在当前 Task 持有执行槽时大于零；Await 在同一锁内拒绝释放。
	syncEventDepth uint8

	// restoredPanicStack 只在 Await 重新抛出 panic 到根任务边界期间临时保存原始堆栈。
	restoredPanicStack []byte
}

// deadlineBindingKind 区分 Service 共用 DeadlineQueue 中的 Await 和业务 Timer。
type deadlineBindingKind uint8

const (
	deadlineBindingAwait deadlineBindingKind = iota
	deadlineBindingLifecycleAwait
	deadlineBindingTimer
)

// lifecycleAwait 保存 OnStart/OnStop 顺序等待在 M8 DeadlineQueue 中的一次活动绑定。
type lifecycleAwait struct {
	token      *lifecycleContext
	generation uint64
	deadlineID timerwheel.DeadlineID
	cancel     context.CancelCauseFunc
	expired    bool
}

// deadlineBinding 防止旧 DeadlineID 误作用到复用后的 Await Task 或业务 Timer。
type deadlineBinding struct {
	kind       deadlineBindingKind
	task       *serviceTask
	token      *taskContext
	lifecycle  *lifecycleAwait
	timer      *businessTimer
	generation uint64
}

// serviceScheduler 串行执行一个 Service 的全部业务任务。
//
// mu 只保护状态和小对象移动，绝不覆盖用户函数。stopMu 只串行化生命周期停止冷路径。
type serviceScheduler struct {
	mu     sync.Mutex
	stopMu sync.Mutex

	state schedulerState
	// activated 区分从 Prepared 直接停止和已经启动过 Runner 的 Draining。
	// 状态进入 Draining 后仅凭 state 无法恢复这一事实，因此单独冻结该生命周期标记。
	activated bool
	// lifecycleGeneration 和 activeLifecycle 验证当前唯一 OnStart/OnStop 生命周期令牌。
	lifecycleGeneration uint64
	activeLifecycle     *lifecycleContext
	// lifecycleAwaitGeneration 和 activeLifecycleAwait 防止旧 Deadline 命中新一轮等待。
	lifecycleAwaitGeneration uint64
	activeLifecycleAwait     *lifecycleAwait
	config                   SchedulerConfig
	logger                   originlog.Logger
	runtime                  Runtime

	ready       *ringqueue.Queue[*serviceTask]
	runningTask *serviceTask
	// taskPool 只复用完整清零的内部 Task；每个根任务仍创建唯一、不复用的 taskContext 令牌。
	taskPool sync.Pool

	accepted int
	running  int
	awaiting int

	// 发现更新只占用常数大小状态，并在统一 Ready FIFO 中最多存在一个同步 Task。
	discoveryRun     func(context.Context)
	discoveryDirty   bool
	discoveryQueued  bool
	discoveryRunning bool

	acceptedHighWatermark int
	dispatchedTotal       uint64
	completedTotal        uint64
	rejectedTotal         uint64
	awaitTotal            uint64
	awaitCanceledTotal    uint64
	awaitTimeoutTotal     uint64
	panicTotal            uint64

	// timerQuotaLastLog 和 timerQuotaSuppressed 聚合同一 Service 的 Timer 额度拒绝诊断。
	// RejectedTotal 仍逐次精确累计，日志只用于低频提示。
	timerQuotaLastLog    time.Time
	timerQuotaSuppressed uint64

	wake       chan struct{}
	runnerDone chan struct{}
	// runnerWorkers 统计包含 Await 原 goroutine 和替补 Runner 在内的真实活动执行链。
	// 只有最后一个退出者才能关闭 runnerDone，避免故障时提前宣告仍有 goroutine 的
	// Scheduler 已经完成回收。
	runnerWorkers  atomic.Int32
	runnerDoneOnce sync.Once
	failureOnce    sync.Once
	failure        atomic.Pointer[schedulerFailureSnapshot]
	// failureLockUnsafe 表示故障边界无法在有限让步后重新取得状态锁。正式 Stop 必须避开
	// 该锁并执行保守清理，不能让一个损坏 Service 卡死整个 Application。
	failureLockUnsafe atomic.Bool
	// finalizer 由 Node 在关闭准入后安装，只能被最后一个 Service Runner 取得一次。
	finalizer          func(context.Context) error
	finalizerTarget    IService
	finalizerContext   context.Context
	finalizerFinish    func()
	finalizerInstalled bool
	finalizerStarted   bool
	finalizerDone      bool
	finalizerResult    error

	lifetimeContext context.Context
	cancelLifetime  context.CancelCauseFunc

	deadlineQueue    *timerwheel.DeadlineQueue
	deadlineBindings map[timerwheel.DeadlineID]deadlineBinding
	deadlineDone     chan struct{}

	timerEngine *timerwheel.Engine
	timers      map[TimerID]*businessTimer
	duePending  *ringqueue.Queue[dueTimerEntry]
	timerPool   sync.Pool
	timerStats  TimerStats

	stopResult error
}

// PrepareScheduler 为 target 创建一次性的 Ready 和 Deadline 控制资源。
//
// 该函数在业务 OnStart 前调用，使 OnStart 可以登记 Timer，同时保证任何用户任务都不会
// 与 OnStart 并发。Prepare 阶段只启动不执行用户代码的 Deadline watcher；
// ActivateScheduler 才会启动普通业务 Runner。
func PrepareScheduler(
	target IService,
	config SchedulerConfig,
	timerEngine *timerwheel.Engine,
) error {
	// 在创建 Queue 或 goroutine 前完成全部静态校验，失败路径不会留下部分资源。
	if target == nil || isNilService(target) {
		return invalidArgument("Service 不能为空")
	}
	base := target.baseService()
	if base == nil || base.runtime == nil {
		return invalidArgument("Service 尚未绑定 Runtime")
	}
	if base.runtime.State() != StateStarting {
		return invalidArgument("ServiceScheduler 只能在 Service Starting 阶段启动")
	}
	if timerEngine == nil {
		return invalidArgument("ServiceScheduler TimerEngine 不能为空")
	}
	normalized, err := normalizedSchedulerConfig(config)
	if err != nil {
		return err
	}
	if base.scheduler.Load() != nil {
		return invalidArgument("ServiceScheduler 不能重复启动")
	}

	// Ready 只按较小初始容量分配；DeadlineQueue 由当前 Scheduler 独占关闭。
	initialCapacity := min(initialReadyCapacity, normalized.MaxTasks)
	ready, err := ringqueue.New[*serviceTask](initialCapacity, normalized.MaxTasks)
	if err != nil {
		return errs.Wrap(errs.CodeInternal, err)
	}
	// DuePending 只按 Node Timer 额度建立硬上限，初始仍使用小容量渐进增长。
	// 它存放轻量值条目，不复制回调，也不会按三百万默认额度预分配。
	dueInitialCapacity := min(initialReadyCapacity, base.runtime.TimerLimit())
	duePending, err := ringqueue.New[dueTimerEntry](
		dueInitialCapacity,
		base.runtime.TimerLimit(),
	)
	if err != nil {
		return errs.Wrap(errs.CodeInternal, err)
	}
	deadlineQueue, err := timerEngine.NewDeadlineQueue()
	if err != nil {
		return fmt.Errorf("创建 Service DeadlineQueue: %w", err)
	}
	lifetimeContext, cancelLifetime := context.WithCancelCause(context.Background())

	// 冻结 Service 级超时覆盖并建立全部同步对象，之后才原子发布 Scheduler。
	scheduler := &serviceScheduler{
		state:            schedulerPrepared,
		config:           normalized,
		logger:           base.runtime.Logger(),
		runtime:          base.runtime,
		ready:            ready,
		wake:             make(chan struct{}, 1),
		runnerDone:       make(chan struct{}),
		lifetimeContext:  lifetimeContext,
		cancelLifetime:   cancelLifetime,
		deadlineQueue:    deadlineQueue,
		deadlineBindings: make(map[timerwheel.DeadlineID]deadlineBinding),
		deadlineDone:     make(chan struct{}),
		timerEngine:      timerEngine,
		timers:           make(map[TimerID]*businessTimer),
		duePending:       duePending,
	}
	if base.defaultAwaitTimeout > 0 {
		scheduler.config.DefaultAwaitTimeout = base.defaultAwaitTimeout
	}
	if !base.scheduler.CompareAndSwap(nil, scheduler) {
		// 并发重复装配不获得所有权；立即按创建逆序释放本次冷路径资源。
		cancelLifetime(errs.ErrServiceStopped)
		deadlineQueue.Close()
		return invalidArgument("ServiceScheduler 不能重复启动")
	}
	// OnStart 默认 Await 也使用 M8 DeadlineQueue，因此控制协程必须先于 OnStart 工作。
	// 它只取消 Context，不会执行 Timer、监听器或其他业务回调。
	go scheduler.watchDeadlines()
	return nil
}

// ActivateScheduler 在 Service 已发布 Running 后启动唯一普通业务 Runner。
func ActivateScheduler(target IService) error {
	// 激活只接受已经由 PrepareScheduler 完整发布的真实 Service。
	if target == nil || isNilService(target) {
		return invalidArgument("Service 不能为空")
	}
	base := target.baseService()
	if base == nil || base.runtime == nil {
		return invalidArgument("Service 尚未绑定 Runtime")
	}
	if base.runtime.State() != StateRunning {
		return invalidArgument("ServiceScheduler 只能在 Service Running 阶段激活")
	}
	scheduler := base.scheduler.Load()
	if scheduler == nil {
		return errs.ErrServiceNotReady
	}

	// 状态转换先于 goroutine 创建；只有首位激活者能取得普通 Runner 的所有权。
	scheduler.mu.Lock()
	if scheduler.state != schedulerPrepared {
		scheduler.mu.Unlock()
		return invalidArgument("ServiceScheduler 不能重复激活")
	}
	scheduler.state = schedulerRunning
	scheduler.activated = true
	promotedDiscovery := scheduler.promoteDiscoveryLocked()
	scheduler.mu.Unlock()

	// 最后一个活动 Runner 关闭 runnerDone；Deadline watcher 已在 Prepare 阶段启动。
	scheduler.startRunner()
	if promotedDiscovery {
		scheduler.notifyRunner()
	}
	return nil
}

// BeginStopScheduler 在 Scheduler 状态锁内关闭新任务和新 Timer 的准入。
//
// Node 必须先调用本函数，再对外发布 Service Stopping，最后调用 StopScheduler 排空。
// Timer 创建和该状态转换使用同一把锁，因此不存在“已经观察到 Stopping 却仍创建成功”
// 的检查与使用竞态。该函数不等待 Runner，也不执行 OnStop。
func BeginStopScheduler(target IService) error {
	if target == nil || isNilService(target) {
		return invalidArgument("Service 不能为空")
	}
	base := target.baseService()
	if base == nil {
		return invalidArgument("Service 基础对象不能为空")
	}
	scheduler := base.scheduler.Load()
	if scheduler == nil {
		return nil
	}
	// 内部故障边界已经判定状态锁所有权不可证明时，不能在正式 Stop 的第一个步骤再次
	// 阻塞等待同一把锁。FinalizeScheduler 会执行不依赖该锁的保守清理并返回根因。
	if scheduler.failureLockUnsafe.Load() {
		return nil
	}

	scheduler.mu.Lock()
	notifyRunner := false
	switch scheduler.state {
	case schedulerPrepared, schedulerRunning:
		// Draining 是全部任务和 Timer 创建入口在同一锁内检查的线性化边界。
		scheduler.state = schedulerDraining
		scheduler.discoveryDirty = false
		scheduler.cancelUnreadyTimersLocked()
		notifyRunner = scheduler.activated
	case schedulerDraining, schedulerFailed, schedulerStopped:
		// 重复关闭准入保持幂等，由 StopScheduler 返回最终停止结果。
	default:
		scheduler.mu.Unlock()
		return errs.ErrServiceNotReady
	}
	scheduler.mu.Unlock()
	if notifyRunner {
		scheduler.notifyRunner()
	}
	return nil
}

// StopScheduler 拒绝新的根任务、排空已接受任务并回收当前 Service 的调度资源。
func StopScheduler(ctx context.Context, target IService) error {
	// Node 可能为 OnStart 失败、从未创建 Scheduler 的实例执行回滚；该路径无需额外清理。
	if target == nil || isNilService(target) {
		return invalidArgument("Service 不能为空")
	}
	if ctx == nil {
		return invalidArgument("停止 ServiceScheduler 的 Context 不能为空")
	}
	base := target.baseService()
	if base == nil {
		return invalidArgument("Service 基础对象不能为空")
	}
	scheduler := base.scheduler.Load()
	if scheduler == nil {
		return nil
	}
	return scheduler.stop(ctx, target, nil)
}

// FinalizeScheduler 排空已接受任务，由最后一个 Runner 独占执行 finalizer，再回收 Scheduler。
//
// Node 使用该入口执行 Service.OnStop。finalizer 为 nil 时等价于只排空并关闭 Scheduler，
// 供独立调度测试和没有业务回调的框架清理使用。
func FinalizeScheduler(
	ctx context.Context,
	target IService,
	finalizer func(context.Context) error,
) error {
	if target == nil || isNilService(target) {
		return invalidArgument("Service 不能为空")
	}
	if ctx == nil {
		return invalidArgument("停止 ServiceScheduler 的 Context 不能为空")
	}
	base := target.baseService()
	if base == nil {
		return invalidArgument("Service 基础对象不能为空")
	}
	scheduler := base.scheduler.Load()
	if scheduler == nil {
		if finalizer == nil {
			return nil
		}
		return errs.ErrServiceNotReady
	}
	return scheduler.stop(ctx, target, finalizer)
}

// dispatch 在同一个短锁事务内完成准入、容量检查、Task 建立和统计更新。
func (scheduler *serviceScheduler) dispatch(fn func(context.Context)) error {
	scheduler.mu.Lock()
	switch scheduler.state {
	case schedulerRunning:
		// 继续执行容量检查。
	case schedulerDraining:
		scheduler.mu.Unlock()
		return errs.ErrServiceStopping
	case schedulerStopped:
		scheduler.mu.Unlock()
		return errs.ErrServiceStopped
	default:
		scheduler.mu.Unlock()
		return errs.ErrServiceNotReady
	}
	notify, err := scheduler.enqueueDispatchLocked(fn)
	scheduler.mu.Unlock()
	if notify {
		scheduler.notifyRunner()
	}
	return err
}

// dispatchEvent 把完整事件通知作为一个 Ready item 提交，不为监听器建立闭包或任务。
func (scheduler *serviceScheduler) dispatchEvent(
	owner *Service,
	slot *eventSlot,
	event Event,
) error {
	scheduler.mu.Lock()
	switch scheduler.state {
	case schedulerRunning:
		// 继续执行统一容量检查。
	case schedulerDraining:
		scheduler.mu.Unlock()
		return errs.ErrServiceStopping
	case schedulerStopped:
		scheduler.mu.Unlock()
		return errs.ErrServiceStopped
	default:
		scheduler.mu.Unlock()
		return errs.ErrServiceNotReady
	}
	promotedTimer := scheduler.promoteDueTimersLocked()
	if scheduler.accepted >= scheduler.config.MaxTasks {
		scheduler.rejectedTotal++
		scheduler.mu.Unlock()
		if promotedTimer {
			scheduler.notifyRunner()
		}
		return errs.ErrServiceQueueFull
	}
	task := scheduler.acquireTaskLocked(nil)
	task.kind = taskKindEvent
	task.eventOwner = owner
	task.eventSlot = slot
	task.event = event
	if !scheduler.ready.Enqueue(task) {
		scheduler.mu.Unlock()
		panicInvariant("service: Event Ready 入队违反容量不变量")
	}
	scheduler.accepted++
	scheduler.dispatchedTotal++
	if scheduler.accepted > scheduler.acceptedHighWatermark {
		scheduler.acceptedHighWatermark = scheduler.accepted
	}
	scheduler.mu.Unlock()
	scheduler.notifyRunner()
	return nil
}

// dispatchContinuation 为一个当前正在执行的已接受任务预留异步完成任务。
//
// Draining 只允许这种能够由有效 Task Context 证明来源的延续，不允许任意 goroutine 借
// 普通 DispatchAsync 在停止边界后增加工作。
func (scheduler *serviceScheduler) dispatchContinuation(
	ctx context.Context,
	fn func(context.Context),
) error {
	token, _ := ctx.Value(taskContextKey{}).(*taskContext)
	if token == nil || token.scheduler != scheduler {
		return errs.ErrInvalidArgument
	}
	task := token.task.Load()
	if task == nil {
		return errs.ErrInvalidArgument
	}

	scheduler.mu.Lock()
	if scheduler.state != schedulerRunning &&
		scheduler.state != schedulerDraining {
		state := scheduler.state
		scheduler.mu.Unlock()
		if state == schedulerFailed {
			return errs.ErrServiceFailed
		}
		if state == schedulerStopped || state == schedulerFinalizing {
			return errs.ErrServiceStopped
		}
		return errs.ErrServiceNotReady
	}
	if token.task.Load() != task ||
		task.context != token ||
		task.scheduler != scheduler ||
		scheduler.runningTask != task ||
		scheduler.running != 1 ||
		task.state != taskRunning {
		scheduler.mu.Unlock()
		return errs.ErrInvalidArgument
	}
	notify, err := scheduler.enqueueDispatchLocked(fn)
	scheduler.mu.Unlock()
	if notify {
		scheduler.notifyRunner()
	}
	return err
}

// enqueueDispatchLocked 完成普通任务与已接受任务延续共用的容量和入队事务。
func (scheduler *serviceScheduler) enqueueDispatchLocked(
	fn func(context.Context),
) (notify bool, result error) {
	// 已经到期的 Timer 先于本次新投递取得空闲额度，但不会越过原先已经位于 Ready
	// 队列中的任务；提升只是追加到同一 FIFO 尾部。
	promotedTimer := scheduler.promoteDueTimersLocked()
	if scheduler.accepted >= scheduler.config.MaxTasks {
		scheduler.rejectedTotal++
		return promotedTimer, errs.ErrServiceQueueFull
	}

	// Task 主对象来自 Scheduler 私有池；不可复用的 Context 令牌负责隔离旧 Context，
	// 同时避免 context.WithValue 的额外包装层。
	task := scheduler.acquireTaskLocked(fn)
	if !scheduler.ready.Enqueue(task) {
		panicInvariant("service: Ready 环形队列在 Accepted 未达到硬上限时拒绝入队")
	}

	scheduler.accepted++
	scheduler.dispatchedTotal++
	if scheduler.accepted > scheduler.acceptedHighWatermark {
		scheduler.acceptedHighWatermark = scheduler.accepted
	}
	// 唤醒信号只描述“可能有新工作”，容量 1 足以合并并发投递。
	return true, nil
}

// run 是当前唯一活动业务 Runner；普通任务返回后由同一 goroutine 继续取后续工作。
// startRunner 在 goroutine 发布前增加所有权计数，消除极短 Runner 先退出的竞态。
func (scheduler *serviceScheduler) startRunner() {
	scheduler.runnerWorkers.Add(1)
	go scheduler.run()
}

// run 驱动一个活动执行链，并在最外层隔离任何逃逸的框架不变量 panic。
func (scheduler *serviceScheduler) run() {
	defer func() {
		if value := recover(); value != nil {
			if _, internal := value.(schedulerInvariantError); internal {
				scheduler.failInvariant(value, debug.Stack())
			} else {
				// 用户代码 panic 应在 Task/finalizer 边界被恢复；逃逸到这里说明框架边界
				// 自身损坏，同样只能隔离当前 Service，不能让整个进程退出。
				scheduler.failInvariant(value, debug.Stack())
			}
		}
		if scheduler.runnerWorkers.Add(-1) == 0 {
			scheduler.runnerDoneOnce.Do(func() {
				close(scheduler.runnerDone)
			})
		}
	}()

	for {
		// 每轮只在短锁内取得一个任务或判断排空完成，用户代码始终在锁外运行。
		task, recovery, finalize, stop := scheduler.nextTask()
		if stop {
			return
		}
		if finalize {
			scheduler.executeFinalizer()
			continue
		}
		if task == nil {
			// wake 是合并信号，陈旧通知只会让循环重新检查一次真实队列状态。
			<-scheduler.wake
			continue
		}
		if recovery {
			// 当前替补 Runner 已在锁内把执行槽归还原任务；发送交接信号后必须立即退出，
			// 不能与恢复后的原 goroutine 同时继续取任务。
			task.awaitHandoff <- struct{}{}
			return
		}

		// 普通任务执行完后仍由当前 Runner 归还槽位并继续循环。
		scheduler.executeTask(task)
	}
}

// failInvariant 原子隔离一个无法继续证明安全的 Scheduler。
func (scheduler *serviceScheduler) failInvariant(value any, stack []byte) {
	scheduler.failureOnce.Do(func() {
		cause := errs.Wrap(
			errs.CodeServiceFailed,
			fmt.Errorf("service scheduler invariant failed: %v", value),
		)

		// 先发布不依赖 Scheduler 锁的稳定根因。panic 可能发生在持锁内部函数中；若直接
		// 阻塞 Lock，当前故障边界会等待自己遗留的锁，进而卡死整个 Node 停止流程。
		scheduler.failure.Store(&schedulerFailureSnapshot{cause: cause})

		// 正常情况下 Scheduler 短锁会立即可得。有限次让步同时覆盖另一个控制 goroutine
		// 正在完成极短事务的情况；仍无法取得时按“锁所有权不可证明”处理，不强行解锁。
		locked := false
		for range 128 {
			if scheduler.mu.TryLock() {
				locked = true
				break
			}
			goruntime.Gosched()
		}
		if locked {
			if scheduler.state != schedulerStopped {
				scheduler.state = schedulerFailed
			}
			scheduler.mu.Unlock()
		} else {
			scheduler.failureLockUnsafe.Store(true)
		}

		// 先取消所有使用 Service lifetime 的等待，再唤醒可能空闲的替补 Runner。
		scheduler.cancelLifetime(cause)
		scheduler.notifyRunner()
		scheduler.logger.ErrorStack(
			"service scheduler invariant failed",
			originlog.String("panic", fmt.Sprint(value)),
			originlog.String("panic_stack", string(stack)),
		)
		scheduler.runtime.ReportFailure(cause)

		// Prepared 阶段尚未拥有 Runner；此时直接发布完成，正式回滚会走 Failed 清理。
		if !scheduler.activated && scheduler.runnerWorkers.Load() == 0 {
			scheduler.runnerDoneOnce.Do(func() {
				close(scheduler.runnerDone)
			})
		}
	})
}

// failureError 返回不依赖 Scheduler 锁的首个内部故障。
func (scheduler *serviceScheduler) failureError() error {
	if scheduler == nil {
		return errs.ErrServiceFailed
	}
	snapshot := scheduler.failure.Load()
	if snapshot == nil || snapshot.cause == nil {
		return errs.ErrServiceFailed
	}
	return snapshot.cause
}

// nextTask 从统一 FIFO 中取得普通任务或恢复项，并提交唯一执行槽状态。
func (scheduler *serviceScheduler) nextTask() (
	task *serviceTask,
	recovery bool,
	finalize bool,
	stop bool,
) {
	scheduler.mu.Lock()
	defer scheduler.mu.Unlock()
	if scheduler.state == schedulerFailed {
		return nil, false, false, true
	}

	// Ready 非空时先遵守 FIFO；Draining 也必须处理完已经接受的全部任务。
	// 被 Pause/Resume/Cancel 失效的 Timer Task 是队列墓碑，在同一锁内清理后继续
	// 检查下一个元素，不占用执行槽，也不调用用户函数。
	for {
		next, ok := scheduler.ready.Dequeue()
		if !ok {
			break
		}
		switch next.state {
		case taskReady:
			// 只有尚未开始的 Timer Task 才可能成为 Pause/Resume/Cancel 留下的墓碑。
			// Timer 回调从 Await 恢复时，Task 是 RecoveryReady、Timer 则仍为 Running；
			// 该组合必须直接恢复原 goroutine，不能套用 Ready 状态校验。
			if next.kind == taskKindTimer &&
				!scheduler.timerTaskCurrentLocked(next) {
				scheduler.discardStaleTimerTaskLocked(next)
				continue
			}
			if scheduler.running != 0 || scheduler.runningTask != nil {
				panicInvariant("service: 普通任务取出时执行槽仍被占用")
			}
			// Timer Task 只有真正取得唯一执行槽时才从 Ready 进入 Running；
			// 普通 Dispatch Task 不需要额外资源状态转换。
			if next.kind == taskKindTimer {
				scheduler.startTimerTaskLocked(next)
			}
			if next.kind == taskKindDiscovery {
				if !scheduler.discoveryQueued || scheduler.discoveryRunning {
					panicInvariant("service: 发现任务排队状态不一致")
				}
				scheduler.discoveryQueued = false
				scheduler.discoveryRunning = true
				// 当前 Task 将同步开始时看到的最新版本；执行期间的新更新会再次置脏。
				scheduler.discoveryDirty = false
			}
			next.state = taskRunning
			scheduler.running = 1
			scheduler.runningTask = next
			return next, false, false, false
		case taskRecoveryReady:
			return scheduler.restoreTaskLocked(next), true, false, false
		default:
			panicInvariant("service: Ready 环形队列包含非法 Task 状态")
		}
	}

	// 只有进入 Draining 且已接受任务归零，最后一个活动 Runner 才能退出。
	if scheduler.state == schedulerDraining && scheduler.accepted == 0 {
		if scheduler.running != 0 || scheduler.runningTask != nil || scheduler.awaiting != 0 {
			panicInvariant("service: Scheduler 排空计数不一致")
		}
		// Stop 所有者安装 finalizer 前不能让空闲 Runner 提前退出。安装完成后同一
		// Runner 原子取得唯一 finalizer 执行权。
		if !scheduler.finalizerInstalled {
			return nil, false, false, false
		}
		scheduler.state = schedulerFinalizing
		scheduler.finalizerStarted = true
		return nil, false, true, false
	}
	if scheduler.state == schedulerFinalizing && scheduler.finalizerDone {
		return nil, false, false, true
	}
	return nil, false, false, false
}

// restoreTaskLocked 把执行槽从当前替补 Runner 转交给等待恢复的原任务 goroutine。
func (scheduler *serviceScheduler) restoreTaskLocked(task *serviceTask) *serviceTask {
	if scheduler.running != 0 || scheduler.runningTask != nil || scheduler.awaiting <= 0 {
		panicInvariant("service: Await 恢复时执行槽或计数不一致")
	}

	// Deadline 覆盖整个外部等待和恢复排队阶段。若时间已经越过有效 Deadline，但 M8
	// 控制协程尚未来得及消费到期 ID，本交接点直接提交同一超时结果。
	if task.awaitDeadlineID != timerwheel.InvalidDeadlineID {
		scheduler.deadlineQueue.Cancel(task.awaitDeadlineID)
		delete(scheduler.deadlineBindings, task.awaitDeadlineID)
		task.awaitDeadlineID = timerwheel.InvalidDeadlineID
	}
	if !task.awaitDeadlineAt.IsZero() &&
		!time.Now().Before(task.awaitDeadlineAt) &&
		context.Cause(task.awaitContext) == nil {
		task.awaitCancel(context.DeadlineExceeded)
	}

	task.state = taskRunning
	scheduler.awaiting--
	scheduler.running = 1
	scheduler.runningTask = task
	return task
}

// executeTask 调用一个普通根任务，并在最外层恢复业务 panic。
func (scheduler *serviceScheduler) executeTask(task *serviceTask) {
	panicValue, panicStack, panicked := callTask(task)
	discoveryTask := task.kind == taskKindDiscovery
	var timerID TimerID
	var timerKind string
	if task.kind == taskKindTimer && task.timer != nil {
		// Timer 对象可能在完成锁事务中回池，日志字段必须在清零前复制为值。
		timerID = task.timer.id
		timerKind = task.timer.kind.String()
	}

	// 无论正常返回或 panic，根任务都只在同一锁事务中完成一次并归还执行槽。
	scheduler.mu.Lock()
	if task.state != taskRunning ||
		scheduler.runningTask != task ||
		scheduler.running != 1 {
		scheduler.mu.Unlock()
		panicInvariant("service: 根任务完成时执行槽状态不一致")
	}
	// 一次性 Timer 无论正常返回还是 panic 都已完成。先解除 Scheduler 索引和统计，
	// 等 Task 清除 timer 指针后再归还对象池，避免池对象仍被活动 Task 引用。
	var taskTimer *businessTimer
	var finishedTimer *businessTimer
	if task.kind == taskKindTimer {
		taskTimer = task.timer
		finishedTimer = scheduler.finishTimerTaskLocked(task, panicked)
	}
	task.state = taskCompleted
	scheduler.runningTask = nil
	scheduler.running = 0
	scheduler.accepted--
	scheduler.completedTotal++
	if discoveryTask {
		if !scheduler.discoveryRunning {
			scheduler.mu.Unlock()
			panicInvariant("service: 发现任务完成时运行标记不存在")
		}
		scheduler.discoveryRunning = false
	}
	if panicked {
		scheduler.panicTotal++
	}

	// 完成时先使不可复用令牌失效，再完整清零并回池。旧 Context 仍保留父 Context，
	// 可以安全查询 Done/Err/Value，但其原子 Task 指针为 nil。
	if taskTimer != nil {
		taskTimer.taskReferences--
		if taskTimer.taskReferences < 0 {
			scheduler.mu.Unlock()
			panicInvariant("service: Timer Task 引用计数下溢")
		}
	}
	scheduler.releaseTaskLocked(task)
	if finishedTimer != nil {
		scheduler.releaseTerminalTimerIfUnreferencedLocked(finishedTimer)
	}
	// 根任务释放 Accepted 额度后，最早到期的 Timer 优先取得该额度并追加到 Ready。
	// 当前 goroutine 会继续 Runner 循环，因此不需要额外唤醒。
	scheduler.promoteDueTimersLocked()
	// Timer 到期项继续保持已有优先级；剩余额度再用于同步最新发现状态。
	scheduler.promoteDiscoveryLocked()
	scheduler.mu.Unlock()

	if panicked {
		// panic 属于关键错误，使用 ErrorStack 的可靠写出路径；panic_stack 字段保存真正的
		// 业务原始位置，日志自身的 stack 则标出框架恢复边界，二者只形成一条日志。
		if timerID != InvalidTimerID {
			scheduler.logger.ErrorStack(
				"service timer callback panic",
				originlog.String("panic", fmt.Sprint(panicValue)),
				originlog.String("panic_stack", string(panicStack)),
				originlog.Uint64("timer_id", uint64(timerID)),
				originlog.String("timer_kind", timerKind),
			)
		} else if timerID == InvalidTimerID {
			scheduler.logger.ErrorStack(
				"service task panic",
				originlog.String("panic", fmt.Sprint(panicValue)),
				originlog.String("panic_stack", string(panicStack)),
			)
		}
	}
}

// callTask 在业务代码最外层捕获 panic，并优先保留 Await 等待函数的原始堆栈。
func callTask(task *serviceTask) (
	panicValue any,
	panicStack []byte,
	panicked bool,
) {
	defer func() {
		if value := recover(); value != nil {
			if _, internal := value.(schedulerInvariantError); internal {
				panic(value)
			}
			panicValue = value
			panicked = true
			if len(task.restoredPanicStack) > 0 {
				panicStack = task.restoredPanicStack
			} else {
				panicStack = debug.Stack()
			}
		}
	}()

	switch task.kind {
	case taskKindDispatch:
		task.fn(task.context)
	case taskKindDiscovery:
		task.fn(task.context)
	case taskKindEvent:
		task.eventOwner.executeAsyncEvent(task.context, task.eventSlot, task.event)
	case taskKindTimer:
		callTimerTask(task)
	default:
		panicInvariant("service: 未知 Task 类型")
	}
	return nil, nil, false
}

// acquireTaskLocked 从 Scheduler 私有池取得完全清零的 Task，并创建唯一 Context 令牌。
//
// 调用方必须持有 scheduler.mu，使获取、初始化和 Ready 发布形成一个事务。
func (scheduler *serviceScheduler) acquireTaskLocked(
	fn func(context.Context),
) *serviceTask {
	item := scheduler.taskPool.Get()
	var task *serviceTask
	if item == nil {
		task = &serviceTask{}
	} else {
		task = item.(*serviceTask)
		if !task.pooled {
			panicInvariant("service: Task 对象池包含未清零对象")
		}
		*task = serviceTask{}
	}

	// token 每个根任务只创建一次且永不回池。原子指针允许旧 Context 与任务完成并发查询，
	// 并通过 token 地址校验阻止池化 Task 的 ABA。
	token := &taskContext{
		Context:   scheduler.lifetimeContext,
		scheduler: scheduler,
	}
	task.scheduler = scheduler
	task.context = token
	task.fn = fn
	task.kind = taskKindDispatch
	task.state = taskReady
	token.task.Store(task)
	return task
}

// releaseTaskLocked 使 Context 令牌失效、清空全部引用并归还 Task。
//
// 调用方必须持有 scheduler.mu，确保已经读取旧 token 的并发误用也只能在锁后看到清零或
// 新 token，不会与 Reset 产生数据竞争。
func (scheduler *serviceScheduler) releaseTaskLocked(task *serviceTask) {
	if task == nil || task.state != taskCompleted || task.context == nil {
		panicInvariant("service: 非法 Task 回池")
	}
	token := task.context
	if token.scheduler != scheduler || token.task.Load() != task {
		panicInvariant("service: Task Context 令牌与对象池归属不一致")
	}
	token.task.Store(nil)
	*task = serviceTask{}
	task.pooled = true
	scheduler.taskPool.Put(task)
}

// executeFinalizer 在最后一个 Service Runner 中执行唯一 OnStop 回调。
func (scheduler *serviceScheduler) executeFinalizer() {
	scheduler.mu.Lock()
	target := scheduler.finalizerTarget
	parent := scheduler.finalizerContext
	finalizer := scheduler.finalizer
	scheduler.mu.Unlock()

	var result error
	if finalizer != nil {
		// Finalizing 状态已经在线性化锁内发布；现在建立只能由当前回调使用的 Await 令牌。
		finalizerContext, finish, err := prepareFinalizerContext(target, parent)
		if err != nil {
			result = err
		} else {
			scheduler.mu.Lock()
			scheduler.finalizerFinish = finish
			scheduler.mu.Unlock()
			result = scheduler.callFinalizer(finalizer, finalizerContext)
			finish()
		}
	}

	// 回调完成后只发布结果并唤醒同一个 Runner 循环；下一轮确认 Finalizing 已完成后退出。
	scheduler.mu.Lock()
	scheduler.finalizerResult = result
	scheduler.finalizerDone = true
	scheduler.mu.Unlock()
	scheduler.notifyRunner()
}

// callFinalizer 把 OnStop panic 限制在当前 Service，并恰好记录一次完整堆栈。
func (scheduler *serviceScheduler) callFinalizer(
	finalizer func(context.Context) error,
	ctx context.Context,
) (result error) {
	defer func() {
		value := recover()
		if value == nil {
			return
		}
		if _, internal := value.(schedulerInvariantError); internal {
			panic(value)
		}
		stack := debug.Stack()
		scheduler.logger.ErrorStack(
			"service OnStop panic",
			originlog.String("panic", fmt.Sprint(value)),
			originlog.String("panic_stack", string(stack)),
		)
		result = errs.NewMessage(errs.CodeInternal, "service OnStop panic")
	}()
	return finalizer(ctx)
}

// stop 串行化重复停止，排空任务、执行 finalizer，再回收全部 Scheduler 资源。
func (scheduler *serviceScheduler) stop(
	ctx context.Context,
	target IService,
	finalizer func(context.Context) error,
) error {
	scheduler.stopMu.Lock()
	defer scheduler.stopMu.Unlock()

	// 如果不变量 panic 遗留了无法重新取得的状态锁，不能再进入任何依赖该锁的正常
	// finalizer 或池化清理路径。取消可证明所有权的 Context/Deadline 后立即返回稳定根因，
	// 让 Node 继续停止其他 Service；未能证明所有权的对象仅保留到进程退出。
	if scheduler.failureLockUnsafe.Load() {
		// 故障发生瞬间也可能只是另一个控制路径短暂持锁。正式 Stop 再做一次非阻塞
		// 取得：成功则回到完整 Failed 清理；仍失败才认定锁所有权确实不可证明。
		if scheduler.mu.TryLock() {
			if scheduler.state != schedulerStopped {
				scheduler.state = schedulerFailed
			}
			scheduler.mu.Unlock()
			scheduler.failureLockUnsafe.Store(false)
		} else {
			cause := scheduler.failureError()
			scheduler.cancelLifetime(cause)
			scheduler.deadlineQueue.Close()
			scheduler.stopResult = cause
			return cause
		}
	}

	// 已经完成的停止保持幂等，并返回首次停止记录的结果。
	scheduler.mu.Lock()
	if scheduler.state == schedulerStopped {
		result := scheduler.stopResult
		scheduler.mu.Unlock()
		return result
	}
	failed := scheduler.state == schedulerFailed
	if scheduler.state == schedulerRunning {
		scheduler.state = schedulerDraining
		// 未进入 Ready 的 Timer 不属于已接受业务任务，立即取消；已经 Ready/Running/Waiting
		// 的回调仍由唯一 Runner 按原 FIFO 排空。
		scheduler.cancelUnreadyTimersLocked()
	} else if scheduler.state == schedulerPrepared {
		// OnStart 失败回滚没有普通 Runner；仍启动一个专用 Service Runner 执行 finalizer，
		// 保持 OnStop 与正常停止相同的执行模型。
		scheduler.state = schedulerDraining
		scheduler.discoveryDirty = false
		scheduler.cancelUnreadyTimersLocked()
	} else if scheduler.state != schedulerDraining && !failed {
		scheduler.mu.Unlock()
		return errs.ErrServiceNotReady
	}
	if failed {
		// Failed 表示某个内部不变量已经无法继续证明。此时不执行可能并发访问损坏状态的
		// 业务 OnStop，只取消全部等待并等真实 Runner goroutine 退出后最大努力回收。
		scheduler.mu.Unlock()
		scheduler.cancelLifetime(scheduler.failureError())
		scheduler.notifyRunner()
	} else {
		if scheduler.finalizerInstalled {
			scheduler.mu.Unlock()
			return errs.ErrInvalidArgument
		}
		scheduler.finalizer = finalizer
		scheduler.finalizerTarget = target
		scheduler.finalizerContext = ctx
		scheduler.finalizerInstalled = true
		startRunner := !scheduler.activated
		if startRunner {
			scheduler.activated = true
		}
		scheduler.mu.Unlock()
		if startRunner {
			scheduler.startRunner()
		}
		scheduler.notifyRunner()
	}

	// 正常排空不提前取消任务；只有总体停止 Context 先结束时才传播取消原因。
	var stopContextError error
	select {
	case <-scheduler.runnerDone:
		// 已接受任务和 finalizer 都已经真实返回。
	case <-ctx.Done():
		cause := context.Cause(ctx)
		if cause == nil {
			cause = ctx.Err()
		}
		scheduler.cancelLifetime(cause)
		stopContextError = stopContextResult(cause)
		scheduler.notifyRunner()
		// Go 无法强杀忽略 Context 的函数，因此仍等待最后任务或 OnStop 真正返回。
		<-scheduler.runnerDone
	}

	// Finalizer 返回后不会再登记 Deadline。关闭 Queue 会唤醒控制协程，
	// 等待它退出后才发布 Stopped，确保全部 goroutine 所有权已回收。
	scheduler.deadlineQueue.Close()
	<-scheduler.deadlineDone
	scheduler.cancelLifetime(errs.ErrServiceStopped)

	scheduler.mu.Lock()
	if failed {
		failureCause := scheduler.failureError()
		scheduler.releaseFailedStorageLocked()
		scheduler.state = schedulerStopped
		scheduler.stopResult = errors.Join(failureCause, stopContextError)
		scheduler.mu.Unlock()
		return errors.Join(failureCause, stopContextError)
	}
	scheduler.cancelAllTimersLocked()
	if scheduler.ready.Len() != 0 || scheduler.accepted != 0 ||
		scheduler.running != 0 || scheduler.awaiting != 0 ||
		scheduler.activeLifecycle != nil ||
		scheduler.activeLifecycleAwait != nil ||
		len(scheduler.deadlineBindings) != 0 ||
		len(scheduler.timers) != 0 ||
		scheduler.duePending.Len() != 0 {
		scheduler.mu.Unlock()
		panicInvariant("service: Scheduler 停止后仍包含运行资源")
	}
	finalizerResult := scheduler.finalizerResult
	scheduler.releaseStoppedStorageLocked()
	scheduler.state = schedulerStopped
	scheduler.stopResult = errors.Join(finalizerResult, stopContextError)
	scheduler.mu.Unlock()
	return errors.Join(finalizerResult, stopContextError)
}

// releaseFailedStorageLocked 在全部真实 Runner 和 Deadline watcher 退出后执行最大努力回收。
//
// Failed 路径不再复用可能依赖精确状态计数的正常池化逻辑；直接断开引用并逐 Timer 归还
// Node 额度，可以避免二次 panic 掩盖首个不变量根因。
func (scheduler *serviceScheduler) releaseFailedStorageLocked() {
	if scheduler.ready != nil {
		for {
			task, ok := scheduler.ready.Dequeue()
			if !ok {
				break
			}
			if task != nil && task.context != nil {
				task.context.task.Store(nil)
			}
		}
	}
	if scheduler.duePending != nil {
		scheduler.duePending.Clear()
	}
	for _, timer := range scheduler.timers {
		if timer != nil && timer.id != InvalidTimerID {
			// 每个仍在 Map 中的 Timer 尚未经过唯一正常回池点，因此仍持有一个 Node 额度。
			scheduler.runtime.ReleaseTimerSlot()
		}
	}
	clear(scheduler.timers)
	clear(scheduler.deadlineBindings)
	if scheduler.activeLifecycle != nil {
		scheduler.activeLifecycle.active.Store(false)
	}
	scheduler.activeLifecycle = nil
	scheduler.activeLifecycleAwait = nil
	scheduler.runningTask = nil
	scheduler.accepted = 0
	scheduler.running = 0
	scheduler.awaiting = 0
	scheduler.timerStats.Active = 0
	scheduler.timerStats.Scheduled = 0
	scheduler.timerStats.DuePending = 0
	scheduler.timerStats.Ready = 0
	scheduler.timerStats.Running = 0
	scheduler.timerStats.Paused = 0
	scheduler.releaseStoppedStorageLocked()
}

// releaseStoppedStorageLocked 释放一次性 Scheduler 在峰值运行期间增长出的容器和私有对象池。
//
// Scheduler 指针会留在 Service 中用于幂等 Stop 和只读统计，因此仅 Clear 队列仍会长期保留
// 底层数组。该函数只能在全部任务、Timer 和 Deadline 均已排空后调用；累计统计与停止结果
// 继续保留，业务存储则断开引用交给 GC。
func (scheduler *serviceScheduler) releaseStoppedStorageLocked() {
	if scheduler.ready.Len() != 0 ||
		len(scheduler.deadlineBindings) != 0 ||
		len(scheduler.timers) != 0 ||
		scheduler.duePending.Len() != 0 {
		panicInvariant("service: 释放 Stopped Scheduler 存储前资源未排空")
	}

	scheduler.ready = nil
	scheduler.discoveryRun = nil
	scheduler.discoveryDirty = false
	scheduler.discoveryQueued = false
	scheduler.discoveryRunning = false
	scheduler.finalizer = nil
	scheduler.finalizerTarget = nil
	scheduler.finalizerContext = nil
	scheduler.finalizerFinish = nil
	scheduler.deadlineQueue = nil
	scheduler.deadlineBindings = nil
	scheduler.timerEngine = nil
	scheduler.timers = nil
	scheduler.duePending = nil

	// sync.Pool 不提供显式清空方法；替换为零值可立即断开当前 Scheduler 对池中对象的引用。
	scheduler.taskPool = sync.Pool{}
	scheduler.timerPool = sync.Pool{}
}

const timerQuotaLogInterval = time.Second

// timerQuotaLogDecisionLocked 聚合高峰期重复的 Node Timer 额度耗尽日志。
//
// 该函数只更新常数大小状态；调用方必须在解锁后执行实际日志写入。
func (scheduler *serviceScheduler) timerQuotaLogDecisionLocked(
	now time.Time,
) (logNow bool, suppressed uint64) {
	windowElapsed := scheduler.timerQuotaLastLog.IsZero() ||
		!now.After(scheduler.timerQuotaLastLog) ||
		now.Sub(scheduler.timerQuotaLastLog) >= timerQuotaLogInterval
	if windowElapsed {
		suppressed = scheduler.timerQuotaSuppressed
		scheduler.timerQuotaSuppressed = 0
		scheduler.timerQuotaLastLog = now
		return true, suppressed
	}

	scheduler.timerQuotaSuppressed++
	return false, 0
}

// stopContextResult 把停止 Context 的原因映射到稳定的框架错误。
func stopContextResult(cause error) error {
	if errors.Is(cause, context.DeadlineExceeded) {
		return errs.Wrap(errs.CodeGracefulShutdownTimeout, cause)
	}
	return errs.Wrap(errs.CodeCanceled, cause)
}

// notifyRunner 非阻塞合并 Ready 或停止状态变化。
func (scheduler *serviceScheduler) notifyRunner() {
	select {
	case scheduler.wake <- struct{}{}:
	default:
	}
}

// statsSnapshot 在一次短锁中复制瞬时值和累计值，保证字段来自同一时刻。
func (scheduler *serviceScheduler) statsSnapshot() ExecutionStats {
	// 无法证明 Scheduler 锁所有权时返回零值比让诊断查询永久阻塞更安全。首个故障根因
	// 仍可通过 Service.Failure 和 Node.ServiceStatus 查询。
	if scheduler.failureLockUnsafe.Load() {
		return ExecutionStats{}
	}
	scheduler.mu.Lock()
	defer scheduler.mu.Unlock()
	return ExecutionStats{
		Accepted:              scheduler.accepted,
		Ready:                 scheduler.ready.Len(),
		Running:               scheduler.running,
		Awaiting:              scheduler.awaiting,
		AcceptedHighWatermark: scheduler.acceptedHighWatermark,
		DispatchedTotal:       scheduler.dispatchedTotal,
		CompletedTotal:        scheduler.completedTotal,
		RejectedTotal:         scheduler.rejectedTotal,
		AwaitTotal:            scheduler.awaitTotal,
		AwaitCanceledTotal:    scheduler.awaitCanceledTotal,
		AwaitTimeoutTotal:     scheduler.awaitTimeoutTotal,
		PanicTotal:            scheduler.panicTotal,
	}
}

// watchDeadlines 及时消费 M8 到期 ID；它只取消 Context，永远不执行业务函数。
func (scheduler *serviceScheduler) watchDeadlines() {
	defer func() {
		if value := recover(); value != nil {
			scheduler.failInvariant(value, debug.Stack())
		}
		close(scheduler.deadlineDone)
	}()

	ids := make([]timerwheel.DeadlineID, 0, deadlineDrainBatch)
	for range scheduler.deadlineQueue.ExpiredSignal() {
		for {
			// 复用批次 Slice，避免稳定到期负载为每个通知分配临时数组。
			ids = ids[:0]
			drained, err := scheduler.deadlineQueue.DrainExpired(ids, deadlineDrainBatch)
			if err != nil {
				if errors.Is(err, timerwheel.ErrDeadlineQueueClosed) {
					return
				}
				panicInvariant(fmt.Sprintf("service: DrainExpired 失败: %v", err))
			}
			if len(drained) == 0 {
				break
			}

			// 每批最多收集固定数量的 CancelFunc，锁外执行 Context 取消，避免任意
			// Context 子树唤醒成本落在 Scheduler 状态锁内。Timer 到期只建立普通
			// Service Task，也绝不在 watcher 中直接执行用户回调。
			var cancels [deadlineDrainBatch]context.CancelCauseFunc
			cancelCount := 0
			wakeRunner := false
			scheduler.mu.Lock()
			for _, id := range drained {
				binding, exists := scheduler.deadlineBindings[id]
				if !exists {
					continue
				}
				switch binding.kind {
				case deadlineBindingAwait:
					task := binding.task
					if task == nil ||
						binding.token == nil ||
						binding.token.task.Load() != task ||
						task.context != binding.token ||
						task.awaitGeneration != binding.generation ||
						task.awaitDeadlineID != id ||
						(task.state != taskWaiting && task.state != taskRecoveryReady) {
						delete(scheduler.deadlineBindings, id)
						continue
					}

					delete(scheduler.deadlineBindings, id)
					task.awaitDeadlineID = timerwheel.InvalidDeadlineID
					cancels[cancelCount] = task.awaitCancel
					cancelCount++
				case deadlineBindingLifecycleAwait:
					lifecycle := binding.lifecycle
					if lifecycle == nil ||
						lifecycle.token == nil ||
						!lifecycle.token.active.Load() ||
						scheduler.activeLifecycleAwait != lifecycle ||
						lifecycle.generation != binding.generation ||
						lifecycle.deadlineID != id {
						delete(scheduler.deadlineBindings, id)
						continue
					}

					// 先在线性化锁内记录到期，再于锁外取消 Context。即使等待函数恰好
					// 自行返回，清理路径也能稳定把本次结果判定为 DeadlineExceeded。
					delete(scheduler.deadlineBindings, id)
					lifecycle.deadlineID = timerwheel.InvalidDeadlineID
					lifecycle.expired = true
					cancels[cancelCount] = lifecycle.cancel
					cancelCount++
				case deadlineBindingTimer:
					timer := binding.timer
					if timer == nil ||
						timer.generation != binding.generation ||
						timer.deadlineID != id ||
						timer.state != businessTimerScheduled ||
						scheduler.timers[timer.id] != timer {
						delete(scheduler.deadlineBindings, id)
						continue
					}

					delete(scheduler.deadlineBindings, id)
					timer.deadlineID = timerwheel.InvalidDeadlineID
					if scheduler.enqueueExpiredTimerLocked(timer) {
						wakeRunner = true
					}
				default:
					delete(scheduler.deadlineBindings, id)
					panicInvariant("service: Deadline 绑定类型无效")
				}
			}
			scheduler.mu.Unlock()

			for index := 0; index < cancelCount; index++ {
				cancels[index](context.DeadlineExceeded)
			}
			if wakeRunner {
				scheduler.notifyRunner()
			}
			if len(drained) < deadlineDrainBatch {
				break
			}
		}
	}
}
