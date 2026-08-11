package kcp

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

// FrameConfig 配置 KCP 可靠字节流中每条 Payload 前的无符号长度字段。
type FrameConfig struct {
	// LengthFieldSize 是长度字段字节数，只允许 1、2、4。
	LengthFieldSize int
	// ByteOrder 是长度字段端序，只允许 big、little；通信双方必须一致。
	ByteOrder string
}

// NoDelayConfig 配置 KCP 更新频率、快速重传和拥塞控制。
type NoDelayConfig struct {
	// Enabled 开启低延迟模式；默认开启。
	Enabled bool
	// Interval 是 KCP 内部更新间隔，只允许 10ms 到 5s 的整毫秒值。
	Interval originconfig.Duration
	// FastResend 是累计多少次跨越 ACK 后触发快速重传；零表示关闭。
	FastResend int
	// DisableCongestionControl 关闭 KCP 拥塞控制，以带宽换取更低时延。
	DisableCongestionControl bool
}

// FECConfig 配置 KCP 前向纠错分片。
type FECConfig struct {
	// DataShards 是一组中的数据分片数；与 ParityShards 同为零时关闭 FEC。
	DataShards int
	// ParityShards 是每组追加的冗余分片数；启用时必须与 DataShards 同为正数。
	ParityShards int
}

// ReconnectConfig 配置托管 KCP Client 每轮断线后的有界指数退避。
type ReconnectConfig struct {
	// Enabled 控制本地 Session 失活或创建失败后是否自动重试。
	Enabled bool
	// MaxAttempts 是每轮连续本地创建失败允许执行的最大重试次数。
	MaxAttempts int
	// InitialDelay 是第一次重试前的等待时间。
	InitialDelay originconfig.Duration
	// MaxDelay 是指数退避单次等待时间的上限。
	MaxDelay originconfig.Duration
	// Jitter 是退避随机抖动比例，范围为 0 到 1。
	Jitter float64
}

// ServerConfig 是可以从 Service 配置严格解码的 KCP Server 配置。
//
// 配置只保存可序列化数据；BlockCrypt 等安全对象必须在 Options 转换后由代码注入。
type ServerConfig struct {
	// Address 是包含端口的 UDP/KCP 监听地址。
	Address string
	// Frame 配置每条 KCP 逻辑消息的长度前缀。
	Frame FrameConfig
	// MTU 是不含 UDP/IP 头的 KCP 报文上限；修改前应结合真实链路验证分片。
	// 启用 BlockCrypt 或 FEC 时，其协议头也必须落在 kcp-go 的 1500 字节缓冲上限内。
	MTU int
	// SendWindow 是 KCP 发送窗口，单位为 Segment。
	SendWindow int
	// ReceiveWindow 是 KCP 接收窗口，单位为 Segment。
	ReceiveWindow int
	// NoDelay 配置 KCP 低延迟模式。
	NoDelay NoDelayConfig
	// ACKNoDelay 立即发送 ACK，可降低确认时延但会增加小包。
	ACKNoDelay bool
	// WriteDelay 把写入延迟到下一次 KCP 更新以利于批量发送；实时消息默认关闭。
	WriteDelay bool
	// FEC 配置前向纠错；通信双方必须使用相同分片组合。
	FEC FECConfig
	// DSCP 是 IPv4 六位 DSCP/IPv6 Traffic Class，范围 0..63；零表示不设置。
	DSCP int
	// SocketReadBuffer 设置 UDP 接收缓冲；0B 保留操作系统默认值。
	SocketReadBuffer originconfig.ByteSize
	// SocketWriteBuffer 设置 UDP 发送缓冲；0B 保留操作系统默认值。
	SocketWriteBuffer originconfig.ByteSize
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
	// ReadIdleTimeout 是完整业务消息的读空闲上限，必须大于零。
	// KCP 没有 FIN，生产值必须大于业务心跳最大间隔。
	ReadIdleTimeout originconfig.Duration
	// WriteTimeout 是写出一条完整业务消息的强制上限。
	WriteTimeout originconfig.Duration
	// SlowClientTimeout 是发送队列连续处于高水位的最长时间。
	SlowClientTimeout originconfig.Duration
}

