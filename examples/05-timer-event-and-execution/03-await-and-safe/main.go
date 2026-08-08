// 本示例展示协作式 Await、普通任务派发以及两种 panic 安全边界。
package main

import (
	"context"
	"fmt"
	"time"

	"github.com/duanhf2012/origin/v3/application"
	"github.com/duanhf2012/origin/v3/service"
)

// app 是当前示例唯一的 Application。
var app = application.New()

// ExecutionService 演示的 API 都属于同一个 Service 调度器。
type ExecutionService struct{ service.Service }

// OnInit 设置没有显式 Deadline 时 Await 使用的默认超时。
func (target *ExecutionService) OnInit() error {
	return target.SetDefaultAwaitTimeout(500 * time.Millisecond)
}

// OnStart 先登记 Timer，等 Service 进入 Running 后再执行示例逻辑。
func (target *ExecutionService) OnStart(context.Context) error {
	target.AfterFunc(100*time.Millisecond, func(ctx context.Context, _ service.TimerID) {
		// Await 等待外部操作时释放 Service 执行权；等待函数不能读写 target 的公共业务字段，
		// 因为同一个 Service 的其他任务可能在此期间并发执行。
		if err := target.Await(ctx, func(waitCtx context.Context) error {
			select {
			case <-time.After(50 * time.Millisecond):
				return nil
			case <-waitCtx.Done():
				return waitCtx.Err()
			}
		}); err != nil {
			target.Logger().Error("await failed")
		} else {
			// Await 返回后当前任务重新获得 Service 串行执行权，可以更新 Service 状态。
			target.Logger().Info("awaited operation completed")
		}

		// RunSafe 在当前 goroutine 同步执行，并把 panic 隔离在安全边界内。
		_ = target.RunSafe(func() { target.Logger().Info("safe synchronous job completed") })
		// GoSafe 创建后台 goroutine，只提供 panic 保底；后台不直接修改 Service 状态。
		_ = target.GoSafe(func() {
			result := "safe background job completed"
			// 后台工作完成后，用 DispatchAsync 把结果交回 Service 串行任务处理。
			if err := target.DispatchAsync(func(context.Context) {
				target.Logger().Info(result)
				stats := target.ExecutionStats()
				target.Logger().Info(fmt.Sprintf(
					"execution stats: accepted=%d awaiting=%d await_total=%d",
					stats.Accepted,
					stats.Awaiting,
					stats.AwaitTotal,
				))
			}); err != nil {
				target.Logger().Error("dispatch background result failed")
			}
		})
	})
	return nil
}

// init 登记执行示例 Service。
func init() { app.Setup(&ExecutionService{}) }

// main 启动 Application。
func main() { app.Start() }
