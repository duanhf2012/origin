package service

import (
	"context"

	"github.com/duanhf2012/origin/v3/errs"
)

// enterSynchronousEvent 验证当前 Service Task 执行权并增加同步事件嵌套深度。
func (scheduler *serviceScheduler) enterSynchronousEvent(
	ctx context.Context,
) (*serviceTask, error) {
	token, _ := ctx.Value(taskContextKey{}).(*taskContext)
	if token == nil || token.scheduler != scheduler {
		return nil, errs.ErrInvalidArgument
	}
	task := token.task.Load()
	if task == nil {
		return nil, errs.ErrInvalidArgument
	}
	scheduler.mu.Lock()
	if token.task.Load() != task || task.context != token ||
		task.scheduler != scheduler || scheduler.runningTask != task ||
		scheduler.running != 1 || task.state != taskRunning {
		scheduler.mu.Unlock()
		return nil, errs.ErrInvalidArgument
	}
	if task.syncEventDepth >= MaxSynchronousEventDepth {
		scheduler.mu.Unlock()
		return nil, errs.NewMessage(errs.CodeInvalidArgument, "同步事件嵌套深度超过 64")
	}
	task.syncEventDepth++
	scheduler.mu.Unlock()

	return task, nil
}

func (scheduler *serviceScheduler) leaveSynchronousEvent(task *serviceTask) {
	scheduler.mu.Lock()
	if task == nil || task.syncEventDepth == 0 {
		scheduler.mu.Unlock()
		panicInvariant("service: 同步事件深度下溢")
	}
	task.syncEventDepth--
	scheduler.mu.Unlock()
}
