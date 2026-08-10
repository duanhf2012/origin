package node

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/duanhf2012/origin/v3/errs"
	originlog "github.com/duanhf2012/origin/v3/log"
	"github.com/duanhf2012/origin/v3/rpc"
	"github.com/duanhf2012/origin/v3/service"
)

// TransportKind 表示当前 Node 使用的远程 RPC 传输类型。
type TransportKind uint8

const (
	// TransportNone 表示当前 Node 只提供本地 RPC。
	TransportNone TransportKind = iota
	// TransportTCP 表示当前 Node 使用 Origin TCP RPC。
	TransportTCP
	// TransportNATS 表示当前 Node 使用 NATS RPC。
	TransportNATS
)

// TransportState 表示当前 Node 整体入站 RPC 能力。
type TransportState uint8

const (
	// TransportDisabled 表示没有配置远程 Transport。
	TransportDisabled TransportState = iota
	// TransportStarting 表示 Transport 正在完成首次启动。
	TransportStarting
	// TransportReady 表示 Transport 可以接收新入站 RPC。
	TransportReady
	// TransportRecovering 表示整体入站能力暂时不可用且正在恢复。
	TransportRecovering
	// TransportFailed 表示 Transport 内部状态无法安全恢复。
	TransportFailed
	// TransportStopping 表示正式 Stop 已经关闭新入站准入。
	TransportStopping
	// TransportStopped 表示 Transport 资源已经全部回收。
	TransportStopped
)

// TransportStatus 是 Node 当前远程 RPC 入站能力的无锁只读快照。
type TransportStatus struct {
	Kind                TransportKind
	State               TransportState
	Reconnects          uint64
	ConsecutiveFailures uint64
	ErrorCode           errs.Code
}

// HealthStatus 是探针和运维适配器需要的最小 Node 健康快照。
type HealthStatus struct {
	Liveness  bool
	Readiness bool
	Degraded  bool
	ErrorCode errs.Code
}

// DiscoveryState 表示当前 Node 的 Provider 同步状态。
type DiscoveryState uint8

const (
	// DiscoveryStarting 表示正在完成首次同步。
	DiscoveryStarting DiscoveryState = iota
	// DiscoveryReady 表示当前拥有可用权威快照。
	DiscoveryReady
	// DiscoveryRecovering 表示失去权威来源且正在持续恢复。
	DiscoveryRecovering
	// DiscoveryStopped 表示 Provider 已经退出。
	DiscoveryStopped
)

// PublicationState 表示当前 Node 的公开记录发布屏障。
type PublicationState uint8

const (
	// PublicationNotRequired 表示私有或没有公开 Service，无需发布。
	PublicationNotRequired PublicationState = iota
	// PublicationPending 表示完整本地记录尚未被后端确认。
	PublicationPending
	// PublicationPublished 表示当前完整本地记录已经确认发布。
	PublicationPublished
)

// DiscoveryStatus 是 Node 当前服务发现状态的无锁只读快照。
type DiscoveryStatus struct {
	Kind                string
	State               DiscoveryState
	Synchronized        bool
	Publication         PublicationState
	Reconnects          uint64
	ConsecutiveFailures uint32
	ErrorCode           errs.Code
}

// ServiceStatus 是本地管理代码查询单个 Service 时取得的冷路径快照。
type ServiceStatus struct {
	State     service.State
	EnteredAt time.Time
	Failure   error
}

// transportStatusSnapshot 和 healthStatusSnapshot 让一次更新原子发布完整结构。
//
// 更新只发生在生命周期或故障冷路径；查询只执行一次原子指针读取和结构复制。
type transportStatusSnapshot struct {
	value TransportStatus
}

type healthStatusSnapshot struct {
	value HealthStatus
}

type discoveryStatusSnapshot struct {
	value DiscoveryStatus
}

// serviceFailureSnapshot 保存本进程内第一个不可恢复根因。
type serviceFailureSnapshot struct {
	cause error
}

// TransportStatus 返回当前 Node 整体入站 RPC 状态。
func (node *Node) TransportStatus() TransportStatus {
	if node == nil {
		return TransportStatus{
			State:     TransportFailed,
			ErrorCode: errs.CodeInternal,
		}
	}
	snapshot := node.transportStatus.Load()
	if snapshot == nil {
		return TransportStatus{}
	}
	return snapshot.value
}

// HealthStatus 返回当前 Node 的存活、就绪和降级状态。
func (node *Node) HealthStatus() HealthStatus {
	if node == nil {
		return HealthStatus{
			Degraded:  true,
			ErrorCode: errs.CodeInternal,
		}
	}
	snapshot := node.healthStatus.Load()
	if snapshot == nil {
		return HealthStatus{}
	}
	return snapshot.value
}

