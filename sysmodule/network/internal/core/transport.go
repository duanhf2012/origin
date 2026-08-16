package core

import (
	"net"

	"github.com/duanhf2012/origin/v3/internal/bufferpool"
)

// TransportStats 是具体传输向公共 Session 提供的固定发送统计。
type TransportStats struct {
	SentMessages  uint64
	SentBytes     uint64
	QueueMessages int
	QueueBytes    int64
	Writable      bool
}

// TransportConn 是 Session Runtime 需要的最小内部连接能力。
//
// Send 成功接管 Buffer，失败不接管；Close 必须幂等且最终触发 Runtime.CloseTransport。
type TransportConn interface {
	LocalAddr() net.Addr
	RemoteAddr() net.Addr
	Send(*bufferpool.Buffer) error
	Close()
	Writable() bool
	Stats() TransportStats
}

// FinalTransportConn 是内置传输提供的最终消息原子入队能力。
type FinalTransportConn interface {
	TransportConn
	SendAndClose(*bufferpool.Buffer) error
}
