package service

import (
	"context"
	"errors"
	"runtime/debug"
	"time"

	"github.com/duanhf2012/origin/v3/errs"
	"github.com/duanhf2012/origin/v3/internal/timerwheel"
)

// managedDeadlineContext 为 M8 管理的默认超时补充标准 Context Deadline 语义。
//
// 内嵌 Context 只负责取消、Done、Err 和 Value；deadline 由 Service 在进入 Await 时一次
// 计算并冻结。该类型不会创建 Go Runtime Timer，真正到期仍由唯一 M8 DeadlineQueue 驱动。
type managedDeadlineContext struct {
	context.Context
	deadline time.Time
}

// AwaitTimeoutOf 返回 target 当前已经冻结的默认 Await 超时。
//
// 该接口供 RPC 等框架组件在投递异步远端调用时复用同一套超时配置。它不会创建 Timer、
// Context 或 goroutine；业务代码仍应直接使用 Service.Await。
func AwaitTimeoutOf(target IService) (time.Duration, error) {
	if target == nil || isNilService(target) {
		return 0, errs.ErrInvalidArgument
	}
	base := target.baseService()
	if base == nil {
		return 0, errs.ErrInvalidArgument
	}
	scheduler := base.scheduler.Load()
	if scheduler == nil {
		return 0, errs.ErrServiceNotReady
	}
	return scheduler.config.DefaultAwaitTimeout, nil
}

// Deadline 返回 M8 当前唯一管理的绝对截止时间。
func (managed *managedDeadlineContext) Deadline() (time.Time, bool) {
	if managed == nil || managed.deadline.IsZero() {
		return time.Time{}, false
	}
	return managed.deadline, true
}

// await 释放当前 Task 的执行权、执行等待函数，并在恢复原 goroutine 后返回结果。
func (scheduler *serviceScheduler) await(
	ctx context.Context,
	fn func(context.Context) error,
) error {
	// 普通业务 Task 和 OnStart 生命周期使用不同的私有令牌与调度语义。
	if token, ok := ctx.Value(taskContextKey{}).(*taskContext); ok &&
		token != nil &&
		token.scheduler == scheduler {
		return scheduler.awaitTask(ctx, fn, token)
	}
	if token, ok := ctx.Value(lifecycleContextKey{}).(*lifecycleContext); ok &&
		token != nil &&
		token.scheduler == scheduler {
		return scheduler.awaitLifecycle(ctx, fn, token)
	}
	return errs.ErrInvalidArgument
}

