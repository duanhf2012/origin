package service

import (
	"context"
	"time"

	"github.com/duanhf2012/origin/v3/errs"
)

// completionContext 把异步调用方的取消语义与回调任务的执行 Value 组合起来。
//
// Deadline、Done 和 Err 来自调用方 Context，保证显式 Go Timer 不被重复创建；Value
// 保留当前根任务的框架私有值，其余业务值和调用预算继续委托原调用方 Context。
type completionContext struct {
	execution context.Context
	caller    context.Context
}

// DispatchAsyncCompletionResult 为当前 Service 预留一个带类型结果的完成任务。wait 在释放 Service 执行槽后
// 运行，callback 在重新取得同一 Service 的串行执行权后运行。
//
// 类型化结果只从 wait 流向 callback；如果 wait 因 Context 已结束而没有执行，callback
// 会收到 T 的零值和对应错误。调度、取消、超时及 panic 语义均委托给下方兼容的包级
// DispatchAsyncCompletion。
//
// Go 1.27 支持具体类型的方法声明自身类型参数，但 IService 接口不能声明这类方法，
// 因此该能力继续使用包级泛型函数。
func DispatchAsyncCompletionResult[T any](
	service IService,
	ctx context.Context,
	wait func(context.Context) (T, error),
	callback func(context.Context, T, error),
) error {
	if service == nil || isNilService(service) || ctx == nil || wait == nil || callback == nil {
		return errs.ErrInvalidArgument
	}

	var result T
	return DispatchAsyncCompletion(
		service,
		ctx,
		func(waitCtx context.Context) error {
			var err error
			result, err = wait(waitCtx)
			return err
		},
		func(callbackCtx context.Context, completionErr error) {
			callback(callbackCtx, result, completionErr)
		},
	)
}

// Deadline 实现 context.Context，并保留调用方唯一显式 Deadline。
func (ctx *completionContext) Deadline() (deadline time.Time, ok bool) {
	return ctx.caller.Deadline()
}

// Done 实现 context.Context，并直接观察调用方取消。
func (ctx *completionContext) Done() <-chan struct{} {
	return ctx.caller.Done()
}

// Err 实现 context.Context，并直接返回调用方取消结果。
func (ctx *completionContext) Err() error {
	return ctx.caller.Err()
}

// Value 仅用执行 Context 提供 Origin 私有任务值，其他值保持调用方语义。
func (ctx *completionContext) Value(key any) any {
	if value := ctx.execution.Value(key); value != nil {
		if _, private := key.(taskContextKey); private {
			return value
		}
	}
	return ctx.caller.Value(key)
}

// DispatchAsyncCompletion 预先占用一个普通 Service 根任务，并在其中等待异步结果。
//
// 该函数是 rpc 生成代码运行底座，不是另一套业务 Post API。成功返回说明回调任务已经
// 进入同一有界 FIFO，因此后续远端完成时不会因为调用方队列突然满而丢失回调。wait 在
// Service.Await 已释放执行权后运行；callback 恢复执行权后运行且严格一次。
func DispatchAsyncCompletion(
	owner IService,
	ctx context.Context,
	wait func(context.Context) error,
	callback func(context.Context, error),
) error {
	// 所有参数必须在占用任务容量前完整有效，失败时绝不留下后台工作。
	if owner == nil || isNilService(owner) || ctx == nil ||
		wait == nil || callback == nil {
		return errs.ErrInvalidArgument
	}

	base := owner.baseService()
	if base == nil {
		return errs.ErrInvalidArgument
	}
	scheduler := base.scheduler.Load()
	if scheduler == nil {
		return errs.ErrServiceNotReady
	}
	continuation := func(taskCtx context.Context) {
		// 组合 Context 只活到本回调任务结束，不交给其他根任务或长期保存。
		merged := &completionContext{
			execution: taskCtx,
			caller:    ctx,
		}
		err := owner.Await(merged, wait)
		callback(taskCtx, err)
	}
	// 在 owner 当前 Task 中提交时保留原有严格校验；普通 goroutine 没有任务令牌，直接
	// 使用同一有界 FIFO 预留完成任务。两条路径都在返回成功前完成容量占用。
	if scheduler.ownsRunningTask(ctx) {
		return scheduler.dispatchContinuation(ctx, continuation)
	}
	return scheduler.dispatch(continuation)
}
