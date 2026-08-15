// Package natsnet 提供 Origin 框架内部复用的 Core NATS 传输能力。
//
// natsnet 只负责连接、Subject、原始字节消息、有限重连和资源生命周期，
// 不包含 Origin RPC Subject、RequestID、ServiceName、序列化或业务调度语义。
package natsnet

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	originlog "github.com/duanhf2012/origin/v3/log"
	"github.com/nats-io/nats.go"
)

const (
	// 通用 NATS 默认单消息上限为 4M；RPC Adapter 会按自己的固定上限显式覆盖。
	defaultMaxMessageSize = 4 * 1024 * 1024
	// 默认连接超时只约束一次 Server TCP 连接和协议握手。
	defaultConnectTimeout = 2 * time.Second
	// 未提供 Deadline 的 Flush、Drain 和 Wait 边界使用 15 秒。
	defaultOperationTimeout = 15 * time.Second
	// NATS 内部 Connection Drain 最长允许 30 秒。
	defaultDrainTimeout = 30 * time.Second
	// 30 秒 Ping 可以比官方默认值更早发现黑洞连接。
	defaultPingInterval = 30 * time.Second
	// 连续两个 Ping 未响应后由官方客户端判定连接失活。
	defaultMaxPingsOutstanding = 2
	// 成功连接后的自动重连最多尝试 60 次。
	defaultMaxReconnectAttempts = 60
	// 重连基础等待沿用成熟客户端的两秒节奏。
	defaultReconnectWait = 2 * time.Second
	// 普通连接增加最多 500ms 抖动，避免大量 Node 同时重连。
	defaultReconnectJitter = 500 * time.Millisecond
	// TLS 建连成本更高，使用最多一秒抖动。
	defaultReconnectTLSJitter = time.Second
	// 重连期间官方客户端最多保留 8M 尚未写出的协议数据。
	defaultReconnectBufferSize = 8 * 1024 * 1024
	// 单订阅默认允许 16384 条待回调消息。
	defaultPendingMessages = 16384
)

// Options 配置一条由 Node 独占、可复用多个 Subject 的 NATS Connection。
type Options struct {
	// Name 是 NATS 监控中显示的客户端名称，必须非空。
	Name string
	// URLs 是初始 Server 地址快照，至少包含一个 nats:// 或 tls:// 地址。
	URLs []string
	// NoRandomize 禁止官方客户端随机选择初始 Server。
	NoRandomize bool
	// NoEcho 禁止当前 Connection 收到自己发布的匹配消息。
	NoEcho bool
	// MaxMessageSize 同时限制本地发布和进入 Handler 的 payload。
	MaxMessageSize int
	// ConnectTimeout 限制单次 TCP 建连和初始握手。
	ConnectTimeout time.Duration
	// DefaultOperationTimeout 为无 Deadline 操作提供统一保底。
	DefaultOperationTimeout time.Duration
	// DrainTimeout 是 NATS Connection 内部排空的最长时间。
	DrainTimeout time.Duration
	// PingInterval 是客户端主动探活周期。
	PingInterval time.Duration
	// MaxPingsOutstanding 是判定失活前允许未响应的 Ping 数量。
	MaxPingsOutstanding int
	// Reconnect 配置成功连接后的有限自动重连。
	Reconnect ReconnectOptions
	// IgnoreAuthErrorAbort 允许官方客户端在重复认证错误后继续重连。
	//
	// 普通调用方默认关闭；Origin RPC Adapter 会固定开启，使凭据轮换后连接可以自行恢复，
	// 而不是因连续两次认证失败永久关闭。
	IgnoreAuthErrorAbort bool
	// Subscription 是每条订阅未显式覆盖时使用的 Pending 上限。
	Subscription SubscriptionDefaults
	// Auth 配置四种互斥认证方式之一。
	Auth AuthOptions
	// TLS 配置服务端校验以及可选双向证书。
	TLS TLSOptions
	// Logger 记录低频生命周期和异常；零值等同 Nop Logger。
	Logger originlog.Logger
}

// ReconnectOptions 配置已连接 Connection 的自动恢复和本地缓冲边界。
type ReconnectOptions struct {
	// Enabled 决定连接断开后是否由 nats.go 自动重连。
	Enabled bool
	// MaxAttempts 是自动重连次数上限；-1 是唯一合法的无限重试哨兵。
	MaxAttempts int
	// Wait 是相邻重连尝试的基础等待。
	Wait time.Duration
	// Jitter 是普通连接额外随机等待的上限。
	Jitter time.Duration
	// TLSJitter 是 TLS 连接额外随机等待的上限。
	TLSJitter time.Duration
	// BufferSize 限制重连期间尚未发出的协议数据。
	BufferSize int
}

