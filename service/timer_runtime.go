package service

import (
	"math"
	"time"

	"github.com/duanhf2012/origin/v3/errs"
	"github.com/duanhf2012/origin/v3/internal/timerwheel"
	originlog "github.com/duanhf2012/origin/v3/log"
)

// businessTimerKind 表示业务 Timer 的重复策略。
type businessTimerKind uint8

const (
	businessTimerAfter businessTimerKind = iota
	businessTimerTicker
	businessTimerCron
)

// String 返回用于诊断日志的稳定短名称。
func (kind businessTimerKind) String() string {
	switch kind {
	case businessTimerAfter:
		return "after"
	case businessTimerTicker:
		return "ticker"
	case businessTimerCron:
		return "cron"
	default:
		return "unknown"
	}
}

// businessTimerState 表示一个 Timer 当前唯一归属的位置。
type businessTimerState uint8

const (
	businessTimerScheduled businessTimerState = iota
	businessTimerDuePending
	businessTimerReady
	businessTimerRunning
	businessTimerPaused
	businessTimerCompleted
	businessTimerCanceled
)

// businessTimer 保存框架内部业务 Timer 的全部可变状态。
//
// 对象只允许在 scheduler.mu 下读写；完成或取消后完整清零并进入当前 Service 私有对象池。
// 业务侧只持有不会复用的 TimerID，因此对象池不会形成外部引用或 ABA 问题。
type businessTimer struct {
	id         TimerID
	kind       businessTimerKind
	state      businessTimerState
	generation uint64

	deadlineID timerwheel.DeadlineID
	callback   TimerFunc
	fireAt     time.Time
	dueAt      time.Time

	// 后续 Ticker、Cron、暂停和恢复步骤复用这些字段，AfterFunc 不读取它们。
	interval  time.Duration
	remaining time.Duration
	schedule  cronSchedule
	location  *time.Location

	pauseAfterRun  bool
	cancelAfterRun bool
	panicCount     uint8

	// dueReferences 和 taskReferences 记录环形队列槽位仍持有的内部指针。Timer 只有进入
	// 终态且两个计数都归零后才能回池，避免 Pause/Resume 留下的墓碑发生 ABA。
	dueReferences  int
	taskReferences int

	// pooled 只在对象已经完整清零且位于 sync.Pool 时为 true。
	pooled bool
}

// dueTimerEntry 固定 Timer 进入 DuePending 时的代次。
//
// Pause 后立即 Resume 可能让同一个 Timer 对象再次到期；只有同时保存 generation，旧队列
// 条目才不会误把新一代 Timer 提前提升。该值结构直接存放在环形队列槽位中，不产生单独分配。
type dueTimerEntry struct {
	timer      *businessTimer
	generation uint64
}

// AfterFunc 创建一个只触发一次的业务 Timer。
//
// 创建成功只表示 Timer 已被当前 Node 接收。即使 delay 为零，回调也必须经过时间轮和
// Service Ready 队列，在后续调度轮次执行，不会在当前调用栈同步调用。
func (service *Service) AfterFunc(delay time.Duration, fn TimerFunc) TimerID {
	// 负时长和空回调没有明确业务语义，直接返回统一无效 ID。
	if service == nil || delay < 0 || fn == nil {
		return InvalidTimerID
	}
	if service.timerCreationError() != nil {
		return InvalidTimerID
	}
	scheduler := service.scheduler.Load()
	if scheduler == nil {
		return InvalidTimerID
	}
	return scheduler.createAfterTimer(delay, fn)
}

// NewTicker 创建固定节拍的周期业务 Timer。
//
// 同一 Ticker 的当前回调完整返回前不会安排下一次 Deadline；忙碌期间错过的周期只合并
// 计数，不并发执行、不补执行历史次数。
func (service *Service) NewTicker(interval time.Duration, fn TimerFunc) TimerID {
	if service == nil || interval <= 0 || fn == nil {
		return InvalidTimerID
	}
	if service.timerCreationError() != nil {
		return InvalidTimerID
	}
	scheduler := service.scheduler.Load()
	if scheduler == nil {
		return InvalidTimerID
	}
	return scheduler.createTicker(interval, fn)
}

