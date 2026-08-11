package tcp

import (
	"math"

	originconfig "github.com/duanhf2012/origin/v3/config"
	"github.com/duanhf2012/origin/v3/errs"
	"github.com/duanhf2012/origin/v3/sysmodule/network"
)

const (
	// FrameByteOrderBig 表示长度字段按大端序编解码。
	FrameByteOrderBig = "big"
	// FrameByteOrderLittle 表示长度字段按小端序编解码。
	FrameByteOrderLittle = "little"
)

// FrameConfig 配置 TCP Payload 前的无符号长度字段。
type FrameConfig struct {
	// LengthFieldSize 是长度字段字节数，只允许 1、2、4。
	LengthFieldSize int
	// ByteOrder 是长度字段端序，只允许 big、little；通信双方必须一致。
	ByteOrder string
}

// ReconnectConfig 配置托管 TCP Client 每轮断线后的有界指数退避。
type ReconnectConfig struct {
	// Enabled 控制是否在初始连接失败或活动连接关闭后自动重试。
	Enabled bool
	// MaxAttempts 是每轮连续失败允许执行的最大重试次数。
	MaxAttempts int
	// InitialDelay 是第一次重试前的等待时间。
	InitialDelay originconfig.Duration
	// MaxDelay 是指数退避单次等待时间的上限。
	MaxDelay originconfig.Duration
	// Jitter 是退避随机抖动比例，范围为 0 到 1。
	Jitter float64
}

// ServerConfig 是可以从 Service 配置严格解码的 TCP Server 配置。
//
// 配置只保存可序列化数据；Handler 等运行期对象通过 Options 方法显式注入。
type ServerConfig struct {
	// Address 是包含端口的 TCP 监听地址。
	Address string
	// Frame 配置每条 TCP 逻辑消息的长度前缀。
	Frame FrameConfig
	// KeepAlive 是连接空闲到 OS 开始发送 TCP KeepAlive 探测前的时间；0s 表示关闭。
	KeepAlive originconfig.Duration
	// MaxSessions 是当前 Server 同时活动的 Session 上限。
	MaxSessions int
	// MaxMessageSize 同时限制入站和出站完整逻辑消息长度。
	MaxMessageSize originconfig.ByteSize
	// ReceivePendingMessages 限制每个 Session 已投递但尚未处理完成的消息数。
	ReceivePendingMessages int
	// ReceivePendingSize 限制每个 Session 待处理 Buffer 的保留容量。
	ReceivePendingSize originconfig.ByteSize
	// ReceivePendingTotalSize 限制当前 Server 全部待处理 Buffer 的保留容量。
	ReceivePendingTotalSize originconfig.ByteSize
	// SendQueueMessages 限制每个 Session 等待发送的完整消息数。
	SendQueueMessages int
	// SendQueueSize 限制每个 Session 排队 Payload 的保留容量。
	SendQueueSize originconfig.ByteSize
	// SendQueueTotalSize 限制当前 Server 排队及正在写出 Payload 的总保留容量。
	SendQueueTotalSize originconfig.ByteSize
	// ReadIdleTimeout 是读取完整业务消息的空闲上限；0s 表示关闭。
	ReadIdleTimeout originconfig.Duration
	// WriteTimeout 是写出一条完整业务消息的强制上限。
	WriteTimeout originconfig.Duration
	// SlowClientTimeout 是发送队列连续处于高水位的最长时间。
	SlowClientTimeout originconfig.Duration
}

// DialerConfig 是 TCP Dialer 和托管 Client 共享的单连接配置。
type DialerConfig struct {
	// Address 是单次拨号或托管 Client 使用的远端 TCP 地址。
	Address string
	// DialTimeout 是一次 TCP 建连尝试的最长时间；调用方 Context 更早到期时以 Context 为准。
	DialTimeout originconfig.Duration
	// Frame 配置每条 TCP 逻辑消息的长度前缀。
	Frame FrameConfig
	// KeepAlive 是连接空闲到 OS 开始发送 TCP KeepAlive 探测前的时间；0s 表示关闭。
	KeepAlive originconfig.Duration
	// MaxMessageSize 同时限制入站和出站完整逻辑消息长度。
	MaxMessageSize originconfig.ByteSize
	// ReceivePendingMessages 限制当前 Session 已投递但尚未处理完成的消息数。
	ReceivePendingMessages int
	// ReceivePendingSize 限制当前 Session 待处理 Buffer 的保留容量。
	ReceivePendingSize originconfig.ByteSize
	// SendQueueMessages 限制当前 Session 等待发送的完整消息数。
	SendQueueMessages int
	// SendQueueSize 限制当前 Session 排队 Payload 的保留容量。
	SendQueueSize originconfig.ByteSize
	// ReadIdleTimeout 是读取完整业务消息的空闲上限；0s 表示关闭。
	ReadIdleTimeout originconfig.Duration
	// WriteTimeout 是写出一条完整业务消息的强制上限。
	WriteTimeout originconfig.Duration
	// SlowClientTimeout 是发送队列连续处于高水位的最长时间。
	SlowClientTimeout originconfig.Duration
}

