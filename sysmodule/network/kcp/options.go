// Package kcp 提供由 Origin Service 托管的 KCP 长度帧 Server、Client 和单次 Dialer。
package kcp

import (
	"context"
	"fmt"
	"math"
	"reflect"
	"strings"
	"time"

	"github.com/klauspost/reedsolomon"
	kcplib "github.com/xtaci/kcp-go/v5"

	"github.com/duanhf2012/origin/v3/errs"
	"github.com/duanhf2012/origin/v3/sysmodule/network"
)

const (
	defaultMTU             = 1400
	defaultWindow          = 1024
	defaultUpdateInterval  = 10 * time.Millisecond
	defaultFastResend      = 2
	defaultReadIdleTimeout = 60 * time.Second
	maxKCPPacketSize       = 1500
	cryptHeaderSize        = 20
	fecHeaderSize          = 8
)

// FrameOptions 配置 KCP Stream Mode 中 Payload 前的无符号长度字段。
type FrameOptions struct {
	// LengthFieldSize 是 Payload 前无符号长度字段的字节数，只允许 1、2、4。
	LengthFieldSize int
	// ByteOrder 是长度字段使用的 Big/Little Endian；通信双方必须一致。
	ByteOrder network.ByteOrder
}

// NoDelayOptions 配置 KCP 更新频率、快速重传和拥塞控制。
type NoDelayOptions struct {
	// Enabled 开启低延迟模式；默认开启。
	Enabled bool
	// Interval 是 KCP 内部更新间隔，只允许 10ms 到 5s 的整毫秒值。
	Interval time.Duration
	// FastResend 是累计多少次跨越 ACK 后触发快速重传；零表示关闭。
	FastResend int
	// DisableCongestionControl 关闭 KCP 拥塞控制，以带宽换取更低时延。
	DisableCongestionControl bool
}

// FECOptions 配置前向纠错分片。
type FECOptions struct {
	// DataShards 是一组中的数据分片数；与 ParityShards 同为零时关闭 FEC。
	DataShards int
	// ParityShards 是每组追加的冗余分片数；启用时必须与 DataShards 同为正数。
	ParityShards int
}

// ServerOptions 配置 KCP Server 的公共网络语义和 UDP/KCP 专属参数。
type ServerOptions struct {
	// Network 保存 Handler、容量、超时和背压等三个传输真正共有的语义。
	Network network.EndpointOptions
	// Frame 配置 KCP 可靠字节流中的逻辑消息边界。
	Frame FrameOptions
	// MTU 是不含 UDP/IP 头的 KCP 报文上限；加密和 FEC 头仍计入库的 1500 字节缓冲上限。
	MTU int
	// SendWindow 是 KCP 发送窗口，单位为 Segment。
	SendWindow int
	// ReceiveWindow 是 KCP 接收窗口，单位为 Segment。
	ReceiveWindow int
	// NoDelay 配置低延迟更新、快速重传和拥塞控制。
	NoDelay NoDelayOptions
	// ACKNoDelay 立即发送 ACK，可降低确认时延但会增加小包。
	ACKNoDelay bool
	// WriteDelay 把写入延迟到下一次 KCP 更新以利于批量发送；实时消息默认关闭。
	WriteDelay bool
	// FEC 配置前向纠错；通信双方必须使用兼容的分片组合。
	FEC FECOptions
	// DSCP 是 IPv4 六位 DSCP/IPv6 Traffic Class，范围 0..63；零表示不设置。
	DSCP int
	// SocketReadBuffer 设置 UDP 接收缓冲字节数；零保留操作系统默认值。
	SocketReadBuffer int
	// SocketWriteBuffer 设置 UDP 发送缓冲字节数；零保留操作系统默认值。
	SocketWriteBuffer int
	// BlockCrypt 由代码注入 KCP 包加密实现；nil 表示不加密。
	// 同一实例被多个 Session 共享时，其 Encrypt/Decrypt 必须并发安全。
	BlockCrypt kcplib.BlockCrypt
}

// DefaultServerOptions 返回经过 Windows 与 Ubuntu 弱网验收的有界、低延迟 KCP Server 默认配置。
func DefaultServerOptions(handler network.Handler) ServerOptions {
	networkOptions := network.DefaultEndpointOptions(handler)
	networkOptions.ReadIdleTimeout = defaultReadIdleTimeout
	return ServerOptions{
		Network: networkOptions,
		Frame: FrameOptions{
			LengthFieldSize: 4,
			ByteOrder:       network.BigEndian,
		},
		MTU:           defaultMTU,
		SendWindow:    defaultWindow,
		ReceiveWindow: defaultWindow,
		NoDelay: NoDelayOptions{
			Enabled:                  true,
			Interval:                 defaultUpdateInterval,
			FastResend:               defaultFastResend,
			DisableCongestionControl: true,
		},
	}
}

