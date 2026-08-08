// Package diagnostics 定义 Origin 进程级诊断的稳定只读快照。
//
// 本包只依赖标准库和稳定错误码，不引用 Application、Node、Service 或 RPC 的可变对象。
// 业务监控适配器可以只依赖 Source，把当前快照映射到自己的监控系统。
package diagnostics

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/duanhf2012/origin/v3/errs"
)

// Source 是业务监控适配器读取当前诊断快照的最小边界。
type Source interface {
	Diagnostics() Snapshot
}

// Duration 是在 Go 内部保留 time.Duration、在 JSON 中输出明确单位字符串的诊断时长。
type Duration time.Duration

// String 返回标准库定义的紧凑带单位时长。
func (duration Duration) String() string {
	return time.Duration(duration).String()
}

// Value 返回供 Go 调用方继续计算的 time.Duration 值。
func (duration Duration) Value() time.Duration {
	return time.Duration(duration)
}

// MarshalJSON 把时长编码为带单位字符串，避免把纳秒整数误当成其他单位。
func (duration Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(duration.String())
}

// UnmarshalJSON 解析 Diagnostics Server 返回的带单位字符串，便于管理工具保存强类型快照。
func (duration *Duration) UnmarshalJSON(data []byte) error {
	if duration == nil {
		return errors.New("diagnostics: Duration receiver is nil")
	}
	var text string
	if err := json.Unmarshal(data, &text); err != nil {
		return err
	}
	parsed, err := time.ParseDuration(text)
	if err != nil {
		return err
	}
	*duration = Duration(parsed)
	return nil
}

// Snapshot 是一次 Application.Diagnostics 调用拥有的不可变进程快照。
type Snapshot struct {
	SchemaVersion uint32              `json:"schema_version"`
	CollectedAt   time.Time           `json:"collected_at"`
	StartedAt     time.Time           `json:"started_at"`
	CollectCost   Duration            `json:"collect_cost"`
	Application   ApplicationSnapshot `json:"application"`
	Log           LogSnapshot         `json:"log"`
	Runtime       RuntimeSnapshot     `json:"runtime"`
	BufferPool    BufferPoolSnapshot  `json:"buffer_pool"`
	Nodes         []NodeSnapshot      `json:"nodes"`
}

// LogSnapshot 保存当前被采集 Application 的两个日志输出端状态。
type LogSnapshot struct {
	Console LogOutputSnapshot `json:"console"`
	File    LogOutputSnapshot `json:"file"`
}

// LogOutputSnapshot 是不会反向依赖 log 包枚举的诊断 DTO。
type LogOutputSnapshot struct {
	Available   bool   `json:"available"`
	Enabled     bool   `json:"enabled"`
	Level       string `json:"level"`
	ConfigLevel string `json:"config_level"`
}

// ApplicationSnapshot 保存进程身份、生命周期和两个诊断 Listener 的当前状态。
type ApplicationSnapshot struct {
	Name              string         `json:"name"`
	State             string         `json:"state"`
	DiagnosticsServer ServerSnapshot `json:"diagnostics_server"`
	Pprof             ServerSnapshot `json:"pprof"`
}

// ServerSnapshot 是 Diagnostics 或 pprof Server 的有界状态摘要。
type ServerSnapshot struct {
	State     string    `json:"state"`
	Address   string    `json:"address"`
	ErrorCode errs.Code `json:"error_code"`
}

// RuntimeSnapshot 保存 Go Runtime 的低频核心内存与 GC 统计。
type RuntimeSnapshot struct {
	Goroutines       int       `json:"goroutines"`
	GOMAXPROCS       int       `json:"gomaxprocs"`
	HeapAllocBytes   uint64    `json:"heap_alloc_bytes"`
	HeapObjects      uint64    `json:"heap_objects"`
	NextGCBytes      uint64    `json:"next_gc_bytes"`
	TotalAllocBytes  uint64    `json:"total_alloc_bytes"`
	GCCycles         uint32    `json:"gc_cycles"`
	GCPauseTotal     Duration  `json:"gc_pause_total"`
	LastGC           time.Time `json:"last_gc"`
	LastGCPause      Duration  `json:"last_gc_pause"`
	MemoryLimitBytes int64     `json:"memory_limit_bytes"`
}

// BufferPoolSnapshot 保存共享 BufferPool 的当前未归还汇总。
type BufferPoolSnapshot struct {
	Enabled            bool  `json:"enabled"`
	InUseBuffers       int64 `json:"in_use_buffers"`
	InUseCapacityBytes int64 `json:"in_use_capacity_bytes"`
	ZeroSizeInUse      int64 `json:"zero_size_in_use"`
	OversizeInUse      int64 `json:"oversize_in_use"`
	OversizeBytes      int64 `json:"oversize_bytes"`
}

// NodeSnapshot 保存一个 Node 的生命周期、健康、发现、RPC 和本地 Service 汇总。
type NodeSnapshot struct {
	NodeID    string                     `json:"node_id"`
	State     string                     `json:"state"`
	Health    HealthSnapshot             `json:"health"`
	Transport TransportSnapshot          `json:"transport"`
	Discovery DiscoverySnapshot          `json:"discovery"`
	RPC       RPCSnapshot                `json:"rpc"`
	Directory DiscoveryDirectorySnapshot `json:"directory"`
	Services  []ServiceSnapshot          `json:"services"`
}