// ClientConfig 配置由 Service 生命周期托管的单 Session KCP Client。
//
// Dialer 是一次性的代码对象，只使用 DialOptions，不从 Service 配置读取参数。KCP 没有远端握手，
// 因此 Client 也不提供会产生错误可用性承诺的 dial_timeout 字段；首条业务应答才证明远端可用。
type ClientConfig struct {
	// Address 是托管 Client 使用的远端 UDP 地址。
	Address string
	// Frame 配置每条 KCP 逻辑消息的长度前缀。
	Frame FrameConfig
	// MTU 是不含 UDP/IP 头的 KCP 报文上限。
	MTU int
	// SendWindow 是 KCP 发送窗口，单位为 Segment。
	SendWindow int
	// ReceiveWindow 是 KCP 接收窗口，单位为 Segment。
	ReceiveWindow int
	// NoDelay 配置 KCP 低延迟模式。
	NoDelay NoDelayConfig
	// ACKNoDelay 立即发送 ACK，可降低确认时延但会增加小包。
	ACKNoDelay bool
	// WriteDelay 把写入延迟到下一次 KCP 更新；实时消息默认关闭。
	WriteDelay bool
	// FEC 配置前向纠错；通信双方必须使用相同分片组合。
	FEC FECConfig
	// DSCP 是六位服务质量标记；零表示不设置。
	DSCP int
	// SocketReadBuffer 设置当前 UDP socket 接收缓冲；0B 保留 OS 默认值。
	SocketReadBuffer originconfig.ByteSize
	// SocketWriteBuffer 设置当前 UDP socket 发送缓冲；0B 保留 OS 默认值。
	SocketWriteBuffer originconfig.ByteSize
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
	// ReadIdleTimeout 是完整业务消息的读空闲上限，必须大于零。
	ReadIdleTimeout originconfig.Duration
	// WriteTimeout 是写出一条完整业务消息的强制上限。
	WriteTimeout originconfig.Duration
	// SlowClientTimeout 是发送队列连续处于高水位的最长时间。
	SlowClientTimeout originconfig.Duration
	// Reconnect 配置 Session 失活或本地创建失败后的有界自动重连。
	Reconnect ReconnectConfig
}