// DiscoveryStatus 返回当前 Provider 同步和发布状态。
func (node *Node) DiscoveryStatus() DiscoveryStatus {
	if node == nil {
		return DiscoveryStatus{
			State:     DiscoveryStopped,
			ErrorCode: errs.CodeInternal,
		}
	}
	snapshot := node.discoveryStatus.Load()
	if snapshot == nil {
		return DiscoveryStatus{
			State:       DiscoveryStopped,
			Publication: PublicationNotRequired,
		}
	}
	return snapshot.value
}

// ServiceStatus 按实际 ServiceName 返回本地生命周期和首个失败根因。
func (node *Node) ServiceStatus(name string) (ServiceStatus, bool) {
	if node == nil || name == "" {
		return ServiceStatus{}, false
	}
	entry, exists := node.byName[name]
	if !exists {
		return ServiceStatus{}, false
	}
	state := entry.loadState()
	return ServiceStatus{
		State:     state.State,
		EnteredAt: state.EnteredAt,
		Failure:   entry.failureCause(),
	}, true
}

// initializeStatus 在 Node 完成静态装配后发布第一份不含 nil 的状态快照。
func (node *Node) initializeStatus(kind rpc.TransportKind) {
	node.transportStatus.Store(&transportStatusSnapshot{
		value: TransportStatus{
			Kind:  mapTransportKind(kind),
			State: initialTransportState(kind),
		},
	})
	node.discoveryAvailable.Store(true)
	if node.discoveryProvider == nil {
		node.discoveryStatus.Store(&discoveryStatusSnapshot{
			value: DiscoveryStatus{
				Kind:         "none",
				State:        DiscoveryStopped,
				Publication:  PublicationNotRequired,
				Synchronized: true,
			},
		})
	}
	node.refreshHealth()
}

// handleTransportEvent 把 RPC 内部事件转换为 Node 公开快照和发现发布动作。
func (node *Node) handleTransportEvent(event rpc.TransportEvent) {
	if node == nil {
		return
	}
	status := TransportStatus{
		Kind:                mapTransportKind(event.Kind),
		State:               mapTransportState(event.State),
		Reconnects:          event.Reconnects,
		ConsecutiveFailures: event.ConsecutiveFailures,
		ErrorCode:           event.ErrorCode,
	}
	node.transportStatus.Store(&transportStatusSnapshot{value: status})

	// 整体入站能力不可用时立即撤销当前 Node；单目标 TCP 断线不会产生这里的整体事件。
	switch status.State {
	case TransportRecovering, TransportFailed:
		operationCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err := node.requestDiscoveryPublication(operationCtx)
		cancel()
		if err != nil {
			node.logger.Error(
				"Transport 不可用时撤销服务发现失败",
				originlog.Err(err),
			)
		}
	case TransportReady:
		// 初次启动尚未越过统一就绪屏障，因此只在已经 Ready 的 Node 上重新发布。
		if node.State() == StateReady {
			operationCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			err := node.requestDiscoveryPublication(operationCtx)
			cancel()
			if err != nil {
				node.logger.Error(
					"Transport 恢复后重新发布服务发现失败",
					originlog.Err(err),
				)
			}
		}
	}
	node.refreshHealth()
}

// updateDiscoveryAvailable 提交外部 Provider 和进程内发现源共用的健康输入。
func (node *Node) updateDiscoveryAvailable(available bool) {
	if node == nil {
		return
	}
	node.discoveryAvailable.Store(available)
	node.refreshHealth()
}

// refreshHealth 根据生命周期、Service、Transport 和 Discovery 固定优先级发布新快照。
func (node *Node) refreshHealth() {
	if node == nil {
		return
	}
	status := HealthStatus{}
	switch node.State() {
	case StateCreated:
		status.ErrorCode = errs.CodeServiceNotReady
	case StateStarting:
		status.Liveness = true
		status.ErrorCode = errs.CodeServiceNotReady
	case StateStopping:
		status.Liveness = true
		status.ErrorCode = errs.CodeServiceStopping
	case StateStopped:
		status.ErrorCode = errs.CodeServiceStopped
	case StateFailed:
		status.Degraded = true
		status.ErrorCode = node.failureCode()
	default:
		status = node.readyHealth()
	}
	node.healthStatus.Store(&healthStatusSnapshot{value: status})
}

