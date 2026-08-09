package node

import (
	"github.com/duanhf2012/origin/v3/diagnostics"
	"github.com/duanhf2012/origin/v3/errs"
	internaldiscovery "github.com/duanhf2012/origin/v3/internal/discovery"
	"github.com/duanhf2012/origin/v3/rpc"
	"github.com/duanhf2012/origin/v3/service"
)

// Diagnostics 返回当前 Node 拥有的独立只读诊断 DTO。
//
// Node 和 Service 的静态 Slice 在装配后不再改变，因此采集不需要全局锁；每个可变叶子
// 通过自身原子快照或统计锁保证局部一致。调用方可以安全修改返回 Slice。
func (node *Node) Diagnostics() diagnostics.NodeSnapshot {
	if node == nil {
		return diagnostics.NodeSnapshot{
			State: "failed",
			Health: diagnostics.HealthSnapshot{
				Degraded:  true,
				ErrorCode: errs.CodeInternal,
			},
			Services: make([]diagnostics.ServiceSnapshot, 0),
		}
	}

	result := diagnostics.NodeSnapshot{
		NodeID:    node.id,
		State:     nodeStateText(node.State()),
		Health:    mapHealthStatus(node.HealthStatus()),
		Transport: mapTransportStatus(node.TransportStatus()),
		Discovery: mapDiscoveryStatus(node.DiscoveryStatus()),
		Services:  make([]diagnostics.ServiceSnapshot, len(node.services)),
	}
	if node.rpcRuntime != nil {
		result.RPC = mapRPCStats(node.rpcRuntime.Stats())
	}
	if node.discovery != nil && node.discovery.directory != nil {
		result.Directory = mapDirectory(node.discovery.directory)
	}
	for index, entry := range node.services {
		result.Services[index] = mapService(entry)
	}
	return result
}

// DiagnosticsSummary 返回当前 Node 的固定大小监控摘要。
//
// 静态 services Slice 在装配后保持只读，因此本方法直接遍历每个 Service 的并发安全叶子
// 快照并累加到一个 ServiceAggregate，不调用 Full Diagnostics，也不建立中间 Service Slice。
func (node *Node) DiagnosticsSummary() diagnostics.NodeSummary {
	if node == nil {
		return diagnostics.NodeSummary{
			State: "failed",
			Health: diagnostics.HealthSnapshot{
				Degraded:  true,
				ErrorCode: errs.CodeInternal,
			},
		}
	}

	result := diagnostics.NodeSummary{
		NodeID:    node.id,
		State:     nodeStateText(node.State()),
		Health:    mapHealthStatus(node.HealthStatus()),
		Transport: mapTransportStatus(node.TransportStatus()),
		Discovery: mapDiscoveryStatus(node.DiscoveryStatus()),
	}
	if node.rpcRuntime != nil {
		result.RPC = mapRPCSummary(node.rpcRuntime.Stats())
	}
	if node.discovery != nil && node.discovery.directory != nil {
		result.Directory = mapDirectory(node.discovery.directory)
	}
	for _, entry := range node.services {
		state := entry.loadState()
		aggregateService(
			&result.Services,
			state.State,
			entry.instance.ExecutionStats(),
			entry.instance.TimerStats(),
			entry.instance.EventStats(),
		)
	}
	return result
}

// aggregateService 把一个 Service 的四个独立叶子累加进 Node 唯一聚合对象。
func aggregateService(
	result *diagnostics.ServiceAggregate,
	state service.State,
	execution service.ExecutionStats,
	timer service.TimerStats,
	event service.EventStats,
) {
	result.Total++
	switch state {
	case service.StateRunning:
		result.States.Running++
	case service.StateRetired:
		result.States.Retired++
	case service.StateFailed:
		result.States.Failed++
	default:
		result.States.Unknown++
	}

	result.Execution.Accepted += execution.Accepted
	result.Execution.Ready += execution.Ready
	result.Execution.Running += execution.Running
	result.Execution.Awaiting += execution.Awaiting
	result.Execution.DispatchedTotal += execution.DispatchedTotal
	result.Execution.CompletedTotal += execution.CompletedTotal
	result.Execution.RejectedTotal += execution.RejectedTotal
	result.Execution.AwaitTimeoutTotal += execution.AwaitTimeoutTotal
	result.Execution.PanicTotal += execution.PanicTotal

	result.Timer.Active += timer.Active
	result.Timer.DuePending += timer.DuePending
	result.Timer.Ready += timer.Ready
	result.Timer.Running += timer.Running
	result.Timer.TriggeredTotal += timer.TriggeredTotal
	result.Timer.CompletedTotal += timer.CompletedTotal
	result.Timer.RejectedTotal += timer.RejectedTotal
	result.Timer.PanicTotal += timer.PanicTotal
	if diagnostics.Duration(timer.MaxReadyDelay) > result.Timer.MaxReadyDelay {
		result.Timer.MaxReadyDelay = diagnostics.Duration(timer.MaxReadyDelay)
	}

	result.Event.SyncNotifiedTotal += event.SyncNotifiedTotal
	result.Event.AsyncNotifiedTotal += event.AsyncNotifiedTotal
	result.Event.HandlerFailureTotal += event.HandlerFailureTotal
}