// DefaultServerConfig 返回与 DefaultServerOptions 完全一致的 KCP Server 默认配置。
func DefaultServerConfig() ServerConfig {
	options := DefaultServerOptions(network.HandlerFuncs{})
	return ServerConfig{
		Address:                 "0.0.0.0:19092",
		Frame:                   kcpFrameConfig(options.Frame),
		MTU:                     options.MTU,
		SendWindow:              options.SendWindow,
		ReceiveWindow:           options.ReceiveWindow,
		NoDelay:                 kcpNoDelayConfig(options.NoDelay),
		ACKNoDelay:              options.ACKNoDelay,
		WriteDelay:              options.WriteDelay,
		FEC:                     kcpFECConfig(options.FEC),
		DSCP:                    options.DSCP,
		SocketReadBuffer:        originconfig.ByteSize(options.SocketReadBuffer),
		SocketWriteBuffer:       originconfig.ByteSize(options.SocketWriteBuffer),
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

// DefaultClientConfig 返回默认不自动重连的托管 KCP Client 配置。
func DefaultClientConfig() ClientConfig {
	options := DefaultClientOptions(network.HandlerFuncs{})
	return ClientConfig{
		Address:                "127.0.0.1:19092",
		Frame:                  kcpFrameConfig(options.Dial.Frame),
		MTU:                    options.Dial.MTU,
		SendWindow:             options.Dial.SendWindow,
		ReceiveWindow:          options.Dial.ReceiveWindow,
		NoDelay:                kcpNoDelayConfig(options.Dial.NoDelay),
		ACKNoDelay:             options.Dial.ACKNoDelay,
		WriteDelay:             options.Dial.WriteDelay,
		FEC:                    kcpFECConfig(options.Dial.FEC),
		DSCP:                   options.Dial.DSCP,
		SocketReadBuffer:       originconfig.ByteSize(options.Dial.SocketReadBuffer),
		SocketWriteBuffer:      originconfig.ByteSize(options.Dial.SocketWriteBuffer),
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

// Options 把 KCP Server 配置转换为已经完整校验的运行期 Options。
//
// 如需加密，应在本方法成功后设置返回值的 BlockCrypt，再交给 NewServer 做包含加密头的最终校验。
func (configured ServerConfig) Options(handler network.Handler) (ServerOptions, error) {
	if err := validateAddress(configured.Address); err != nil {
		return ServerOptions{}, err
	}
	frame, err := configured.Frame.options()
	if err != nil {
		return ServerOptions{}, err
	}
	maxMessageSize, err := kcpConfigMessageSize(configured.MaxMessageSize)
	if err != nil {
		return ServerOptions{}, err
	}
	readBuffer, err := kcpConfigSocketBuffer("socket_read_buffer", configured.SocketReadBuffer)
	if err != nil {
		return ServerOptions{}, err
	}
	writeBuffer, err := kcpConfigSocketBuffer("socket_write_buffer", configured.SocketWriteBuffer)
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
		Frame:             frame,
		MTU:               configured.MTU,
		SendWindow:        configured.SendWindow,
		ReceiveWindow:     configured.ReceiveWindow,
		NoDelay:           configured.NoDelay.options(),
		ACKNoDelay:        configured.ACKNoDelay,
		WriteDelay:        configured.WriteDelay,
		FEC:               configured.FEC.options(),
		DSCP:              configured.DSCP,
		SocketReadBuffer:  readBuffer,
		SocketWriteBuffer: writeBuffer,
	}
	if err := validateServerOptions(options); err != nil {
		return ServerOptions{}, err
	}
	return options, nil
}

// dialOptions 把托管 Client 的连接字段转换为固定单 Session 的运行期 Options。
func (configured ClientConfig) dialOptions(handler network.Handler) (DialOptions, error) {
	if err := validateAddress(configured.Address); err != nil {
		return DialOptions{}, err
	}
	frame, err := configured.Frame.options()
	if err != nil {
		return DialOptions{}, err
	}
	maxMessageSize, err := kcpConfigMessageSize(configured.MaxMessageSize)
	if err != nil {
		return DialOptions{}, err
	}
	readBuffer, err := kcpConfigSocketBuffer("socket_read_buffer", configured.SocketReadBuffer)
	if err != nil {
		return DialOptions{}, err
	}
	writeBuffer, err := kcpConfigSocketBuffer("socket_write_buffer", configured.SocketWriteBuffer)
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
		Frame:             frame,
		MTU:               configured.MTU,
		SendWindow:        configured.SendWindow,
		ReceiveWindow:     configured.ReceiveWindow,
		NoDelay:           configured.NoDelay.options(),
		ACKNoDelay:        configured.ACKNoDelay,
		WriteDelay:        configured.WriteDelay,
		FEC:               configured.FEC.options(),
		DSCP:              configured.DSCP,
		SocketReadBuffer:  readBuffer,
		SocketWriteBuffer: writeBuffer,
	}
	if err := validateDialOptions(options); err != nil {
		return DialOptions{}, err
	}
	return options, nil
}

// Options 把托管 KCP Client 配置转换为已经完整校验的运行期 Options。
//
// 如需加密，应设置返回值的 Dial.BlockCrypt，再交给 NewClient 做最终校验。
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
	if err := validateClientOptions(options); err != nil {
		return ClientOptions{}, err
	}
	return options, nil
}

func (configured FrameConfig) options() (FrameOptions, error) {
	var order network.ByteOrder
	switch configured.ByteOrder {
	case FrameByteOrderBig:
		order = network.BigEndian
	case FrameByteOrderLittle:
		order = network.LittleEndian
	default:
		return FrameOptions{}, errs.NewMessage(
			errs.CodeInvalidConfig,
			"kcp.frame.byte_order 只能是 big 或 little",
		)
	}
	return FrameOptions{LengthFieldSize: configured.LengthFieldSize, ByteOrder: order}, nil
}

func (configured NoDelayConfig) options() NoDelayOptions {
	return NoDelayOptions{
		Enabled:                  configured.Enabled,
		Interval:                 configured.Interval.Duration(),
		FastResend:               configured.FastResend,
		DisableCongestionControl: configured.DisableCongestionControl,
	}
}

func (configured FECConfig) options() FECOptions {
	return FECOptions{DataShards: configured.DataShards, ParityShards: configured.ParityShards}
}

func kcpFrameConfig(options FrameOptions) FrameConfig {
	order := FrameByteOrderBig
	if options.ByteOrder == network.LittleEndian {
		order = FrameByteOrderLittle
	}
	return FrameConfig{LengthFieldSize: options.LengthFieldSize, ByteOrder: order}
}

func kcpNoDelayConfig(options NoDelayOptions) NoDelayConfig {
	return NoDelayConfig{
		Enabled:                  options.Enabled,
		Interval:                 originconfig.Duration(options.Interval),
		FastResend:               options.FastResend,
		DisableCongestionControl: options.DisableCongestionControl,
	}
}

func kcpFECConfig(options FECOptions) FECConfig {
	return FECConfig{DataShards: options.DataShards, ParityShards: options.ParityShards}
}

func kcpConfigMessageSize(value originconfig.ByteSize) (int, error) {
	bytes := value.Bytes()
	if bytes > int64(math.MaxInt) {
		return 0, errs.NewMessage(
			errs.CodeInvalidConfig,
			"kcp.max_message_size 超出当前平台 int 范围",
		)
	}
	return int(bytes), nil
}

func kcpConfigSocketBuffer(name string, value originconfig.ByteSize) (int, error) {
	bytes := value.Bytes()
	if bytes > int64(math.MaxInt) {
		return 0, errs.NewMessage(
			errs.CodeInvalidConfig,
			"kcp."+name+" 超出当前平台 int 范围",
		)
	}
	return int(bytes), nil
}
