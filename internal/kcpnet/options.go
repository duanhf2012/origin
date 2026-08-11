// Package kcpnet 提供 Origin 网络 Module 内部使用的 KCP 长度帧传输。
package kcpnet

import (
	"math"
	"time"

	"github.com/klauspost/reedsolomon"
	kcplib "github.com/xtaci/kcp-go/v5"

	"github.com/duanhf2012/origin/v3/internal/bufferpool"
	"github.com/duanhf2012/origin/v3/internal/bytebudget"
	"github.com/duanhf2012/origin/v3/internal/lengthframe"
	originlog "github.com/duanhf2012/origin/v3/log"
)

const (
	// KCP 库内部 UDP 报文缓冲固定为 1500 字节；附加加密/FEC 头也必须包含在该上限内。
	maxPacketSize = 1500
	minMTU        = 50
	cryptOverhead = 20
	fecOverhead   = 8
)

// FrameOptions 配置 KCP Stream Mode 上的长度字段。
type FrameOptions struct {
	LengthFieldSize int
	ByteOrder       lengthframe.ByteOrder
}

// NoDelayOptions 配置 KCP 的低延迟更新和快速重传。
type NoDelayOptions struct {
	Enabled                  bool
	Interval                 time.Duration
	FastResend               int
	DisableCongestionControl bool
}

// ProtocolOptions 配置一条 KCP Session 的协议参数。
type ProtocolOptions struct {
	MTU           int
	SendWindow    int
	ReceiveWindow int
	NoDelay       NoDelayOptions
	ACKNoDelay    bool
	WriteDelay    bool
}

// FECOptions 配置 KCP 数据分片和冗余分片；0/0 表示关闭。
type FECOptions struct {
	DataShards   int
	ParityShards int
}

// ConnectionOptions 配置一条 KCP 长度帧连接的队列、超时和依赖。
type ConnectionOptions struct {
	Pool              *bufferpool.Pool
	Logger            originlog.Logger
	Frame             FrameOptions
	Protocol          ProtocolOptions
	MaxMessageSize    int
	SendQueueMessages int
	SendQueueBytes    int64
	SendBudget        *bytebudget.Budget
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	SlowClientTimeout time.Duration
}

// ListenOptions 配置 KCP Listener、握手前参数和连接准入。
type ListenOptions struct {
	MaxConnections    int
	BlockCrypt        kcplib.BlockCrypt
	FEC               FECOptions
	DSCP              int
	SocketReadBuffer  int
	SocketWriteBuffer int
	Connection        ConnectionOptions
}

// DialOptions 配置一次本地 KCP Session 创建及其 UDP socket。
type DialOptions struct {
	BlockCrypt        kcplib.BlockCrypt
	FEC               FECOptions
	DSCP              int
	SocketReadBuffer  int
	SocketWriteBuffer int
	Connection        ConnectionOptions
}

// Handler 接收内部 KCP 连接的有序生命周期和完整消息。
type Handler interface {
	OnOpen(*Conn)
	OnMessage(*Conn, *bufferpool.Buffer) error
	OnClose(*Conn, error)
}

// WritableHandler 是发送队列跨越高低水位时的可选通知。
type WritableHandler interface {
	OnWritableChanged(*Conn, bool)
}

func validateConnectionOptions(options ConnectionOptions) error {
	if options.Pool == nil || options.SendBudget == nil {
		return invalidConfig("kcpnet: Pool 和 SendBudget 不能为空")
	}
	switch options.Frame.LengthFieldSize {
	case 1, 2, 4:
	default:
		return invalidConfig("kcpnet: LengthFieldSize 只能是 1、2 或 4")
	}
	if options.Frame.ByteOrder != lengthframe.BigEndian &&
		options.Frame.ByteOrder != lengthframe.LittleEndian {
		return invalidConfig("kcpnet: ByteOrder 无效")
	}
	if options.MaxMessageSize <= 0 ||
		uint64(options.MaxMessageSize) > maxFramePayload(options.Frame.LengthFieldSize) {
		return invalidConfig("kcpnet: MaxMessageSize 超出长度字段表达范围")
	}
	if options.SendQueueMessages <= 0 ||
		options.SendQueueBytes < int64(bufferpool.RetainedCapacity(options.MaxMessageSize)) ||
		options.SendBudget.Snapshot().Limit < options.SendQueueBytes {
		return invalidConfig("kcpnet: 发送队列容量无效")
	}
	if options.ReadTimeout <= 0 || options.WriteTimeout <= 0 ||
		options.SlowClientTimeout <= 0 {
		return invalidConfig("kcpnet: 读空闲、写或慢连接超时必须大于零")
	}
	if err := validateProtocolOptions(options.Protocol); err != nil {
		return err
	}
	return nil
}

