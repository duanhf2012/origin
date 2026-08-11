package websocket

import (
	"math"

	originconfig "github.com/duanhf2012/origin/v3/config"
	"github.com/duanhf2012/origin/v3/errs"
	"github.com/duanhf2012/origin/v3/sysmodule/network"
)

const (
	// ConfigMessageTypeBinary 使用 WebSocket Binary Data Message 承载 Raw、PB 等二进制协议。
	ConfigMessageTypeBinary = "binary"
	// ConfigMessageTypeText 使用 WebSocket Text Data Message，Payload 必须是有效 UTF-8。
	ConfigMessageTypeText = "text"
)

// ReconnectConfig 配置托管 WebSocket Client 每轮断线后的有界指数退避。
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

// ServerConfig 是可以从 Service 配置严格解码的 WebSocket Server 配置。
//
// TLS、Origin 校验和响应 Header 保存运行期对象或安全策略，必须在 Options 转换后由代码注入。
type ServerConfig struct {
	// Address 是 HTTP/WebSocket 监听地址。
	Address string
	// Path 是执行 WebSocket Upgrade 的 HTTP 路由，必须以斜杠开头。
	Path string
	// MessageType 只允许 binary 或 text，并在连接生命周期内保持不变。
	MessageType string
	// HandshakeTimeout 是 HTTP Upgrade 握手的最长时间。
	HandshakeTimeout originconfig.Duration
	// PingInterval 是发送 WebSocket 协议 Ping 控制帧的间隔；与 PongTimeout 同为 0s 时关闭。
	PingInterval originconfig.Duration
	// PongTimeout 是协议 Ping 发出后等待 Pong 的最长时间，启用时必须大于 PingInterval。
	PongTimeout originconfig.Duration
	// Subprotocols 是允许协商的 WebSocket 子协议，空切片表示不协商。
	Subprotocols []string
	// MaxSessions 是当前 Server 同时活动的 Session 上限。
	MaxSessions int
	// MaxMessageSize 同时限制入站和出站完整 Data Message 长度。
	MaxMessageSize originconfig.ByteSize
	// ReceivePendingMessages 限制每个 Session 已投递但尚未处理完成的 Data Message 数。
	ReceivePendingMessages int
	// ReceivePendingSize 限制每个 Session 待处理 Buffer 的保留容量。
	ReceivePendingSize originconfig.ByteSize
	// ReceivePendingTotalSize 限制当前 Server 全部待处理 Buffer 的保留容量。
	ReceivePendingTotalSize originconfig.ByteSize
	// SendQueueMessages 限制每个 Session 等待发送的完整 Data Message 数。
	SendQueueMessages int
	// SendQueueSize 限制每个 Session 排队 Payload 的保留容量。
	SendQueueSize originconfig.ByteSize
	// SendQueueTotalSize 限制当前 Server 排队及正在写出 Payload 的总保留容量。
	SendQueueTotalSize originconfig.ByteSize
	// ReadIdleTimeout 是业务 Data Message 的读空闲上限；0s 表示关闭，协议 Ping/Pong 不刷新它。
	ReadIdleTimeout originconfig.Duration
	// WriteTimeout 是写出一条完整 Data Message 的强制上限。
	WriteTimeout originconfig.Duration
	// SlowClientTimeout 是发送队列连续处于高水位的最长时间。
	SlowClientTimeout originconfig.Duration
}