func mapHealthStatus(status HealthStatus) diagnostics.HealthSnapshot {
	return diagnostics.HealthSnapshot{
		Liveness:  status.Liveness,
		Readiness: status.Readiness,
		Degraded:  status.Degraded,
		ErrorCode: status.ErrorCode,
	}
}

func mapTransportStatus(status TransportStatus) diagnostics.TransportSnapshot {
	return diagnostics.TransportSnapshot{
		Kind:                transportKindText(status.Kind),
		State:               transportStateText(status.State),
		Reconnects:          status.Reconnects,
		ConsecutiveFailures: status.ConsecutiveFailures,
		ErrorCode:           status.ErrorCode,
	}
}

func mapDiscoveryStatus(status DiscoveryStatus) diagnostics.DiscoverySnapshot {
	return diagnostics.DiscoverySnapshot{
		Kind:                status.Kind,
		State:               discoveryStateText(status.State),
		Synchronized:        status.Synchronized,
		Publication:         publicationStateText(status.Publication),
		Reconnects:          status.Reconnects,
		ConsecutiveFailures: status.ConsecutiveFailures,
		ErrorCode:           status.ErrorCode,
	}
}

func mapDirectory(directory *internaldiscovery.Directory) diagnostics.DiscoveryDirectorySnapshot {
	stats := directory.Stats()
	return diagnostics.DiscoveryDirectorySnapshot{
		Version:  stats.Version,
		Nodes:    stats.Nodes,
		Services: stats.Services,
		Running:  stats.Running,
		Retired:  stats.Retired,
	}
}

func mapRPCStats(stats rpc.Stats) diagnostics.RPCSnapshot {
	return diagnostics.RPCSnapshot{
		Local: mapRPCTransportStats(stats.Local),
		TCP:   mapRPCTransportStats(stats.TCP),
		NATS:  mapRPCTransportStats(stats.NATS),
	}
}

func mapRPCSummary(stats rpc.Stats) diagnostics.RPCSummary {
	var result diagnostics.RPCSummary
	aggregateRPCSummary(&result, stats.Local)
	aggregateRPCSummary(&result, stats.TCP)
	aggregateRPCSummary(&result, stats.NATS)
	return result
}

func aggregateRPCSummary(result *diagnostics.RPCSummary, stats rpc.TransportStats) {
	result.Pending += stats.Pending
	if stats.PendingHighWater > result.PendingHighWater {
		result.PendingHighWater = stats.PendingHighWater
	}
	result.OutboundCompleted += stats.OutboundCompleted
	result.OutboundFailed += stats.OutboundFailed
	result.OutboundTimeout += stats.OutboundTimeout
	result.OutboundRejected += stats.OutboundRejected
	result.InboundCompleted += stats.InboundCompleted
	result.InboundFailed += stats.InboundFailed
	result.InboundTimeout += stats.InboundTimeout
	result.InboundRejected += stats.InboundRejected
	result.PayloadSentBytes += stats.PayloadSentBytes
	result.PayloadReceivedBytes += stats.PayloadReceivedBytes
}

func mapRPCTransportStats(stats rpc.TransportStats) diagnostics.RPCTransportSnapshot {
	return diagnostics.RPCTransportSnapshot{
		Pending:              stats.Pending,
		PendingHighWater:     stats.PendingHighWater,
		OutboundAccepted:     stats.OutboundAccepted,
		OutboundCompleted:    stats.OutboundCompleted,
		OutboundFailed:       stats.OutboundFailed,
		OutboundTimeout:      stats.OutboundTimeout,
		OutboundRejected:     stats.OutboundRejected,
		InboundAccepted:      stats.InboundAccepted,
		InboundCompleted:     stats.InboundCompleted,
		InboundFailed:        stats.InboundFailed,
		InboundTimeout:       stats.InboundTimeout,
		InboundRejected:      stats.InboundRejected,
		PayloadSentBytes:     stats.PayloadSentBytes,
		PayloadReceivedBytes: stats.PayloadReceivedBytes,
		Reconnects:           stats.Reconnects,
		ConsecutiveFailures:  stats.ConsecutiveFailures,
	}
}