// SubscriptionDefaults 配置每条异步订阅的默认 Pending 消息数上限。
type SubscriptionDefaults struct {
	// PendingMessages 限制尚未进入或完成回调的消息数。
	PendingMessages int
}

// AuthOptions 配置 NATS 客户端认证；四种认证模式互斥。
type AuthOptions struct {
	// Username 与 Password 组成普通用户认证。
	Username string
	// Password 只能在 Username 非空时出现。
	Password string
	// Token 配置单 Token 认证。
	Token string
	// CredentialsFile 指向包含用户 JWT 和 Seed 的官方 Credentials 文件。
	CredentialsFile string
	// NKeySeedFile 指向单独的用户 NKey Seed 文件。
	NKeySeedFile string
}

// TLSOptions 配置 NATS TLS 连接。
type TLSOptions struct {
	// Enabled 显式启用 TLS；tls:// URL 也会隐式启用。
	Enabled bool
	// CAFile 是用于校验服务端证书的可选 PEM CA 文件。
	CAFile string
	// CertFile 和 KeyFile 必须同时配置，用于双向 TLS。
	CertFile string
	// KeyFile 是客户端证书对应的私钥文件。
	KeyFile string
	// ServerName 覆盖 TLS 服务端名称校验目标。
	ServerName string
	// InsecureSkipVerify 显式关闭证书校验，只适合受控测试环境。
	InsecureSkipVerify bool
}

// SubscriptionOptions 配置一条普通订阅或 Queue Group 订阅。
type SubscriptionOptions struct {
	// Queue 为空表示普通订阅，非空表示 Queue Group 名称。
	Queue string
	// PendingMessages 为零时使用 Connection 默认值。
	PendingMessages int
}

// DefaultOptions 返回一份具有完整安全边界的 NATS 默认配置。
func DefaultOptions(name string, urls ...string) Options {
	// URLs 复制到独占切片，避免调用方随后修改配置快照。
	copiedURLs := append([]string(nil), urls...)
	return Options{
		Name:                    name,
		URLs:                    copiedURLs,
		MaxMessageSize:          defaultMaxMessageSize,
		ConnectTimeout:          defaultConnectTimeout,
		DefaultOperationTimeout: defaultOperationTimeout,
		DrainTimeout:            defaultDrainTimeout,
		PingInterval:            defaultPingInterval,
		MaxPingsOutstanding:     defaultMaxPingsOutstanding,
		Reconnect: ReconnectOptions{
			Enabled:     true,
			MaxAttempts: defaultMaxReconnectAttempts,
			Wait:        defaultReconnectWait,
			Jitter:      defaultReconnectJitter,
			TLSJitter:   defaultReconnectTLSJitter,
			BufferSize:  defaultReconnectBufferSize,
		},
		Subscription: SubscriptionDefaults{
			PendingMessages: defaultPendingMessages,
		},
		Logger: originlog.NewNop(),
	}
}

