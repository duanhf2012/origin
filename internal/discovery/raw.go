// Package discovery 实现 Node 私有的服务发现原始快照、筛选和不可变查询目录。
//
// 本包位于 internal 下，业务代码只能使用公开 discovery 包的只读 DTO，不能提交或修改
// 原始发现事实。
package discovery

// Transport 表示远端 Node 对业务 RPC 使用的传输类型。
type Transport uint8

const (
	// TransportNone 表示该 Node 没有可供远端 RPC 使用的业务传输。
	TransportNone Transport = iota
	// TransportTCP 表示该 Node 使用 Origin TCP RPC。
	TransportTCP
	// TransportNATS 表示通过 NATS 建立远端 RPC 连接。
	TransportNATS
)

// ServiceState 是发现目录需要传播的最小公开运行状态。
type ServiceState uint8

const (
	// ServiceStateUnknown 只作为零值和非法输入防护，不能进入有效快照。
	ServiceStateUnknown ServiceState = iota
	// ServiceStateRunning 表示 Service 正常提供业务能力。
	ServiceStateRunning
	// ServiceStateRetired 表示 Service 处于可观察的退休状态，普通 RPC 和其他业务仍按
	// Running 规则执行；是否拒绝具体操作由业务自行决定。
	ServiceStateRetired
)

// RawSnapshot 是一个发现数据源当前确认的完整公开 Node 集合。
//
// ApplySnapshot 会复制其中全部可变容器；调用方可以在返回后安全复用自己的 Slice 和 Map。
type RawSnapshot struct {
	Nodes []RawNode
}

// RawNode 描述一个 Node 进程会话及其全部公开 Service。
type RawNode struct {
	NodeID    string
	SessionID uint64
	Labels    map[string]string
	Transport Transport
	Address   string
	Services  []RawService
}

// RawService 描述一个公开 Service 的发现状态和可选 RPC 契约。
type RawService struct {
	ServiceName         string
	State               ServiceState
	ContractID          uint64
	ContractFingerprint [32]byte
}
