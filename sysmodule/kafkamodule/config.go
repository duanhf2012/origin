package kafkamodule

import (
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/IBM/sarama"
	"github.com/duanhf2012/origin/v3/config"
)

// ClusterConfig 描述 Producer、Consumer 和 Admin 共用的一个逻辑 Kafka 集群。
type ClusterConfig struct {
	// Brokers 是去重后的 host:port Seed 地址；生产建议至少配置两个 Broker。
	Brokers []string `json:"brokers"`
	// Version 是集群中最低 Broker 的 Kafka 版本，例如 4.0.0。
	Version string `json:"version"`
	// ClientID 是 Kafka 审计和指标使用的稳定客户端标识；省略时使用 origin-kafka。
	ClientID string `json:"client_id"`
	// DialTimeout 是单个 Broker 建连超时；省略时为 10s。
	DialTimeout config.Duration `json:"dial_timeout"`
	// ReadTimeout 是读取 Broker 响应的超时；省略时为 30s。
	ReadTimeout config.Duration `json:"read_timeout"`
	// WriteTimeout 是向 Broker 写请求的超时；省略时为 30s。
	WriteTimeout config.Duration `json:"write_timeout"`
	// KeepAlive 是 TCP Keepalive 探测周期，不是 Consumer Group 心跳；省略时为 30s。
	KeepAlive config.Duration `json:"keep_alive"`
	// MetadataTimeout 是一次 Metadata 请求的总等待上限；省略时为 10s。
	MetadataTimeout config.Duration `json:"metadata_timeout"`
	// MetadataRefreshInterval 是后台 Metadata 刷新周期；省略时为 10m，显式负值非法。
	MetadataRefreshInterval config.Duration `json:"metadata_refresh_interval"`
	// MetadataRetryMax 是单次 Metadata 刷新的重试次数；省略时为 3。
	MetadataRetryMax int `json:"metadata_retry_max"`
	// MetadataRetryBackoff 是 Metadata 重试间隔；省略时为 250ms。
	MetadataRetryBackoff config.Duration `json:"metadata_retry_backoff"`
	// AllowAutoTopicCreation 允许 Broker 在 Metadata 请求时自动建 Topic；生产建议保持 false。
	AllowAutoTopicCreation bool `json:"allow_auto_topic_creation"`
	// TLS 保存 TLS 或 mTLS 配置。
	TLS TLSConfig `json:"tls"`
	// SASL 保存 PLAIN 或 SCRAM 身份认证配置。
	SASL SASLConfig `json:"sasl"`
}

// TLSConfig 描述 Kafka TLS 连接；不提供跳过服务端证书验证的选项。
type TLSConfig struct {
	// Enable 启用 TLS。
	Enable bool `json:"enable"`
	// CAFile 是追加到系统 Root CA Pool 的 PEM 文件；空值仅使用系统 CA。
	CAFile string `json:"ca_file"`
	// CertFile 是 mTLS 客户端证书文件，必须与 KeyFile 同时配置。
	CertFile string `json:"cert_file"`
	// KeyFile 是 mTLS 客户端私钥文件，必须与 CertFile 同时配置。
	KeyFile string `json:"key_file"`
	// ServerName 是证书验证使用的服务端名称；地址与证书名称不同时显式配置。
	ServerName string `json:"server_name"`
}

// SASLConfig 描述 Kafka SASL 身份认证。
type SASLConfig struct {
	// Enable 启用 SASL。
	Enable bool `json:"enable"`
	// Mechanism 支持 plain、scram_sha_256 和 scram_sha_512；省略时为 plain。
	Mechanism string `json:"mechanism"`
	// Username 是 SASL 用户名，启用 SASL 时必填。
	Username string `json:"username"`
	// Password 是 SASL 密码，启用 SASL 时必填；不要输出到日志或错误。
	Password string `json:"password"`
}