// validateOptions 在读取凭据文件、建立 socket 或启动 goroutine 前验证完整配置。
func validateOptions(options Options) (bool, error) {
	// Name 和 URL 是定位连接以及建立连接池的最小必填信息。
	if strings.TrimSpace(options.Name) == "" {
		return false, invalidConfig("natsnet: Name 不能为空")
	}
	if len(options.URLs) == 0 {
		return false, invalidConfig("natsnet: URLs 不能为空")
	}

	// 检查所有地址格式、传输协议和内嵌认证的一致性。
	var hasPlainURL bool
	var hasTLSURL bool
	var hasURLAuth bool
	for _, rawURL := range options.URLs {
		parsed, err := url.Parse(rawURL)
		if err != nil || parsed.Host == "" {
			return false, invalidConfig("natsnet: URL 格式无效")
		}
		switch strings.ToLower(parsed.Scheme) {
		case "nats":
			hasPlainURL = true
		case "tls":
			hasTLSURL = true
		default:
			return false, invalidConfig("natsnet: URL 只支持 nats 或 tls Scheme")
		}
		if parsed.User != nil {
			hasURLAuth = true
		}
	}
	if hasPlainURL && hasTLSURL {
		return false, invalidConfig("natsnet: 不能混用明文与 TLS Seed URL")
	}

	// 消息、超时和探活参数必须形成明确且有界的运行配置。
	if options.MaxMessageSize <= 0 {
		return false, invalidConfig("natsnet: MaxMessageSize 必须大于零")
	}
	if options.ConnectTimeout <= 0 {
		return false, invalidConfig("natsnet: ConnectTimeout 必须大于零")
	}
	if options.DefaultOperationTimeout <= 0 {
		return false, invalidConfig("natsnet: DefaultOperationTimeout 必须大于零")
	}
	if options.DrainTimeout <= 0 {
		return false, invalidConfig("natsnet: DrainTimeout 必须大于零")
	}
	if options.PingInterval <= 0 {
		return false, invalidConfig("natsnet: PingInterval 必须大于零")
	}
	if options.MaxPingsOutstanding <= 0 {
		return false, invalidConfig("natsnet: MaxPingsOutstanding 必须大于零")
	}

	// -1 与 nats.go 的标准无限重试语义一致；其他负数仍属于容易误配的非法值。
	if options.Reconnect.MaxAttempts < -1 {
		return false, invalidConfig("natsnet: Reconnect.MaxAttempts 只能为 -1 或非负数")
	}
	if options.Reconnect.Wait <= 0 {
		return false, invalidConfig("natsnet: Reconnect.Wait 必须大于零")
	}
	if options.Reconnect.Jitter < 0 || options.Reconnect.TLSJitter < 0 {
		return false, invalidConfig("natsnet: Reconnect Jitter 不能为负数")
	}
	if options.Reconnect.BufferSize != -1 &&
		options.Reconnect.BufferSize < options.MaxMessageSize {
		return false, invalidConfig("natsnet: Reconnect.BufferSize 不能小于 MaxMessageSize")
	}

	// Subscription 只保留消息数上限；字节数由 nats.go 的 -1 明确关闭。
	if options.Subscription.PendingMessages <= 0 {
		return false, invalidConfig("natsnet: Subscription.PendingMessages 必须大于零")
	}

	// 认证模式严格互斥，URL 内嵌认证也视作一种独立模式。
	authModes := 0
	if options.Auth.Username != "" || options.Auth.Password != "" {
		if options.Auth.Username == "" {
			return false, invalidConfig("natsnet: Password 不能脱离 Username 使用")
		}
		authModes++
	}
	if options.Auth.Token != "" {
		authModes++
	}
	if options.Auth.CredentialsFile != "" {
		authModes++
	}
	if options.Auth.NKeySeedFile != "" {
		authModes++
	}
	if hasURLAuth {
		authModes++
	}
	if authModes > 1 {
		return false, invalidConfig("natsnet: 认证方式必须互斥")
	}

	// 官方 Credentials 和 NKey 仍由 nats.go 解析，这里只提前验证文件可访问。
	if err := validateRegularFile(options.Auth.CredentialsFile, "CredentialsFile"); err != nil {
		return false, err
	}
	if err := validateRegularFile(options.Auth.NKeySeedFile, "NKeySeedFile"); err != nil {
		return false, err
	}

	// 双向 TLS 证书和私钥必须成对出现，所有 TLS 字段必须在 TLS 模式下使用。
	if (options.TLS.CertFile == "") != (options.TLS.KeyFile == "") {
		return false, invalidConfig("natsnet: TLS CertFile 和 KeyFile 必须同时配置")
	}
	tlsEnabled := options.TLS.Enabled || hasTLSURL
	hasTLSFields := options.TLS.CAFile != "" ||
		options.TLS.CertFile != "" ||
		options.TLS.KeyFile != "" ||
		options.TLS.ServerName != "" ||
		options.TLS.InsecureSkipVerify
	if hasTLSFields && !tlsEnabled {
		return false, invalidConfig("natsnet: TLS 字段需要先启用 TLS")
	}
	if err := validateRegularFile(options.TLS.CAFile, "TLS CAFile"); err != nil {
		return false, err
	}
	if err := validateRegularFile(options.TLS.CertFile, "TLS CertFile"); err != nil {
		return false, err
	}
	if err := validateRegularFile(options.TLS.KeyFile, "TLS KeyFile"); err != nil {
		return false, err
	}
	return tlsEnabled, nil
}

// validateSubscriptionOptions 解析零值默认项并验证 Pending 消息数上限。
func validateSubscriptionOptions(
	defaults SubscriptionDefaults,
	options SubscriptionOptions,
) (SubscriptionOptions, error) {
	// 零值只表示“使用连接默认值”，负数不允许借用 nats.go 的无限消息数语义。
	if options.PendingMessages == 0 {
		options.PendingMessages = defaults.PendingMessages
	}
	if options.PendingMessages < 0 {
		return SubscriptionOptions{}, invalidConfig(
			"natsnet: Subscription PendingMessages 不能为负数",
		)
	}
	return options, nil
}

// validateRegularFile 验证可选配置文件存在且不是目录。
func validateRegularFile(path, field string) error {
	// 空路径表示该能力未启用，不进行文件系统访问。
	if path == "" {
		return nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return invalidConfig(fmt.Sprintf("natsnet: %s 无法访问", field))
	}
	if !info.Mode().IsRegular() {
		return invalidConfig(fmt.Sprintf("natsnet: %s 必须是普通文件", field))
	}
	return nil
}

