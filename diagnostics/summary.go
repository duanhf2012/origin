package diagnostics

import (
	"encoding/json"
	"time"

	"github.com/duanhf2012/origin/v3/errs"
)

// Summary is the bounded, low-cardinality document returned by default from
// the diagnostics Admin route. Detailed topology and per-Service data remain
// available only from the Full Snapshot (detail=full).
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

// MarshalJSON fixes the zero-value and nil-Nodes wire shape to [] rather than
// null, so monitoring consumers have one stable document form.
func (summary Summary) MarshalJSON() ([]byte, error) {
	type summaryJSON Summary
	if summary.Nodes == nil {
		summary.Nodes = make([]NodeSummary, 0)
	}
	return json.Marshal(summaryJSON(summary))
}

// ApplicationSummary contains identity, lifecycle state and bounded listener
// health. Listener addresses remain Full-only.
type ApplicationSummary struct {
	Name        string          `json:"name"`
	State       string          `json:"state"`
	AdminServer ListenerSummary `json:"admin_server"`
	Pprof       ListenerSummary `json:"pprof"`
}

// ListenerSummary keeps only a listener's health signal. Its address is
// configuration/debug topology rather than a per-second monitoring metric.
type ListenerSummary struct {
	State     string    `json:"state"`
	ErrorCode errs.Code `json:"error_code"`
}

// RuntimeSummary contains Go-runtime measurements useful for alerting and
// capacity diagnosis. Host RSS/container working set and process CPU are
// deliberately external-metrics concerns.
type RuntimeSummary struct {
	Goroutines            int      `json:"goroutines"`
	RunnableGoroutines    uint64   `json:"runnable_goroutines"`
	GOMAXPROCS            int      `json:"gomaxprocs"`
	GoMemoryUsedBytes     uint64   `json:"go_memory_used_bytes"`
	MemoryLimitConfigured bool     `json:"memory_limit_configured"`
	MemoryLimitBytes      int64    `json:"memory_limit_bytes"`
	HeapGoalBytes         uint64   `json:"heap_goal_bytes"`
	HeapAllocBytes        uint64   `json:"heap_alloc_bytes"`
	TotalAllocBytes       uint64   `json:"total_alloc_bytes"`
	GCCycles              uint32   `json:"gc_cycles"`
	GCPauseTotal          Duration `json:"gc_pause_total"`
	GCCPUSecondsTotal     float64  `json:"gc_cpu_seconds_total"`
	MutexWaitSecondsTotal float64  `json:"mutex_wait_seconds_total"`
}

// NodeSummary retains the bounded availability, discovery and aggregate work
// signals required to triage a Node without leaking endpoint/service names.
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

// RPCSummary aggregates all fixed RPC transports. Pending is summed while
// pending_high_water is the maximum observed transport high-water mark.
type RPCSummary struct {
	Pending              uint64 `json:"pending"`
	PendingHighWater     uint64 `json:"pending_high_water"`
	OutboundCompleted    uint64 `json:"outbound_completed"`
	OutboundFailed       uint64 `json:"outbound_failed"`
	OutboundTimeout      uint64 `json:"outbound_timeout"`
	OutboundRejected     uint64 `json:"outbound_rejected"`
	InboundCompleted     uint64 `json:"inbound_completed"`
	InboundFailed        uint64 `json:"inbound_failed"`
	InboundTimeout       uint64 `json:"inbound_timeout"`
	InboundRejected      uint64 `json:"inbound_rejected"`
	PayloadSentBytes     uint64 `json:"payload_sent_bytes"`
	PayloadReceivedBytes uint64 `json:"payload_received_bytes"`
}

// ServiceAggregate holds one Node-wide aggregate, not a per-Service DTO list.
type ServiceAggregate struct {
	Total     int                   `json:"total"`
	States    ServiceStateAggregate `json:"states"`
	Execution ExecutionAggregate    `json:"execution"`
	Timer     TimerAggregate        `json:"timer"`
	Event     EventAggregate        `json:"event"`
}

// ServiceStateAggregate excludes normal transition noise. Every state other
// than running, retired and failed (including stopped) is counted as unknown.
type ServiceStateAggregate struct {
	Running int `json:"running"`
	Retired int `json:"retired"`
	Failed  int `json:"failed"`
	Unknown int `json:"unknown"`
}

// ExecutionAggregate exposes occupancy gauges plus scheduler result counters.
type ExecutionAggregate struct {
	Accepted          int    `json:"accepted"`
	Ready             int    `json:"ready"`
	Running           int    `json:"running"`
	Awaiting          int    `json:"awaiting"`
	DispatchedTotal   uint64 `json:"dispatched_total"`
	CompletedTotal    uint64 `json:"completed_total"`
	RejectedTotal     uint64 `json:"rejected_total"`
	AwaitTimeoutTotal uint64 `json:"await_timeout_total"`
	PanicTotal        uint64 `json:"panic_total"`
}

// TimerAggregate exposes timer work gauges plus terminal counters.
type TimerAggregate struct {
	Active         int      `json:"active"`
	DuePending     int      `json:"due_pending"`
	Ready          int      `json:"ready"`
	Running        int      `json:"running"`
	TriggeredTotal uint64   `json:"triggered_total"`
	CompletedTotal uint64   `json:"completed_total"`
	RejectedTotal  uint64   `json:"rejected_total"`
	PanicTotal     uint64   `json:"panic_total"`
	MaxReadyDelay  Duration `json:"max_ready_delay"`
}

// EventAggregate keeps traffic counters beside handler failures so an error
// count always has workload context.
type EventAggregate struct {
	SyncNotifiedTotal   uint64 `json:"sync_notified_total"`
	AsyncNotifiedTotal  uint64 `json:"async_notified_total"`
	HandlerFailureTotal uint64 `json:"handler_failure_total"`
}
