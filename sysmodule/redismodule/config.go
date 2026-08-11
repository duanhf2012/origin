package redismodule

import (
	"crypto/tls"
	"crypto/x509"
	"net"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/duanhf2012/origin/v3/config"
	"github.com/duanhf2012/origin/v3/errs"
	"github.com/redis/go-redis/v9"
)

// Mode 指定 Redis 的服务端拓扑。
type Mode string

const (
	// ModeStandalone 表示单 Redis 数据节点。
	ModeStandalone Mode = "standalone"
	// ModeSentinel 表示由 Redis Sentinel 发现和切换 Master。
	ModeSentinel Mode = "sentinel"
	// ModeCluster 表示 Redis Cluster 分片集群。
	ModeCluster Mode = "cluster"
)

// SentinelConfig 保存 Sentinel 拓扑专属配置。
type SentinelConfig struct {
	// MasterName 是 Sentinel monitor 的 Master 名称；Sentinel 模式必填。
	MasterName string
	// Username 是 Sentinel 自身 ACL 用户；留空表示不认证。
	Username string
	// Password 是 Sentinel 自身密码；留空表示不认证且不会回退使用数据节点密码。
	Password string
}

// ClusterConfig 保存 Redis Cluster 专属配置。
type ClusterConfig struct {
	// ReadFromReplicas 允许只读命令访问 Replica；默认关闭以避免无意读取复制延迟数据。
	ReadFromReplicas bool
	// RouteByLatency 在允许 Replica 读取时按延迟选取节点。
	RouteByLatency bool
	// MaxRedirects 是网络错误和 MOVED/ASK 等拓扑恢复的最多处理次数；0 使用默认值 3。
	MaxRedirects int
}

// Config 描述一个逻辑 Redis 部署及其有界连接池。
//
// 所有时间字段在 YAML/JSON 中使用带单位字符串，例如 500ms、5s、30m。零值字段会在
// New/Setup 内应用 DefaultConfig；Addresses 没有默认值。
type Config struct {
	// Mode 指定 standalone、sentinel 或 cluster；省略时为 standalone。
	Mode Mode
	// Addresses 是 host:port 地址列表；Standalone 必须一个，其他模式至少一个。
	Addresses []string
	// Username 是 Redis 数据节点 ACL 用户；留空表示不认证。
	Username string
	// Password 是 Redis 数据节点密码；留空表示不认证，错误与日志不得输出该值。
	Password string
	// Database 是逻辑数据库编号；默认 0，Cluster 只能使用 0。
	Database int
	// ClientName 是 CLIENT SETNAME 使用的连接名称；留空不设置。
	ClientName string
	// Protocol 是 RESP 版本，只允许 2 或 3；省略时为 3。
	Protocol int
	// TLS 控制是否启用 TLS。
	TLS bool
	// TLSCAFile 是追加到系统 Root CA Pool 的 PEM CA 文件；仅 TLS 开启时可用。
	TLSCAFile string
	// DialTimeout 是单次建连超时；省略时为 5s。
	DialTimeout config.Duration
	// DialAttempts 是一次取连接时包含首次在内的最多建连尝试数；0 使用 5。
	DialAttempts int
	// DialRetryInterval 是建连失败后再次尝试前的固定等待；省略时为 100ms。
	DialRetryInterval config.Duration
	// ReadTimeout 是网络读取兜底超时；省略时为 5s。
	ReadTimeout config.Duration
	// WriteTimeout 是网络写入兜底超时；省略时为 5s。
	WriteTimeout config.Duration
	// PoolTimeout 是连接池达到硬上限后等待连接的最长时间；省略时为 6s。
	PoolTimeout config.Duration
	// PoolSize 是每节点基础连接数；0 按拓扑和 GOMAXPROCS 计算。
	PoolSize int
	// MaxConcurrentDials 是每节点并发建连上限；0 取最终 PoolSize。
	MaxConcurrentDials int
	// MaxActiveConnections 是每节点连接硬上限；0 取最终 PoolSize。
	MaxActiveConnections int
	// MinIdleConnections 是每节点预热的最小空闲连接数；默认 0。
	MinIdleConnections int
	// ConnectionMaxIdleTime 是连接最大空闲时间；省略时为 30m。
	ConnectionMaxIdleTime config.Duration
	// MaxRetries 是每条命令的自动重试次数；默认 0，表示禁用命令重试。
	MaxRetries int
	// MinRetryBackoff 是命令重试最小退避；省略时为 10ms。
	MinRetryBackoff config.Duration
	// MaxRetryBackoff 是命令重试最大退避；省略时为 1s。
	MaxRetryBackoff config.Duration
	// Sentinel 保存 Sentinel 模式专属配置。
	Sentinel SentinelConfig
	// Cluster 保存 Cluster 模式专属配置。
	Cluster ClusterConfig
}

