package service

import (
	"context"

	"github.com/duanhf2012/origin/v3/errs"
)

// executeControl 让冷路径状态控制既可由外部调用，也可在当前 Task 内直接执行。
func (scheduler *serviceScheduler) executeControl(
	ctx context.Context,
	control func(context.Context) error,
) error {
	if ctx == nil || control == nil {
		return errs.ErrInvalidArgument
	}
	if scheduler.ownsRunningTask(ctx) {
		return control(ctx)
	}

	result := make(chan error, 1)
	err := scheduler.dispatch(func(taskCtx context.Context) {
		merged := &completionContext{execution: taskCtx, caller: ctx}
		if callerErr := ctx.Err(); callerErr != nil {
			result <- errs.Wrap(errs.CodeOf(callerErr), callerErr)
			return
		}
		result <- control(merged)
	})
	if err != nil {
		return err
	}
	select {
	case err := <-result:
		return err
	case <-ctx.Done():
		return errs.Wrap(errs.CodeOf(ctx.Err()), ctx.Err())
	}
}

func (scheduler *serviceScheduler) ownsRunningTask(ctx context.Context) bool {
	token, _ := ctx.Value(taskContextKey{}).(*taskContext)
	if token == nil || token.scheduler != scheduler {
		return false
	}
	task := token.task.Load()
	if task == nil {
		return false
	}
	scheduler.mu.Lock()
	owned := token.task.Load() == task && task.context == token &&
		task.scheduler == scheduler && scheduler.runningTask == task &&
		scheduler.running == 1 && task.state == taskRunning
	scheduler.mu.Unlock()
	return owned
}
