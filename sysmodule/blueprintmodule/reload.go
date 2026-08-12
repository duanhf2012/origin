package blueprintmodule

import "context"

// ReloadResult 描述显式热加载是否已经发布，以及发布后的蓝图数量。
type ReloadResult struct {
	// GraphCount 是成功编译并发布的蓝图数量；未发布时为零。
	GraphCount int
	// Applied 表示本次新图池已经由引擎原子发布。
	Applied bool
}

// Reload 在不占用 Service 工作协程的阶段读取、解析并编译全部蓝图，成功后原子发布新图池。
//
// Reload 必须从所属 Service 工作协程调用。同一 Module 同时只运行一个事务，第二个调用立即返回
// ErrReloadInProgress。活动 Execution 固定旧编译快照；同一 Instance 的下一次 Run/Start 使用新图。
func (module *Module) Reload(ctx context.Context) (ReloadResult, error) {
	if module == nil || ctx == nil {
		return ReloadResult{}, ErrInvalidArgument
	}
	if err := ctx.Err(); err != nil {
		return ReloadResult{}, err
	}
	if !module.reloadInProgress.CompareAndSwap(false, true) {
		return ReloadResult{}, ErrReloadInProgress
	}
	defer module.reloadInProgress.Store(false)

	// 只取得稳定引擎指针，不跨 HotReload 持有包装层锁。调用来自 Service Task，正常 Stop 会等待任务排空。
	engine, err := module.runningEngine()
	if err != nil {
		return ReloadResult{}, err
	}
	var result ReloadResult
	var reloadErr error
	err = module.Await(ctx, func(context.Context) error {
		current, currentErr := engine.HotReload()
		if currentErr != nil {
			reloadErr = currentErr
			return currentErr
		}
		if current != nil {
			result.GraphCount = current.GraphCount
		}
		result.Applied = true
		return nil
	})
	if reloadErr != nil {
		module.stats.reloadFailedTotal.Add(1)
		return result, reloadErr
	}
	if result.Applied {
		module.stats.reloadedTotal.Add(1)
	}
	// Await 的 Deadline 还覆盖 Service 恢复排队；此时事务可能已成功发布，调用者必须同时检查 Applied。
	if err != nil {
		return result, err
	}
	return result, nil
}
