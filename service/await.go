package service

import (
	"context"
	"errors"
	"runtime/debug"
	"time"

	"github.com/duanhf2012/origin/v3/errs"
)

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

// await 释放当前 Task 的执行权、执行等待函数，并在恢复原 goroutine 后返回结果。
func (scheduler *serviceScheduler) await(
	ctx context.Context,
	fn func(context.Context) error,
) error {
	// 执行身份从 owner 当前执行帧捕获；业务 Context 不再承担 Task 令牌传递职责。
	if ctx == nil || fn == nil || preparedOperationContext(ctx, scheduler) == nil {
		return errs.ErrInvalidArgument
	}
	scheduler.mu.Lock()
	var taskToken *taskContext
	var lifecycleToken *lifecycleContext
	if scheduler.runningTask != nil &&
		scheduler.running == 1 &&
		scheduler.runningTask.state == taskRunning {
		taskToken = scheduler.runningTask.context
	} else if scheduler.activeLifecycle != nil &&
		scheduler.activeLifecycle.active.Load() {
		lifecycleToken = scheduler.activeLifecycle
	}
	scheduler.mu.Unlock()

	if taskToken != nil {
		return scheduler.awaitTask(ctx, fn, taskToken)
	}
	if lifecycleToken != nil {
		return scheduler.awaitLifecycle(ctx, fn, lifecycleToken)
	}
	return errs.NewMessage(
		errs.CodeInvalidArgument,
		"Await 需要活动的 Origin Service 执行环境；普通 goroutine 请使用 CallXxx",
	)
}

// awaitTask 释放普通根 Task 的执行权，并在 FIFO 恢复后继续原调用栈。
func (scheduler *serviceScheduler) awaitTask(
	ctx context.Context,
	fn func(context.Context) error,
	token *taskContext,
) error {
	// 执行令牌已经从 owner 当前帧捕获；调用方 Context 只携带取消、Deadline 与 Value。
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

	// operationContext 已在公开调用入口冻结唯一 Deadline；本阶段不得重新应用默认值。
	deadlineAt, exists := ctx.Deadline()
	if !exists || time.Until(deadlineAt) <= 0 {
		scheduler.mu.Unlock()
		return errs.ErrDeadlineExceeded
	}

	// 每次 Await 增加代次并建立全新交接 Channel；同一 Task 连续 Await 时，旧 Deadline 和
	// 旧信号不能命中新一轮等待。
	task.awaitGeneration++
	generation := task.awaitGeneration
	task.awaitContext = ctx
	task.awaitDeadlineAt = deadlineAt
	task.awaitHandoff = make(chan struct{}, 1)
	task.awaitError = nil
	task.awaitExpired = false
	task.awaitPanic = nil
	task.awaitPanicStack = nil

	task.state = taskWaiting
	scheduler.runningTask = nil
	scheduler.running = 0
	scheduler.awaiting++
	scheduler.awaitTotal++
	scheduler.mu.Unlock()

	// 当前 goroutine 从此不再持有 Service 执行权。替补 Runner 负责处理 Ready 任务；
	// 等待函数则直接在原 goroutine 执行，不额外创建“执行 fn”的辅助 goroutine。
	scheduler.startRunner()
	waitError, panicValue, panicStack, panicked := callAwaitFunction(fn, ctx)

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

	// Deadline 覆盖恢复排队阶段；operationContext 由最外层公开调用统一清理。
	cause := context.Cause(task.awaitContext)
	finalError := task.awaitError
	if task.awaitExpired || errors.Is(cause, context.DeadlineExceeded) {
		finalError = errs.ErrDeadlineExceeded
	} else if cause != nil {
		finalError = awaitContextError(cause)
	}

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
	task.awaitDeadlineAt = time.Time{}
	task.awaitHandoff = nil
	task.awaitError = nil
	task.awaitExpired = false
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

	// operationContext 已统一冻结 Deadline，生命周期阶段不能重新得到一份默认预算。
	deadlineAt, exists := ctx.Deadline()
	if !exists || time.Until(deadlineAt) <= 0 {
		scheduler.mu.Unlock()
		return errs.ErrDeadlineExceeded
	}
	binding := &lifecycleAwait{
		token: token,
	}
	scheduler.activeLifecycleAwait = binding
	scheduler.awaiting++
	scheduler.awaitTotal++
	scheduler.mu.Unlock()

	// 等待函数就在原生命周期 goroutine 执行；Finalizer 不创建替补 Runner。
	waitError, panicValue, _, panicked := callAwaitFunction(fn, ctx)

	// 清理当前生命周期 Await 占用；调用级 Deadline 由 operationContext 的唯一 owner 清理。
	scheduler.mu.Lock()
	if scheduler.activeLifecycleAwait != binding {
		scheduler.mu.Unlock()
		panicInvariant("service: 生命周期 Await 活动绑定不一致")
	}
	scheduler.activeLifecycleAwait = nil
	scheduler.awaiting--
	scheduler.mu.Unlock()

	// Deadline 还覆盖等待函数返回前的完整阶段；到期 watcher 尚未调度时也按绝对时间判定。
	cause := context.Cause(ctx)
	finalError := waitError
	if !panicked {
		// 与普通 Task Await 保持一致：panic 是本次调用的最终控制流，不再把同一时刻
		// 观察到的取消或超时重复计入 Await 返回错误统计。
		if !time.Now().Before(deadlineAt) || errors.Is(cause, context.DeadlineExceeded) {
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
