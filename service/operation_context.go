package service

import (
	"context"
	"sync"
	"time"

	"github.com/duanhf2012/origin/v3/errs"
	"github.com/duanhf2012/origin/v3/internal/timerwheel"
)

// operationContextKey 是框架组件识别已经冻结调用预算的私有键。
//
// 业务包无法构造该键；它只防止 RPC 的 Prepare、编码和等待阶段重复建立默认 Deadline，
// 不承担 Service Task 执行身份证明。
type operationContextKey struct{}

// operationContext 保存一次公开等待或 RPC 调用唯一的控制 Context 和绝对 Deadline。
//
// 显式 Deadline 继续由调用方 Context 管理；没有显式 Deadline 时只在所属 Service 的
// DeadlineQueue 中登记一次。Service 生命周期通过 context.AfterFunc 合并，不为每次
// 调用创建常驻辅助 goroutine。
type operationContext struct {
	context.Context
	scheduler *serviceScheduler
	deadline  time.Time
	cancel    context.CancelCauseFunc
	stopHard  func() bool

	deadlineID timerwheel.DeadlineID
	previous   *operationContext
	next       *operationContext

	closeOnce sync.Once
	closed    bool
	released  bool
}

// Deadline 返回本次公开调用已经冻结的唯一绝对截止时间。
func (operation *operationContext) Deadline() (time.Time, bool) {
	if operation == nil || operation.deadline.IsZero() {
		return time.Time{}, false
	}
	return operation.deadline, true
}

// Value 优先公开私有调用标记，其余业务 Value 仍由调用方 Context 提供。
func (operation *operationContext) Value(key any) any {
	if _, matched := key.(operationContextKey); matched {
		return operation
	}
	return operation.Context.Value(key)
}

// PrepareOperationContext 为有响应的 RPC Call 和 Async 冻结一次调用预算。
//
// nil、Background 和 TODO 都表示没有显式 Deadline，依次使用 Service、Node 和内置默认
// 超时。返回的 finish 必须在同步调用结束、提交失败或异步回调完成后执行，并可安全重复。
func PrepareOperationContext(
	target IService,
	control context.Context,
) (context.Context, func(), error) {
	return prepareOperationContext(target, control, false)
}

// PrepareAwaitContext 为通用 Await 和生成的 AwaitXxx 冻结预算并校验当前执行环境。
//
// 执行身份来自 owner Scheduler 当前活动的普通 Task 或生命周期帧；control 只负责取消、
// Deadline 和普通 Value。普通 goroutine 应使用生成的 CallXxx，而不是调用 AwaitXxx。
func PrepareAwaitContext(
	target IService,
	control context.Context,
) (context.Context, func(), error) {
	return prepareOperationContext(target, control, true)
}