// ClientConfig 配置由 Service 生命周期托管的单连接 WebSocket Client。
//
// Dialer 是一次性的代码对象，只使用 DialOptions，不从 Service 配置读取参数。
type ClientConfig struct {
	// URL 是托管 Client 使用的完整远端地址，包含 ws/wss Scheme、主机和路径。
	URL string
	// MessageType 只允许 binary 或 text，并在连接生命周期内保持不变。
	MessageType string
	// HandshakeTimeout 是 DNS、TCP、TLS 和 HTTP Upgrade 整体握手的最长时间。
	HandshakeTimeout originconfig.Duration
	// PingInterval 是发送 WebSocket 协议 Ping 控制帧的间隔；与 PongTimeout 同为 0s 时关闭。
	PingInterval originconfig.Duration
	// PongTimeout 是协议 Ping 发出后等待 Pong 的最长时间，启用时必须大于 PingInterval。
	PongTimeout originconfig.Duration
	// Subprotocols 是 Client 提议的 WebSocket 子协议，空切片表示不协商。
	Subprotocols []string
	// MaxMessageSize 同时限制入站和出站完整 Data Message 长度。
	MaxMessageSize originconfig.ByteSize
	// ReceivePendingMessages 限制当前 Session 已投递但尚未处理完成的 Data Message 数。
	ReceivePendingMessages int
	// ReceivePendingSize 限制当前 Session 待处理 Buffer 的保留容量。
	ReceivePendingSize originconfig.ByteSize
	// SendQueueMessages 限制当前 Session 等待发送的完整 Data Message 数。
	SendQueueMessages int
	// SendQueueSize 限制当前 Session 排队 Payload 的保留容量。
	SendQueueSize originconfig.ByteSize
	// ReadIdleTimeout 是业务 Data Message 的读空闲上限；0s 表示关闭，协议 Ping/Pong 不刷新它。
	ReadIdleTimeout originconfig.Duration
	// WriteTimeout 是写出一条完整 Data Message 的强制上限。
	WriteTimeout originconfig.Duration
	// SlowClientTimeout 是发送队列连续处于高水位的最长时间。
	SlowClientTimeout originconfig.Duration
	// Reconnect 配置初始失败和断线后的有界自动重连。
	Reconnect ReconnectConfig
}

// DefaultServerConfig 返回与 DefaultServerOptions 完全一致的 WebSocket Server 默认配置。
func DefaultServerConfig() ServerConfig {
	// 从唯一的运行期默认值生成配置，防止配置和 Options 在后续迭代中发生漂移。
	options := DefaultServerOptions(network.HandlerFuncs{})
	return ServerConfig{
		Address:                 "0.0.0.0:19091",
		Path:                    options.Path,
		MessageType:             configMessageType(options.MessageType),
		HandshakeTimeout:        originconfig.Duration(options.HandshakeTimeout),
		PingInterval:            originconfig.Duration(options.PingInterval),
		PongTimeout:             originconfig.Duration(options.PongTimeout),
		Subprotocols:            append([]string(nil), options.Subprotocols...),
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

// DefaultClientConfig 返回默认不自动重连的托管 WebSocket Client 配置。
func DefaultClientConfig() ClientConfig {
	options := DefaultClientOptions(network.HandlerFuncs{})
	return ClientConfig{
		URL:                    "ws://127.0.0.1:19091/ws",
		MessageType:            configMessageType(options.Dial.MessageType),
		HandshakeTimeout:       originconfig.Duration(options.Dial.HandshakeTimeout),
		PingInterval:           originconfig.Duration(options.Dial.PingInterval),
		PongTimeout:            originconfig.Duration(options.Dial.PongTimeout),
		Subprotocols:           append([]string(nil), options.Dial.Subprotocols...),
		MaxMessageSize:         originconfig.ByteSize(options.Dial.Network.MaxMessageSize),
		ReceivePendingMessages: options.Dial.Network.ReceivePendingMessages,
		ReceivePendingSize:     originconfig.ByteSize(options.Dial.Network.ReceivePendingSize),
		SendQueueMessages:      options.Dial.Network.SendQueueMessages,
		SendQueueSize:          originconfig.ByteSize(options.Dial.Network.SendQueueSize),
		ReadIdleTimeout:        originconfig.Duration(options.Dial.Network.ReadIdleTimeout),
		WriteTimeout:           originconfig.Duration(options.Dial.Network.WriteTimeout),
		SlowClientTimeout:      originconfig.Duration(options.Dial.Network.SlowClientTimeout),
		Reconnect: ReconnectConfig{
			Enabled:      options.Reconnect.Enabled,
			MaxAttempts:  options.Reconnect.MaxAttempts,
			InitialDelay: originconfig.Duration(options.Reconnect.InitialDelay),
			MaxDelay:     originconfig.Duration(options.Reconnect.MaxDelay),
			Jitter:       options.Reconnect.Jitter,
		},
	}
}

// Options 把 WebSocket Server 配置转换为已经完整校验的运行期 Options。
func (configured ServerConfig) Options(handler network.Handler) (ServerOptions, error) {
	// 地址、消息类型和容量全部在启动冷路径验证，错误时不创建 HTTP Server 或 goroutine。
	if err := validateAddress(configured.Address); err != nil {
		return ServerOptions{}, err
	}
	maxMessageSize, err := websocketConfigMessageSize(configured.MaxMessageSize)
	if err != nil {
		return ServerOptions{}, err
	}
	messageType, err := configured.messageType()
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
		Path:             configured.Path,
		MessageType:      messageType,
		HandshakeTimeout: configured.HandshakeTimeout.Duration(),
		PingInterval:     configured.PingInterval.Duration(),
		PongTimeout:      configured.PongTimeout.Duration(),
		Subprotocols:     append([]string(nil), configured.Subprotocols...),
	}
	if err := validateServerOptions(options); err != nil {
		return ServerOptions{}, err
	}
	return options, nil
}

// dialOptions 把托管 Client 的连接字段转换为固定单 Session 的运行期 Options。
func (configured ClientConfig) dialOptions(handler network.Handler) (DialOptions, error) {
	// Client 只有一个活动 Session，总预算直接等于单 Session 上限，避免公开冗余字段。
	maxMessageSize, err := websocketConfigMessageSize(configured.MaxMessageSize)
	if err != nil {
		return DialOptions{}, err
	}
	messageType, err := configured.messageType()
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
		MessageType:      messageType,
		HandshakeTimeout: configured.HandshakeTimeout.Duration(),
		PingInterval:     configured.PingInterval.Duration(),
		PongTimeout:      configured.PongTimeout.Duration(),
		Subprotocols:     append([]string(nil), configured.Subprotocols...),
	}
	if err := validateDialOptions(configured.URL, options); err != nil {
		return DialOptions{}, err
	}
	return options, nil
}