// DefaultConfig 返回固定默认值；Addresses 与依赖拓扑/GOMAXPROCS 的连接数保持零值。
func DefaultConfig() Config {
	return Config{
		Mode:                  ModeStandalone,
		Protocol:              3,
		DialTimeout:           config.Duration(5 * time.Second),
		DialAttempts:          5,
		DialRetryInterval:     config.Duration(100 * time.Millisecond),
		ReadTimeout:           config.Duration(5 * time.Second),
		WriteTimeout:          config.Duration(5 * time.Second),
		PoolTimeout:           config.Duration(6 * time.Second),
		ConnectionMaxIdleTime: config.Duration(30 * time.Minute),
		MinRetryBackoff:       config.Duration(10 * time.Millisecond),
		MaxRetryBackoff:       config.Duration(time.Second),
		Cluster:               ClusterConfig{MaxRedirects: 3},
	}
}

func normalizeConfig(input Config) (Config, error) {
	result := input
	defaults := DefaultConfig()
	if result.Mode == "" {
		result.Mode = defaults.Mode
	}
	if result.Protocol == 0 {
		result.Protocol = defaults.Protocol
	}
	if result.DialTimeout == 0 {
		result.DialTimeout = defaults.DialTimeout
	}
	if result.DialAttempts == 0 {
		result.DialAttempts = defaults.DialAttempts
	}
	if result.DialRetryInterval == 0 {
		result.DialRetryInterval = defaults.DialRetryInterval
	}
	if result.ReadTimeout == 0 {
		result.ReadTimeout = defaults.ReadTimeout
	}
	if result.WriteTimeout == 0 {
		result.WriteTimeout = defaults.WriteTimeout
	}
	if result.PoolTimeout == 0 {
		result.PoolTimeout = defaults.PoolTimeout
	}
	if result.ConnectionMaxIdleTime == 0 {
		result.ConnectionMaxIdleTime = defaults.ConnectionMaxIdleTime
	}
	if result.MinRetryBackoff == 0 {
		result.MinRetryBackoff = defaults.MinRetryBackoff
	}
	if result.MaxRetryBackoff == 0 {
		result.MaxRetryBackoff = defaults.MaxRetryBackoff
	}
	if result.Cluster.MaxRedirects == 0 {
		result.Cluster.MaxRedirects = defaults.Cluster.MaxRedirects
	}

	result.ClientName = strings.TrimSpace(result.ClientName)
	result.TLSCAFile = strings.TrimSpace(result.TLSCAFile)
	result.Sentinel.MasterName = strings.TrimSpace(result.Sentinel.MasterName)
	result.Sentinel.Username = strings.TrimSpace(result.Sentinel.Username)
	result.Addresses = append([]string(nil), input.Addresses...)
	seen := make(map[string]struct{}, len(result.Addresses))
	for index, address := range result.Addresses {
		address = strings.TrimSpace(address)
		if address == "" {
			return Config{}, invalidConfig("redismodule 地址不能为空")
		}
		host, portText, err := net.SplitHostPort(address)
		if err != nil || strings.TrimSpace(host) == "" {
			return Config{}, invalidConfig("redismodule 地址必须使用 host:port 格式")
		}
		port, err := strconv.Atoi(portText)
		if err != nil || port < 1 || port > 65535 {
			return Config{}, invalidConfig("redismodule 地址端口必须位于 1 到 65535")
		}
		if _, exists := seen[address]; exists {
			return Config{}, invalidConfig("redismodule 地址不能重复")
		}
		seen[address] = struct{}{}
		result.Addresses[index] = address
	}

	if result.Mode != ModeStandalone && result.Mode != ModeSentinel && result.Mode != ModeCluster {
		return Config{}, invalidConfig("redismodule Mode 只支持 standalone、sentinel 或 cluster")
	}
	if len(result.Addresses) == 0 || (result.Mode == ModeStandalone && len(result.Addresses) != 1) {
		return Config{}, invalidConfig("redismodule Addresses 数量不符合当前拓扑")
	}
	if result.Protocol != 2 && result.Protocol != 3 {
		return Config{}, invalidConfig("redismodule Protocol 只支持 2 或 3")
	}
	if result.Database < 0 || (result.Mode == ModeCluster && result.Database != 0) {
		return Config{}, invalidConfig("redismodule Database 不符合当前拓扑")
	}
	if result.Mode == ModeSentinel && result.Sentinel.MasterName == "" {
		return Config{}, invalidConfig("redismodule Sentinel MasterName 不能为空")
	}
	if result.Mode != ModeSentinel && (result.Sentinel.MasterName != "" || result.Sentinel.Username != "" || result.Sentinel.Password != "") {
		return Config{}, invalidConfig("redismodule 非 Sentinel 模式不能配置 Sentinel 字段")
	}
	if result.Mode != ModeCluster && (result.Cluster.ReadFromReplicas || result.Cluster.RouteByLatency) {
		return Config{}, invalidConfig("redismodule 非 Cluster 模式不能配置 Cluster 路由字段")
	}
	if result.Cluster.RouteByLatency && !result.Cluster.ReadFromReplicas {
		return Config{}, invalidConfig("redismodule RouteByLatency 需要同时开启 ReadFromReplicas")
	}
	if result.Mode == ModeCluster && result.MaxRetries != 0 {
		return Config{}, invalidConfig("redismodule Cluster 禁止叠加节点级命令重试")
	}
	if result.TLSCAFile != "" && !result.TLS {
		return Config{}, invalidConfig("redismodule TLSCAFile 只能在 TLS 开启时使用")
	}

	durations := []time.Duration{
		result.DialTimeout.Duration(), result.DialRetryInterval.Duration(), result.ReadTimeout.Duration(),
		result.WriteTimeout.Duration(), result.PoolTimeout.Duration(), result.ConnectionMaxIdleTime.Duration(),
		result.MinRetryBackoff.Duration(), result.MaxRetryBackoff.Duration(),
	}
	for _, duration := range durations {
		if duration < 0 {
			return Config{}, invalidConfig("redismodule 时间配置不能为负数")
		}
	}
	if result.DialAttempts < 1 || result.PoolSize < 0 || result.MaxConcurrentDials < 0 ||
		result.MaxActiveConnections < 0 || result.MinIdleConnections < 0 || result.MaxRetries < 0 ||
		result.Cluster.MaxRedirects < 0 {
		return Config{}, invalidConfig("redismodule 数量配置不能为负数")
	}
	if result.MinRetryBackoff.Duration() > result.MaxRetryBackoff.Duration() {
		return Config{}, invalidConfig("redismodule MinRetryBackoff 不能大于 MaxRetryBackoff")
	}
	if result.PoolSize == 0 {
		multiplier := 10
		if result.Mode == ModeCluster {
			multiplier = 5
		}
		result.PoolSize = multiplier * runtime.GOMAXPROCS(0)
	}
	if result.MaxConcurrentDials == 0 {
		result.MaxConcurrentDials = result.PoolSize
	}
	if result.MaxActiveConnections == 0 {
		result.MaxActiveConnections = result.PoolSize
	}
	if result.MaxConcurrentDials > result.PoolSize || result.MaxActiveConnections < result.PoolSize ||
		result.MinIdleConnections > result.MaxActiveConnections {
		return Config{}, invalidConfig("redismodule 连接池上下限配置无效")
	}
	return result, nil
}

