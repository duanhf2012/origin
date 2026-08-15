// Package network 定义 TCP、WebSocket 和 KCP 长连接 Module 共享的最小公共契约。
//
// 本包只公开真实共有的 Session、Handler、容量、状态和统计语义，不包含任何具体传输参数，
// 也不向业务暴露 Buffer Pool、发送队列或 Service Scheduler。
package network

import (
	"context"
	"net"
)

// SessionID 是跨传输、跨 Module 和跨进程生命周期工程上实际唯一的连接标识。
//
// 每个新 Session 使用独立的 128 位安全随机数，外观是固定 22 字符的无填充 Base64URL 文本；
// 空字符串表示未绑定或无效 Session。
type SessionID string

// Transport 标识 Session 使用的底层长连接传输。
type Transport uint8

const (
	// TransportTCP 表示有序 TCP 字节流上的长度帧。
	TransportTCP Transport = iota + 1
	// TransportWebSocket 表示 WebSocket 原生逻辑消息。
	TransportWebSocket
	// TransportKCP 表示 KCP 流上的长度帧。
	TransportKCP
)

// ByteOrder 表示长度字段或二进制协议整数使用的固定端序。
type ByteOrder uint8

const (
	// BigEndian 是默认网络字节序。
	BigEndian ByteOrder = iota + 1
	// LittleEndian 支持使用小端整数的游戏客户端协议。
	LittleEndian
)

// Session 是三个传输共同提供的并发安全连接外观。
//
// Handler 回调在所属 Service 串行上下文执行；Send、Close、Writable、Cause 和 Stats 可以由
// 其他 goroutine 并发调用。Session 关闭后永不恢复，Client 重连会产生新 Session。
type Session interface {
	// ID 返回当前连接稳定且非空的全局唯一标识。
	ID() SessionID
	// Transport 返回底层传输类型。
	Transport() Transport
	// LocalAddr 返回连接建立时保存的本地地址。
	LocalAddr() net.Addr
	// RemoteAddr 返回连接建立时保存的远端地址。
	RemoteAddr() net.Addr
	// Context 返回随 Session 关闭而取消的只读 Context。
	Context() context.Context
	// Done 返回 OnClose 和全部资源清理完成后关闭的信号。
	Done() <-chan struct{}
	// Send 安全复制 payload 并非阻塞地提交本地有界发送队列。
	Send(payload []byte) error
	// Close 幂等发起立即关闭；nil cause 使用稳定本地主动关闭原因。
	Close(cause error)
	// Writable 返回发送队列的瞬时高低水位状态，最终准入仍以 Send 返回值为准。
	Writable() bool
	// Cause 在 Done 关闭后返回最终原因，仍运行时返回 nil。
	Cause() error
	// Stats 返回当前 Session 的固定字段统计快照。
	Stats() SessionStats
}
