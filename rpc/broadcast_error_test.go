package rpc

import (
	"errors"
	"testing"

	"github.com/duanhf2012/origin/v3/errs"
)

// TestBroadcastErrorReadOnlyView 锁定部分失败和全部失败的只读公共外观。
func TestBroadcastErrorReadOnlyView(t *testing.T) {
	// 使用稳定 NodeID 顺序构造两个失败，验证读取不会暴露内部 Slice。
	failures := []BroadcastFailure{
		{NodeID: "game-2", Err: errs.ErrTransportUnavailable},
		{NodeID: "game-3", Err: errs.ErrTransportOverloaded},
	}
	partial := newBroadcastError(3, 1, failures)
	if partial.Total() != 3 || partial.Succeeded() != 1 ||
		partial.FailureCount() != 2 || partial.Code() != errs.CodeRPCBroadcastPartialFailed {
		t.Fatalf("部分失败统计不正确: %v", partial)
	}
	first, ok := partial.Failure(0)
	if !ok || first.NodeID != "game-2" || !errors.Is(first.Err, errs.ErrTransportUnavailable) {
		t.Fatalf("第一个失败详情不正确: %+v, %v", first, ok)
	}
	if _, ok := partial.Failure(-1); ok {
		t.Fatal("负索引不应返回失败详情")
	}
	if _, ok := partial.Failure(2); ok {
		t.Fatal("越界索引不应返回失败详情")
	}
	if partial.Error() != "rpc broadcast failed: total=3 succeeded=1 failed=2" {
		t.Fatalf("聚合错误文本不稳定: %q", partial.Error())
	}
	if errs.CodeOf(partial) != errs.CodeRPCBroadcastPartialFailed ||
		!errors.Is(partial, errs.ErrRPCBroadcastPartialFailed) ||
		!errors.Is(partial, errs.ErrTransportOverloaded) {
		t.Fatalf("部分失败错误链不完整: %v", partial)
	}

	// 多目标零成功必须使用 2011，并继续匹配任一逐目标失败原因。
	failed := newBroadcastError(2, 0, failures)
	if failed.Code() != errs.CodeRPCBroadcastFailed ||
		errs.CodeOf(failed) != errs.CodeRPCBroadcastFailed ||
		!errors.Is(failed, errs.ErrRPCBroadcastFailed) ||
		!errors.Is(failed, errs.ErrTransportUnavailable) {
		t.Fatalf("全部失败错误链不完整: %v", failed)
	}
}
