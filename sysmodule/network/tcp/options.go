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

const defaultKeepAlive = 30 * time.Second

// FrameOptions 配置 TCP Payload 前的无符号长度字段。
type FrameOptions struct {
	LengthFieldSize int
	ByteOrder       network.ByteOrder
}

// ServerOptions 配置 TCP Server 的公共网络语义和 TCP 专属参数。
type ServerOptions struct {
	Network   network.EndpointOptions
	Frame     FrameOptions
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
	Network   network.EndpointOptions
	Frame     FrameOptions
	KeepAlive time.Duration
}

// DefaultDialOptions 返回单 Session 拨号默认配置。
func DefaultDialOptions(handler network.Handler) DialOptions {
	options := DefaultServerOptions(handler)
	options.Network.MaxSessions = 1
	return DialOptions(options)
}

// ReconnectOptions 配置 Client 的有界指数退避。
type ReconnectOptions struct {
	Enabled      bool
	MaxAttempts  int
	InitialDelay time.Duration
	MaxDelay     time.Duration
	Jitter       float64
}

// ClientOptions 配置托管 TCP Client。
type ClientOptions struct {
	Dial        DialOptions
	Reconnect   ReconnectOptions
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
	return validateServerOptions(ServerOptions(options))
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