// HealthSnapshot 保存探针和运维读取的 Node 健康结论。
type HealthSnapshot struct {
	Liveness  bool      `json:"liveness"`
	Readiness bool      `json:"readiness"`
	Degraded  bool      `json:"degraded"`
	ErrorCode errs.Code `json:"error_code"`
}

// TransportSnapshot 保存 Node 当前远程 RPC Transport 的状态和恢复累计值。
type TransportSnapshot struct {
	Kind                string    `json:"kind"`
	State               string    `json:"state"`
	Reconnects          uint64    `json:"reconnects"`
	ConsecutiveFailures uint64    `json:"consecutive_failures"`
	ErrorCode           errs.Code `json:"error_code"`
}

// DiscoverySnapshot 保存 Node 服务发现同步和发布屏障状态。
type DiscoverySnapshot struct {
	Kind                string    `json:"kind"`
	State               string    `json:"state"`
	Synchronized        bool      `json:"synchronized"`
	Publication         string    `json:"publication"`
	Reconnects          uint64    `json:"reconnects"`
	ConsecutiveFailures uint32    `json:"consecutive_failures"`
	ErrorCode           errs.Code `json:"error_code"`
}

// DiscoveryDirectorySnapshot 保存当前可见远端目录的有界数量，不复制实例和地址。
type DiscoveryDirectorySnapshot struct {
	Version  uint64 `json:"version"`
	Nodes    int    `json:"nodes"`
	Services int    `json:"services"`
	Running  int    `json:"running"`
	Retired  int    `json:"retired"`
}

// RPCSnapshot 按固定传输类别保存一个 Node 的 RPC 汇总。
type RPCSnapshot struct {
	Local RPCTransportSnapshot `json:"local"`
	TCP   RPCTransportSnapshot `json:"tcp"`
	NATS  RPCTransportSnapshot `json:"nats"`
}

// RPCTransportSnapshot 保存一类 RPC Transport 的固定、低基数累计值。
type RPCTransportSnapshot struct {
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
	Reconnects           uint64 `json:"reconnects"`
	ConsecutiveFailures  uint64 `json:"consecutive_failures"`
}

// ServiceSnapshot 保存一个本地 Service 的生命周期和现有三个叶子统计。
type ServiceSnapshot struct {
	ServiceName string            `json:"service_name"`
	State       string            `json:"state"`
	EnteredAt   time.Time         `json:"entered_at"`
	ErrorCode   errs.Code         `json:"error_code"`
	Execution   ExecutionSnapshot `json:"execution"`
	Timer       TimerSnapshot     `json:"timer"`
	Event       EventSnapshot     `json:"event"`
}

// ExecutionSnapshot 保存 Service Scheduler 当前容量和累计结果。
type ExecutionSnapshot struct {
	Accepted              int    `json:"accepted"`
	Ready                 int    `json:"ready"`
	Running               int    `json:"running"`
	Awaiting              int    `json:"awaiting"`
	AcceptedHighWatermark int    `json:"accepted_high_watermark"`
	DispatchedTotal       uint64 `json:"dispatched_total"`
	CompletedTotal        uint64 `json:"completed_total"`
	RejectedTotal         uint64 `json:"rejected_total"`
	AwaitTotal            uint64 `json:"await_total"`
	AwaitCanceledTotal    uint64 `json:"await_canceled_total"`
	AwaitTimeoutTotal     uint64 `json:"await_timeout_total"`
	PanicTotal            uint64 `json:"panic_total"`
}

// TimerSnapshot 保存 Service 业务 Timer 当前容量、累计结果和就绪延迟。
type TimerSnapshot struct {
	Active                  int      `json:"active"`
	Scheduled               int      `json:"scheduled"`
	DuePending              int      `json:"due_pending"`
	Ready                   int      `json:"ready"`
	Running                 int      `json:"running"`
	Paused                  int      `json:"paused"`
	ActiveHighWatermark     int      `json:"active_high_watermark"`
	CreatedTotal            uint64   `json:"created_total"`
	RejectedTotal           uint64   `json:"rejected_total"`
	TriggeredTotal          uint64   `json:"triggered_total"`
	CompletedTotal          uint64   `json:"completed_total"`
	CanceledTotal           uint64   `json:"canceled_total"`
	PausedTotal             uint64   `json:"paused_total"`
	ResumedTotal            uint64   `json:"resumed_total"`
	CoalescedTotal          uint64   `json:"coalesced_total"`
	PanicTotal              uint64   `json:"panic_total"`
	PanicLimitCanceledTotal uint64   `json:"panic_limit_canceled_total"`
	LastReadyDelay          Duration `json:"last_ready_delay"`
	MaxReadyDelay           Duration `json:"max_ready_delay"`
}

// EventSnapshot 保存 Service 本地同步和异步事件累计结果。
type EventSnapshot struct {
	SyncNotifiedTotal   uint64 `json:"sync_notified_total"`
	AsyncNotifiedTotal  uint64 `json:"async_notified_total"`
	HandlerFailureTotal uint64 `json:"handler_failure_total"`
}
