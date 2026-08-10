package bytebudget

import (
	"sync"
	"testing"
)

func TestBudgetBoundariesAndSnapshot(t *testing.T) {
	t.Parallel()

	// 先覆盖构造、零请求、负请求和恰好达到硬上限。
	if _, err := New(0); err != ErrInvalidLimit {
		t.Fatalf("New(0) error=%v，期望 ErrInvalidLimit", err)
	}
	budget, err := New(10)
	if err != nil {
		t.Fatalf("New error=%v", err)
	}
	if !budget.TryAcquire(0) || budget.TryAcquire(-1) {
		t.Fatal("零/负请求语义错误")
	}
	if !budget.TryAcquire(4) || !budget.TryAcquire(6) || budget.TryAcquire(1) {
		t.Fatal("硬上限预留语义错误")
	}
	if got := budget.Snapshot(); got.Limit != 10 || got.Used != 10 || got.HighWatermark != 10 {
		t.Fatalf("满额快照错误：%+v", got)
	}

	// 归还后可以再次预留，峰值保持单调。
	budget.Release(3)
	if !budget.TryAcquire(2) {
		t.Fatal("归还后重新预留失败")
	}
	if got := budget.Snapshot(); got.Used != 9 || got.HighWatermark != 10 {
		t.Fatalf("归还后快照错误：%+v", got)
	}
	budget.Release(9)
	if got := budget.Snapshot(); got.Used != 0 || got.HighWatermark != 10 {
		t.Fatalf("最终快照错误：%+v", got)
	}
}

func TestBudgetConcurrentNeverExceedsLimit(t *testing.T) {
	t.Parallel()

	// 多个 goroutine 反复竞争一个较小额度，成功者在本轮内归还。
	const (
		workers    = 32
		iterations = 2000
		limit      = 64
	)
	budget, err := New(limit)
	if err != nil {
		t.Fatalf("New error=%v", err)
	}
	var wait sync.WaitGroup
	wait.Add(workers)
	for worker := 0; worker < workers; worker++ {
		go func(worker int) {
			defer wait.Done()
			size := int64(worker%8 + 1)
			for iteration := 0; iteration < iterations; iteration++ {
				if budget.TryAcquire(size) {
					if used := budget.Snapshot().Used; used > limit {
						t.Errorf("Used=%d 超过 limit=%d", used, limit)
						budget.Release(size)
						return
					}
					budget.Release(size)
				}
			}
		}(worker)
	}
	wait.Wait()

	// 全部 worker 结束后没有额度泄漏，峰值不会越界。
	snapshot := budget.Snapshot()
	if snapshot.Used != 0 || snapshot.HighWatermark > limit || snapshot.HighWatermark <= 0 {
		t.Fatalf("并发结束快照错误：%+v", snapshot)
	}
}

func TestBudgetRejectsInvalidRelease(t *testing.T) {
	t.Parallel()

	// 统一辅助验证负数和超过已预留额度都会立即暴露内部错误。
	budget, err := New(4)
	if err != nil {
		t.Fatalf("New error=%v", err)
	}
	assertBudgetPanic(t, func() { budget.Release(-1) })
	assertBudgetPanic(t, func() { budget.Release(1) })
	if !budget.TryAcquire(2) {
		t.Fatal("预留失败")
	}
	assertBudgetPanic(t, func() { budget.Release(3) })
	budget.Release(2)
}

func assertBudgetPanic(t *testing.T, fn func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Fatal("期望 panic，但调用正常返回")
		}
	}()
	fn()
}
