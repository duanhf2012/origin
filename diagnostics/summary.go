package diagnostics

import (
	"encoding/json"
	"time"
)

// Summary 是面向秒级监控采集的低基数 Application 诊断文档。
//
// 它按 Node 保留固定数量字段，并把每个 Node 的全部 Service 直接汇总成一个对象，避免
// Full Snapshot 的响应大小和输出 DTO 分配随 Service 明细线性增长。
type Summary struct {
	SchemaVersion uint32             `json:"schema_version"`
	CollectedAt   time.Time          `json:"collected_at"`
	StartedAt     time.Time          `json:"started_at"`
	CollectCost   Duration           `json:"collect_cost"`
	Application   ApplicationSummary `json:"application"`
	Runtime       RuntimeSummary     `json:"runtime"`
	BufferPool    BufferPoolSnapshot `json:"buffer_pool"`
	Nodes         []NodeSummary      `json:"nodes"`
}

// MarshalJSON 固定 Summary 零值和 nil Nodes 的线协议为 []，避免监控消费方同时处理 null。
func (summary Summary) MarshalJSON() ([]byte, error) {
	type summaryJSON Summary
	if summary.Nodes == nil {
		summary.Nodes = make([]NodeSummary, 0)
	}
	return json.Marshal(summaryJSON(summary))
}

// ApplicationSummary 保存 Application 身份、生命周期和管理 Listener 的低基数状态。
type ApplicationSummary struct {
	Name        string         `json:"name"`
	State       string         `json:"state"`
	AdminServer ServerSnapshot `json:"admin_server"`
	Pprof       ServerSnapshot `json:"pprof"`
}

// RuntimeSummary 保存 Go Runtime 面向监控的内存、调度、GC 和互斥等待累计值。
// GoMemoryUsedBytes 表示 Go Runtime 已取得且尚未归还给操作系统的内存，不等同于 RSS。
type RuntimeSummary struct {
	Goroutines            int      `json:"goroutines"`
	RunnableGoroutines    uint64   `json:"runnable_goroutines"`
	GOMAXPROCS            int      `json:"gomaxprocs"`
	GoMemoryUsedBytes     uint64   `json:"go_memory_used_bytes"`
	MemoryLimitBytes      int64    `json:"memory_limit_bytes"`
	HeapAllocBytes        uint64   `json:"heap_alloc_bytes"`
	HeapObjects           uint64   `json:"heap_objects"`
	TotalAllocBytes       uint64   `json:"total_alloc_bytes"`
	GCCycles              uint32   `json:"gc_cycles"`
	GCPauseTotal          Duration `json:"gc_pause_total"`
	GCCPUSecondsTotal     float64  `json:"gc_cpu_seconds_total"`
	MutexWaitSecondsTotal float64  `json:"mutex_wait_seconds_total"`
}

// NodeSummary 保存一个 Node 的固定状态以及全部本地 Service 的单一聚合结果。
type NodeSummary struct {
	NodeID    string                     `json:"node_id"`
	State     string                     `json:"state"`
	Health    HealthSnapshot             `json:"health"`
	Transport TransportSnapshot          `json:"transport"`
	Discovery DiscoverySnapshot          `json:"discovery"`
	RPC       RPCSummary                 `json:"rpc"`
	Directory DiscoveryDirectorySnapshot `json:"directory"`
	Services  ServiceAggregate           `json:"services"`
}

// RPCSummary 按固定 Local、TCP、NATS 三类保存 RPC 监控累计，不重复 Node Transport 的恢复字段。
type RPCSummary struct {
	Local RPCTransportSummary `json:"local"`
	TCP   RPCTransportSummary `json:"tcp"`
	NATS  RPCTransportSummary `json:"nats"`
}

// RPCTransportSummary 保存一类 RPC Transport 的积压、吞吐、结果和字节累计。
type RPCTransportSummary struct {
	Pending              uint64 `json:"pending"`
	PendingHighWater     uint64 `json:"pending_high_water"`
	OutboundAccepted     uint64 `json:"outbound_accepted"`
	OutboundCompleted    uint64 `json:"outbound_completed"`
	OutboundFailed       uint64 `json:"outbound_failed"`
	OutboundTimeout      uint64 `json:"outbound_timeout"`
	OutboundRejected     uint64 `json:"outbound_rejected"`
	InboundAccepted      uint64 `json:"inbound_accepted"`
	InboundCompleted     uint64 `json:"inbound_completed"`
	InboundFailed        uint64 `json:"inbound_failed"`
	InboundTimeout       uint64 `json:"inbound_timeout"`
	InboundRejected      uint64 `json:"inbound_rejected"`
	PayloadSentBytes     uint64 `json:"payload_sent_bytes"`
	PayloadReceivedBytes uint64 `json:"payload_received_bytes"`
}

// ServiceAggregate 保存一个 Node 内全部 Service 的生命周期数量和三类运行叶子累计。
type ServiceAggregate struct {
	Total     int                   `json:"total"`
	States    ServiceStateAggregate `json:"states"`
	Execution ExecutionAggregate    `json:"execution"`
	Timer     TimerAggregate        `json:"timer"`
	Event     EventAggregate        `json:"event"`
}

// ServiceStateAggregate 按稳定生命周期枚举保存 Service 数量。
type ServiceStateAggregate struct {
	Created      int `json:"created"`
	Initializing int `json:"initializing"`
	Initialized  int `json:"initialized"`
	Starting     int `json:"starting"`
	Running      int `json:"running"`
	Retired      int `json:"retired"`
	Stopping     int `json:"stopping"`
	Stopped      int `json:"stopped"`
	Failed       int `json:"failed"`
	Unknown      int `json:"unknown"`
}

// ExecutionAggregate 保存 Service Scheduler 当前任务数量以及过载拒绝和 panic 累计。
type ExecutionAggregate struct {
	Accepted      int    `json:"accepted"`
	Ready         int    `json:"ready"`
	Running       int    `json:"running"`
	Awaiting      int    `json:"awaiting"`
	RejectedTotal uint64 `json:"rejected_total"`
	PanicTotal    uint64 `json:"panic_total"`
}

// TimerAggregate 保存全部业务 Timer 的关键当前数量以及拒绝和 panic 累计。
type TimerAggregate struct {
	Active        int    `json:"active"`
	DuePending    int    `json:"due_pending"`
	Ready         int    `json:"ready"`
	Running       int    `json:"running"`
	RejectedTotal uint64 `json:"rejected_total"`
	PanicTotal    uint64 `json:"panic_total"`
}

// EventAggregate 保存本地事件 Handler 失败累计。
type EventAggregate struct {
	HandlerFailureTotal uint64 `json:"handler_failure_total"`
}