// DialOptions 配置一次 KCP Session 创建及其 UDP socket。
//
// KCP 没有 TCP/HTTP 式连接握手；创建成功只表示本地 UDP Session 就绪，不证明远端已响应。
type DialOptions struct {
	// Network 保存单 Session 的 Handler、容量、超时和背压语义；MaxSessions 必须为 1。
	Network network.EndpointOptions
	// Frame 配置 KCP 可靠字节流中的逻辑消息边界。
	Frame FrameOptions
	// MTU 是不含 UDP/IP 头的 KCP 报文上限。
	MTU int
	// SendWindow 是 KCP 发送窗口，单位为 Segment。
	SendWindow int
	// ReceiveWindow 是 KCP 接收窗口，单位为 Segment。
	ReceiveWindow int
	// NoDelay 配置低延迟更新、快速重传和拥塞控制。
	NoDelay NoDelayOptions
	// ACKNoDelay 立即发送 ACK，可降低确认时延但会增加小包。
	ACKNoDelay bool
	// WriteDelay 把写入延迟到下一次 KCP 更新；实时消息默认关闭。
	WriteDelay bool
	// FEC 配置前向纠错；通信双方必须使用兼容的分片组合。
	FEC FECOptions
	// DSCP 是六位服务质量标记；零表示不设置。
	DSCP int
	// SocketReadBuffer 设置当前 UDP socket 接收缓冲；零保留 OS 默认值。
	SocketReadBuffer int
	// SocketWriteBuffer 设置当前 UDP socket 发送缓冲；零保留 OS 默认值。
	SocketWriteBuffer int
	// BlockCrypt 由代码注入包加密实现；nil 表示不加密。
	BlockCrypt kcplib.BlockCrypt
}

// DefaultDialOptions 返回单 Session KCP 拨号默认配置。
func DefaultDialOptions(handler network.Handler) DialOptions {
	options := DefaultServerOptions(handler)
	options.Network.MaxSessions = 1
	return dialOptionsFromServer(options)
}

// ReconnectOptions 配置 Client 的有界指数退避。
type ReconnectOptions struct {
	// Enabled 控制 Session 关闭或本地创建失败后是否自动重试。
	Enabled bool
	// MaxAttempts 是一轮连续本地创建失败允许执行的最大重试次数。
	MaxAttempts int
	// InitialDelay 是第一次重试前的等待时间。
	InitialDelay time.Duration
	// MaxDelay 是指数退避单次等待时间的上限。
	MaxDelay time.Duration
	// Jitter 是退避随机抖动比例，范围为 0 到 1。
	Jitter float64
}

// ClientOptions 配置托管 KCP Client。
type ClientOptions struct {
	// Dial 配置每次创建的 KCP Session 和 UDP socket。
	Dial DialOptions
	// Reconnect 配置 Client 唯一重连 Worker 的有界退避。
	Reconnect ReconnectOptions
	// StateChange 在所属 Service 串行上下文中接收不可变状态快照；nil 表示不通知。
	StateChange func(context.Context, network.ClientStateSnapshot)
}

// DefaultClientOptions 返回默认不自动重连的托管 KCP Client 配置。
func DefaultClientOptions(handler network.Handler) ClientOptions {
	return ClientOptions{
		Dial: DefaultDialOptions(handler),
		Reconnect: ReconnectOptions{
			Enabled:      false,
			MaxAttempts:  10,
			InitialDelay: 200 * time.Millisecond,
			MaxDelay:     5 * time.Second,
			Jitter:       0.2,
		},
	}
}

func dialOptionsFromServer(options ServerOptions) DialOptions {
	return DialOptions{
		Network:           options.Network,
		Frame:             options.Frame,
		MTU:               options.MTU,
		SendWindow:        options.SendWindow,
		ReceiveWindow:     options.ReceiveWindow,
		NoDelay:           options.NoDelay,
		ACKNoDelay:        options.ACKNoDelay,
		WriteDelay:        options.WriteDelay,
		FEC:               options.FEC,
		DSCP:              options.DSCP,
		SocketReadBuffer:  options.SocketReadBuffer,
		SocketWriteBuffer: options.SocketWriteBuffer,
		BlockCrypt:        options.BlockCrypt,
	}
}

func serverOptionsFromDial(options DialOptions) ServerOptions {
	return ServerOptions{
		Network:           options.Network,
		Frame:             options.Frame,
		MTU:               options.MTU,
		SendWindow:        options.SendWindow,
		ReceiveWindow:     options.ReceiveWindow,
		NoDelay:           options.NoDelay,
		ACKNoDelay:        options.ACKNoDelay,
		WriteDelay:        options.WriteDelay,
		FEC:               options.FEC,
		DSCP:              options.DSCP,
		SocketReadBuffer:  options.SocketReadBuffer,
		SocketWriteBuffer: options.SocketWriteBuffer,
		BlockCrypt:        options.BlockCrypt,
	}
}

func validateAddress(address string) error {
	if strings.TrimSpace(address) == "" {
		return errs.NewMessage(errs.CodeInvalidArgument, "kcp: 地址不能为空")
	}
	return nil
}

