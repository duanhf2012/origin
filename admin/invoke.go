package admin

import (
	"context"

	"github.com/duanhf2012/origin/v3/errs"
	"github.com/duanhf2012/origin/v3/service"
)

// serviceInvokeResult 是 Service 任务向同步管理调用交付的唯一终态。
type serviceInvokeResult struct {
	response Response
	err      error
}

// InvokeService 把管理 Endpoint 投递到目标 Service 的有界 FIFO，并等待 Handler 终态。
func InvokeService(
	callerCtx context.Context,
	target service.IService,
	endpoint Endpoint,
	request Request,
) (Response, error) {
	// 已经终止的调用不能占用 Service 队列，更不能执行 Handler 副作用。
	if err := callerCtx.Err(); err != nil {
		return Response{}, errs.New(errs.CodeOf(err))
	}

	// 容量一保证调用方离开后，已接受任务仍可完成唯一一次非阻塞结果交付。
	resultChannel := make(chan serviceInvokeResult, 1)
	err := target.DispatchAsync(func(taskCtx context.Context) {
		// FIFO 排队期间取消的调用只完成队列项，不再进入可能产生副作用的 Handler。
		if callerErr := callerCtx.Err(); callerErr != nil {
			resultChannel <- serviceInvokeResult{err: errs.New(errs.CodeOf(callerErr))}
			return
		}

		// Handler Context 派生自真实 Task Context，保留 Await 所需的 Service 执行身份；
		// 客户端取消只通过 AfterFunc 合并，完成后必须解除回调引用。
		handlerCtx, cancel := context.WithCancel(taskCtx)
		stopCancelPropagation := context.AfterFunc(callerCtx, cancel)
		if callerErr := callerCtx.Err(); callerErr != nil {
			stopCancelPropagation()
			cancel()
			resultChannel <- serviceInvokeResult{err: errs.New(errs.CodeOf(callerErr))}
			return
		}
		response, invokeErr := endpoint.Invoke(handlerCtx, request)
		stopCancelPropagation()
		cancel()
		resultChannel <- serviceInvokeResult{response: response, err: invokeErr}
	})
	if err != nil {
		return Response{}, err
	}

	// 客户端取消只停止调用方等待；已接受任务仍在 Service 槽内完成，不隐式回滚业务提交。
	select {
	case result := <-resultChannel:
		return result.response, result.err
	case <-callerCtx.Done():
		return Response{}, errs.New(errs.CodeOf(callerCtx.Err()))
	}
}