// timerCreationError 允许 OnStart 的 Starting 阶段预登记 Timer，并在 Node 一旦发布
// Stopping 时立即关闭准入，不留下 Runtime 状态与 Scheduler 状态之间的竞争窗口。
func (service *Service) timerCreationError() error {
	switch service.State() {
	case StateStarting, StateRunning:
		return nil
	case StateStopping:
		return errs.ErrServiceStopping
	case StateStopped, StateFailed:
		return errs.ErrServiceStopped
	default:
		return errs.ErrServiceNotReady
	}
}

// PauseTimer 暂停尚未开始的 Timer，并保存 After/Ticker 的剩余延迟。
func (service *Service) PauseTimer(timerID TimerID) bool {
	if service == nil || timerID == InvalidTimerID {
		return false
	}
	scheduler := service.scheduler.Load()
	if scheduler == nil {
		return false
	}
	return scheduler.pauseTimer(timerID)
}

// ResumeTimer 恢复已经暂停的 Timer。
func (service *Service) ResumeTimer(timerID TimerID) bool {
	if service == nil || timerID == InvalidTimerID {
		return false
	}
	scheduler := service.scheduler.Load()
	if scheduler == nil {
		return false
	}
	return scheduler.resumeTimer(timerID)
}

// CancelTimer 取消 Timer，并把调用方保存的非零 TimerID 无条件清零。
//
// 清零先于内部状态裁决，因此未知、已完成或属于其他 Service 的旧 ID 也不会残留在业务变量
// 中。调用方自身仍需保证该变量不被多个 goroutine 无同步读写。
func (service *Service) CancelTimer(timerID *TimerID) bool {
	if timerID == nil {
		return false
	}
	id := *timerID
	if id == InvalidTimerID {
		return false
	}
	*timerID = InvalidTimerID

	if service == nil {
		return false
	}
	scheduler := service.scheduler.Load()
	if scheduler == nil {
		return false
	}
	return scheduler.cancelTimer(id)
}

// TimerStats 返回当前 Service 业务 Timer 的一致统计快照。
func (service *Service) TimerStats() TimerStats {
	if service == nil {
		return TimerStats{}
	}
	scheduler := service.scheduler.Load()
	if scheduler == nil {
		return TimerStats{}
	}
	return scheduler.timerStatsSnapshot()
}

// pauseTimer 在线性化锁内裁决到期、开始执行和暂停之间的先后顺序。
func (scheduler *serviceScheduler) pauseTimer(timerID TimerID) bool {
	scheduler.mu.Lock()
	defer scheduler.mu.Unlock()

	timer := scheduler.timers[timerID]
	if timer == nil {
		return false
	}
	switch timer.state {
	case businessTimerScheduled:
		// 即使 Deadline 已由时间轮移入到期队列，删除 Binding 和增加代次也能保证
		// watcher 随后只跳过旧 ID，不会创建回调任务。
		scheduler.deadlineQueue.Cancel(timer.deadlineID)
		delete(scheduler.deadlineBindings, timer.deadlineID)
		timer.deadlineID = timerwheel.InvalidDeadlineID
		timer.remaining = timer.fireAt.Sub(scheduler.timerEngine.Now())
		if timer.remaining < 0 {
			timer.remaining = 0
		}
		timer.generation++
		timer.state = businessTimerPaused
		scheduler.timerStats.Scheduled--
		scheduler.timerStats.Paused++
	case businessTimerDuePending:
		timer.remaining = 0
		timer.generation++
		timer.state = businessTimerPaused
		scheduler.timerStats.DuePending--
		scheduler.timerStats.Paused++
	case businessTimerReady:
		// Ready Task 不能从环形队列中间删除。代次变化使其成为墓碑，Runner 出队时
		// 只清理旧 Task；Timer 对象继续由 Paused 状态持有。
		timer.remaining = 0
		timer.generation++
		timer.state = businessTimerPaused
		scheduler.timerStats.Ready--
		scheduler.timerStats.Paused++
	case businessTimerRunning:
		if timer.kind == businessTimerAfter ||
			timer.pauseAfterRun ||
			timer.cancelAfterRun {
			return false
		}
		// 周期回调已经开始时不能强制中断；完成路径会计算下一名义点并转入 Paused。
		timer.pauseAfterRun = true
		scheduler.timerStats.PausedTotal++
		return true
	default:
		// AfterFunc 已经 Running 时不能停止当前回调；重复暂停和终态均返回 false。
		return false
	}
	scheduler.timerStats.PausedTotal++
	return true
}