func buildUniversalOptions(current Config, customTLS *tls.Config) (*redis.UniversalOptions, error) {
	var tlsConfig *tls.Config
	if customTLS != nil {
		if current.TLSCAFile != "" {
			return nil, invalidConfig("redismodule TLSCAFile 与 WithTLSConfig 不能同时使用")
		}
		if !current.TLS {
			return nil, invalidConfig("redismodule WithTLSConfig 需要启用 TLS")
		}
		tlsConfig = customTLS.Clone()
	} else if current.TLS {
		var err error
		tlsConfig, err = loadTLSConfig(current.TLSCAFile)
		if err != nil {
			return nil, err
		}
	}
	if tlsConfig != nil && tlsConfig.InsecureSkipVerify {
		return nil, invalidConfig("redismodule 禁止跳过 TLS 证书校验")
	}
	maxRetries := -1
	if current.MaxRetries > 0 {
		maxRetries = current.MaxRetries
	}
	return &redis.UniversalOptions{
		Addrs: append([]string(nil), current.Addresses...), ClientName: current.ClientName, DB: current.Database,
		Protocol: current.Protocol, Username: current.Username, Password: current.Password,
		SentinelUsername: current.Sentinel.Username, SentinelPassword: current.Sentinel.Password,
		MaxRetries: maxRetries, MinRetryBackoff: current.MinRetryBackoff.Duration(), MaxRetryBackoff: current.MaxRetryBackoff.Duration(),
		// go-redis 字段名为 DialerRetries，但 v9.22.0 的连接池循环把它作为包含首次的总尝试数。
		DialTimeout: current.DialTimeout.Duration(), DialerRetries: current.DialAttempts,
		DialerRetryTimeout: current.DialRetryInterval.Duration(), ReadTimeout: current.ReadTimeout.Duration(),
		WriteTimeout: current.WriteTimeout.Duration(), ContextTimeoutEnabled: true,
		PoolSize: current.PoolSize, MaxConcurrentDials: current.MaxConcurrentDials,
		PoolTimeout: current.PoolTimeout.Duration(), MinIdleConns: current.MinIdleConnections,
		MaxActiveConns: current.MaxActiveConnections, ConnMaxIdleTime: current.ConnectionMaxIdleTime.Duration(),
		TLSConfig: tlsConfig, MaxRedirects: current.Cluster.MaxRedirects, ReadOnly: current.Cluster.ReadFromReplicas,
		RouteByLatency: current.Cluster.RouteByLatency, MasterName: current.Sentinel.MasterName,
		IsClusterMode: current.Mode == ModeCluster,
	}, nil
}

func loadTLSConfig(caFile string) (*tls.Config, error) {
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12}
	if caFile == "" {
		return tlsConfig, nil
	}
	pemData, err := os.ReadFile(caFile)
	if err != nil {
		return nil, invalidConfig("redismodule 无法读取 TLS CA 文件")
	}
	roots, err := x509.SystemCertPool()
	if err != nil || roots == nil {
		roots = x509.NewCertPool()
	}
	if !roots.AppendCertsFromPEM(pemData) {
		return nil, invalidConfig("redismodule TLS CA 文件不包含有效证书")
	}
	tlsConfig.RootCAs = roots
	return tlsConfig, nil
}

func invalidConfig(message string) error { return errs.NewMessage(errs.CodeInvalidConfig, message) }
