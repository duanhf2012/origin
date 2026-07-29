package provider

import "github.com/duanhf2012/origin/v3/errs"

// Transport 表示远端 Node 对业务 RPC 使用的传输。
type Transport uint8

const (
	// TransportInvalid 是非法零值。
	TransportInvalid Transport = iota
	// TransportNone 表示没有远端 RPC 传输。
	TransportNone
	// TransportTCP 表示 Origin TCP RPC。
	TransportTCP
	// TransportNATS 表示 Origin NATS RPC。
	TransportNATS
)

// ServiceState 是发现记录携带的公开 Service 状态。
type ServiceState uint8

const (
	// ServiceStateInvalid 是非法零值。
	ServiceStateInvalid ServiceState = iota
	// ServiceStateRunning 表示正常提供服务。
	ServiceStateRunning
	// ServiceStateRetired 表示已退休但仍可按业务规则处理普通 RPC。
	ServiceStateRetired
)

// State 是 Provider 当前连接与同步状态。
type State uint8

const (
	// StateStarting 表示正在完成首次连接和同步。
	StateStarting State = iota
	// StateReady 表示权威来源和本地镜像可用。
	StateReady
	// StateRecovering 表示暂时失去权威来源并正在恢复。
	StateRecovering
	// StateStopped 表示 Provider 已经停止。
	StateStopped
)

// Snapshot 是 Provider 当前确认的完整远端 Node 集合。
type Snapshot struct {
	Nodes []Node
}

// Node 描述一个远端 Node 进程会话及其全部公开 Service。
type Node struct {
	NodeID    string
	SessionID uint64
	Labels    map[string]string
	Transport Transport
	Address   string
	Services  []Service
}

// Service 描述一个公开 Service 和可选 RPC 契约。
type Service struct {
	ServiceName         string
	State               ServiceState
	ContractID          uint64
	ContractFingerprint [32]byte
}

// Report 是 Provider 提交给框架的紧凑健康事实。
type Report struct {
	State               State
	Reconnects          uint64
	ConsecutiveFailures uint32
	ErrorCode           errs.Code
}