// resumeTimer 为 Paused After/Ticker 重新登记剩余延迟。
func (scheduler *serviceScheduler) resumeTimer(timerID TimerID) bool {
	scheduler.mu.Lock()
	defer scheduler.mu.Unlock()

	timer := scheduler.timers[timerID]
	if timer == nil {
		return false
	}
	if timer.state == businessTimerRunning &&
		timer.kind != businessTimerAfter &&
		timer.pauseAfterRun &&
		!timer.cancelAfterRun {
		// 当前周期回调尚未完成时，Resume 只撤销完成后暂停标记。
		timer.pauseAfterRun = false
		scheduler.timerStats.ResumedTotal++
		return true
	}
	if timer.state != businessTimerPaused {
		return false
	}
	if scheduler.state != schedulerPrepared && scheduler.state != schedulerRunning {
		return false
	}

	// Cron 暂停期间不补历史触发，恢复时从当前墙上时间重新寻找未来匹配点。
	// After/Ticker 则使用暂停时保存的剩余相对延迟。
	now := scheduler.timerEngine.Now()
	delay := timer.remaining
	fireAt := now.Add(delay)
	if timer.kind == businessTimerCron {
		cronNow := now.In(timer.location)
		fireAt = timer.schedule.Next(cronNow)
		if fireAt.IsZero() || !fireAt.After(cronNow) {
			return false
		}
		delay = fireAt.Sub(cronNow)
	}

	// 先登记新 Deadline，失败时保留原 Paused 状态，调用方可以稍后重试或取消。
	deadlineID, err := scheduler.deadlineQueue.ScheduleAfter(delay)
	if err != nil {
		return false
	}
	timer.generation++
	timer.deadlineID = deadlineID
	timer.fireAt = fireAt
	timer.remaining = 0
	timer.state = businessTimerScheduled
	scheduler.deadlineBindings[deadlineID] = deadlineBinding{
		kind:       deadlineBindingTimer,
		timer:      timer,
		generation: timer.generation,
	}
	scheduler.timerStats.Paused--
	scheduler.timerStats.Scheduled++
	scheduler.timerStats.ResumedTotal++
	return true
}

// cancelTimer 取消仍未开始的 AfterFunc。
func (scheduler *serviceScheduler) cancelTimer(timerID TimerID) bool {
	scheduler.mu.Lock()
	defer scheduler.mu.Unlock()

	timer := scheduler.timers[timerID]
	if timer == nil {
		return false
	}
	switch timer.state {
	case businessTimerScheduled:
		scheduler.deadlineQueue.Cancel(timer.deadlineID)
		delete(scheduler.deadlineBindings, timer.deadlineID)
		timer.deadlineID = timerwheel.InvalidDeadlineID
		timer.generation++
		timer.state = businessTimerCanceled
		scheduler.timerStats.Scheduled--
		scheduler.timerStats.Active--
		scheduler.timerStats.CanceledTotal++
		scheduler.releaseTerminalTimerIfUnreferencedLocked(timer)
	case businessTimerDuePending:
		// DuePending 环形队列不支持中间删除。保留对象直到带旧 generation 的墓碑
		// 出队，避免对象池复用后旧指针命中新 Timer。
		timer.generation++
		timer.state = businessTimerCanceled
		scheduler.timerStats.DuePending--
		scheduler.timerStats.Active--
		scheduler.timerStats.CanceledTotal++
		scheduler.releaseTerminalTimerIfUnreferencedLocked(timer)
	case businessTimerReady:
		// Ready 环形队列不支持中间删除。Timer 保留到旧 Task 出队，避免对象池复用后
		// 旧 Task 指针命中新对象；逻辑 Active 和取消累计在此刻立即提交。
		timer.generation++
		timer.state = businessTimerCanceled
		scheduler.timerStats.Ready--
		scheduler.timerStats.Active--
		scheduler.timerStats.CanceledTotal++
		scheduler.releaseTerminalTimerIfUnreferencedLocked(timer)
	case businessTimerPaused:
		timer.generation++
		timer.state = businessTimerCanceled
		scheduler.timerStats.Paused--
		scheduler.timerStats.Active--
		scheduler.timerStats.CanceledTotal++
		scheduler.releaseTerminalTimerIfUnreferencedLocked(timer)
	case businessTimerRunning:
		if timer.kind == businessTimerAfter || timer.cancelAfterRun {
			return false
		}
		// 当前周期回调继续执行，完成路径只负责清理，不再登记下一次 Deadline。
		timer.cancelAfterRun = true
		timer.pauseAfterRun = false
		scheduler.timerStats.CanceledTotal++
	default:
		// AfterFunc 已经 Running 时不能强杀当前用户回调。
		return false
	}
	return true
}