// readyHealth 计算 StateReady 下的综合状态。
func (node *Node) readyHealth() HealthStatus {
	publicFailed := 0
	for _, entry := range node.services {
		if entry.private || node.private {
			continue
		}
		if entry.loadState().State == service.StateFailed {
			publicFailed++
		}
	}
	if node.publicServices > 0 && publicFailed == node.publicServices {
		return HealthStatus{
			Liveness:  true,
			Degraded:  true,
			ErrorCode: errs.CodeServiceFailed,
		}
	}

	transport := node.TransportStatus()
	if transport.State == TransportRecovering || transport.State == TransportFailed {
		return HealthStatus{
			Liveness:  true,
			Degraded:  true,
			ErrorCode: errs.CodeTransportUnavailable,
		}
	}
	if !node.discoveryAvailable.Load() {
		return HealthStatus{
			Liveness:  true,
			Degraded:  true,
			ErrorCode: errs.CodeDiscoveryUnavailable,
		}
	}
	if publicFailed > 0 {
		return HealthStatus{
			Liveness:  true,
			Readiness: true,
			Degraded:  true,
			ErrorCode: errs.CodeServiceFailed,
		}
	}
	return HealthStatus{
		Liveness:  true,
		Readiness: true,
	}
}

// failureCode 返回启动失败时可以公开的稳定错误码。
func (node *Node) failureCode() errs.Code {
	if node == nil {
		return errs.CodeInternal
	}
	for _, entry := range node.services {
		if cause := entry.failureCause(); cause != nil {
			return errs.CodeOf(cause)
		}
	}
	return errs.CodeInternal
}

// serviceFailureResult 按配置顺序聚合当前 Node 已经隔离的 Service 摘要。
func (node *Node) serviceFailureResult() error {
	if node == nil {
		return nil
	}
	failures := make([]error, 0)
	for _, entry := range node.services {
		if cause := entry.failureCause(); cause != nil {
			failures = append(failures, errs.Wrap(
				errs.CodeServiceFailed,
				fmt.Errorf("Service %q failed: %w", entry.name, cause),
			))
		}
	}
	return errors.Join(failures...)
}

// failureCause 返回当前 Service 首个失败根因。
func (entry *serviceEntry) failureCause() error {
	if entry == nil {
		return nil
	}
	snapshot := entry.failure.Load()
	if snapshot == nil {
		return nil
	}
	return snapshot.cause
}

// recordFailure 只保存首个根因；后续清理错误通过日志和最终 errors.Join 单独保留。
func (entry *serviceEntry) recordFailure(cause error) bool {
	if entry == nil || cause == nil {
		return false
	}
	return entry.failure.CompareAndSwap(nil, &serviceFailureSnapshot{cause: cause})
}

// recordServiceFailure 隔离一个无法证明调度状态安全的 Service。
//
// 该路径不执行 Stop，也不改变 Node/Application 生命周期；它只拒绝该 Service 的后续工作、
// 更新发现和健康快照，并把首个安全摘要交给 Application 留待正式 Stop 后汇总。
func (node *Node) recordServiceFailure(entry *serviceEntry, cause error) {
	if node == nil || entry == nil || cause == nil || !entry.recordFailure(cause) {
		return
	}
	entry.setState(service.StateFailed)

	// Ready Node 只通过唯一发布协调器更新；它会在串行边界内重建最新完整快照。
	if node.State() == StateReady {
		operationCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err := node.requestDiscoveryPublication(operationCtx)
		cancel()
		if err != nil {
			node.logger.Error(
				"隔离 Service 后更新服务发现失败",
				originlog.String("service_name", entry.name),
				originlog.Err(err),
			)
		}
	}
	node.refreshHealth()
	if node.serviceFailure != nil {
		node.serviceFailure(node.id, entry.name, cause)
	}
}

func mapTransportKind(kind rpc.TransportKind) TransportKind {
	switch kind {
	case rpc.TransportKindTCP:
		return TransportTCP
	case rpc.TransportKindNATS:
		return TransportNATS
	default:
		return TransportNone
	}
}

func initialTransportState(kind rpc.TransportKind) TransportState {
	if kind == rpc.TransportKindNone {
		return TransportDisabled
	}
	return TransportStarting
}

func mapTransportState(state rpc.TransportState) TransportState {
	switch state {
	case rpc.TransportStateStarting:
		return TransportStarting
	case rpc.TransportStateReady:
		return TransportReady
	case rpc.TransportStateRecovering:
		return TransportRecovering
	case rpc.TransportStateFailed:
		return TransportFailed
	case rpc.TransportStateStopping:
		return TransportStopping
	case rpc.TransportStateStopped:
		return TransportStopped
	default:
		return TransportDisabled
	}
}
