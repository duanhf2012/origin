package rpc

import (
	"errors"
	"sync/atomic"
	"testing"

	"github.com/duanhf2012/origin/v3/errs"
)

// TestNATSPendingCapacityRollbackAndSessions 锁定预占、回滚和双会话校验。
func TestNATSPendingCapacityRollbackAndSessions(t *testing.T) {
	table := newNATSPendingTable(2)
	complete := func(*Buffer, error) {}
	if err := table.reserve(1, 101, complete); err != nil {
		t.Fatalf("reserve(1) error = %v", err)
	}
	if err := table.reserve(2, 102, complete); err != nil {
		t.Fatalf("reserve(2) error = %v", err)
	}
	if err := table.reserve(3, 103, complete); !errors.Is(
		err,
		errs.ErrTransportOverloaded,
	) {
		t.Fatalf("满表 reserve error = %v", err)
	}

	// 错误来源会话或错误本地目标会话不能取走 pending。
	if _, ok := table.take(1, 999, 201, 201); ok {
		t.Fatal("错误来源 SessionID 取走 pending")
	}
	if _, ok := table.take(1, 101, 999, 201); ok {
		t.Fatal("错误目标 SessionID 取走 pending")
	}
	if _, ok := table.take(1, 101, 201, 201); !ok {
		t.Fatal("两个 SessionID 匹配时没有取得 pending")
	}

	// Publish 失败回滚不调用 complete，并立即释放容量。
	table.rollback(2)
	if err := table.reserve(3, 103, complete); err != nil {
		t.Fatalf("回滚后 reserve error = %v", err)
	}
}

// TestNATSPendingCompletesExactlyOnce 验证取消与终态关闭只允许一个路径取得调用。
func TestNATSPendingCompletesExactlyOnce(t *testing.T) {
	table := newNATSPendingTable(4)
	var calls atomic.Int32
	complete := func(_ *Buffer, err error) {
		if !errors.Is(err, errs.ErrCanceled) {
			t.Errorf("complete error = %v", err)
		}
		calls.Add(1)
	}
	if err := table.reserve(1, 101, complete); err != nil {
		t.Fatal(err)
	}
	table.cancel(1, errs.ErrCanceled)
	table.cancel(1, errs.ErrCanceled)
	table.failAll(errs.ErrTransportUnavailable)
	if calls.Load() != 1 {
		t.Fatalf("complete 调用次数 = %d", calls.Load())
	}
}