// createAfterTimer 在一个 Scheduler 锁事务内取得 Node 额度、建立对象并登记 Deadline。
func (scheduler *serviceScheduler) createAfterTimer(
	delay time.Duration,
	fn TimerFunc,
) TimerID {
	return scheduler.createRelativeTimer(
		businessTimerAfter,
		delay,
		0,
		fn,
	)
}

// createTicker 创建首个名义触发点为“当前时刻 + interval”的周期 Timer。
func (scheduler *serviceScheduler) createTicker(
	interval time.Duration,
	fn TimerFunc,
) TimerID {
	return scheduler.createRelativeTimer(
		businessTimerTicker,
		interval,
		interval,
		fn,
	)
}

// createRelativeTimer 在一个 Scheduler 锁事务内取得 Node 额度、建立对象并登记 Deadline。
func (scheduler *serviceScheduler) createRelativeTimer(
	kind businessTimerKind,
	delay time.Duration,
	interval time.Duration,
	fn TimerFunc,
) TimerID {
	scheduler.mu.Lock()
	// 外层 Service 状态只是无锁快速拒绝。停止可能在该快照后完成并释放 timerEngine，
	// 因此必须先在 Scheduler 锁内重新裁决，再读取任何停止时会清空的运行资源。
	if scheduler.state != schedulerPrepared &&
		scheduler.state != schedulerRunning {
		scheduler.timerStats.RejectedTotal++
		scheduler.mu.Unlock()
		return InvalidTimerID
	}
	now := scheduler.timerEngine.Now()
	timerID, quotaRejected := scheduler.createTimerLocked(
		kind,
		now.Add(delay),
		interval,
		nil,
		nil,
		fn,
	)
	logQuota, suppressed := false, uint64(0)
	if quotaRejected {
		logQuota, suppressed = scheduler.timerQuotaLogDecisionLocked(time.Now())
	}
	scheduler.mu.Unlock()

	// 实际日志写入必须位于 Scheduler 锁外；默认异步日志不会让高峰拒绝反向阻塞调度锁。
	if logQuota {
		scheduler.logger.Warn(
			"service timer quota exhausted",
			originlog.Int("timer_limit", scheduler.runtime.TimerLimit()),
			originlog.Uint64("suppressed_timer_rejections", suppressed),
		)
	}
	return timerID
}

// createTimerLocked 完成所有 Timer 类型共用的准入、对象建立和 Deadline 登记。
func (scheduler *serviceScheduler) createTimerLocked(
	kind businessTimerKind,
	fireAt time.Time,
	interval time.Duration,
	schedule cronSchedule,
	location *time.Location,
	fn TimerFunc,
) (timerID TimerID, quotaRejected bool) {
	// Prepared 对应 OnStart 阶段，可以登记但不会执行；Running 阶段正常登记。
	// Draining 以后禁止产生新工作，避免优雅关闭无法收敛。
	if scheduler.state != schedulerPrepared && scheduler.state != schedulerRunning {
		scheduler.timerStats.RejectedTotal++
		return InvalidTimerID, false
	}

	// 额度由 Node 统一管理，全部 Service 共享上限且不需要预分配三百万个槽位。
	timerID, acquired := scheduler.runtime.AcquireTimerSlot()
	if !acquired || timerID == InvalidTimerID {
		scheduler.timerStats.RejectedTotal++
		return InvalidTimerID, true
	}

	// 只有取得额度后才从池中获取对象，失败回滚时始终成对归还 Node 额度。
	timer := scheduler.acquireBusinessTimerLocked()
	timer.id = timerID
	timer.kind = kind
	timer.state = businessTimerScheduled
	timer.generation = 1
	timer.callback = fn
	timer.interval = interval
	timer.schedule = schedule
	timer.location = location
	timer.fireAt = fireAt

	delay := fireAt.Sub(scheduler.timerEngine.Now())
	if delay < 0 {
		delay = 0
	}
	deadlineID, err := scheduler.deadlineQueue.ScheduleAfter(delay)
	if err != nil {
		scheduler.releaseBusinessTimerLocked(timer)
		scheduler.timerStats.RejectedTotal++
		return InvalidTimerID, false
	}
	timer.deadlineID = deadlineID
	scheduler.timers[timerID] = timer
	scheduler.deadlineBindings[deadlineID] = deadlineBinding{
		kind:       deadlineBindingTimer,
		timer:      timer,
		generation: timer.generation,
	}

	// 当前值和累计值在同一锁内提交，TimerStats 因而不会看到半创建状态。
	scheduler.timerStats.Active++
	scheduler.timerStats.Scheduled++
	scheduler.timerStats.CreatedTotal++
	if scheduler.timerStats.Active > scheduler.timerStats.ActiveHighWatermark {
		scheduler.timerStats.ActiveHighWatermark = scheduler.timerStats.Active
	}
	return timerID, false
}

