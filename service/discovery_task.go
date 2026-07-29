package service

import (
	"context"

	"github.com/duanhf2012/origin/v3/errs"
)

// MarkDiscoveryDirty 请求所属 Service 在唯一 FIFO Runner 中同步最新发现状态。
//
// 该函数只供 node 发现运行时调用。多次请求会合并为一个常数大小脏标记；任务额度已满时
// 不丢失最终状态，而是在任一已接受任务完成释放额度后提升一次发现任务。
func MarkDiscoveryDirty(
	target IService,
	run func(context.Context),
) error {
	// 发现同步必须绑定真实 Service 和稳定交付函数，不能建立空任务。
	if target == nil || isNilService(target) || run == nil {
		return errs.ErrInvalidArgument
	}
	base := target.baseService()
	if base == nil {
		return errs.ErrInvalidArgument
	}
	scheduler := base.scheduler.Load()
	if scheduler == nil {
		return errs.ErrServiceNotReady
	}
	return scheduler.markDiscoveryDirty(run)
}

// markDiscoveryDirty 在线性化短锁中保存最新交付函数并尝试建立唯一 FIFO Task。
func (scheduler *serviceScheduler) markDiscoveryDirty(
	run func(context.Context),
) error {
	scheduler.mu.Lock()
	switch scheduler.state {
	case schedulerPrepared:
		// OnInit/OnStart 注册只保存意图，整个 Node 越过就绪屏障前不运行用户回调。
	case schedulerRunning:
		// Running 可以立即尝试取得任务额度。
	case schedulerDraining:
		scheduler.mu.Unlock()
		return errs.ErrServiceStopping
	case schedulerStopped:
		scheduler.mu.Unlock()
		return errs.ErrServiceStopped
	default:
		scheduler.mu.Unlock()
		return errs.ErrServiceNotReady
	}
	scheduler.discoveryRun = run
	scheduler.discoveryDirty = true
	promoted := scheduler.promoteDiscoveryLocked()
	scheduler.mu.Unlock()
	if promoted {
		scheduler.notifyRunner()
	}
	return nil
}

// promoteDiscoveryLocked 在有空闲 Accepted 额度时把唯一脏标记追加到统一 FIFO 尾部。
func (scheduler *serviceScheduler) promoteDiscoveryLocked() bool {
	if scheduler.state != schedulerRunning ||
		!scheduler.discoveryDirty ||
		scheduler.discoveryQueued ||
		scheduler.discoveryRunning ||
		scheduler.discoveryRun == nil ||
		scheduler.accepted >= scheduler.config.MaxTasks {
		return false
	}

	// Task 继续复用 Scheduler 私有对象池；发现同步本身不建立第二种队列或 goroutine。
	task := scheduler.acquireTaskLocked(scheduler.discoveryRun)
	task.kind = taskKindDiscovery
	if !scheduler.ready.Enqueue(task) {
		panic("service: 发现任务在 Accepted 未达到硬上限时无法进入 Ready")
	}
	scheduler.discoveryQueued = true
	scheduler.accepted++
	scheduler.dispatchedTotal++
	if scheduler.accepted > scheduler.acceptedHighWatermark {
		scheduler.acceptedHighWatermark = scheduler.accepted
	}
	return true
}