// ClientConfig 配置由 Service 生命周期托管的单连接 TCP Client。
type ClientConfig struct {
	// DialerConfig 匿名嵌入后保持与 Dialer 相同的扁平 YAML 字段。
	DialerConfig
	// Reconnect 配置初始失败和断线后的有界自动重连。
	Reconnect ReconnectConfig
}

// DefaultServerConfig 返回与 DefaultServerOptions 完全一致的 TCP Server 默认配置。
func DefaultServerConfig() ServerConfig {
	// 从唯一的运行期默认值生成配置，防止两套默认值随迭代发生漂移。
	options := DefaultServerOptions(network.HandlerFuncs{})
	return ServerConfig{
		Address:                 "0.0.0.0:19090",
		Frame:                   frameConfig(options.Frame),
		KeepAlive:               originconfig.Duration(options.KeepAlive),
		MaxSessions:             options.Network.MaxSessions,
		MaxMessageSize:          originconfig.ByteSize(options.Network.MaxMessageSize),
		ReceivePendingMessages:  options.Network.ReceivePendingMessages,
		ReceivePendingSize:      originconfig.ByteSize(options.Network.ReceivePendingSize),
		ReceivePendingTotalSize: originconfig.ByteSize(options.Network.ReceivePendingTotalSize),
		SendQueueMessages:       options.Network.SendQueueMessages,
		SendQueueSize:           originconfig.ByteSize(options.Network.SendQueueSize),
		SendQueueTotalSize:      originconfig.ByteSize(options.Network.SendQueueTotalSize),
		ReadIdleTimeout:         originconfig.Duration(options.Network.ReadIdleTimeout),
		WriteTimeout:            originconfig.Duration(options.Network.WriteTimeout),
		SlowClientTimeout:       originconfig.Duration(options.Network.SlowClientTimeout),
	}
}

// DefaultDialerConfig 返回单连接、单次拨号且不重试的 TCP Dialer 默认配置。
func DefaultDialerConfig() DialerConfig {
	// DialOptions 已经固定 MaxSessions=1；配置不再暴露该不可修改字段和冗余总预算。
	options := DefaultDialOptions(network.HandlerFuncs{})
	return DialerConfig{
		Address:                "127.0.0.1:19090",
		DialTimeout:            originconfig.Duration(options.DialTimeout),
		Frame:                  frameConfig(options.Frame),
		KeepAlive:              originconfig.Duration(options.KeepAlive),
		MaxMessageSize:         originconfig.ByteSize(options.Network.MaxMessageSize),
		ReceivePendingMessages: options.Network.ReceivePendingMessages,
		ReceivePendingSize:     originconfig.ByteSize(options.Network.ReceivePendingSize),
		SendQueueMessages:      options.Network.SendQueueMessages,
		SendQueueSize:          originconfig.ByteSize(options.Network.SendQueueSize),
		ReadIdleTimeout:        originconfig.Duration(options.Network.ReadIdleTimeout),
		WriteTimeout:           originconfig.Duration(options.Network.WriteTimeout),
		SlowClientTimeout:      originconfig.Duration(options.Network.SlowClientTimeout),
	}
}

// DefaultClientConfig 返回默认不自动重连的托管 TCP Client 配置。
func DefaultClientConfig() ClientConfig {
	// Client 的连接字段复用 Dialer 默认值，重连字段从现有 ClientOptions 派生。
	options := DefaultClientOptions(network.HandlerFuncs{})
	return ClientConfig{
		DialerConfig: DefaultDialerConfig(),
		Reconnect: ReconnectConfig{
			Enabled:      options.Reconnect.Enabled,
			MaxAttempts:  options.Reconnect.MaxAttempts,
			InitialDelay: originconfig.Duration(options.Reconnect.InitialDelay),
			MaxDelay:     originconfig.Duration(options.Reconnect.MaxDelay),
			Jitter:       options.Reconnect.Jitter,
		},
	}
}

