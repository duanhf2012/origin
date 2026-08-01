package rpc

import (
	"math"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/duanhf2012/origin/v3/errs"
)

const (
	// TransportTCP 表示当前 Node 通过 Origin TCP 线协议收发远程 RPC。
	TransportTCP = "tcp"
	// TransportNATS 表示当前 Node 通过共享 NATS Connection 收发远程 RPC。
	TransportNATS = "nats"

	// DefaultSendQueueMessages 为每条 TCP 连接保留足够的小包突发容量。
	DefaultSendQueueMessages = 16_384
	// MaxSendQueueMessages 防止项目用超大 TCP 队列长期隐藏过载。
	MaxSendQueueMessages = 65_536
	// DefaultReceiveQueueMessages 是每条 NATS Request/Response Subscription 的默认回调队列。
	DefaultReceiveQueueMessages = 16_384
	// MaxReceiveQueueMessages 防止慢消费者把大量 NATS 消息长期留在客户端内存中。
	MaxReceiveQueueMessages = 65_536
	// DefaultReadIdleTimeout 同时决定应用层 Ping/Pong 和连接读空闲检测周期。
	DefaultReadIdleTimeout = 15 * time.Second
	// DefaultWriteTimeout 限制写出一个完整 TCP RPC 帧的最长时间。
	DefaultWriteTimeout = 15 * time.Second
	// DefaultDialTimeout 限制一次 TCP Dial 冷路径。
	DefaultDialTimeout = 5 * time.Second
	// DefaultHandshakeTimeout 限制连接建立后的 TCP 身份与目录交换。
	DefaultHandshakeTimeout = 5 * time.Second
	// DefaultPendingPerSession 是单条 TCP 出站会话的固定有界 Request 上限。
	DefaultPendingPerSession = 65_536
	// DefaultPendingPerNode 是一个 NATS Node 的固定有界 Request 上限。
	DefaultPendingPerNode = 65_536
	// DefaultMaxBroadcastSize 限制一次广播按全部意图目标放大的业务 payload 总字节数。
	DefaultMaxBroadcastSize = 64 * 1024 * 1024
	// MaxBroadcastSize 是项目可以显式配置的一次广播放大硬上限。
	MaxBroadcastSize = 1024 * 1024 * 1024
)

// Config 是一个 Node 创建后冻结的远程 RPC 配置。
//
// TCP 与 NATS 使用指针表达配置块是否存在。这样 Validate 可以在创建网络资源前严格拒绝
// “选择 TCP 却遗漏 tcp”以及“同时配置 tcp 和 nats”这类容易造成部署歧义的配置。
type Config struct {
	// Transport 选择当前 Node 使用的远程传输，只允许 tcp 或 nats。
	Transport string
	// MaxPayloadSize 是不含 Origin 包络的单个业务 payload 上限。
	MaxPayloadSize int
	// MaxBroadcastSize 是一次广播的 payload 大小乘以意图目标数的上限。
	MaxBroadcastSize int
	// TCP 只在 TransportTCP 下存在。
	TCP *TCPConfig
	// NATS 只在 TransportNATS 下存在。
	NATS *NATSConfig
}

// TCPConfig 定义 TCP RPC 对项目公开的最小配置。
type TCPConfig struct {
	// Listen 是当前 Node 绑定的 TCP 地址。
	Listen string
	// Advertise 是其他 Node 可以连接的确定地址。
	Advertise string
	// SendQueueMessages 是每条连接最多等待发送的完整 RPC 消息数量。
	SendQueueMessages int
	// ReadIdleTimeout 是连接读空闲上限；零表示关闭应用层心跳和底层读超时。
	ReadIdleTimeout time.Duration
	// WriteTimeout 是写一个完整帧的上限，必须为正数。
	WriteTimeout time.Duration
}