// acquireBusinessTimerLocked 从当前 Service 私有池取得一个完全清零的 Timer 对象。
func (scheduler *serviceScheduler) acquireBusinessTimerLocked() *businessTimer {
	item := scheduler.timerPool.Get()
	if item == nil {
		return &businessTimer{}
	}
	timer := item.(*businessTimer)
	if !timer.pooled {
		panic("service: Timer 对象池包含未清零对象")
	}
	*timer = businessTimer{}
	return timer
}

// releaseBusinessTimerLocked 清除全部业务引用并同时归还 Node 额度。
//
// 调用方必须先从 timers、deadlineBindings 和任务对象中解除该 Timer 的全部内部引用。
func (scheduler *serviceScheduler) releaseBusinessTimerLocked(timer *businessTimer) {
	if timer == nil || timer.id == InvalidTimerID {
		panic("service: 非法 Timer 回池")
	}
	*timer = businessTimer{}
	timer.pooled = true
	scheduler.timerPool.Put(timer)
	scheduler.runtime.ReleaseTimerSlot()
}

// releaseTerminalTimerIfUnreferencedLocked 在终态 Timer 完全脱离内部队列后执行唯一回收。
func (scheduler *serviceScheduler) releaseTerminalTimerIfUnreferencedLocked(
	timer *businessTimer,
) bool {
	if timer == nil ||
		(timer.state != businessTimerCompleted &&
			timer.state != businessTimerCanceled) {
		return false
	}
	if timer.deadlineID != timerwheel.InvalidDeadlineID ||
		timer.dueReferences != 0 ||
		timer.taskReferences != 0 {
		return false
	}
	if scheduler.timers[timer.id] != timer {
		panic("service: 终态 Timer Map 所有权不一致")
	}
	delete(scheduler.timers, timer.id)
	scheduler.releaseBusinessTimerLocked(timer)
	return true
}

// enqueueExpiredTimerLocked 把一个已到期 Timer 转换成普通 Service Task。
//
// M10 后续步骤会在此处补充 DuePending 队列；基础 AfterFunc 路径先确保容量充足时与普通
// DispatchAsync 使用完全相同的执行槽、Await 和 panic 边界。
func (scheduler *serviceScheduler) enqueueExpiredTimerLocked(timer *businessTimer) bool {
	if timer == nil ||
		timer.state != businessTimerScheduled ||
		scheduler.timers[timer.id] != timer {
		return false
	}

	// Cron 使用墙上名义时间。若系统时间在内部 Deadline 等待期间向后调整，旧 Deadline
	// 到期不代表 Cron 已到名义点；重新登记剩余时间，不能提前执行回调。
	if timer.kind == businessTimerCron {
		now := scheduler.timerEngine.Now().In(timer.location)
		if now.Before(timer.fireAt) {
			deadlineID, err := scheduler.deadlineQueue.ScheduleAfter(
				timer.fireAt.Sub(now),
			)
			if err != nil {
				timer.state = businessTimerCanceled
				scheduler.timerStats.Scheduled--
				scheduler.timerStats.Active--
				scheduler.timerStats.CanceledTotal++
				scheduler.releaseTerminalTimerIfUnreferencedLocked(timer)
				return false
			}
			timer.generation++
			timer.deadlineID = deadlineID
			scheduler.deadlineBindings[deadlineID] = deadlineBinding{
				kind:       deadlineBindingTimer,
				timer:      timer,
				generation: timer.generation,
			}
			return false
		}
	}

	timer.state = businessTimerDuePending
	timer.dueAt = scheduler.timerEngine.Now()
	scheduler.timerStats.Scheduled--
	scheduler.timerStats.DuePending++
	if !scheduler.duePending.Enqueue(dueTimerEntry{
		timer:      timer,
		generation: timer.generation,
	}) {
		panic("service: DuePending 数量超过 Node Timer 额度")
	}
	timer.dueReferences++
	return scheduler.promoteDueTimersLocked()
}