// buildTLSConfig 读取已经校验的 TLS 文件并构造独占配置快照。
func buildTLSConfig(options TLSOptions) (*tls.Config, error) {
	// TLS 1.2 是当前兼顾安全性和服务兼容性的最低版本。
	config := &tls.Config{
		MinVersion:         tls.VersionTLS12,
		ServerName:         options.ServerName,
		InsecureSkipVerify: options.InsecureSkipVerify, //nolint:gosec // 仅在调用方显式开启时生效。
	}

	// 显式 CA 在系统根证书基础上追加，既支持公共证书也支持内部 CA。
	if options.CAFile != "" {
		pool, err := x509.SystemCertPool()
		if err != nil || pool == nil {
			pool = x509.NewCertPool()
		}
		pem, err := os.ReadFile(options.CAFile)
		if err != nil {
			return nil, invalidConfig("natsnet: TLS CAFile 无法读取")
		}
		if !pool.AppendCertsFromPEM(pem) {
			return nil, invalidConfig("natsnet: TLS CAFile 不包含有效证书")
		}
		config.RootCAs = pool
	}

	// 客户端证书已经在校验阶段保证成对出现，此处加载并交给 tls.Config 独占。
	if options.CertFile != "" {
		certificate, err := tls.LoadX509KeyPair(options.CertFile, options.KeyFile)
		if err != nil {
			return nil, invalidConfig("natsnet: TLS 客户端证书或私钥无效")
		}
		config.Certificates = []tls.Certificate{certificate}
	}
	return config, nil
}

// buildNATSOptions 把 Origin 配置转换为官方客户端选项。
func buildNATSOptions(
	options Options,
	tlsEnabled bool,
	conn *Conn,
	dialer *initialDialer,
) ([]nats.Option, error) {
	// 基础连接、重连、探活和 Drain 参数全部显式传入，避免依赖未来版本默认值变化。
	result := []nats.Option{
		nats.Name(options.Name),
		nats.Timeout(options.ConnectTimeout),
		nats.PingInterval(options.PingInterval),
		nats.MaxPingsOutstanding(options.MaxPingsOutstanding),
		nats.ReconnectWait(options.Reconnect.Wait),
		nats.MaxReconnects(options.Reconnect.MaxAttempts),
		nats.ReconnectJitter(options.Reconnect.Jitter, options.Reconnect.TLSJitter),
		nats.ReconnectBufSize(options.Reconnect.BufferSize),
		nats.DrainTimeout(options.DrainTimeout),
		nats.SetCustomDialer(dialer),
		nats.DisconnectErrHandler(conn.handleDisconnected),
		nats.ReconnectHandler(conn.handleReconnected),
		nats.ClosedHandler(conn.handleClosed),
		nats.ErrorHandler(conn.handleAsyncError),
		nats.LameDuckModeHandler(conn.handleLameDuck),
	}
	if !options.Reconnect.Enabled {
		result = append(result, nats.NoReconnect())
	}
	if options.NoRandomize {
		result = append(result, nats.DontRandomize())
	}
	if options.NoEcho {
		result = append(result, nats.NoEcho())
	}
	if options.IgnoreAuthErrorAbort {
		result = append(result, nats.IgnoreAuthErrorAbort())
	}

	// 认证只添加已经确认的一种模式；URL 内嵌认证由官方客户端直接处理。
	switch {
	case options.Auth.Username != "":
		result = append(result, nats.UserInfo(options.Auth.Username, options.Auth.Password))
	case options.Auth.Token != "":
		result = append(result, nats.Token(options.Auth.Token))
	case options.Auth.CredentialsFile != "":
		result = append(result, nats.UserCredentials(options.Auth.CredentialsFile))
	case options.Auth.NKeySeedFile != "":
		nkeyOption, err := nats.NkeyOptionFromSeed(options.Auth.NKeySeedFile)
		if err != nil {
			return nil, invalidConfig("natsnet: NKeySeedFile 内容无效")
		}
		result = append(result, nkeyOption)
	}

	// TLS 文件在连接前构造成独占 tls.Config，官方客户端负责握手和重连复用。
	if tlsEnabled {
		tlsConfig, err := buildTLSConfig(options.TLS)
		if err != nil {
			return nil, err
		}
		result = append(result, nats.Secure(tlsConfig))
	}
	return result, nil
}

// safeURL 返回不包含 UserInfo、Query 和 Fragment 的可记录 Server 地址。
func safeURL(rawURL string) string {
	// 无法解析的地址不原样返回，避免意外把凭据写入日志。
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	parsed.User = nil
	parsed.RawQuery = ""
	parsed.ForceQuery = false
	parsed.Fragment = ""
	return parsed.String()
}