// NATSConfig 定义 NATS RPC 对项目公开的最小配置。
//
// 重连次数、Ping、Drain、NoEcho 和 Reconnect Buffer 等 nats.go 细节由 RPC Adapter 使用
// 固定安全默认值，避免业务配置与底层客户端版本强耦合。
type NATSConfig struct {
	// Namespace 隔离开发、测试和生产环境，并作为 NATS Subject 的单个 Token。
	Namespace string
	// URLs 是初始 NATS Server 地址快照。
	URLs []string
	// ReceiveQueueMessages 分别限制 Request 和 Response Subscription 的待回调消息数。
	ReceiveQueueMessages int
	// Auth 配置四种互斥认证方式之一。
	Auth NATSAuthConfig
	// TLS 配置服务端校验和可选双向证书。
	TLS NATSTLSConfig
}

// NATSAuthConfig 配置 NATS 用户认证；密码和凭据内容不得进入日志或错误详情。
type NATSAuthConfig struct {
	Username        string
	Password        string
	Token           string
	CredentialsFile string
	NKeySeedFile    string
}

// NATSTLSConfig 配置 NATS TLS 和可选双向证书。
type NATSTLSConfig struct {
	Enabled            bool
	CAFile             string
	CertFile           string
	KeyFile            string
	ServerName         string
	InsecureSkipVerify bool
}

// DefaultTCPConfig 返回具有稳定容量和超时的 TCP 配置块。
func DefaultTCPConfig() *TCPConfig {
	return &TCPConfig{
		SendQueueMessages: DefaultSendQueueMessages,
		ReadIdleTimeout:   DefaultReadIdleTimeout,
		WriteTimeout:      DefaultWriteTimeout,
	}
}

// DefaultNATSConfig 返回具有稳定接收队列容量的 NATS 配置块。
//
// Namespace 和 URLs 与实际部署相关，因此必须由项目显式填写。
func DefaultNATSConfig() *NATSConfig {
	return &NATSConfig{
		ReceiveQueueMessages: DefaultReceiveQueueMessages,
	}
}

// DefaultConfig 返回 TCP RPC 的完整默认快照。
//
// Listen 和 Advertise 必须由项目填写；其余字段可直接沿用。调用方若选择 NATS，应把 TCP
// 置 nil，并从 DefaultNATSConfig 开始构造 NATS 配置块。
func DefaultConfig() Config {
	return Config{
		Transport:        TransportTCP,
		MaxPayloadSize:   DefaultMaxPayloadSize,
		MaxBroadcastSize: DefaultMaxBroadcastSize,
		TCP:              DefaultTCPConfig(),
	}
}

// Validate 在创建 Connection、Listener 或 goroutine 前验证完整 RPC 配置。
func (config Config) Validate() error {
	// 所有传输共享同一个业务 payload 上限；外层包络由各 Adapter 单独预留。
	if config.MaxPayloadSize <= 0 ||
		config.MaxPayloadSize > math.MaxInt-wireEnvelopeSize ||
		uint64(config.MaxPayloadSize) > uint64(math.MaxUint32-wireEnvelopeSize) {
		return invalidRPCConfig("rpc.max_payload_size 超出四字节帧可表达范围")
	}
	// 广播放大上限必须为正且不得超过已经确认的 1G 硬边界。
	if config.MaxBroadcastSize <= 0 || config.MaxBroadcastSize > MaxBroadcastSize {
		return invalidRPCConfig("rpc.max_broadcast_size 必须位于 1B～1G")
	}

	// 传输类型决定且只决定一个有效配置块，禁止静默忽略另一个配置块。
	switch config.Transport {
	case TransportTCP:
		if config.TCP == nil {
			return invalidRPCConfig("rpc.transport 为 tcp 时必须配置 rpc.tcp")
		}
		if config.NATS != nil {
			return invalidRPCConfig("rpc.transport 为 tcp 时不能配置 rpc.nats")
		}
		return validateTCPConfig(*config.TCP)
	case TransportNATS:
		if config.NATS == nil {
			return invalidRPCConfig("rpc.transport 为 nats 时必须配置 rpc.nats")
		}
		if config.TCP != nil {
			return invalidRPCConfig("rpc.transport 为 nats 时不能配置 rpc.tcp")
		}
		return validateNATSConfig(*config.NATS)
	default:
		return invalidRPCConfig("rpc.transport 必须是 tcp 或 nats")
	}
}

