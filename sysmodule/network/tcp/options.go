// Package tcp 提供由 Origin Service 托管的 TCP 长度帧 Server、Client 和单次 Dialer。
package tcp

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/duanhf2012/origin/v3/errs"
	"github.com/duanhf2012/origin/v3/sysmodule/network"
)

const (
	defaultKeepAlive   = 30 * time.Second
	defaultDialTimeout = 10 * time.Second
)

// FrameOptions 配置 TCP Payload 前的无符号长度字段。
type FrameOptions struct {
	// LengthFieldSize 是 Payload 前无符号长度字段的字节数，只允许 1、2、4。
	LengthFieldSize int
	// ByteOrder 是长度字段使用的 Big/Little Endian；通信双方必须一致。
	ByteOrder network.ByteOrder
}

// ServerOptions 配置 TCP Server 的公共网络语义和 TCP 专属参数。
type ServerOptions struct {
	// Network 保存 Handler、容量、超时和背压等三个传输真正共有的语义。
	Network network.EndpointOptions
	// Frame 配置 TCP 字节流中的逻辑消息边界。
	Frame FrameOptions
	// KeepAlive 是空闲到 OS 开始发送 TCP KeepAlive 探测前的时间；零表示关闭。
	KeepAlive time.Duration
}

// DefaultServerOptions 返回有界、安全的 TCP Server 默认配置。
func DefaultServerOptions(handler network.Handler) ServerOptions {
	return ServerOptions{
		Network: network.DefaultEndpointOptions(handler),
		Frame: FrameOptions{
			LengthFieldSize: 4,
			ByteOrder:       network.BigEndian,
		},
		KeepAlive: defaultKeepAlive,
	}
}

// DialOptions 配置一次 TCP 拨号及其连接语义。
type DialOptions struct {
	// Network 保存单 Session 的 Handler、容量、超时和背压语义；MaxSessions 必须为 1。
	Network network.EndpointOptions
	// Frame 配置 TCP 字节流中的逻辑消息边界。
	Frame FrameOptions
	// KeepAlive 是空闲到 OS 开始发送 TCP KeepAlive 探测前的时间；零表示关闭。
	KeepAlive time.Duration
	// DialTimeout 是一次 TCP 建连尝试的最长时间；调用方 Context 更早到期时优先生效。
	DialTimeout time.Duration
}

// DefaultDialOptions 返回单 Session 拨号默认配置。
func DefaultDialOptions(handler network.Handler) DialOptions {
	options := DefaultServerOptions(handler)
	options.Network.MaxSessions = 1
	return DialOptions{
		Network:     options.Network,
		Frame:       options.Frame,
		KeepAlive:   options.KeepAlive,
		DialTimeout: defaultDialTimeout,
	}
}

// ReconnectOptions 配置 Client 的有界指数退避。
type ReconnectOptions struct {
	// Enabled 控制初始连接失败或活动连接关闭后是否自动重试。
	Enabled bool
	// MaxAttempts 是每轮连续失败允许执行的最大重试次数。
	MaxAttempts int
	// InitialDelay 是第一次重试前的等待时间。
	InitialDelay time.Duration
	// MaxDelay 是指数退避单次等待时间的上限。
	MaxDelay time.Duration
	// Jitter 是退避随机抖动比例，范围为 0 到 1。
	Jitter float64
}

// ClientOptions 配置托管 TCP Client。
type ClientOptions struct {
	// Dial 配置每次连接建立后 Session 使用的传输语义。
	Dial DialOptions
	// Reconnect 配置 Client 唯一重连 Worker 的有界退避。
	Reconnect ReconnectOptions
	// StateChange 在所属 Service 串行上下文中接收不可变状态快照；nil 表示不通知。
	StateChange func(context.Context, network.ClientStateSnapshot)
}

// DefaultClientOptions 返回默认不自动重连的托管 Client 配置。
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

func validateAddress(address string) error {
	if strings.TrimSpace(address) == "" {
		return errs.NewMessage(errs.CodeInvalidArgument, "tcp: 地址不能为空")
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
	if options.KeepAlive < 0 {
		return errs.NewMessage(errs.CodeInvalidConfig, "tcp.keep_alive 不能为负数")
	}
	return nil
}

func validateDialOptions(options DialOptions) error {
	if options.Network.MaxSessions != 1 {
		return errs.NewMessage(errs.CodeInvalidConfig, "tcp Dial/Client 的 max_sessions 必须为 1")
	}
	if options.DialTimeout <= 0 {
		return errs.NewMessage(errs.CodeInvalidConfig, "tcp.dial_timeout 必须大于零")
	}
	return validateServerOptions(ServerOptions{
		Network:   options.Network,
		Frame:     options.Frame,
		KeepAlive: options.KeepAlive,
	})
}

func validateClientOptions(options ClientOptions) error {
	if err := validateDialOptions(options.Dial); err != nil {
		return err
	}
	if options.Reconnect.MaxAttempts <= 0 || options.Reconnect.InitialDelay <= 0 ||
		options.Reconnect.MaxDelay < options.Reconnect.InitialDelay ||
		options.Reconnect.Jitter < 0 || options.Reconnect.Jitter > 1 {
		return errs.NewMessage(errs.CodeInvalidConfig, "tcp.reconnect 配置无效")
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
		return errs.NewMessage(errs.CodeInvalidConfig, "tcp.length_field_size 只能是 1、2 或 4")
	}
	if frame.ByteOrder != network.BigEndian && frame.ByteOrder != network.LittleEndian {
		return errs.NewMessage(errs.CodeInvalidConfig, "tcp.byte_order 无效")
	}
	if maxMessageSize <= 0 || uint64(maxMessageSize) > maximum {
		return errs.NewMessage(errs.CodeInvalidConfig, fmt.Sprintf(
			"tcp.max_message_size 超出 %d 字节长度字段的表达范围",
			frame.LengthFieldSize,
		))
	}
	return nil
}
