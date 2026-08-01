package node

import (
	"github.com/duanhf2012/origin/v3/diagnostics"
	"github.com/duanhf2012/origin/v3/errs"
	internaldiscovery "github.com/duanhf2012/origin/v3/internal/discovery"
	"github.com/duanhf2012/origin/v3/rpc"
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
	result := diagnostics.DiscoveryDirectorySnapshot{
		Version:  stats.Version,
		Nodes:    stats.Nodes,
		Services: stats.Services,
	}
	// All 返回同一不可变快照的内部只读 Slice；这里只累计状态，不复制地址、标签或实例。
	for _, instance := range directory.All() {
		switch instance.State {
		case internaldiscovery.ServiceStateRunning:
			result.Running++
		case internaldiscovery.ServiceStateRetired:
			result.Retired++
		}
	}
	return result
}

func mapRPCStats(stats rpc.Stats) diagnostics.RPCSnapshot {
	return diagnostics.RPCSnapshot{
		Local: mapRPCTransportStats(stats.Local),
		TCP:   mapRPCTransportStats(stats.TCP),
		NATS:  mapRPCTransportStats(stats.NATS),
	}
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