// Options 把托管 WebSocket Client 配置转换为已经完整校验的运行期 Options。
func (configured ClientConfig) Options(handler network.Handler) (ClientOptions, error) {
	dial, err := configured.dialOptions(handler)
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
	if err := validateClientOptions(configured.URL, options); err != nil {
		return ClientOptions{}, err
	}
	return options, nil
}

// messageType 把配置字符串转换为热路径使用的固定消息类型枚举。
func (configured ServerConfig) messageType() (MessageType, error) {
	return parseConfigMessageType(configured.MessageType)
}

// messageType 把 Client 配置字符串转换为热路径使用的固定消息类型枚举。
func (configured ClientConfig) messageType() (MessageType, error) {
	return parseConfigMessageType(configured.MessageType)
}

// parseConfigMessageType 只接受文档明确公开的两个小写稳定值。
func parseConfigMessageType(value string) (MessageType, error) {
	switch value {
	case ConfigMessageTypeBinary:
		return BinaryMessage, nil
	case ConfigMessageTypeText:
		return TextMessage, nil
	default:
		return 0, errs.NewMessage(errs.CodeInvalidConfig, "websocket.message_type 只能是 binary 或 text")
	}
}

// configMessageType 把运行期枚举转换为使用者可读的稳定配置字符串。
func configMessageType(value MessageType) string {
	if value == TextMessage {
		return ConfigMessageTypeText
	}
	return ConfigMessageTypeBinary
}

// websocketConfigMessageSize 检查 ByteSize 能否安全写入平台相关的 int 运行字段。
func websocketConfigMessageSize(value originconfig.ByteSize) (int, error) {
	bytes := value.Bytes()
	if bytes > int64(math.MaxInt) {
		return 0, errs.NewMessage(errs.CodeInvalidConfig, "websocket.max_message_size 超出当前平台 int 范围")
	}
	return int(bytes), nil
}