func validateServerOptions(options ServerOptions) error {
	if err := options.Network.Validate(); err != nil {
		return err
	}
	if err := validateFrame(options.Frame, options.Network.MaxMessageSize); err != nil {
		return err
	}
	if options.Network.ReadIdleTimeout <= 0 {
		return errs.NewMessage(errs.CodeInvalidConfig, "kcp.read_idle_timeout 必须大于零")
	}
	if options.MTU < 50 || options.MTU > maxKCPPacketSize {
		return errs.NewMessage(errs.CodeInvalidConfig, "kcp.mtu 必须在 50 到 1500 之间")
	}
	if options.SendWindow <= 0 || options.SendWindow > math.MaxUint16 ||
		options.ReceiveWindow <= 0 || options.ReceiveWindow > math.MaxUint16 {
		return errs.NewMessage(errs.CodeInvalidConfig, "kcp 发送和接收窗口必须在 1 到 65535 之间")
	}
	if options.NoDelay.Interval < 10*time.Millisecond ||
		options.NoDelay.Interval > 5*time.Second ||
		options.NoDelay.Interval%time.Millisecond != 0 ||
		options.NoDelay.FastResend < 0 {
		return errs.NewMessage(errs.CodeInvalidConfig, "kcp.no_delay 配置无效")
	}
	if err := validateFEC(options.FEC); err != nil {
		return err
	}
	if options.DSCP < 0 || options.DSCP > 63 {
		return errs.NewMessage(errs.CodeInvalidConfig, "kcp.dscp 必须在 0 到 63 之间")
	}
	if options.SocketReadBuffer < 0 || options.SocketWriteBuffer < 0 {
		return errs.NewMessage(errs.CodeInvalidConfig, "kcp socket buffer 不能为负数")
	}
	if options.BlockCrypt != nil && isNilBlockCrypt(options.BlockCrypt) {
		return errs.NewMessage(errs.CodeInvalidConfig, "kcp.block_crypt 不能是 typed nil")
	}
	overhead := 0
	if options.BlockCrypt != nil {
		overhead += cryptHeaderSize
	}
	if options.FEC.DataShards > 0 {
		overhead += fecHeaderSize
	}
	if options.MTU > maxKCPPacketSize-overhead {
		return errs.NewMessage(
			errs.CodeInvalidConfig,
			"kcp.mtu 加上加密/FEC 头超过 1500 字节库内上限",
		)
	}
	return nil
}

func validateDialOptions(options DialOptions) error {
	if options.Network.MaxSessions != 1 {
		return errs.NewMessage(errs.CodeInvalidConfig, "kcp Dial/Client 的 max_sessions 必须为 1")
	}
	return validateServerOptions(serverOptionsFromDial(options))
}

func validateClientOptions(options ClientOptions) error {
	if err := validateDialOptions(options.Dial); err != nil {
		return err
	}
	if options.Reconnect.MaxAttempts <= 0 || options.Reconnect.InitialDelay <= 0 ||
		options.Reconnect.MaxDelay < options.Reconnect.InitialDelay ||
		options.Reconnect.Jitter < 0 || options.Reconnect.Jitter > 1 {
		return errs.NewMessage(errs.CodeInvalidConfig, "kcp.reconnect 配置无效")
	}
	return nil
}

func validateFrame(frame FrameOptions, maxMessageSize int) error {
	var maximum uint64
	switch frame.LengthFieldSize {
	case 1:
		maximum = math.MaxUint8
	case 2:
		maximum = math.MaxUint16
	case 4:
		maximum = math.MaxUint32
	default:
		return errs.NewMessage(errs.CodeInvalidConfig, "kcp.length_field_size 只能是 1、2 或 4")
	}
	if frame.ByteOrder != network.BigEndian && frame.ByteOrder != network.LittleEndian {
		return errs.NewMessage(errs.CodeInvalidConfig, "kcp.byte_order 无效")
	}
	if maxMessageSize <= 0 || uint64(maxMessageSize) > maximum {
		return errs.NewMessage(errs.CodeInvalidConfig, fmt.Sprintf(
			"kcp.max_message_size 超出 %d 字节长度字段的表达范围",
			frame.LengthFieldSize,
		))
	}
	return nil
}

func validateFEC(options FECOptions) error {
	if (options.DataShards == 0) != (options.ParityShards == 0) ||
		options.DataShards < 0 || options.ParityShards < 0 {
		return errs.NewMessage(errs.CodeInvalidConfig, "kcp.fec 必须是 0/0 或两个正数")
	}
	if options.DataShards == 0 {
		return nil
	}
	if options.DataShards+options.ParityShards > 256 {
		return errs.NewMessage(errs.CodeInvalidConfig, "kcp.fec 分片总数不能超过 256")
	}
	if _, err := reedsolomon.New(options.DataShards, options.ParityShards); err != nil {
		return errs.NewMessage(errs.CodeInvalidConfig, "kcp.fec 分片组合无效")
	}
	return nil
}

func isNilBlockCrypt(block kcplib.BlockCrypt) bool {
	value := reflect.ValueOf(block)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}
