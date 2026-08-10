// Package bytebudget 提供不保存对象、不包含队列策略的有界字节额度。
//
// Budget 只负责跨多个所有者原子预留、归还和峰值统计。调用方决定消息顺序、失败行为和
// Buffer 所有权，避免这个基础算法反向依赖网络、RPC 或 Service。
package bytebudget

import (
	"errors"
	"sync/atomic"
)

var (
	// ErrInvalidLimit 表示额度上限不能形成有效的正数边界。
	ErrInvalidLimit = errors.New("bytebudget: invalid limit")
)

// Snapshot 是同一 Budget 的近似并发统计快照。
//
// Used 和 HighWatermark 可能来自相邻时刻，但停止预留和释放后会形成稳定结果。
type Snapshot struct {
	// Limit 是构造时冻结的字节硬上限。
	Limit int64
	// Used 是当前已经预留且尚未释放的字节数。
	Used int64
	// HighWatermark 是当前生命周期内 Used 达到过的最大值。
	HighWatermark int64
}

// Budget 是一个并发安全、不会超过固定 Limit 的字节额度。
//
// Budget 不得在开始使用后复制。零值只能查询，不能成功预留正数额度。
type Budget struct {
	limit int64
	used  atomic.Int64
	high  atomic.Int64
}

// New 创建一个具有固定正数上限的 Budget。
func New(limit int64) (*Budget, error) {
	// 零或负数不能表达“有界但可使用”的容量，也不把零值解释为无限。
	if limit <= 0 {
		return nil, ErrInvalidLimit
	}
	return &Budget{limit: limit}, nil
}

// TryAcquire 尝试原子预留 size 字节。
//
// 成功后调用方必须在所有相关对象真正释放时调用一次 Release。失败和负数请求不修改状态；
// 零字节请求成功且无需对应 Release。
func (budget *Budget) TryAcquire(size int64) bool {
	// nil/零值 Budget 不能取得正额度；零大小保持统一成功语义。
	if size == 0 {
		return true
	}
	if budget == nil || size < 0 || budget.limit <= 0 {
		return false
	}

	// 使用“size > limit-current”避免 current+size 的有符号整数溢出。
	for {
		current := budget.used.Load()
		if current < 0 || size > budget.limit-current {
			return false
		}
		next := current + size
		if !budget.used.CompareAndSwap(current, next) {
			continue
		}
		budget.recordHigh(next)
		return true
	}
}

// Release 归还一次已经成功预留的正数字节额度。
//
// 负数、nil、零值或释放超过 Used 都表示框架内部所有权记账错误，因此直接 panic。
func (budget *Budget) Release(size int64) {
	// 零大小没有真实预留，允许统一清理路径直接忽略。
	if size == 0 {
		return
	}
	if budget == nil || size < 0 || budget.limit <= 0 {
		panic("bytebudget: 非法 Release")
	}

	// CAS 同时检测并发释放后的最新 Used，任何一次过量释放都会尽早暴露。
	for {
		current := budget.used.Load()
		if size > current {
			panic("bytebudget: Release 超过已预留额度")
		}
		if budget.used.CompareAndSwap(current, current-size) {
			return
		}
	}
}

// Snapshot 返回当前额度、使用量和历史峰值。
func (budget *Budget) Snapshot() Snapshot {
	if budget == nil {
		return Snapshot{}
	}
	return Snapshot{
		Limit:         budget.limit,
		Used:          budget.used.Load(),
		HighWatermark: budget.high.Load(),
	}
}

// recordHigh 用单调 CAS 更新峰值，不让普通 Release 参与该原子变量。
func (budget *Budget) recordHigh(value int64) {
	for {
		current := budget.high.Load()
		if value <= current || budget.high.CompareAndSwap(current, value) {
			return
		}
	}
}