// awaitTask 释放普通根 Task 的执行权，并在 FIFO 恢复后继续原调用栈。
func (scheduler *serviceScheduler) awaitTask(
	ctx context.Context,
	fn func(context.Context) error,
	token *taskContext,
) error {
	// 私有令牌必须来自当前 Scheduler 正在执行的根任务。Background Context、另一个
	// Service 的 Context 或已经完成的旧 Context 都不能释放当前执行槽。
	task := token.task.Load()
	if task == nil {
		return errs.ErrInvalidArgument
	}
	if cause := context.Cause(ctx); cause != nil {
		return awaitContextError(cause)
	}

	// 全部状态检查、Deadline 登记和执行槽释放在一个短锁事务中提交。任何失败都发生在
	// 调用 fn 和创建替补 Runner 之前。
	scheduler.mu.Lock()
	if scheduler.state != schedulerRunning && scheduler.state != schedulerDraining {
		scheduler.mu.Unlock()
		return errs.ErrServiceStopped
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
	if scheduler.awaiting >= scheduler.config.MaxAwaitTasks {
		scheduler.rejectedTotal++
		scheduler.mu.Unlock()
		return errs.ErrServiceQueueFull
	}

	// 调用方显式 Deadline 已由父 Context 的 Go Runtime Timer 管理，不能再登记 M8。
	// 没有显式值时才使用冻结的 Service/Node 默认值，并建立唯一一条 M8 Deadline。
	now := time.Now()
	deadlineAt, explicitDeadline := ctx.Deadline()
	if !explicitDeadline {
		deadlineAt = now.Add(scheduler.config.DefaultAwaitTimeout)
	}
	delay := time.Until(deadlineAt)
	if delay <= 0 {
		scheduler.mu.Unlock()
		return errs.ErrDeadlineExceeded
	}

	// 先创建不带新 Timer 的可取消子 Context。默认超时路径再用轻量包装公开 Deadline，
	// 使 Redis、数据库和后续 RPC 等下游仍能读取标准截止时间。
	cancelContext, cancelWait := context.WithCancelCause(ctx)
	var waitContext context.Context = cancelContext
	deadlineID := timerwheel.InvalidDeadlineID
	if !explicitDeadline {
		waitContext = &managedDeadlineContext{
			Context:  cancelContext,
			deadline: deadlineAt,
		}
		var err error
		deadlineID, err = scheduler.deadlineQueue.ScheduleAfter(delay)
		if err != nil {
			scheduler.mu.Unlock()
			cancelWait(err)
			return errs.Wrap(errs.CodeInternal, err)
		}
	}

	// 每次 Await 增加代次并建立全新交接 Channel；同一 Task 连续 Await 时，旧 Deadline 和
	// 旧信号不能命中新一轮等待。
	task.awaitGeneration++
	generation := task.awaitGeneration
	task.awaitContext = waitContext
	task.awaitInput = ctx
	task.awaitCancel = cancelWait
	task.awaitDeadlineID = deadlineID
	task.awaitDeadlineAt = deadlineAt
	task.awaitHandoff = make(chan struct{}, 1)
	task.awaitError = nil
	task.awaitPanic = nil
	task.awaitPanicStack = nil
	if deadlineID != timerwheel.InvalidDeadlineID {
		// 只有默认超时需要 watcher 绑定；显式 Deadline 直接由父 Context 取消 waitContext。
		scheduler.deadlineBindings[deadlineID] = deadlineBinding{
			kind:       deadlineBindingAwait,
			task:       task,
			token:      token,
			generation: generation,
		}
	}

	task.state = taskWaiting
	scheduler.runningTask = nil
	scheduler.running = 0
	scheduler.awaiting++
	scheduler.awaitTotal++
	scheduler.mu.Unlock()

	// 当前 goroutine 从此不再持有 Service 执行权。替补 Runner 负责处理 Ready 任务；
	// 等待函数则直接在原 goroutine 执行，不额外创建“执行 fn”的辅助 goroutine。
	scheduler.startRunner()
	waitError, panicValue, panicStack, panicked := callAwaitFunction(fn, waitContext)

	// 外部等待完成只把同一个根任务转为恢复项，不增加 Accepted，也不经过容量拒绝。
	scheduler.mu.Lock()
	if task.awaitGeneration != generation || task.state != taskWaiting {
		scheduler.mu.Unlock()
		panicInvariant("service: Await 完成时 Task 状态或代次不一致")
	}
	task.awaitError = waitError
	task.awaitPanic = panicValue
	task.awaitPanicStack = panicStack
	task.state = taskRecoveryReady
	if !scheduler.ready.Enqueue(task) {
		scheduler.mu.Unlock()
		panicInvariant("service: 已接受 Await 恢复项无法进入 Ready 环形队列")
	}
	scheduler.mu.Unlock()
	scheduler.notifyRunner()

	// 只有活动替补 Runner 从 FIFO 取到本恢复项、归还执行槽并退出后，原 goroutine 才继续。
	<-task.awaitHandoff

	// Deadline 覆盖恢复排队阶段。先观察等待 Context 和原输入 Context 的最终原因，再调用
	// CancelFunc 释放资源；否则 cancel(nil) 会把成功结果误标为 context.Canceled。
	cause := context.Cause(task.awaitContext)
	if cause == nil {
		cause = context.Cause(task.awaitInput)
	}
	finalError := task.awaitError
	if cause != nil {
		finalError = awaitContextError(cause)
	}
	task.awaitCancel(nil)

	// 恢复后当前 goroutine 已重新持有唯一执行槽，可以在短锁内完成统计和临时引用清理。
	scheduler.mu.Lock()
	if scheduler.runningTask != task || scheduler.running != 1 ||
		task.state != taskRunning || task.awaitGeneration != generation {
		scheduler.mu.Unlock()
		panicInvariant("service: Await 恢复后执行槽状态不一致")
	}
	panicValue = task.awaitPanic
	panicStack = task.awaitPanicStack
	panicked = panicked || panicValue != nil
	if !panicked {
		// panic 是本次 Await 的最终控制流，不再把恢复排队期间同时发生的取消或超时重复
		// 计作 Await 返回错误；根任务边界会单独累计 PanicTotal。
		if errors.Is(finalError, context.DeadlineExceeded) ||
			errs.IsCode(finalError, errs.CodeDeadlineExceeded) {
			scheduler.awaitTimeoutTotal++
		} else if errors.Is(finalError, context.Canceled) ||
			errs.IsCode(finalError, errs.CodeCanceled) {
			scheduler.awaitCanceledTotal++
		}
	}

	task.awaitContext = nil
	task.awaitInput = nil
	task.awaitCancel = nil
	task.awaitDeadlineID = timerwheel.InvalidDeadlineID
	task.awaitDeadlineAt = time.Time{}
	task.awaitHandoff = nil
	task.awaitError = nil
	task.awaitPanic = nil
	task.awaitPanicStack = nil
	scheduler.mu.Unlock()

	// 等待函数 panic 必须在重新获得执行权后展开。根任务边界优先记录原始等待位置堆栈。
	if panicked {
		task.restoredPanicStack = panicStack
		panic(panicValue)
	}
	return finalError
}

// awaitLifecycle 在当前 OnStart/OnStop goroutine 中顺序等待，不让出普通业务执行槽。
func (scheduler *serviceScheduler) awaitLifecycle(
	ctx context.Context,
	fn func(context.Context) error,
	token *lifecycleContext,
) error {
	// Context 必须仍属于当前活动生命周期代次，父 Context 已取消时不进入用户函数。
	if !token.active.Load() {
		return errs.ErrInvalidArgument
	}
	if cause := context.Cause(ctx); cause != nil {
		return awaitContextError(cause)
	}

	// 启动期只接受 Prepared，停止期只接受 Finalizing；二者都保持唯一顺序 Await。
	expectedState := schedulerPrepared
	if token.phase == lifecyclePhaseFinalizer {
		expectedState = schedulerFinalizing
	}
	scheduler.mu.Lock()
	if scheduler.state != expectedState ||
		scheduler.activeLifecycle != token ||
		scheduler.lifecycleGeneration != token.generation ||
		!token.active.Load() {
		scheduler.mu.Unlock()
		return errs.ErrInvalidArgument
	}
	if scheduler.activeLifecycleAwait != nil ||
		scheduler.awaiting >= scheduler.config.MaxAwaitTasks {
		scheduler.rejectedTotal++
		scheduler.mu.Unlock()
		return errs.ErrServiceQueueFull
	}

	// 显式 Deadline 由父 Context 的 Go Timer 管理；没有显式值时只登记一条 M8 Deadline。
	now := time.Now()
	deadlineAt, explicitDeadline := ctx.Deadline()
	if !explicitDeadline {
		deadlineAt = now.Add(scheduler.config.DefaultAwaitTimeout)
	}
	delay := time.Until(deadlineAt)
	if delay <= 0 {
		scheduler.mu.Unlock()
		return errs.ErrDeadlineExceeded
	}
	cancelContext, cancelWait := context.WithCancelCause(ctx)
	var waitContext context.Context = cancelContext
	binding := &lifecycleAwait{
		token:      token,
		cancel:     cancelWait,
		deadlineID: timerwheel.InvalidDeadlineID,
	}
	if !explicitDeadline {
		waitContext = &managedDeadlineContext{
			Context:  cancelContext,
			deadline: deadlineAt,
		}
		deadlineID, err := scheduler.deadlineQueue.ScheduleAfter(delay)
		if err != nil {
			scheduler.mu.Unlock()
			cancelWait(err)
			return errs.Wrap(errs.CodeInternal, err)
		}
		scheduler.lifecycleAwaitGeneration++
		binding.generation = scheduler.lifecycleAwaitGeneration
		binding.deadlineID = deadlineID
		scheduler.deadlineBindings[deadlineID] = deadlineBinding{
			kind:       deadlineBindingLifecycleAwait,
			lifecycle:  binding,
			generation: binding.generation,
		}
	}
	scheduler.activeLifecycleAwait = binding
	scheduler.awaiting++
	scheduler.awaitTotal++
	scheduler.mu.Unlock()

	// 等待函数就在原生命周期 goroutine 执行；Finalizer 不创建替补 Runner。
	waitError, panicValue, _, panicked := callAwaitFunction(fn, waitContext)

	// 解除仍未到期的 M8 绑定，并在锁内冻结最终到期标记和统计。
	scheduler.mu.Lock()
	if scheduler.activeLifecycleAwait != binding {
		scheduler.mu.Unlock()
		panicInvariant("service: 生命周期 Await 活动绑定不一致")
	}
	if binding.deadlineID != timerwheel.InvalidDeadlineID {
		scheduler.deadlineQueue.Cancel(binding.deadlineID)
		delete(scheduler.deadlineBindings, binding.deadlineID)
		binding.deadlineID = timerwheel.InvalidDeadlineID
	}
	scheduler.activeLifecycleAwait = nil
	scheduler.awaiting--
	expired := binding.expired
	scheduler.mu.Unlock()

	// 必须在 cancel(nil) 前读取父/等待 Context 原因，否则成功路径会被主动清理标为取消。
	cause := context.Cause(waitContext)
	if cause == nil {
		cause = context.Cause(ctx)
	}
	cancelWait(nil)
	finalError := waitError
	if expired || errors.Is(cause, context.DeadlineExceeded) {
		finalError = errs.ErrDeadlineExceeded
		scheduler.mu.Lock()
		scheduler.awaitTimeoutTotal++
		scheduler.mu.Unlock()
	} else if cause != nil {
		finalError = awaitContextError(cause)
		scheduler.mu.Lock()
		scheduler.awaitCanceledTotal++
		scheduler.mu.Unlock()
	}

	// 外层生命周期边界负责统一恢复和日志；这里保持等待函数原 panic 控制流。
	if panicked {
		panic(panicValue)
	}
	return finalError
}

// callAwaitFunction 在原任务 goroutine 中调用等待函数，并在没有执行权时截住 panic。
func callAwaitFunction(
	fn func(context.Context) error,
	ctx context.Context,
) (
	waitError error,
	panicValue any,
	panicStack []byte,
	panicked bool,
) {
	defer func() {
		if value := recover(); value != nil {
			panicValue = value
			panicStack = debug.Stack()
			panicked = true
		}
	}()
	waitError = fn(ctx)
	return waitError, nil, nil, false
}

// awaitContextError 把标准 Context 原因映射为稳定 Origin 哨兵，并保留自定义取消原因语义。
func awaitContextError(cause error) error {
	if errors.Is(cause, context.DeadlineExceeded) {
		return errs.ErrDeadlineExceeded
	}
	return errs.ErrCanceled
}