// ProducerConfig 描述受管 Producer 的可靠性、批聚合和双有界提交容量。
type ProducerConfig struct {
	Cluster ClusterConfig `json:"cluster"`
	// RequiredAcks 支持 none、one 和 all；省略时为 all。
	RequiredAcks string `json:"required_acks"`
	// Idempotent 启用 Kafka 幂等 Producer；nil 表示使用默认 true，显式 false 表示关闭。
	Idempotent *bool `json:"idempotent"`
	// Compression 支持 none、gzip、snappy、lz4 和 zstd；省略时为 snappy。
	Compression string `json:"compression"`
	// MaxMessageSize 是单条消息最大编码字节数；省略时为 1M。
	MaxMessageSize config.ByteSize `json:"max_message_size"`
	// DeliveryTimeout 是 Broker 等待 Ack 的上限；省略时为 10s。
	DeliveryTimeout config.Duration `json:"delivery_timeout"`
	// RetryMax 是可重试发送错误的重试次数；省略时为 3。
	RetryMax int `json:"retry_max"`
	// RetryBackoff 是发送重试间隔；省略时为 100ms。
	RetryBackoff config.Duration `json:"retry_backoff"`
	// RetryBufferMessages 是 Sarama Retry Bridge 的消息数硬上限；省略时为 4096。
	RetryBufferMessages int `json:"retry_buffer_messages"`
	// RetryBufferSize 是 Sarama Retry Bridge 的字节数硬上限；省略时为 32M。
	RetryBufferSize config.ByteSize `json:"retry_buffer_size"`
	// FlushMessages 是触发一次发送批次的最佳努力消息数；0 表示尽快发送。
	FlushMessages int `json:"flush_messages"`
	// FlushSize 是触发一次发送批次的最佳努力字节数；0 表示不按字节触发。
	FlushSize config.ByteSize `json:"flush_size"`
	// FlushInterval 是批次最长等待时间；0 表示不按时间聚合。
	FlushInterval config.Duration `json:"flush_interval"`
	// FlushMaxMessages 是单次请求的消息数硬上限；0 使用 Sarama 上限。
	FlushMaxMessages int `json:"flush_max_messages"`
	// SubmitQueueMessages 是 Origin 提交队列和在途消息的数量上限；省略时为 1024。
	SubmitQueueMessages int `json:"submit_queue_messages"`
	// SubmitQueueSize 是 Origin 提交队列和在途 Payload 的总字节上限；省略时为 64M。
	SubmitQueueSize config.ByteSize `json:"submit_queue_size"`
	// ChannelBufferMessages 是 Sarama 内部 Channel 容量；省略时为 256。
	ChannelBufferMessages int `json:"channel_buffer_messages"`
}

// ConsumerConfig 描述受管 Consumer Group、Fetch、恢复和业务 Handler 策略。
type ConsumerConfig struct {
	Cluster ClusterConfig `json:"cluster"`
	// GroupID 是稳定的 Consumer Group 标识，必填。
	GroupID string `json:"group_id"`
	// Topics 是去重后的非空 Topic 列表，首批不支持正则订阅。
	Topics []string `json:"topics"`
	// InitialOffset 仅在不存在已提交 Offset 时生效，支持 newest 和 oldest。
	InitialOffset string `json:"initial_offset"`
	// BalanceStrategy 支持 cooperative_sticky、sticky、round_robin 和 range。
	BalanceStrategy string `json:"balance_strategy"`
	// InstanceID 是静态成员标识；只有实例身份稳定且唯一时才配置。
	InstanceID string `json:"instance_id"`
	// SessionTimeout 是 Broker 判定成员失效的时间；省略时为 10s。
	SessionTimeout config.Duration `json:"session_timeout"`
	// HeartbeatInterval 是 Group 心跳周期；省略时为 3s 且必须小于 SessionTimeout。
	HeartbeatInterval config.Duration `json:"heartbeat_interval"`
	// RebalanceTimeout 是 Rebalance 中业务收尾上限；省略时为 60s。
	RebalanceTimeout config.Duration `json:"rebalance_timeout"`
	// AutoCommitInterval 是已成功 Mark Offset 的提交周期；省略时为 1s。
	AutoCommitInterval config.Duration `json:"auto_commit_interval"`
	// IsolationLevel 支持 read_committed 和 read_uncommitted；省略时为 read_committed。
	IsolationLevel string `json:"isolation_level"`
	// ResetInvalidOffsets 允许 Sarama 在 Offset 越界时重置；默认 false 以避免静默跳读。
	ResetInvalidOffsets bool `json:"reset_invalid_offsets"`
	// FetchMinSize 是 Broker 返回 Fetch 的最小字节数；省略时为 1B。
	FetchMinSize config.ByteSize `json:"fetch_min_size"`
	// FetchDefaultPartitionSize 是单 Partition 常规 Fetch 大小；省略时为 1M。
	FetchDefaultPartitionSize config.ByteSize `json:"fetch_default_partition_size"`
	// FetchMaxPartitionSize 是单 Partition Fetch 上限；省略时为 4M。
	FetchMaxPartitionSize config.ByteSize `json:"fetch_max_partition_size"`
	// FetchMaxTotalSize 是一次 Fetch 的总字节上限；省略时为 50M。
	FetchMaxTotalSize config.ByteSize `json:"fetch_max_total_size"`
	// FetchMaxWait 是 Broker 等待 FetchMinSize 的最长时间；省略时为 500ms。
	FetchMaxWait config.Duration `json:"fetch_max_wait"`
	// MaxProcessingTime 是 Sarama 向消费 Channel 投递的预算，不是 Handler 超时；省略时为 100ms。
	MaxProcessingTime config.Duration `json:"max_processing_time"`
	// ChannelBufferMessages 是 Sarama 内部 Channel 容量；省略时为 256。
	ChannelBufferMessages int `json:"channel_buffer_messages"`
	// RecoveryInitialBackoff 是基础设施恢复退避起点；省略时为 250ms。
	RecoveryInitialBackoff config.Duration `json:"recovery_initial_backoff"`
	// RecoveryMaxBackoff 是基础设施恢复单次等待上限；省略时为 30s。
	RecoveryMaxBackoff config.Duration `json:"recovery_max_backoff"`
	// HandlerRetryMax 是业务 Handler 的自动重试次数；默认 0，不自动重试。
	HandlerRetryMax int `json:"handler_retry_max"`
	// HandlerRetryBackoff 是 Handler 重试间隔；省略时为 1s。
	HandlerRetryBackoff config.Duration `json:"handler_retry_backoff"`
	// Batch 保存批量 Handler 的聚合边界；只有 SetupBatch 使用。
	Batch BatchConfig `json:"batch"`
}

