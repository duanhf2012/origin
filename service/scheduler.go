package service

import (
	"context"
	"errors"
	"fmt"
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
	schedulerStopped
)

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
	scheduler *serviceScheduler
	context   *taskContext
	fn        func(context.Context)
	kind      serviceTaskKind
	timer     *businessTimer
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

	// restoredPanicStack 只在 Await 重新抛出 panic 到根任务边界期间临时保存原始堆栈。
	restoredPanicStack []byte
}

// deadlineBindingKind 区分 Service 共用 DeadlineQueue 中的 Await 和业务 Timer。
type deadlineBindingKind uint8

const (
	deadlineBindingAwait deadlineBindingKind = iota
	deadlineBindingStartAwait
	deadlineBindingTimer
)

// startAwait 保存 OnStart 顺序等待在 M8 DeadlineQueue 中的一次活动绑定。
type startAwait struct {
	token      *startContext
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
	start      *startAwait
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
	// startGeneration 和 startActive 只验证 Node 当前唯一 OnStart 生命周期令牌。
	startGeneration uint64
	startActive     bool
	// startAwaitGeneration 和 activeStartAwait 防止旧默认 Deadline 命中新一轮生命周期等待。
	startAwaitGeneration uint64
	activeStartAwait     *startAwait
	config               SchedulerConfig
	logger               originlog.Logger
	runtime              Runtime

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

	// timerPanicLastLog 和 timerPanicSuppressed 只聚合周期 Timer 的重复 panic 日志。
	// 状态受 mu 保护；达到 99 次自动取消时会强制越过时间窗口输出最终诊断。
	timerPanicLastLog    time.Time
	timerPanicSuppressed uint64
	// timerQuotaLastLog 和 timerQuotaSuppressed 聚合同一 Service 的 Timer 额度拒绝诊断。
	// RejectedTotal 仍逐次精确累计，日志只用于低频提示。
	timerQuotaLastLog    time.Time
	timerQuotaSuppressed uint64

	wake       chan struct{}
	runnerDone chan struct{}

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
	go scheduler.run()
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

	scheduler.mu.Lock()
	notifyRunner := false
	switch scheduler.state {
	case schedulerPrepared, schedulerRunning:
		// Draining 是全部任务和 Timer 创建入口在同一锁内检查的线性化边界。
		scheduler.state = schedulerDraining
		scheduler.discoveryDirty = false
		scheduler.cancelUnreadyTimersLocked()
		notifyRunner = scheduler.activated
	case schedulerDraining, schedulerStopped:
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
	return scheduler.stop(ctx)
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

	// 已经到期的 Timer 先于本次新投递取得空闲额度，但不会越过原先已经位于 Ready
	// 队列中的任务；提升只是追加到同一 FIFO 尾部。
	promotedTimer := scheduler.promoteDueTimersLocked()
	if scheduler.accepted >= scheduler.config.MaxTasks {
		scheduler.rejectedTotal++
		scheduler.mu.Unlock()
		if promotedTimer {
			scheduler.notifyRunner()
		}
		return errs.ErrServiceQueueFull
	}

	// Task 主对象来自 Scheduler 私有池；不可复用的 Context 令牌负责隔离旧 Context，
	// 同时避免 context.WithValue 的额外包装层。
	task := scheduler.acquireTaskLocked(fn)
	if !scheduler.ready.Enqueue(task) {
		scheduler.mu.Unlock()
		panic("service: Ready 环形队列在 Accepted 未达到硬上限时拒绝入队")
	}

	scheduler.accepted++
	scheduler.dispatchedTotal++
	if scheduler.accepted > scheduler.acceptedHighWatermark {
		scheduler.acceptedHighWatermark = scheduler.accepted
	}
	scheduler.mu.Unlock()

	// 唤醒信号只描述“可能有新工作”，容量 1 足以合并并发投递。
	scheduler.notifyRunner()
	return nil
}

// run 是当前唯一活动业务 Runner；普通任务返回后由同一 goroutine 继续取后续工作。
func (scheduler *serviceScheduler) run() {
	for {
		// 每轮只在短锁内取得一个任务或判断排空完成，用户代码始终在锁外运行。
		task, recovery, stop := scheduler.nextTask()
		if stop {
			close(scheduler.runnerDone)
			return
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

// nextTask 从统一 FIFO 中取得普通任务或恢复项，并提交唯一执行槽状态。
func (scheduler *serviceScheduler) nextTask() (
	task *serviceTask,
	recovery bool,
	stop bool,
) {
	scheduler.mu.Lock()
	defer scheduler.mu.Unlock()

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
				panic("service: 普通任务取出时执行槽仍被占用")
			}
			// Timer Task 只有真正取得唯一执行槽时才从 Ready 进入 Running；
			// 普通 Dispatch Task 不需要额外资源状态转换。
			if next.kind == taskKindTimer {
				scheduler.startTimerTaskLocked(next)
			}
			if next.kind == taskKindDiscovery {
				if !scheduler.discoveryQueued || scheduler.discoveryRunning {
					panic("service: 发现任务排队状态不一致")
				}
				scheduler.discoveryQueued = false
				scheduler.discoveryRunning = true
				// 当前 Task 将同步开始时看到的最新版本；执行期间的新更新会再次置脏。
				scheduler.discoveryDirty = false
			}
			next.state = taskRunning
			scheduler.running = 1
			scheduler.runningTask = next
			return next, false, false
		case taskRecoveryReady:
			return scheduler.restoreTaskLocked(next), true, false
		default:
			panic("service: Ready 环形队列包含非法 Task 状态")
		}
	}

	// 只有进入 Draining 且已接受任务归零，最后一个活动 Runner 才能退出。
	if scheduler.state == schedulerDraining && scheduler.accepted == 0 {
		if scheduler.running != 0 || scheduler.runningTask != nil || scheduler.awaiting != 0 {
			panic("service: Scheduler 排空计数不一致")
		}
		return nil, false, true
	}
	return nil, false, false
}

// restoreTaskLocked 把执行槽从当前替补 Runner 转交给等待恢复的原任务 goroutine。
func (scheduler *serviceScheduler) restoreTaskLocked(task *serviceTask) *serviceTask {
	if scheduler.running != 0 || scheduler.runningTask != nil || scheduler.awaiting <= 0 {
		panic("service: Await 恢复时执行槽或计数不一致")
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
	logTimerPanic := true
	var suppressedTimerPanics uint64
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
		panic("service: 根任务完成时执行槽状态不一致")
	}
	// 一次性 Timer 无论正常返回还是 panic 都已完成。先解除 Scheduler 索引和统计，
	// 等 Task 清除 timer 指针后再归还对象池，避免池对象仍被活动 Task 引用。
	var taskTimer *businessTimer
	var finishedTimer *businessTimer
	if task.kind == taskKindTimer {
		taskTimer = task.timer
		finishedTimer = scheduler.finishTimerTaskLocked(task, panicked)
		if panicked && taskTimer.kind != businessTimerAfter {
			// 周期 Timer 可能持续 panic。普通重复项只按一秒窗口聚合；第 99 次导致
			// 自动取消时强制写出，确保最终原因不会因限流丢失。
			panicLimitReached := taskTimer.panicCount >= maxConsecutiveTimerPanics &&
				taskTimer.state == businessTimerCanceled
			logTimerPanic, suppressedTimerPanics =
				scheduler.timerPanicLogDecisionLocked(
					time.Now(),
					panicLimitReached,
				)
		}
	}
	task.state = taskCompleted
	scheduler.runningTask = nil
	scheduler.running = 0
	scheduler.accepted--
	scheduler.completedTotal++
	if discoveryTask {
		if !scheduler.discoveryRunning {
			scheduler.mu.Unlock()
			panic("service: 发现任务完成时运行标记不存在")
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
			panic("service: Timer Task 引用计数下溢")
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
		if timerID != InvalidTimerID && logTimerPanic {
			scheduler.logger.ErrorStack(
				"service timer callback panic",
				originlog.String("panic", fmt.Sprint(panicValue)),
				originlog.String("panic_stack", string(panicStack)),
				originlog.Uint64("timer_id", uint64(timerID)),
				originlog.String("timer_kind", timerKind),
				originlog.Uint64(
					"suppressed_timer_panics",
					suppressedTimerPanics,
				),
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
	case taskKindTimer:
		callTimerTask(task)
	default:
		panic("service: 未知 Task 类型")
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
			panic("service: Task 对象池包含未清零对象")
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
		panic("service: 非法 Task 回池")
	}
	token := task.context
	if token.scheduler != scheduler || token.task.Load() != task {
		panic("service: Task Context 令牌与对象池归属不一致")
	}
	token.task.Store(nil)
	*task = serviceTask{}
	task.pooled = true
	scheduler.taskPool.Put(task)
}

// stop 串行化重复停止，取消到期等待后仍等待真实任务返回，避免 OnStop 并发访问状态。
func (scheduler *serviceScheduler) stop(ctx context.Context) error {
	scheduler.stopMu.Lock()
	defer scheduler.stopMu.Unlock()

	// 已经完成的停止保持幂等，并返回首次停止记录的结果。
	scheduler.mu.Lock()
	if scheduler.state == schedulerStopped {
		result := scheduler.stopResult
		scheduler.mu.Unlock()
		return result
	}
	if scheduler.state == schedulerPrepared ||
		(scheduler.state == schedulerDraining && !scheduler.activated) {
		// Prepared 阶段没有普通 Runner，但 Deadline watcher 已经服务过 OnStart 默认超时。
		// 当前停止者关闭 Queue 并等待 watcher 退出，再回收全部冷路径资源。
		scheduler.state = schedulerDraining
		scheduler.discoveryDirty = false
		scheduler.ready.Clear()
		scheduler.cancelUnreadyTimersLocked()
		scheduler.mu.Unlock()
		scheduler.deadlineQueue.Close()
		<-scheduler.deadlineDone
		scheduler.cancelLifetime(errs.ErrServiceStopped)
		scheduler.mu.Lock()
		if scheduler.awaiting != 0 || scheduler.activeStartAwait != nil ||
			len(scheduler.deadlineBindings) != 0 {
			scheduler.mu.Unlock()
			panic("service: Prepared Scheduler 停止时仍包含生命周期 Await")
		}
		scheduler.cancelAllTimersLocked()
		scheduler.releaseStoppedStorageLocked()
		scheduler.state = schedulerStopped
		scheduler.stopResult = nil
		scheduler.mu.Unlock()
		return nil
	}
	if scheduler.state == schedulerRunning {
		scheduler.state = schedulerDraining
		// 未进入 Ready 的 Timer 不属于已接受业务任务，立即取消；已经 Ready/Running/Waiting
		// 的回调仍由唯一 Runner 按原 FIFO 排空。
		scheduler.cancelUnreadyTimersLocked()
	} else if scheduler.state != schedulerDraining {
		scheduler.mu.Unlock()
		return errs.ErrServiceNotReady
	}
	scheduler.mu.Unlock()
	scheduler.notifyRunner()

	// 正常排空不提前取消任务；只有调用方停止 Context 先结束时才传播取消原因。
	var stopContextError error
	select {
	case <-scheduler.runnerDone:
		// 所有已接受任务已经自然完成。
	case <-ctx.Done():
		cause := context.Cause(ctx)
		if cause == nil {
			cause = ctx.Err()
		}
		scheduler.cancelLifetime(cause)
		stopContextError = stopContextResult(cause)
		scheduler.notifyRunner()
		// Go 无法强杀忽略 Context 的函数，因此仍等待最后一个任务真正返回。
		<-scheduler.runnerDone
	}

	// Runner 已退出且 Accepted 为零后，不会再登记 Deadline。关闭 Queue 会唤醒控制协程，
	// 等待它退出后才发布 Stopped，确保全部 goroutine 所有权已回收。
	scheduler.deadlineQueue.Close()
	<-scheduler.deadlineDone
	scheduler.cancelLifetime(errs.ErrServiceStopped)

	scheduler.mu.Lock()
	scheduler.cancelAllTimersLocked()
	if scheduler.ready.Len() != 0 || scheduler.accepted != 0 ||
		scheduler.running != 0 || scheduler.awaiting != 0 ||
		len(scheduler.deadlineBindings) != 0 ||
		len(scheduler.timers) != 0 ||
		scheduler.duePending.Len() != 0 {
		scheduler.mu.Unlock()
		panic("service: Scheduler 停止后仍包含运行资源")
	}
	scheduler.releaseStoppedStorageLocked()
	scheduler.state = schedulerStopped
	scheduler.stopResult = stopContextError
	scheduler.mu.Unlock()
	return stopContextError
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
		panic("service: 释放 Stopped Scheduler 存储前资源未排空")
	}

	scheduler.ready = nil
	scheduler.discoveryRun = nil
	scheduler.discoveryDirty = false
	scheduler.discoveryQueued = false
	scheduler.discoveryRunning = false
	scheduler.deadlineQueue = nil
	scheduler.deadlineBindings = nil
	scheduler.timerEngine = nil
	scheduler.timers = nil
	scheduler.duePending = nil

	// sync.Pool 不提供显式清空方法；替换为零值可立即断开当前 Scheduler 对池中对象的引用。
	scheduler.taskPool = sync.Pool{}
	scheduler.timerPool = sync.Pool{}
}

const timerPanicLogInterval = time.Second

// timerPanicLogDecisionLocked 决定一个周期 Timer panic 是否立即写日志。
//
// 每个 Service 在一个窗口内只可靠写出第一条重复 panic；其余条目累计到下一条日志的
// suppressed_timer_panics 字段。force 用于第 99 次自动取消，保证最终日志不受窗口限制。
func (scheduler *serviceScheduler) timerPanicLogDecisionLocked(
	now time.Time,
	force bool,
) (logNow bool, suppressed uint64) {
	windowElapsed := scheduler.timerPanicLastLog.IsZero() ||
		!now.After(scheduler.timerPanicLastLog) ||
		now.Sub(scheduler.timerPanicLastLog) >= timerPanicLogInterval
	if force || windowElapsed {
		suppressed = scheduler.timerPanicSuppressed
		scheduler.timerPanicSuppressed = 0
		scheduler.timerPanicLastLog = now
		return true, suppressed
	}

	scheduler.timerPanicSuppressed++
	return false, 0
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
	defer close(scheduler.deadlineDone)

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
				panic(fmt.Sprintf("service: DrainExpired 失败: %v", err))
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
				case deadlineBindingStartAwait:
					start := binding.start
					if start == nil ||
						start.token == nil ||
						!start.token.active.Load() ||
						scheduler.activeStartAwait != start ||
						start.generation != binding.generation ||
						start.deadlineID != id {
						delete(scheduler.deadlineBindings, id)
						continue
					}

					// 先在线性化锁内记录到期，再于锁外取消 Context。即使等待函数恰好
					// 自行返回，清理路径也能稳定把本次结果判定为 DeadlineExceeded。
					delete(scheduler.deadlineBindings, id)
					start.deadlineID = timerwheel.InvalidDeadlineID
					start.expired = true
					cancels[cancelCount] = start.cancel
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
					panic("service: Deadline 绑定类型无效")
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
