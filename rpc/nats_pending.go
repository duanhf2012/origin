package rpc

import (
	"sync"

	"github.com/duanhf2012/origin/v3/errs"
)

// natsPendingCall 保存一条 Node 级 NATS 请求的最小关联状态。
type natsPendingCall struct {
	targetSessionID uint64
	complete        func(*Buffer, error)
}

// natsPendingTable 是 NATS 共享 Response Subject 对应的固定有界 pending 表。
//
// 它不预分配 65536 个槽位，也不池化回调闭包；Map 只随真实并发量增长。所有 complete 都
// 在锁外调用，避免业务恢复路径重入 pending 锁。
type natsPendingTable struct {
	mu       sync.Mutex
	max      int
	closed   bool
	requests map[uint64]natsPendingCall
}

// newNATSPendingTable 创建没有预分配大容量桶的 pending 表。
func newNATSPendingTable(max int) *natsPendingTable {
	return &natsPendingTable{
		max:      max,
		requests: make(map[uint64]natsPendingCall),
	}
}

// reserve 在 Publish 前预占 RequestID；成功后调用方才能发送消息。
func (table *natsPendingTable) reserve(
	requestID uint64,
	targetSessionID uint64,
	complete func(*Buffer, error),
) error {
	if table == nil || requestID == 0 || targetSessionID == 0 || complete == nil {
		return errs.ErrInvalidArgument
	}
	table.mu.Lock()
	defer table.mu.Unlock()
	if table.closed {
		return errs.ErrTransportUnavailable
	}
	if len(table.requests) >= table.max {
		return errs.ErrTransportOverloaded
	}
	if _, duplicate := table.requests[requestID]; duplicate {
		return errs.ErrTransportProtocol
	}
	table.requests[requestID] = natsPendingCall{
		targetSessionID: targetSessionID,
		complete:        complete,
	}
	return nil
}

// rollback 删除尚未成功 Publish 的预占项，不调用完成函数。
func (table *natsPendingTable) rollback(requestID uint64) {
	if table == nil || requestID == 0 {
		return
	}
	table.mu.Lock()
	delete(table.requests, requestID)
	table.mu.Unlock()
}

// take 只有在 RequestID、来源目标会话和本地目标会话都匹配时才取得 pending。
func (table *natsPendingTable) take(
	requestID uint64,
	responseSourceSessionID uint64,
	responseTargetSessionID uint64,
	localSessionID uint64,
) (natsPendingCall, bool) {
	if table == nil || requestID == 0 {
		return natsPendingCall{}, false
	}
	table.mu.Lock()
	call, exists := table.requests[requestID]
	if exists &&
		call.targetSessionID == responseSourceSessionID &&
		responseTargetSessionID == localSessionID {
		delete(table.requests, requestID)
	} else {
		exists = false
	}
	table.mu.Unlock()
	return call, exists
}

// cancel 由调用方 Context 的唯一终态删除 pending 并完成等待者。
func (table *natsPendingTable) cancel(requestID uint64, cause error) {
	if table == nil || requestID == 0 {
		return
	}
	table.mu.Lock()
	call, exists := table.requests[requestID]
	if exists {
		delete(table.requests, requestID)
	}
	table.mu.Unlock()
	if exists && cause != nil {
		call.complete(nil, cause)
	}
}

// failAll 把表切换为终态，并在锁外完成全部在途调用。
func (table *natsPendingTable) failAll(cause error) {
	if table == nil {
		return
	}
	table.mu.Lock()
	if table.closed {
		table.mu.Unlock()
		return
	}
	table.closed = true
	requests := table.requests
	table.requests = nil
	table.mu.Unlock()
	for _, call := range requests {
		call.complete(nil, cause)
	}
}