// BatchConfig 描述消费批次的消息数、Payload 字节数和等待时间三重边界。
type BatchConfig struct {
	// MaxMessages 是单批消息数硬上限；批量模式省略时为 100。
	MaxMessages int `json:"max_messages"`
	// MaxSize 是单批 Payload 总字节上限；批量模式省略时为 1M。
	MaxSize config.ByteSize `json:"max_size"`
	// MaxWait 是第一条进入后等待聚合的最长时间；批量模式省略时为 50ms。
	MaxWait config.Duration `json:"max_wait"`
}

func normalizeClusterConfig(input ClusterConfig) (ClusterConfig, error) {
	result := input
	result.Brokers = append([]string(nil), input.Brokers...)
	result.Version = strings.TrimSpace(input.Version)
	result.ClientID = strings.TrimSpace(input.ClientID)
	result.TLS.CAFile = strings.TrimSpace(input.TLS.CAFile)
	result.TLS.CertFile = strings.TrimSpace(input.TLS.CertFile)
	result.TLS.KeyFile = strings.TrimSpace(input.TLS.KeyFile)
	result.TLS.ServerName = strings.TrimSpace(input.TLS.ServerName)
	result.SASL.Mechanism = strings.ToLower(strings.TrimSpace(input.SASL.Mechanism))
	result.SASL.Username = strings.TrimSpace(input.SASL.Username)
	if len(result.Brokers) == 0 || result.Version == "" {
		return ClusterConfig{}, invalidConfig("kafkamodule cluster.brokers 和 cluster.version 必填")
	}
	seen := make(map[string]struct{}, len(result.Brokers))
	for index, address := range result.Brokers {
		address = strings.TrimSpace(address)
		host, portText, err := net.SplitHostPort(address)
		if err != nil || strings.TrimSpace(host) == "" {
			return ClusterConfig{}, invalidConfig("kafkamodule Broker 必须使用 host:port 格式")
		}
		port, err := strconv.Atoi(portText)
		if err != nil || port < 1 || port > 65535 {
			return ClusterConfig{}, invalidConfig("kafkamodule Broker 端口必须位于 1 到 65535")
		}
		if _, exists := seen[address]; exists {
			return ClusterConfig{}, invalidConfig("kafkamodule Broker 地址不能重复")
		}
		seen[address] = struct{}{}
		result.Brokers[index] = address
	}
	if _, err := sarama.ParseKafkaVersion(result.Version); err != nil {
		return ClusterConfig{}, invalidConfig("kafkamodule cluster.version 无效")
	}
	if result.ClientID == "" {
		result.ClientID = "origin-kafka"
	}
	if result.DialTimeout == 0 {
		result.DialTimeout = config.Duration(10 * time.Second)
	}
	if result.ReadTimeout == 0 {
		result.ReadTimeout = config.Duration(30 * time.Second)
	}
	if result.WriteTimeout == 0 {
		result.WriteTimeout = config.Duration(30 * time.Second)
	}
	if result.KeepAlive == 0 {
		result.KeepAlive = config.Duration(30 * time.Second)
	}
	if result.MetadataTimeout == 0 {
		result.MetadataTimeout = config.Duration(10 * time.Second)
	}
	if result.MetadataRefreshInterval == 0 {
		result.MetadataRefreshInterval = config.Duration(10 * time.Minute)
	}
	if result.MetadataRetryMax == 0 {
		result.MetadataRetryMax = 3
	}
	if result.MetadataRetryBackoff == 0 {
		result.MetadataRetryBackoff = config.Duration(250 * time.Millisecond)
	}
	for _, value := range []time.Duration{result.DialTimeout.Duration(), result.ReadTimeout.Duration(), result.WriteTimeout.Duration(), result.KeepAlive.Duration(), result.MetadataTimeout.Duration(), result.MetadataRefreshInterval.Duration(), result.MetadataRetryBackoff.Duration()} {
		if value < 0 {
			return ClusterConfig{}, invalidConfig("kafkamodule Cluster 时间配置不能为负数")
		}
	}
	if result.DialTimeout == 0 || result.ReadTimeout == 0 || result.WriteTimeout == 0 || result.MetadataTimeout == 0 || result.MetadataRetryBackoff == 0 || result.MetadataRetryMax < 0 {
		return ClusterConfig{}, invalidConfig("kafkamodule Cluster 超时和重试配置无效")
	}
	if !result.TLS.Enable && (result.TLS.CAFile != "" || result.TLS.CertFile != "" || result.TLS.KeyFile != "" || result.TLS.ServerName != "") {
		return ClusterConfig{}, invalidConfig("kafkamodule TLS 字段只能在 tls.enable=true 时配置")
	}
	if (result.TLS.CertFile == "") != (result.TLS.KeyFile == "") {
		return ClusterConfig{}, invalidConfig("kafkamodule TLS cert_file 和 key_file 必须同时配置")
	}
	if result.SASL.Enable {
		if result.SASL.Mechanism == "" {
			result.SASL.Mechanism = "plain"
		}
		if result.SASL.Username == "" || result.SASL.Password == "" {
			return ClusterConfig{}, invalidConfig("kafkamodule 启用 SASL 时 username 和 password 必填")
		}
		if result.SASL.Mechanism != "plain" && result.SASL.Mechanism != "scram_sha_256" && result.SASL.Mechanism != "scram_sha_512" {
			return ClusterConfig{}, invalidConfig("kafkamodule SASL mechanism 无效")
		}
	} else if result.SASL.Mechanism != "" || result.SASL.Username != "" || result.SASL.Password != "" {
		return ClusterConfig{}, invalidConfig("kafkamodule SASL 字段只能在 sasl.enable=true 时配置")
	}
	return result, nil
}

