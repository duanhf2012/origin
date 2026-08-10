package network

import "time"

// SessionStats 是单条 Session 在某一时刻的固定字段统计快照。
type SessionStats struct {
	// ReceivedMessages 是已经完整读取的逻辑消息数。
	ReceivedMessages uint64
	// ReceivedBytes 是已经完整读取的逻辑 Payload 字节数。
	ReceivedBytes uint64
	// SentMessages 是已经完整写出的逻辑消息数。
	SentMessages uint64
	// SentBytes 是已经完整写出的逻辑 Payload 字节数。
	SentBytes uint64
	// ReceivePendingMessages 是已经提交但 Handler 尚未返回的消息数。
	ReceivePendingMessages int
	// ReceivePendingSize 是当前待处理 Buffer 的保留容量。
	ReceivePendingSize int64
	// SendQueueMessages 是当前等待 Writer 取得的消息数。
	SendQueueMessages int
	// SendQueueSize 是当前等待 Writer 取得的 Payload 保留容量。
	SendQueueSize int64
	// Writable 是当前发送高低水位状态。
	Writable bool
}

// EndpointStats 是 Server、Client 或 Dialer Runtime 的聚合统计快照。
type EndpointStats struct {
	// ActiveSessions 是当前尚未完成 Close 的 Session 数。
	ActiveSessions int
	// OpenedSessions 是当前 Runtime 累计成功建立的 Session 数。
	OpenedSessions uint64
	// ClosedSessions 是当前 Runtime 累计完成 Close 的 Session 数。
	ClosedSessions uint64
	// RejectedSessions 是因连接或运行容量拒绝的 Session 数。
	RejectedSessions uint64
	// ReceivePendingSize 是当前全部入站任务持有的 Buffer 容量。
	ReceivePendingSize int64
	// ReceivePendingHighWatermark 是入站总容量历史峰值。
	ReceivePendingHighWatermark int64
	// SendQueueSize 是当前排队及正在写出的 Payload 容量。
	SendQueueSize int64
	// SendQueueHighWatermark 是出站总容量历史峰值。
	SendQueueHighWatermark int64
	// ReceiveOverloads 是入站 Session/Module/Scheduler 过载次数。
	ReceiveOverloads uint64
	// SendOverloads 是 Send 因本地有界容量被拒绝的次数。
	SendOverloads uint64
	// SlowClientCloses 是连续高水位导致的 Session 关闭次数。
	SlowClientCloses uint64
	// ProtocolErrors 是违反帧或消息协议导致的关闭次数。
	ProtocolErrors uint64
}

// ClientState 表示托管 Client 的连接级状态，而不是某条 Session 的生命周期。
type ClientState uint8

const (
	// ClientStopped 表示 Client 尚未启动、已经停止或重试耗尽。
	ClientStopped ClientState = iota
	// ClientConnecting 表示正在执行首次拨号。
	ClientConnecting
	// ClientConnected 表示当前存在活动 Session。
	ClientConnected
	// ClientReconnecting 表示旧 Session 已关闭且正在有界退避/重试。
	ClientReconnecting
)

// ClientStateSnapshot 是一次 Client 状态转换后交付给 Service 的只读值。
type ClientStateSnapshot struct {
	// State 是转换后的状态。
	State ClientState
	// Attempt 是当前连续连接序列已经执行的拨号次数。
	Attempt int
	// NextDelay 是下一次重试前的退避；不再重试时为零。
	NextDelay time.Duration
	// LastError 是最近一次拨号或 Session 关闭原因。
	LastError error
}
