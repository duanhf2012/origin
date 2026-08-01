package rpc

import (
	"errors"
	"fmt"
	"sort"

	"github.com/duanhf2012/origin/v3/errs"
)

// BroadcastFailure 描述一个广播意图目标没有完成本地提交的稳定原因。
//
// 一次 Broadcast 只绑定一个 ServiceName，因此详情只保留 NodeID 和非 nil 原因，避免为每个
// 失败目标重复保存公共字符串。
type BroadcastFailure struct {
	// NodeID 是失败目标的稳定节点标识。
	NodeID string
	// Err 是该目标在准备或提交阶段产生的具体错误。
	Err error
}

// BroadcastError 是多目标广播部分失败或全部失败的只读聚合错误。
//
// 字段保持私有，调用方只能通过访问器读取稳定快照，不能修改内部失败顺序或统计。
type BroadcastError struct {
	total     int
	succeeded int
	code      errs.Code
	failures  []BroadcastFailure
}

// newBroadcastError 冻结失败详情并按 NodeID 排序。
func newBroadcastError(total, succeeded int, failures []BroadcastFailure) *BroadcastError {
	// 失败结果可能在返回后被业务长期保存，因此必须独占底层数组，不能复用提交阶段临时空间。
	frozen := append([]BroadcastFailure(nil), failures...)
	sort.Slice(frozen, func(left, right int) bool {
		return frozen[left].NodeID < frozen[right].NodeID
	})
	code := errs.CodeRPCBroadcastPartialFailed
	if succeeded == 0 && total > 1 {
		code = errs.CodeRPCBroadcastFailed
	}
	return &BroadcastError{
		total:     total,
		succeeded: succeeded,
		code:      code,
		failures:  frozen,
	}
}

// Error 返回不展开 NodeID 和底层原因的稳定汇总文本，避免大规模失败形成日志风暴。
func (broadcastErr *BroadcastError) Error() string {
	if broadcastErr == nil {
		return "rpc broadcast failed: total=0 succeeded=0 failed=0"
	}
	return fmt.Sprintf(
		"rpc broadcast failed: total=%d succeeded=%d failed=%d",
		broadcastErr.total,
		broadcastErr.succeeded,
		len(broadcastErr.failures),
	)
}

// Total 返回本次广播计划中的意图目标总数。
func (broadcastErr *BroadcastError) Total() int {
	if broadcastErr == nil {
		return 0
	}
	return broadcastErr.total
}

// Succeeded 返回完成本地提交的目标数量。
func (broadcastErr *BroadcastError) Succeeded() int {
	if broadcastErr == nil {
		return 0
	}
	return broadcastErr.succeeded
}

// FailureCount 返回没有完成本地提交的目标数量。
func (broadcastErr *BroadcastError) FailureCount() int {
	if broadcastErr == nil {
		return 0
	}
	return len(broadcastErr.failures)
}

// Failure 返回指定位置的失败详情；越界索引返回零值和 false。
func (broadcastErr *BroadcastError) Failure(index int) (BroadcastFailure, bool) {
	if broadcastErr == nil || index < 0 || index >= len(broadcastErr.failures) {
		return BroadcastFailure{}, false
	}
	return broadcastErr.failures[index], true
}

// Code 返回 2010 部分失败或 2011 多目标全部失败的稳定错误码。
func (broadcastErr *BroadcastError) Code() errs.Code {
	if broadcastErr == nil {
		return errs.CodeRPCBroadcastFailed
	}
	return broadcastErr.code
}

// Is 同时支持聚合哨兵和任一逐目标底层原因的 errors.Is 匹配。
func (broadcastErr *BroadcastError) Is(target error) bool {
	if broadcastErr == nil || target == nil {
		return false
	}
	if coder, ok := target.(errs.Coder); ok && coder.Code() == broadcastErr.code {
		return true
	}
	for _, failure := range broadcastErr.failures {
		if errors.Is(failure.Err, target) {
			return true
		}
	}
	return false
}