func normalizeProducerConfig(input ProducerConfig) (ProducerConfig, error) {
	result := input
	cluster, err := normalizeClusterConfig(input.Cluster)
	if err != nil {
		return ProducerConfig{}, err
	}
	result.Cluster = cluster
	result.RequiredAcks = strings.ToLower(strings.TrimSpace(input.RequiredAcks))
	if result.RequiredAcks == "" {
		result.RequiredAcks = "all"
	}
	if result.Idempotent == nil {
		enabled := true
		result.Idempotent = &enabled
	}
	result.Compression = strings.ToLower(strings.TrimSpace(input.Compression))
	if result.Compression == "" {
		result.Compression = "snappy"
	}
	if result.MaxMessageSize == 0 {
		result.MaxMessageSize = config.ByteSize(1 << 20)
	}
	if result.DeliveryTimeout == 0 {
		result.DeliveryTimeout = config.Duration(10 * time.Second)
	}
	if result.RetryMax == 0 {
		result.RetryMax = 3
	}
	if result.RetryBackoff == 0 {
		result.RetryBackoff = config.Duration(100 * time.Millisecond)
	}
	if result.RetryBufferMessages == 0 {
		result.RetryBufferMessages = 4096
	}
	if result.RetryBufferSize == 0 {
		result.RetryBufferSize = config.ByteSize(32 << 20)
	}
	if result.SubmitQueueMessages == 0 {
		result.SubmitQueueMessages = 1024
	}
	if result.SubmitQueueSize == 0 {
		result.SubmitQueueSize = config.ByteSize(64 << 20)
	}
	if result.ChannelBufferMessages == 0 {
		result.ChannelBufferMessages = 256
	}
	if result.RequiredAcks != "none" && result.RequiredAcks != "one" && result.RequiredAcks != "all" {
		return ProducerConfig{}, invalidConfig("kafkamodule required_acks 无效")
	}
	if *result.Idempotent && result.RequiredAcks != "all" {
		return ProducerConfig{}, invalidConfig("kafkamodule 幂等 Producer 必须使用 required_acks=all")
	}
	switch result.Compression {
	case "none", "gzip", "snappy", "lz4", "zstd":
	default:
		return ProducerConfig{}, invalidConfig("kafkamodule compression 无效")
	}
	if result.MaxMessageSize.Bytes() <= 0 || result.RetryBufferSize.Bytes() <= 0 || result.SubmitQueueSize.Bytes() <= 0 || result.DeliveryTimeout.Duration() <= 0 || result.RetryBackoff.Duration() <= 0 || result.RetryMax < 0 || result.RetryBufferMessages <= 0 || result.SubmitQueueMessages <= 0 || result.ChannelBufferMessages <= 0 || result.FlushMessages < 0 || result.FlushSize.Bytes() < 0 || result.FlushInterval.Duration() < 0 || result.FlushMaxMessages < 0 {
		return ProducerConfig{}, invalidConfig("kafkamodule Producer 数量、容量或时间配置无效")
	}
	if result.FlushMaxMessages > 0 && result.FlushMessages > result.FlushMaxMessages {
		return ProducerConfig{}, invalidConfig("kafkamodule flush_messages 不能大于 flush_max_messages")
	}
	if result.MaxMessageSize.Bytes() > result.SubmitQueueSize.Bytes() {
		return ProducerConfig{}, invalidConfig("kafkamodule max_message_size 不能大于 submit_queue_size")
	}
	return result, nil
}

