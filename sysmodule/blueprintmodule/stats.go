package blueprintmodule

import "sync/atomic"

// Stats 是 Blueprint Module 的精确轻量统计快照。
//
// 首版只统计包装层可以零 goroutine、零终态句柄保留得到的数据，不为高基数执行或节点建立额外指标。
type Stats struct {
	// ActiveInstances 是当前尚未关闭的长期或临时 Instance 数量。
	ActiveInstances int
	// CreatedTotal 是成功创建 Instance 的累计数量。
	CreatedTotal uint64
	// ClosedTotal 是成功完成首次关闭的 Instance 累计数量。
	ClosedTotal uint64
	// StartedTotal 是成功创建底层 Execution 的累计数量。
	StartedTotal uint64
	// ReloadedTotal 是成功发布新图池的热加载累计数量。
	ReloadedTotal uint64
	// ReloadFailedTotal 是热加载事务失败的累计数量。
	ReloadFailedTotal uint64
}

type moduleStats struct {
	createdTotal      atomic.Uint64
	closedTotal       atomic.Uint64
	startedTotal      atomic.Uint64
	reloadedTotal     atomic.Uint64
	reloadFailedTotal atomic.Uint64
}

// Stats 返回不会阻塞蓝图执行的统计快照。
func (module *Module) Stats() Stats {
	if module == nil {
		return Stats{}
	}
	module.mu.RLock()
	active := len(module.instances)
	module.mu.RUnlock()
	return Stats{
		ActiveInstances:   active,
		CreatedTotal:      module.stats.createdTotal.Load(),
		ClosedTotal:       module.stats.closedTotal.Load(),
		StartedTotal:      module.stats.startedTotal.Load(),
		ReloadedTotal:     module.stats.reloadedTotal.Load(),
		ReloadFailedTotal: module.stats.reloadFailedTotal.Load(),
	}
}
