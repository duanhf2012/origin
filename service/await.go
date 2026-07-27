package service

import (
	"context"
	"errors"
	"runtime/debug"
	"time"

	"github.com/duanhf2012/origin/v3/errs"
	"github.com/duanhf2012/origin/v3/internal/timerwheel"
)

// await 释放当前 Task 的执行权、执行等待函数，并在恢复原 goroutine 后返回结果。
func (scheduler *serviceScheduler) await(
	ctx context.Context,
	fn func(context.Context) error,
) error {
	// 私有令牌必须来自当前 Scheduler 正在执行的根任务。Background Context、另一个
	// Service 的 Context 或已经完成的旧 Context 都不能释放当前执行槽。
	token, ok := ctx.Value(taskContextKey{}).(*taskContext)
	if !ok || token == nil || token.scheduler != scheduler {
		return errs.ErrInvalidArgument
	}
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

	// 调用方显式 Deadline 优先；没有显式值时使用已经冻结的 Service/Node 默认值。
	now := time.Now()
	deadlineAt, hasDeadline := ctx.Deadline()
	if !hasDeadline {
		deadlineAt = now.Add(scheduler.config.DefaultAwaitTimeout)
	}
	delay := time.Until(deadlineAt)
	if delay <= 0 {
		scheduler.mu.Unlock()
		return errs.ErrDeadlineExceeded
	}
	waitContext, cancelWait := context.WithCancelCause(ctx)
	deadlineID, err := scheduler.deadlineQueue.ScheduleAfter(delay)
	if err != nil {
		scheduler.mu.Unlock()
		cancelWait(err)
		return errs.Wrap(errs.CodeInternal, err)
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
	scheduler.deadlineBindings[deadlineID] = deadlineBinding{
		task:       task,
		token:      token,
		generation: generation,
	}

	task.state = taskWaiting
	scheduler.runningTask = nil
	scheduler.running = 0
	scheduler.awaiting++
	scheduler.awaitTotal++
	scheduler.mu.Unlock()

	// 当前 goroutine 从此不再持有 Service 执行权。替补 Runner 负责处理 Ready 任务；
	// 等待函数则直接在原 goroutine 执行，不额外创建“执行 fn”的辅助 goroutine。
	go scheduler.run()
	waitError, panicValue, panicStack, panicked := callAwaitFunction(fn, waitContext)

	// 外部等待完成只把同一个根任务转为恢复项，不增加 Accepted，也不经过容量拒绝。
	scheduler.mu.Lock()
	if task.awaitGeneration != generation || task.state != taskWaiting {
		scheduler.mu.Unlock()
		panic("service: Await 完成时 Task 状态或代次不一致")
	}
	task.awaitError = waitError
	task.awaitPanic = panicValue
	task.awaitPanicStack = panicStack
	task.state = taskRecoveryReady
	if !scheduler.ready.Enqueue(task) {
		scheduler.mu.Unlock()
		panic("service: 已接受 Await 恢复项无法进入 Ready 环形队列")
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
		panic("service: Await 恢复后执行槽状态不一致")
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