func mapService(entry *serviceEntry) diagnostics.ServiceSnapshot {
	state := entry.loadState()
	execution := entry.instance.ExecutionStats()
	timer := entry.instance.TimerStats()
	event := entry.instance.EventStats()
	return diagnostics.ServiceSnapshot{
		ServiceName: entry.name,
		State:       state.State.String(),
		EnteredAt:   state.EnteredAt,
		ErrorCode:   errs.CodeOf(entry.failureCause()),
		Execution: diagnostics.ExecutionSnapshot{
			Accepted:              execution.Accepted,
			Ready:                 execution.Ready,
			Running:               execution.Running,
			Awaiting:              execution.Awaiting,
			AcceptedHighWatermark: execution.AcceptedHighWatermark,
			DispatchedTotal:       execution.DispatchedTotal,
			CompletedTotal:        execution.CompletedTotal,
			RejectedTotal:         execution.RejectedTotal,
			AwaitTotal:            execution.AwaitTotal,
			AwaitCanceledTotal:    execution.AwaitCanceledTotal,
			AwaitTimeoutTotal:     execution.AwaitTimeoutTotal,
			PanicTotal:            execution.PanicTotal,
		},
		Timer: diagnostics.TimerSnapshot{
			Active:                  timer.Active,
			Scheduled:               timer.Scheduled,
			DuePending:              timer.DuePending,
			Ready:                   timer.Ready,
			Running:                 timer.Running,
			Paused:                  timer.Paused,
			ActiveHighWatermark:     timer.ActiveHighWatermark,
			CreatedTotal:            timer.CreatedTotal,
			RejectedTotal:           timer.RejectedTotal,
			TriggeredTotal:          timer.TriggeredTotal,
			CompletedTotal:          timer.CompletedTotal,
			CanceledTotal:           timer.CanceledTotal,
			PausedTotal:             timer.PausedTotal,
			ResumedTotal:            timer.ResumedTotal,
			CoalescedTotal:          timer.CoalescedTotal,
			PanicTotal:              timer.PanicTotal,
			PanicLimitCanceledTotal: timer.PanicLimitCanceledTotal,
			LastReadyDelay:          diagnostics.Duration(timer.LastReadyDelay),
			MaxReadyDelay:           diagnostics.Duration(timer.MaxReadyDelay),
		},
		Event: diagnostics.EventSnapshot{
			SyncNotifiedTotal:   event.SyncNotifiedTotal,
			AsyncNotifiedTotal:  event.AsyncNotifiedTotal,
			HandlerFailureTotal: event.HandlerFailureTotal,
		},
	}
}

func nodeStateText(state State) string {
	switch state {
	case StateCreated:
		return "created"
	case StateStarting:
		return "starting"
	case StateReady:
		return "ready"
	case StateStopping:
		return "stopping"
	case StateStopped:
		return "stopped"
	case StateFailed:
		return "failed"
	default:
		return "unknown"
	}
}

func transportKindText(kind TransportKind) string {
	switch kind {
	case TransportTCP:
		return "tcp"
	case TransportNATS:
		return "nats"
	default:
		return "none"
	}
}

func transportStateText(state TransportState) string {
	switch state {
	case TransportDisabled:
		return "disabled"
	case TransportStarting:
		return "starting"
	case TransportReady:
		return "ready"
	case TransportRecovering:
		return "recovering"
	case TransportFailed:
		return "failed"
	case TransportStopping:
		return "stopping"
	case TransportStopped:
		return "stopped"
	default:
		return "unknown"
	}
}

func discoveryStateText(state DiscoveryState) string {
	switch state {
	case DiscoveryStarting:
		return "starting"
	case DiscoveryReady:
		return "ready"
	case DiscoveryRecovering:
		return "recovering"
	case DiscoveryStopped:
		return "stopped"
	default:
		return "unknown"
	}
}

func publicationStateText(state PublicationState) string {
	switch state {
	case PublicationNotRequired:
		return "not_required"
	case PublicationPending:
		return "pending"
	case PublicationPublished:
		return "published"
	default:
		return "unknown"
	}
}