// Options 把 TCP Server 配置转换为已经完整校验的运行期 Options。
func (configured ServerConfig) Options(handler network.Handler) (ServerOptions, error) {
	// 地址和可序列化字段全部在启动冷路径验证，错误时不创建 Listener 或 goroutine。
	if err := validateAddress(configured.Address); err != nil {
		return ServerOptions{}, err
	}
	maxMessageSize, err := configMessageSize(configured.MaxMessageSize)
	if err != nil {
		return ServerOptions{}, err
	}
	frame, err := configured.Frame.options()
	if err != nil {
		return ServerOptions{}, err
	}
	options := ServerOptions{
		Network: network.EndpointOptions{
			Handler:                 handler,
			MaxSessions:             configured.MaxSessions,
			MaxMessageSize:          maxMessageSize,
			ReceivePendingMessages:  configured.ReceivePendingMessages,
			ReceivePendingSize:      configured.ReceivePendingSize.Bytes(),
			ReceivePendingTotalSize: configured.ReceivePendingTotalSize.Bytes(),
			SendQueueMessages:       configured.SendQueueMessages,
			SendQueueSize:           configured.SendQueueSize.Bytes(),
			SendQueueTotalSize:      configured.SendQueueTotalSize.Bytes(),
			ReadIdleTimeout:         configured.ReadIdleTimeout.Duration(),
			WriteTimeout:            configured.WriteTimeout.Duration(),
			SlowClientTimeout:       configured.SlowClientTimeout.Duration(),
		},
		Frame:     frame,
		KeepAlive: configured.KeepAlive.Duration(),
	}
	if err := validateServerOptions(options); err != nil {
		return ServerOptions{}, err
	}
	return options, nil
}

// Options 把 TCP Dialer 配置转换为固定单 Session 的运行期 Options。
func (configured DialerConfig) Options(handler network.Handler) (DialOptions, error) {
	// Client/Dialer 只有一个活动 Session，总预算直接等于单 Session 上限，避免公开冗余字段。
	if err := validateAddress(configured.Address); err != nil {
		return DialOptions{}, err
	}
	maxMessageSize, err := configMessageSize(configured.MaxMessageSize)
	if err != nil {
		return DialOptions{}, err
	}
	frame, err := configured.Frame.options()
	if err != nil {
		return DialOptions{}, err
	}
	options := DialOptions{
		Network: network.EndpointOptions{
			Handler:                 handler,
			MaxSessions:             1,
			MaxMessageSize:          maxMessageSize,
			ReceivePendingMessages:  configured.ReceivePendingMessages,
			ReceivePendingSize:      configured.ReceivePendingSize.Bytes(),
			ReceivePendingTotalSize: configured.ReceivePendingSize.Bytes(),
			SendQueueMessages:       configured.SendQueueMessages,
			SendQueueSize:           configured.SendQueueSize.Bytes(),
			SendQueueTotalSize:      configured.SendQueueSize.Bytes(),
			ReadIdleTimeout:         configured.ReadIdleTimeout.Duration(),
			WriteTimeout:            configured.WriteTimeout.Duration(),
			SlowClientTimeout:       configured.SlowClientTimeout.Duration(),
		},
		Frame:       frame,
		KeepAlive:   configured.KeepAlive.Duration(),
		DialTimeout: configured.DialTimeout.Duration(),
	}
	if err := validateDialOptions(options); err != nil {
		return DialOptions{}, err
	}
	return options, nil
}

// Options 把托管 TCP Client 配置转换为已经完整校验的运行期 Options。
func (configured ClientConfig) Options(handler network.Handler) (ClientOptions, error) {
	// 先复用 Dialer 的单连接转换，再叠加只属于托管 Client 的重连策略。
	dial, err := configured.DialerConfig.Options(handler)
	if err != nil {
		return ClientOptions{}, err
	}
	options := ClientOptions{
		Dial: dial,
		Reconnect: ReconnectOptions{
			Enabled:      configured.Reconnect.Enabled,
			MaxAttempts:  configured.Reconnect.MaxAttempts,
			InitialDelay: configured.Reconnect.InitialDelay.Duration(),
			MaxDelay:     configured.Reconnect.MaxDelay.Duration(),
			Jitter:       configured.Reconnect.Jitter,
		},
	}
	if err := validateClientOptions(options); err != nil {
		return ClientOptions{}, err
	}
	return options, nil
}

// options 把配置端序转换为热路径使用的固定枚举。
func (configured FrameConfig) options() (FrameOptions, error) {
	var order network.ByteOrder
	switch configured.ByteOrder {
	case FrameByteOrderBig:
		order = network.BigEndian
	case FrameByteOrderLittle:
		order = network.LittleEndian
	default:
		return FrameOptions{}, errs.NewMessage(errs.CodeInvalidConfig, "tcp.frame.byte_order 只能是 big 或 little")
	}
	return FrameOptions{LengthFieldSize: configured.LengthFieldSize, ByteOrder: order}, nil
}

// frameConfig 把运行期枚举转换成使用者可读的稳定配置字符串。
func frameConfig(options FrameOptions) FrameConfig {
	order := FrameByteOrderBig
	if options.ByteOrder == network.LittleEndian {
		order = FrameByteOrderLittle
	}
	return FrameConfig{LengthFieldSize: options.LengthFieldSize, ByteOrder: order}
}

// configMessageSize 检查 ByteSize 能否安全写入平台相关的 int 运行字段。
func configMessageSize(value originconfig.ByteSize) (int, error) {
	bytes := value.Bytes()
	if bytes > int64(math.MaxInt) {
		return 0, errs.NewMessage(errs.CodeInvalidConfig, "tcp.max_message_size 超出当前平台 int 范围")
	}
	return int(bytes), nil
}