// promoteDueTimersLocked 按到期 FIFO 把 Timer 提升到现有 Ready 队列。
//
// 调用方必须持有 scheduler.mu。该方法只在 Running 阶段准入新 Timer Task；Draining
// 阶段的到期项由停止清理统一取消，不能让关闭过程重新产生工作。
func (scheduler *serviceScheduler) promoteDueTimersLocked() bool {
	if scheduler.state != schedulerRunning {
		return false
	}

	promoted := false
	for scheduler.accepted < scheduler.config.MaxTasks {
		entry, ok := scheduler.duePending.Dequeue()
		if !ok {
			break
		}
		timer := entry.timer
		if timer == nil {
			panic("service: DuePending 包含空 Timer")
		}
		timer.dueReferences--
		if timer.dueReferences < 0 {
			panic("service: DuePending Timer 引用计数下溢")
		}

		// 代次或状态不匹配表示 Pause/Resume/Cancel 留下的墓碑。Canceled Timer 已无
		// 其他活动引用，可以在墓碑出队时完成对象池和 Node 额度回收。
		if entry.generation != timer.generation ||
			timer.state != businessTimerDuePending ||
			scheduler.timers[timer.id] != timer {
			if timer.state == businessTimerCanceled &&
				scheduler.timers[timer.id] == timer {
				scheduler.releaseTerminalTimerIfUnreferencedLocked(timer)
			}
			continue
		}

		timer.state = businessTimerReady
		scheduler.timerStats.DuePending--
		scheduler.timerStats.Ready++

		task := scheduler.acquireTaskLocked(nil)
		task.kind = taskKindTimer
		task.timer = timer
		task.timerGeneration = timer.generation
		timer.taskReferences++
		if !scheduler.ready.Enqueue(task) {
			panic("service: Timer Task 在 Accepted 未达到硬上限时拒绝入队")
		}
		scheduler.accepted++
		if scheduler.accepted > scheduler.acceptedHighWatermark {
			scheduler.acceptedHighWatermark = scheduler.accepted
		}
		promoted = true
	}
	return promoted
}

// startTimerTaskLocked 在 Ready 出队时把 Timer 统计切换到 Running。
func (scheduler *serviceScheduler) startTimerTaskLocked(task *serviceTask) {
	timer := task.timer
	if task.kind != taskKindTimer ||
		timer == nil ||
		task.timerGeneration != timer.generation ||
		timer.state != businessTimerReady ||
		scheduler.timers[timer.id] != timer {
		panic("service: Timer Task 与 Timer 状态不一致")
	}
	timer.state = businessTimerRunning
	scheduler.timerStats.Ready--
	scheduler.timerStats.Running++
	scheduler.timerStats.TriggeredTotal++

	// Ready 延迟统一使用 TimerEngine 的 Clock，确保真实运行和确定性测试处于同一时间轴。
	delay := scheduler.timerEngine.Now().Sub(timer.dueAt)
	if delay < 0 {
		delay = 0
	}
	scheduler.timerStats.LastReadyDelay = delay
	if delay > scheduler.timerStats.MaxReadyDelay {
		scheduler.timerStats.MaxReadyDelay = delay
	}
}

// timerTaskCurrentLocked 判断 Ready Task 是否仍代表 Timer 的当前一代触发。
func (scheduler *serviceScheduler) timerTaskCurrentLocked(task *serviceTask) bool {
	if task == nil || task.kind != taskKindTimer || task.timer == nil {
		return false
	}
	timer := task.timer
	return task.timerGeneration == timer.generation &&
		timer.state == businessTimerReady &&
		scheduler.timers[timer.id] == timer
}

