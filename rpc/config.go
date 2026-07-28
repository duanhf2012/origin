package rpc

import (
	"math"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/duanhf2012/origin/v3/errs"
)

const (
	// TransportTCP 是 M13 唯一具有运行语义的远端 RPC Transport。
	TransportTCP = "tcp"

	// DefaultSendQueueFrames 为可信 Node 连接保留足够的小包突发容量。
	DefaultSendQueueFrames = 16_384
	// MaxSendQueueFrames 防止项目用超大队列长期隐藏过载。
	MaxSendQueueFrames = 65_536
	// DefaultReadTimeout 同时决定应用层 Ping/Pong 和读空闲检测周期。
	DefaultReadTimeout = 15 * time.Second
	// DefaultWriteTimeout 限制写出一个完整 RPC 帧的最长时间。
	DefaultWriteTimeout = 15 * time.Second
	// DefaultDialTimeout 限制一次 TCP Dial 冷路径。
	DefaultDialTimeout = 5 * time.Second
	// DefaultHandshakeTimeout 限制连接建立后的 ORP1 身份与目录交换。
	DefaultHandshakeTimeout = 5 * time.Second
	// DefaultPendingPerSession 是单条出站会话的固定有界 Request 上限。
	DefaultPendingPerSession = 65_536

	// rpcMinimumSendQueueBytes 是不向配置公开的 M5 大包内存保护下限。
	rpcMinimumSendQueueBytes = 8 * 1024 * 1024
)

// Config 是一个 Node 创建后冻结的远端 RPC 配置。
//
// 配置省略由 node.Config 的 nil 指针表达；存在 Config 时 Transport 必须为 tcp。
type Config struct {
	// Transport 选择当前 Node 使用的远端传输。
	Transport string
	// MaxMessageSize 是不含 ORP1 包络的业务 payload 上限。
	MaxMessageSize int
	// TCP 保存当前 Node 的监听、发送队列和连接超时。
	TCP TCPConfig
}

// TCPConfig 定义 M13 TCP RPC 的最小公开配置。
type TCPConfig struct {
	// Listen 是当前 Node 绑定的 TCP 地址。
	Listen string
	// Advertise 是其他 Node 可以连接的确定地址。
	Advertise string
	// SendQueueFrames 是每条连接最多等待发送的完整 RPC 包数量。
	SendQueueFrames int
	// ReadTimeout 是读空闲上限；零同时关闭应用层心跳和 M5 ReadTimeout。
	ReadTimeout time.Duration
	// WriteTimeout 是写一个完整帧的上限，必须为正数。
	WriteTimeout time.Duration
}

// DefaultConfig 返回 M13 TCP RPC 的完整默认值。
//
// Listen 和 Advertise 必须由项目填写；其余字段可以直接沿用该快照。
func DefaultConfig() Config {
	return Config{
		Transport:      TransportTCP,
		MaxMessageSize: DefaultMaxMessageSize,
		TCP: TCPConfig{
			SendQueueFrames: DefaultSendQueueFrames,
			ReadTimeout:     DefaultReadTimeout,
			WriteTimeout:    DefaultWriteTimeout,
		},
	}
}

// Validate 在创建 Listener、连接或 goroutine 前验证完整 RPC 配置。
func (config Config) Validate() error {
	// M13 不能接受尚未实现的 NATS 或未知 Transport。
	if config.Transport != TransportTCP {
		return invalidRPCConfig("rpc.transport 必须是 tcp")
	}
	if config.MaxMessageSize <= 0 ||
		config.MaxMessageSize > math.MaxInt-wireEnvelopeSize ||
		uint64(config.MaxMessageSize) > uint64(math.MaxUint32-wireEnvelopeSize) {
		return invalidRPCConfig("rpc.max_message_size 超出 ORP1 四字节帧可表达范围")
	}
	if config.TCP.SendQueueFrames <= 0 ||
		config.TCP.SendQueueFrames > MaxSendQueueFrames {
		return invalidRPCConfig("rpc.tcp.send_queue_frames 必须位于 1～65536")
	}
	if config.TCP.ReadTimeout < 0 {
		return invalidRPCConfig("rpc.tcp.read_timeout 不能为负数")
	}
	if config.TCP.WriteTimeout <= 0 {
		return invalidRPCConfig("rpc.tcp.write_timeout 必须大于零")
	}
	if err := validateListenAddress(config.TCP.Listen); err != nil {
		return err
	}
	if err := validateAdvertiseAddress(config.TCP.Advertise); err != nil {
		return err
	}
	return nil
}

// frameLimit 返回交给 M5 的“业务 payload + ORP1 包络”完整帧上限。
func (config Config) frameLimit() int {
	return config.MaxMessageSize + wireEnvelopeSize
}

// sendQueueBytes 返回 M5 双重有界队列使用的内部字节额度。
func (config Config) sendQueueBytes() int {
	// 至少允许一条最大合法帧进入队列；普通 4M 配置仍使用稳定的 8M 下限。
	limit := config.frameLimit()
	if limit < rpcMinimumSendQueueBytes {
		return rpcMinimumSendQueueBytes
	}
	return limit
}

// validateListenAddress 校验监听地址具有可解析主机和非零端口。
func validateListenAddress(address string) error {
	trimmed := strings.TrimSpace(address)
	host, portText, err := net.SplitHostPort(trimmed)
	if err != nil {
		return invalidRPCConfig("rpc.tcp.listen 必须是 host:port")
	}
	// Listen 允许空 host 和通配地址，但端口必须由部署配置明确给出。
	_ = host
	port, err := strconv.Atoi(portText)
	if err != nil || port <= 0 || port > math.MaxUint16 {
		return invalidRPCConfig("rpc.tcp.listen 端口必须位于 1～65535")
	}
	return nil
}

// validateAdvertiseAddress 拒绝其他 Node 无法连接的通配主机、空主机和零端口。
func validateAdvertiseAddress(address string) error {
	trimmed := strings.TrimSpace(address)
	host, portText, err := net.SplitHostPort(trimmed)
	if err != nil {
		return invalidRPCConfig("rpc.tcp.advertise 必须是 host:port")
	}
	host = strings.TrimSpace(host)
	if host == "" || host == "0.0.0.0" || host == "::" {
		return invalidRPCConfig("rpc.tcp.advertise 不能使用空主机或通配地址")
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port <= 0 || port > math.MaxUint16 {
		return invalidRPCConfig("rpc.tcp.advertise 端口必须位于 1～65535")
	}
	return nil
}

// invalidRPCConfig 创建带稳定错误码的配置诊断。
func invalidRPCConfig(message string) error {
	return errs.NewMessage(errs.CodeInvalidConfig, message)
}