// prepareOperationContext 在一个短锁事务中确认生命周期、计算 Deadline 并登记默认计时项。
func prepareOperationContext(
	target IService,
	control context.Context,
	requireExecution bool,
) (context.Context, func(), error) {
	// Context 的 nil 只表示没有调用方控制语义；进入任何标准库或下游组件前先规范化。
	if target == nil || isNilService(target) {
		return nil, nil, errs.ErrInvalidArgument
	}
	if control == nil {
		control = context.Background()
	}
	base := target.baseService()
	if base == nil {
		return nil, nil, errs.ErrInvalidArgument
	}
	scheduler := base.scheduler.Load()
	if scheduler == nil {
		return nil, nil, errs.ErrServiceNotReady
	}

	// Await 必须在 owner 当前执行帧中创建；Call/Async 还要遵守停止后的新工作边界。
	scheduler.mu.Lock()
	if scheduler.state == schedulerFailed {
		scheduler.mu.Unlock()
		return nil, nil, errs.ErrServiceFailed
	}
	if scheduler.state == schedulerStopped {
		scheduler.mu.Unlock()
		return nil, nil, errs.ErrServiceStopped
	}
	hard := scheduler.lifetimeContext
	if requireExecution {
		switch {
		case scheduler.runningTask != nil &&
			scheduler.running == 1 &&
			scheduler.runningTask.state == taskRunning:
			// 普通 Task 的硬边界是整个 Service 生命周期；业务调用链只由 control 决定。
		case scheduler.activeLifecycle != nil &&
			scheduler.activeLifecycle.active.Load():
			// OnStart/OnStop 还必须服从当前生命周期父 Context，例如启动回滚或停止期限。
			hard = scheduler.activeLifecycle
		default:
			scheduler.mu.Unlock()
			return nil, nil, errs.NewMessage(
				errs.CodeInvalidArgument,
				"Await 需要活动的 Origin Service 执行环境；普通 goroutine 请使用 CallXxx",
			)
		}
	} else {
		switch scheduler.state {
		case schedulerPrepared, schedulerRunning:
			// Starting 期间 Call 可以直接等待；Async 是否能预留回调仍由 FIFO 准入决定。
		case schedulerDraining:
			// 停止阶段只允许当前已接受 Task 派生 Async 延续，拒绝外部 goroutine 以
			// Call/Async 增加新的排空工作。nil/Background 在该边界不携带延续证明。
			token, _ := control.Value(taskContextKey{}).(*taskContext)
			var task *serviceTask
			if token != nil && token.scheduler == scheduler {
				task = token.task.Load()
			}
			if task == nil || token.task.Load() != task ||
				task.context != token || task.scheduler != scheduler ||
				scheduler.runningTask != task || scheduler.running != 1 ||
				task.state != taskRunning {
				scheduler.mu.Unlock()
				return nil, nil, errs.ErrServiceStopping
			}
		case schedulerFinalizing:
			scheduler.mu.Unlock()
			return nil, nil, errs.ErrServiceStopped
		default:
			scheduler.mu.Unlock()
			return nil, nil, errs.ErrServiceNotReady
		}
	}
	if cause := context.Cause(hard); cause != nil {
		scheduler.mu.Unlock()
		return nil, nil, awaitContextError(cause)
	}
	// Await 的执行环境错误优先于调用方 Context 终态：一个已经失效的旧 Context 不能在
	// 普通 goroutine 中重新获得 Service 执行权。Call/Async 等普通调用仍直接返回取消。
	if cause := context.Cause(control); cause != nil {
		scheduler.mu.Unlock()
		return nil, nil, awaitContextError(cause)
	}

	// 显式 Deadline 完全覆盖默认值；没有显式值时，每次公开调用重新取得一份默认预算。
	now := time.Now()
	deadline, explicitDeadline := control.Deadline()
	managedDefault := !explicitDeadline
	if managedDefault {
		deadline = now.Add(scheduler.config.DefaultAwaitTimeout)
	}
	if hardDeadline, exists := hard.Deadline(); exists && hardDeadline.Before(deadline) {
		deadline = hardDeadline
		managedDefault = false
	}
	delay := time.Until(deadline)
	if delay <= 0 {
		scheduler.mu.Unlock()
		return nil, nil, errs.ErrDeadlineExceeded
	}

	// control 管理业务取消，hard 的回调只补充不可绕过的框架生命周期取消。
	derived, cancel := context.WithCancelCause(control)
	operation := &operationContext{
		Context:    derived,
		scheduler:  scheduler,
		deadline:   deadline,
		cancel:     cancel,
		deadlineID: timerwheel.InvalidDeadlineID,
	}
	operation.stopHard = context.AfterFunc(hard, func() {
		cause := context.Cause(hard)
		if cause == nil {
			cause = hard.Err()
		}
		operation.cancel(cause)
	})

	// 默认预算只登记一条 Deadline；显式或硬生命周期 Deadline 已有自己的物理计时器。
	if managedDefault {
		deadlineID, err := scheduler.deadlineQueue.ScheduleAfter(delay)
		if err != nil {
			scheduler.mu.Unlock()
			operation.stopHard()
			operation.cancel(err)
			return nil, nil, errs.Wrap(errs.CodeInternal, err)
		}
		operation.deadlineID = deadlineID
		scheduler.deadlineBindings[deadlineID] = deadlineBinding{
			kind:      deadlineBindingOperation,
			operation: operation,
		}
	}
	// 侵入式链表让 Failed 清理能够找到显式 Deadline 操作，同时避免为每次调用增加
	// map entry 分配。正常完成和故障隔离都在 scheduler.mu 下 O(1) 摘除。
	operation.next = scheduler.operationHead
	if scheduler.operationHead != nil {
		scheduler.operationHead.previous = operation
	}
	scheduler.operationHead = operation
	scheduler.operations++
	scheduler.mu.Unlock()

	return operation, operation.close, nil
}

// close 取消尚未到期的默认计时项，并断开父 Context 回调和全部调用引用。
func (operation *operationContext) close() {
	if operation == nil {
		return
	}
	operation.closeOnce.Do(func() {
		scheduler := operation.scheduler
		if scheduler != nil {
			scheduler.mu.Lock()
			if !operation.released {
				operation.closed = true
				operation.released = true
				if operation.deadlineID != timerwheel.InvalidDeadlineID &&
					scheduler.deadlineQueue != nil {
					scheduler.deadlineQueue.Cancel(operation.deadlineID)
					delete(scheduler.deadlineBindings, operation.deadlineID)
					operation.deadlineID = timerwheel.InvalidDeadlineID
				}
				if operation.previous != nil {
					operation.previous.next = operation.next
				} else if scheduler.operationHead == operation {
					scheduler.operationHead = operation.next
				} else {
					scheduler.mu.Unlock()
					panicInvariant("service: 公开调用预算链表头不一致")
				}
				if operation.next != nil {
					operation.next.previous = operation.previous
				}
				operation.previous = nil
				operation.next = nil
				if scheduler.operations <= 0 {
					scheduler.mu.Unlock()
					panicInvariant("service: 公开调用预算计数下溢")
				}
				scheduler.operations--
			}
			scheduler.mu.Unlock()
			scheduler.notifyRunner()
		}
		if operation.stopHard != nil {
			operation.stopHard()
		}
		operation.cancel(nil)
	})
}

// preparedOperationContext 返回 ctx 携带且属于 scheduler 的框架调用 Context。
func preparedOperationContext(
	ctx context.Context,
	scheduler *serviceScheduler,
) *operationContext {
	if ctx == nil || scheduler == nil {
		return nil
	}
	operation, _ := ctx.Value(operationContextKey{}).(*operationContext)
	if operation == nil || operation.scheduler != scheduler {
		return nil
	}
	return operation
}