// discardStaleTimerTaskLocked 清理 Pause/Resume/Cancel 留在 Ready 队列中的旧 Task。
func (scheduler *serviceScheduler) discardStaleTimerTaskLocked(task *serviceTask) {
	if task == nil ||
		task.kind != taskKindTimer ||
		task.state != taskReady ||
		task.timer == nil {
		panic("service: 非法 Timer Task 墓碑")
	}
	timer := task.timer
	timer.taskReferences--
	if timer.taskReferences < 0 {
		panic("service: Timer Task 引用计数下溢")
	}

	// 墓碑仍然是已经准入的 Service Task，必须在出队时对称减少 Accepted 并归还 Task 池。
	task.state = taskCompleted
	scheduler.accepted--
	scheduler.completedTotal++
	scheduler.releaseTaskLocked(task)

	if timer.state == businessTimerCanceled &&
		scheduler.timers[timer.id] == timer {
		scheduler.releaseTerminalTimerIfUnreferencedLocked(timer)
	}
	// 墓碑释放的 Accepted 额度立即交给最早 DuePending Timer；nextTask 随后继续
	// 从同一 Ready FIFO 取任务。
	scheduler.promoteDueTimersLocked()
}

const maxConsecutiveTimerPanics = 99

// finishTimerTaskLocked 完成当前 Timer 回调，并返回可在 Task 清理后安全回池的对象。
func (scheduler *serviceScheduler) finishTimerTaskLocked(
	task *serviceTask,
	panicked bool,
) *businessTimer {
	timer := task.timer
	if task.kind != taskKindTimer ||
		timer == nil ||
		task.timerGeneration != timer.generation ||
		timer.state != businessTimerRunning ||
		scheduler.timers[timer.id] != timer {
		panic("service: Timer 回调完成状态不一致")
	}

	if panicked {
		timer.panicCount++
		scheduler.timerStats.PanicTotal++
	} else {
		// 任意一次正常返回都会打断连续 panic 序列。
		timer.panicCount = 0
	}

	if timer.kind == businessTimerAfter {
		timer.state = businessTimerCompleted
		scheduler.timerStats.Running--
		scheduler.timerStats.Active--
		scheduler.timerStats.CompletedTotal++
		return timer
	}

	// 周期 Timer 在停止中、业务主动取消或达到连续 panic 上限后都不再产生新工作。
	if scheduler.state != schedulerRunning || timer.cancelAfterRun {
		timer.state = businessTimerCanceled
		scheduler.timerStats.Running--
		scheduler.timerStats.Active--
		if scheduler.state != schedulerRunning && !timer.cancelAfterRun {
			scheduler.timerStats.CanceledTotal++
		}
		return timer
	}
	if timer.panicCount >= maxConsecutiveTimerPanics {
		timer.state = businessTimerCanceled
		scheduler.timerStats.Running--
		scheduler.timerStats.Active--
		scheduler.timerStats.PanicLimitCanceledTotal++
		return timer
	}

	// Ticker 按固定节拍计算下一点；Cron 每轮从当前墙上时间寻找下一匹配点。
	now := scheduler.timerEngine.Now()
	var next time.Time
	var skipped uint64
	var valid bool
	switch timer.kind {
	case businessTimerTicker:
		next, skipped, valid = nextTickerTime(
			timer.fireAt,
			now,
			timer.interval,
		)
	case businessTimerCron:
		cronNow := now.In(timer.location)
		next = timer.schedule.Next(cronNow)
		valid = !next.IsZero() && next.After(cronNow)
	default:
		panic("service: 未知周期 Timer 类型")
	}
	if !valid {
		timer.state = businessTimerCanceled
		scheduler.timerStats.Running--
		scheduler.timerStats.Active--
		scheduler.timerStats.CanceledTotal++
		return timer
	}
	scheduler.timerStats.CoalescedTotal += skipped

	if timer.pauseAfterRun {
		timer.pauseAfterRun = false
		if timer.kind == businessTimerTicker {
			timer.remaining = next.Sub(now)
			if timer.remaining < 0 {
				timer.remaining = 0
			}
		} else {
			// Cron Resume 固定从恢复时刻重算，不保存当前回调完成时的墙上剩余时间。
			timer.remaining = 0
		}
		timer.state = businessTimerPaused
		scheduler.timerStats.Running--
		scheduler.timerStats.Paused++
		return nil
	}

	delay := next.Sub(now)
	if delay < 0 {
		delay = 0
	}
	deadlineID, err := scheduler.deadlineQueue.ScheduleAfter(delay)
	if err != nil {
		timer.state = businessTimerCanceled
		scheduler.timerStats.Running--
		scheduler.timerStats.Active--
		scheduler.timerStats.CanceledTotal++
		return timer
	}
	timer.generation++
	timer.deadlineID = deadlineID
	timer.fireAt = next
	timer.state = businessTimerScheduled
	scheduler.deadlineBindings[deadlineID] = deadlineBinding{
		kind:       deadlineBindingTimer,
		timer:      timer,
		generation: timer.generation,
	}
	scheduler.timerStats.Running--
	scheduler.timerStats.Scheduled++
	return nil
}

