package service

import (
	"context"
	"time"

	"github.com/duanhf2012/origin/v3/errs"
)

// completionContext 把异步调用方的取消语义与回调任务的 Service 执行令牌组合起来。
//
// Deadline、Done 和 Err 来自调用方 Context，保证显式 Go Timer 不被重复创建；Value
// 优先读取当前根任务，使 Await 能验证执行权，其余业务值再委托原调用方 Context。
type completionContext struct {
	execution context.Context
	caller    context.Context
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

// Value 仅用执行 Context 提供 Origin 私有令牌，其他值保持调用方语义。
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

	return owner.DispatchAsync(func(taskCtx context.Context) {
		// 组合 Context 只活到本回调任务结束，不交给其他根任务或长期保存。
		merged := &completionContext{
			execution: taskCtx,
			caller:    ctx,
		}
		err := owner.Await(merged, wait)
		callback(taskCtx, err)
	})
}