// validateTCPConfig 校验 TCP 地址、队列和读写超时。
func validateTCPConfig(config TCPConfig) error {
	if config.SendQueueMessages <= 0 ||
		config.SendQueueMessages > MaxSendQueueMessages {
		return invalidRPCConfig("rpc.tcp.send_queue_messages 必须位于 1～65536")
	}
	if config.ReadIdleTimeout < 0 {
		return invalidRPCConfig("rpc.tcp.read_idle_timeout 不能为负数")
	}
	if config.WriteTimeout <= 0 {
		return invalidRPCConfig("rpc.tcp.write_timeout 必须大于零")
	}
	if err := validateListenAddress(config.Listen); err != nil {
		return err
	}
	return validateAdvertiseAddress(config.Advertise)
}

// validateNATSConfig 校验 Subject 隔离、服务器、回调队列以及认证与 TLS 互斥关系。
func validateNATSConfig(config NATSConfig) error {
	if !validSubjectToken(config.Namespace) {
		return invalidRPCConfig(
			"rpc.nats.namespace 必须是 63 字符以内的小写 kebab-case",
		)
	}
	if len(config.URLs) == 0 {
		return invalidRPCConfig("rpc.nats.urls 不能为空")
	}
	for _, rawURL := range config.URLs {
		parsed, err := url.Parse(rawURL)
		if err != nil || parsed.Host == "" {
			return invalidRPCConfig("rpc.nats.urls 包含无效地址")
		}
		if parsed.Scheme != "nats" && parsed.Scheme != "tls" {
			return invalidRPCConfig("rpc.nats.urls 只支持 nats 或 tls")
		}
	}
	if config.ReceiveQueueMessages <= 0 ||
		config.ReceiveQueueMessages > MaxReceiveQueueMessages {
		return invalidRPCConfig("rpc.nats.receive_queue_messages 必须位于 1～65536")
	}

	// 用户名认证、Token、Credentials 和 NKey 是四种互斥方式。
	authModes := 0
	if config.Auth.Username != "" || config.Auth.Password != "" {
		authModes++
		if config.Auth.Username == "" {
			return invalidRPCConfig("rpc.nats.auth.password 必须与 username 一起配置")
		}
	}
	if config.Auth.Token != "" {
		authModes++
	}
	if config.Auth.CredentialsFile != "" {
		authModes++
	}
	if config.Auth.NKeySeedFile != "" {
		authModes++
	}
	if authModes > 1 {
		return invalidRPCConfig("rpc.nats.auth 只能选择一种认证方式")
	}

	// 双向 TLS 的证书与私钥必须成对出现；其余文件可由底层在连接前详细校验。
	if (config.TLS.CertFile == "") != (config.TLS.KeyFile == "") {
		return invalidRPCConfig("rpc.nats.tls.cert_file 和 key_file 必须同时配置")
	}
	hasTLSFields := config.TLS.CAFile != "" ||
		config.TLS.CertFile != "" ||
		config.TLS.KeyFile != "" ||
		config.TLS.ServerName != "" ||
		config.TLS.InsecureSkipVerify
	if hasTLSFields && !config.TLS.Enabled {
		return invalidRPCConfig("rpc.nats.tls 其他字段需要先启用 enabled")
	}
	return nil
}

// validSubjectToken 验证 Namespace 使用单个安全 NATS Subject Token。
func validSubjectToken(value string) bool {
	if len(value) == 0 || len(value) > 63 ||
		value[0] < 'a' || value[0] > 'z' ||
		value[len(value)-1] == '-' {
		return false
	}
	previousDash := false
	for index := 1; index < len(value); index++ {
		character := value[index]
		switch {
		case character >= 'a' && character <= 'z':
			previousDash = false
		case character >= '0' && character <= '9':
			previousDash = false
		case character == '-' && !previousDash:
			previousDash = true
		default:
			return false
		}
	}
	return true
}

// frameLimit 返回交给 M5 的“业务 payload + TCP RPC 包络”完整帧上限。
func (config Config) frameLimit() int {
	return config.MaxPayloadSize + wireEnvelopeSize
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