func validateProtocolOptions(options ProtocolOptions) error {
	if options.MTU < minMTU || options.MTU > maxPacketSize {
		return invalidConfig("kcpnet: MTU 必须在 50 到 1500 之间")
	}
	if options.SendWindow <= 0 || options.SendWindow > math.MaxUint16 ||
		options.ReceiveWindow <= 0 || options.ReceiveWindow > math.MaxUint16 {
		return invalidConfig("kcpnet: 发送和接收窗口必须在 1 到 65535 之间")
	}
	if options.NoDelay.Interval < 10*time.Millisecond ||
		options.NoDelay.Interval > 5*time.Second ||
		options.NoDelay.Interval%time.Millisecond != 0 ||
		options.NoDelay.FastResend < 0 {
		return invalidConfig("kcpnet: NoDelay 间隔或快速重传配置无效")
	}
	return nil
}

func validateWireOptions(
	block kcplib.BlockCrypt,
	fec FECOptions,
	dscp int,
	readBuffer int,
	writeBuffer int,
	mtu int,
) error {
	if (fec.DataShards == 0) != (fec.ParityShards == 0) ||
		fec.DataShards < 0 || fec.ParityShards < 0 {
		return invalidConfig("kcpnet: FEC 必须是 0/0 或两个正数")
	}
	if fec.DataShards > 0 {
		if fec.DataShards+fec.ParityShards > 256 {
			return invalidConfig("kcpnet: FEC 数据和冗余分片总数不能超过 256")
		}
		if _, err := reedsolomon.New(fec.DataShards, fec.ParityShards); err != nil {
			return invalidConfig("kcpnet: FEC 分片组合无效")
		}
	}
	if dscp < 0 || dscp > 63 {
		return invalidConfig("kcpnet: DSCP 必须在 0 到 63 之间")
	}
	if readBuffer < 0 || writeBuffer < 0 {
		return invalidConfig("kcpnet: UDP Socket Buffer 不能为负数")
	}
	overhead := 0
	if block != nil {
		overhead += cryptOverhead
	}
	if fec.DataShards > 0 {
		overhead += fecOverhead
	}
	if mtu > maxPacketSize-overhead {
		return invalidConfig("kcpnet: MTU 加上加密/FEC 头超过 1500 字节库内上限")
	}
	return nil
}

func validateListenOptions(options ListenOptions) error {
	if options.MaxConnections <= 0 {
		return invalidConfig("kcpnet: MaxConnections 必须大于零")
	}
	if err := validateConnectionOptions(options.Connection); err != nil {
		return err
	}
	return validateWireOptions(
		options.BlockCrypt,
		options.FEC,
		options.DSCP,
		options.SocketReadBuffer,
		options.SocketWriteBuffer,
		options.Connection.Protocol.MTU,
	)
}

func validateDialOptions(options DialOptions) error {
	if err := validateConnectionOptions(options.Connection); err != nil {
		return err
	}
	return validateWireOptions(
		options.BlockCrypt,
		options.FEC,
		options.DSCP,
		options.SocketReadBuffer,
		options.SocketWriteBuffer,
		options.Connection.Protocol.MTU,
	)
}

func maxFramePayload(size int) uint64 {
	switch size {
	case 1:
		return math.MaxUint8
	case 2:
		return math.MaxUint16
	case 4:
		return math.MaxUint32
	default:
		return 0
	}
}
