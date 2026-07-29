package natsnet

// Status 表示包装层对外公开的 NATS Connection 生命周期状态。
type Status uint8

const (
	// StatusConnecting 表示初始连接尚未完成。
	StatusConnecting Status = iota
	// StatusConnected 表示当前已经连接到一个 NATS Server。
	StatusConnected
	// StatusReconnecting 表示已连接过但当前正在有限自动重连。
	StatusReconnecting
	// StatusDraining 表示已停止准入并正在排空订阅和 Publish。
	StatusDraining
	// StatusClosed 表示连接已经进入不会自行恢复的终态。
	StatusClosed
)

// EventType 表示 NATS 基础设施生命周期或异步异常事件。
type EventType uint8

const (
	// EventConnected 表示初始连接成功。
	EventConnected EventType = iota
	// EventDisconnected 表示当前 Server 连接断开。
	EventDisconnected
	// EventReconnected 表示官方客户端已经连接到可用 Server。
	EventReconnected
	// EventLameDuck 表示当前 Server 正在优雅退出。
	EventLameDuck
	// EventAsyncError 表示慢消费者、权限或 Handler panic 等异步错误。
	EventAsyncError
	// EventClosed 表示当前 Conn 已经进入最终关闭状态。
	EventClosed
)

// Event 是交给内部连接管理层的轻量基础设施事件。
type Event struct {
	// Type 标识事件类型。
	Type EventType
	// URL 是移除认证信息和 Query 后的 Server 地址。
	URL string
	// Subject 是异步错误关联的可选 Subject。
	Subject string
	// Err 是已经映射到 Origin 错误码且不包含秘密的可选原因。
	Err error
}

// EventHandler 接收低频连接和异步错误事件。
//
// Handler 必须快速返回，不能直接执行 Service 业务逻辑。panic 会被 natsnet 恢复并记录，
// 不会破坏 nats.go 的异步回调调度器。
type EventHandler func(event Event)

// ConnStats 是官方客户端累计连接统计的稳定快照。
type ConnStats struct {
	// InMessages 是收到的 Core NATS 消息总数。
	InMessages uint64
	// OutMessages 是发布的 Core NATS 消息总数。
	OutMessages uint64
	// InBytes 是收到的 payload 总字节数。
	InBytes uint64
	// OutBytes 是发布的 payload 总字节数。
	OutBytes uint64
	// Reconnects 是成功重连次数。
	Reconnects uint64
}

// SubscriptionStats 是一条异步订阅当前 Pending 和累计丢弃统计。
type SubscriptionStats struct {
	// PendingMessages 是尚未完成回调的消息数。
	PendingMessages int
	// DroppedMessages 是因 Pending 上限而丢弃的累计消息数。
	DroppedMessages int
}