// nextTickerTime 返回严格晚于 now 的下一个固定节拍名义点。
func nextTickerTime(
	nominal time.Time,
	now time.Time,
	interval time.Duration,
) (next time.Time, skipped uint64, valid bool) {
	if interval <= 0 {
		return time.Time{}, 0, false
	}
	firstCandidate := nominal.Add(interval)
	if now.Before(firstCandidate) {
		return firstCandidate, 0, true
	}

	// time.Duration 使用 int64 纳秒。先检查步数再相乘，避免极端系统时间跳变导致
	// Duration 乘法回绕并把周期错误安排到过去。
	elapsed := now.Sub(nominal)
	steps := uint64(elapsed/interval) + 1
	maxSteps := uint64(math.MaxInt64 / int64(interval))
	if steps > maxSteps {
		return time.Time{}, 0, false
	}
	return nominal.Add(time.Duration(steps) * interval), steps - 1, true
}

// cancelUnreadyTimersLocked 在 Stop 发布 Draining 时立即取消尚未进入 Ready 的 Timer。
//
// Ready/Running/Waiting 回调已经属于 Accepted 排空集合，必须保留；Scheduled、Paused 和
// DuePending 不属于该集合，应立即停止并释放能够安全释放的 Node 额度。
func (scheduler *serviceScheduler) cancelUnreadyTimersLocked() {
	for _, timer := range scheduler.timers {
		switch timer.state {
		case businessTimerScheduled:
			if timer.deadlineID != timerwheel.InvalidDeadlineID {
				scheduler.deadlineQueue.Cancel(timer.deadlineID)
				delete(scheduler.deadlineBindings, timer.deadlineID)
				timer.deadlineID = timerwheel.InvalidDeadlineID
			}
			timer.generation++
			timer.state = businessTimerCanceled
			scheduler.timerStats.Scheduled--
			scheduler.timerStats.Active--
			scheduler.timerStats.CanceledTotal++
			scheduler.releaseTerminalTimerIfUnreferencedLocked(timer)
		case businessTimerDuePending:
			timer.generation++
			timer.state = businessTimerCanceled
			scheduler.timerStats.DuePending--
			scheduler.timerStats.Active--
			scheduler.timerStats.CanceledTotal++
		case businessTimerPaused:
			timer.generation++
			timer.state = businessTimerCanceled
			scheduler.timerStats.Paused--
			scheduler.timerStats.Active--
			scheduler.timerStats.CanceledTotal++
			scheduler.releaseTerminalTimerIfUnreferencedLocked(timer)
		case businessTimerReady, businessTimerRunning:
			// 已接受回调由 Runner 排空。
		case businessTimerCanceled:
			// 控制接口已经提交统计，只等待墓碑引用清理。
		default:
			panic("service: Stop 遇到非法 Timer 状态")
		}
	}

	// Stop 不再提升 DuePending。逐项出队而不是直接 Clear，以便对称归还内部引用计数，
	// 并在最后一个墓碑离开时安全回收 Timer 对象。
	for {
		entry, ok := scheduler.duePending.Dequeue()
		if !ok {
			break
		}
		timer := entry.timer
		if timer == nil || timer.dueReferences <= 0 {
			panic("service: Stop 清理 DuePending 引用计数不一致")
		}
		timer.dueReferences--
		if timer.state == businessTimerCanceled {
			scheduler.releaseTerminalTimerIfUnreferencedLocked(timer)
		}
	}
}

// cancelAllTimersLocked 是 Runner 排空后的最终一致性检查和冷路径回收。
func (scheduler *serviceScheduler) cancelAllTimersLocked() {
	scheduler.cancelUnreadyTimersLocked()
	if len(scheduler.timers) != 0 {
		panic("service: Scheduler 排空后仍有 Timer 内部引用")
	}
}

// timerStatsSnapshot 在一次短锁内复制全部 Timer 当前值和累计值。
func (scheduler *serviceScheduler) timerStatsSnapshot() TimerStats {
	scheduler.mu.Lock()
	defer scheduler.mu.Unlock()
	return scheduler.timerStats
}

// callTimerTask 是 Timer Task 的统一用户回调入口。
func callTimerTask(task *serviceTask) {
	timer := task.timer
	timer.callback(task.context, timer.id)
}