func normalizeConsumerConfig(input ConsumerConfig, batch bool) (ConsumerConfig, error) {
	result := input
	cluster, err := normalizeClusterConfig(input.Cluster)
	if err != nil {
		return ConsumerConfig{}, err
	}
	result.Cluster = cluster
	result.GroupID = strings.TrimSpace(input.GroupID)
	result.InstanceID = strings.TrimSpace(input.InstanceID)
	result.InitialOffset = strings.ToLower(strings.TrimSpace(input.InitialOffset))
	if result.InitialOffset == "" {
		result.InitialOffset = "newest"
	}
	result.BalanceStrategy = strings.ToLower(strings.TrimSpace(input.BalanceStrategy))
	if result.BalanceStrategy == "" {
		result.BalanceStrategy = "cooperative_sticky"
	}
	result.IsolationLevel = strings.ToLower(strings.TrimSpace(input.IsolationLevel))
	if result.IsolationLevel == "" {
		result.IsolationLevel = "read_committed"
	}
	result.Topics = append([]string(nil), input.Topics...)
	if result.GroupID == "" || len(result.Topics) == 0 {
		return ConsumerConfig{}, invalidConfig("kafkamodule consumer group_id 和 topics 必填")
	}
	seen := make(map[string]struct{}, len(result.Topics))
	for index, topic := range result.Topics {
		topic = strings.TrimSpace(topic)
		if topic == "" {
			return ConsumerConfig{}, invalidConfig("kafkamodule topic 不能为空")
		}
		if _, ok := seen[topic]; ok {
			return ConsumerConfig{}, invalidConfig("kafkamodule topic 不能重复")
		}
		seen[topic] = struct{}{}
		result.Topics[index] = topic
	}
	if result.SessionTimeout == 0 {
		result.SessionTimeout = config.Duration(10 * time.Second)
	}
	if result.HeartbeatInterval == 0 {
		result.HeartbeatInterval = config.Duration(3 * time.Second)
	}
	if result.RebalanceTimeout == 0 {
		result.RebalanceTimeout = config.Duration(60 * time.Second)
	}
	if result.AutoCommitInterval == 0 {
		result.AutoCommitInterval = config.Duration(time.Second)
	}
	if result.FetchMinSize == 0 {
		result.FetchMinSize = 1
	}
	if result.FetchDefaultPartitionSize == 0 {
		result.FetchDefaultPartitionSize = config.ByteSize(1 << 20)
	}
	if result.FetchMaxPartitionSize == 0 {
		result.FetchMaxPartitionSize = config.ByteSize(4 << 20)
	}
	if result.FetchMaxTotalSize == 0 {
		result.FetchMaxTotalSize = config.ByteSize(50 << 20)
	}
	if result.FetchMaxWait == 0 {
		result.FetchMaxWait = config.Duration(500 * time.Millisecond)
	}
	if result.MaxProcessingTime == 0 {
		result.MaxProcessingTime = config.Duration(100 * time.Millisecond)
	}
	if result.ChannelBufferMessages == 0 {
		result.ChannelBufferMessages = 256
	}
	if result.RecoveryInitialBackoff == 0 {
		result.RecoveryInitialBackoff = config.Duration(250 * time.Millisecond)
	}
	if result.RecoveryMaxBackoff == 0 {
		result.RecoveryMaxBackoff = config.Duration(30 * time.Second)
	}
	if result.HandlerRetryBackoff == 0 {
		result.HandlerRetryBackoff = config.Duration(time.Second)
	}
	if batch {
		if result.Batch.MaxMessages == 0 {
			result.Batch.MaxMessages = 100
		}
		if result.Batch.MaxSize == 0 {
			result.Batch.MaxSize = config.ByteSize(1 << 20)
		}
		if result.Batch.MaxWait == 0 {
			result.Batch.MaxWait = config.Duration(50 * time.Millisecond)
		}
	}
	if result.InitialOffset != "newest" && result.InitialOffset != "oldest" {
		return ConsumerConfig{}, invalidConfig("kafkamodule initial_offset 无效")
	}
	switch result.BalanceStrategy {
	case "cooperative_sticky", "sticky", "round_robin", "range":
	default:
		return ConsumerConfig{}, invalidConfig("kafkamodule balance_strategy 无效")
	}
	if result.IsolationLevel != "read_committed" && result.IsolationLevel != "read_uncommitted" {
		return ConsumerConfig{}, invalidConfig("kafkamodule isolation_level 无效")
	}
	if result.SessionTimeout.Duration() <= 0 || result.HeartbeatInterval.Duration() <= 0 || result.HeartbeatInterval.Duration() >= result.SessionTimeout.Duration() || result.HeartbeatInterval.Duration()*3 > result.SessionTimeout.Duration() || result.RebalanceTimeout.Duration() <= 0 || result.AutoCommitInterval.Duration() <= 0 || result.FetchMaxWait.Duration() <= 0 || result.MaxProcessingTime.Duration() <= 0 || result.RecoveryInitialBackoff.Duration() <= 0 || result.RecoveryMaxBackoff.Duration() < result.RecoveryInitialBackoff.Duration() || result.HandlerRetryBackoff.Duration() <= 0 {
		return ConsumerConfig{}, invalidConfig("kafkamodule Consumer 时间配置无效")
	}
	if result.FetchMinSize.Bytes() <= 0 || result.FetchDefaultPartitionSize.Bytes() <= 0 || result.FetchMaxPartitionSize.Bytes() < result.FetchDefaultPartitionSize.Bytes() || result.FetchMaxTotalSize.Bytes() < result.FetchMaxPartitionSize.Bytes() || result.ChannelBufferMessages <= 0 || result.HandlerRetryMax < 0 {
		return ConsumerConfig{}, invalidConfig("kafkamodule Consumer 容量或数量配置无效")
	}
	if batch && (result.Batch.MaxMessages <= 0 || result.Batch.MaxSize.Bytes() <= 0 || result.Batch.MaxWait.Duration() <= 0) {
		return ConsumerConfig{}, invalidConfig("kafkamodule Batch 配置无效")
	}
	return result, nil
}
