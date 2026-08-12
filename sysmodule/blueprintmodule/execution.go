package blueprintmodule

import (
	"context"
	"sync"

	blueprint "github.com/duanhf2012/OriginBlueprint/engine/go/blueprint"
	"github.com/duanhf2012/origin/v3/service"
)

// Completion 是 Execution 终态后在所属 Service 工作协程执行的完成回调。
type Completion func(ctx context.Context, returns PortArray, err error)

// Execution 是底层并发安全执行句柄的受限包装。
//
// 包装层不暴露 Graph、VM 或 Dispatcher。Done、State、Result 和 Cancel 可以从任意 goroutine 调用；业务状态
// 修改只应放在 Run 返回后或 OnComplete 回调中。
type Execution struct {
	execution            *blueprint.Execution
	module               *Module
	context              context.Context
	mu                   sync.Mutex
	completionRegistered bool
}

// ID 返回本次执行在当前 Blueprint 引擎内的诊断 ID。
func (execution *Execution) ID() uint64 {
	if execution == nil || execution.execution == nil {
		return 0
	}
	return execution.execution.ID()
}

// Done 返回终态关闭的只读 Channel。
func (execution *Execution) Done() <-chan struct{} {
	if execution == nil || execution.execution == nil {
		return nil
	}
	return execution.execution.Done()
}

// State 返回当前执行状态。
func (execution *Execution) State() ExecutionState {
	if execution == nil || execution.execution == nil {
		return ExecutionFailed
	}
	return execution.execution.State()
}

// IsDone 报告 Execution 是否已经进入 Completed、Canceled 或 Failed 终态。
func (execution *Execution) IsDone() bool {
	return execution != nil && execution.execution != nil && execution.execution.IsDone()
}

// Result 返回终态结果快照；尚未完成时返回 ErrExecutionPending。
func (execution *Execution) Result() (PortArray, error) {
	if execution == nil || execution.execution == nil {
		return nil, ErrInvalidArgument
	}
	return execution.execution.Result()
}

// Cancel 请求取消 Execution；只有首次从非终态接受取消时返回 true。
func (execution *Execution) Cancel() bool {
	return execution != nil && execution.execution != nil && execution.execution.Cancel()
}

// OnComplete 预留一个所属 Service 根任务，等待 Execution 终态后在 Service 工作协程执行 callback。
//
// 推荐紧跟 Start 在同一个 Service 任务中登记。每个 Execution 最多登记一次；成功返回表示有界 FIFO 已为
// 回调预留容量。登记失败不会取消 Execution，调用者仍可读取 Done/Result 或显式 Cancel。
func (execution *Execution) OnComplete(callback Completion) error {
	if execution == nil || execution.execution == nil || execution.module == nil ||
		execution.context == nil || callback == nil {
		return ErrInvalidArgument
	}

	// 单次登记状态只在成功预留后提交；队列拒绝时允许调用者稍后重试。
	execution.mu.Lock()
	if execution.completionRegistered {
		execution.mu.Unlock()
		return invalidArgument("blueprintmodule Execution 只能登记一个 Completion")
	}
	execution.completionRegistered = true
	execution.mu.Unlock()

	var returns PortArray
	var resultErr error
	err := service.DispatchAsyncCompletion(
		execution.module.Service(),
		execution.context,
		func(waitCtx context.Context) error {
			select {
			case <-execution.Done():
				returns, resultErr = execution.Result()
				return nil
			case <-waitCtx.Done():
				execution.Cancel()
				<-execution.Done()
				returns, resultErr = execution.Result()
				return waitCtx.Err()
			}
		},
		func(taskCtx context.Context, completionErr error) {
			if completionErr != nil && resultErr == nil {
				resultErr = completionErr
			}
			callback(taskCtx, returns, resultErr)
		},
	)
	if err != nil {
		execution.mu.Lock()
		execution.completionRegistered = false
		execution.mu.Unlock()
		return err
	}
	return nil
}
