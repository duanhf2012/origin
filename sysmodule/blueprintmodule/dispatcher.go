package blueprintmodule

import (
	"context"

	blueprint "github.com/duanhf2012/OriginBlueprint/engine/go/blueprint"
)

// serviceDispatcher 让首次执行保留当前 Service 执行权，并把所有恢复片段投递回同一 Service FIFO。
type serviceDispatcher struct{ module *Module }

var _ blueprint.ExecutionDispatcher = (*serviceDispatcher)(nil)

// SubmitInitial 只能由公共 Start/Run 在所属 Service 工作协程调用，因此直接内联执行到同步终态或首次 Yield。
func (dispatcher *serviceDispatcher) SubmitInitial(task func()) error {
	if dispatcher == nil || dispatcher.module == nil || task == nil {
		return blueprint.ErrExecutionRejected
	}
	task()
	return nil
}

// Submit 接受任意外部 goroutine 的 Resume，把后续 VM 片段放回所属 Service 有界任务队列。
func (dispatcher *serviceDispatcher) Submit(task func()) error {
	if dispatcher == nil || dispatcher.module == nil || task == nil {
		return blueprint.ErrExecutionRejected
	}
	return dispatcher.module.DispatchAsync(func(context.Context) { task() })
}
