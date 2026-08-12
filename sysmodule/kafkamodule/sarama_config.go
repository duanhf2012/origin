package kafkamodule

import (
	"crypto/tls"
	"crypto/x509"
	"math"
	"os"

	"github.com/IBM/sarama"
	"github.com/xdg-go/scram"
)

// BuildProducerSaramaConfig 构建满足受管 Producer 不变量的 Sarama 配置。
func BuildProducerSaramaConfig(input ProducerConfig, options ...ProducerOption) (*sarama.Config, error) {
	current, err := normalizeProducerConfig(input)
	if err != nil {
		return nil, err
	}
	result, err := buildClusterSaramaConfig(current.Cluster)
	if err != nil {
		return nil, err
	}
	acks := map[string]sarama.RequiredAcks{"none": sarama.NoResponse, "one": sarama.WaitForLocal, "all": sarama.WaitForAll}
	compression := map[string]sarama.CompressionCodec{"none": sarama.CompressionNone, "gzip": sarama.CompressionGZIP, "snappy": sarama.CompressionSnappy, "lz4": sarama.CompressionLZ4, "zstd": sarama.CompressionZSTD}
	result.Producer.RequiredAcks = acks[current.RequiredAcks]
	result.Producer.Idempotent = *current.Idempotent
	result.Producer.Compression = compression[current.Compression]
	result.Producer.MaxMessageBytes = int(current.MaxMessageSize.Bytes())
	result.Producer.Timeout = current.DeliveryTimeout.Duration()
	result.Producer.Retry.Max = current.RetryMax
	result.Producer.Retry.Backoff = current.RetryBackoff.Duration()
	result.Producer.Retry.MaxBufferLength = current.RetryBufferMessages
	result.Producer.Retry.MaxBufferBytes = current.RetryBufferSize.Bytes()
	result.Producer.Flush.Messages = current.FlushMessages
	result.Producer.Flush.Bytes = int(current.FlushSize.Bytes())
	result.Producer.Flush.Frequency = current.FlushInterval.Duration()
	result.Producer.Flush.MaxMessages = current.FlushMaxMessages
	result.Producer.Return.Successes = true
	result.Producer.Return.Errors = true
	result.ChannelBufferSize = current.ChannelBufferMessages
	if *current.Idempotent {
		result.Net.MaxOpenRequests = 1
	}
	selected := producerOptions{}
	for _, option := range options {
		if option == nil {
			return nil, invalidConfig("kafkamodule ProducerOption 不能为空")
		}
		option.applyProducer(&selected)
	}
	if err = applyHooks(result, selected.hooks); err != nil {
		return nil, err
	}
	if err = validateManagedProducerConfig(result); err != nil {
		return nil, err
	}
	if err = result.Validate(); err != nil {
		return nil, invalidConfig("kafkamodule Sarama Producer 配置无效: " + err.Error())
	}
	return result, nil
}

// BuildConsumerSaramaConfig 构建满足成功处理后 Mark 语义的受管 Consumer 配置。
func BuildConsumerSaramaConfig(input ConsumerConfig, options ...ConsumerOption) (*sarama.Config, error) {
	current, err := normalizeConsumerConfig(input, false)
	if err != nil {
		return nil, err
	}
	return buildConsumerSaramaConfig(current, options)
}

func buildConsumerSaramaConfig(current ConsumerConfig, options []ConsumerOption) (*sarama.Config, error) {
	result, err := buildClusterSaramaConfig(current.Cluster)
	if err != nil {
		return nil, err
	}
	result.Consumer.Group.Session.Timeout = current.SessionTimeout.Duration()
	result.Consumer.Group.Heartbeat.Interval = current.HeartbeatInterval.Duration()
	result.Consumer.Group.Rebalance.Timeout = current.RebalanceTimeout.Duration()
	result.Consumer.Group.InstanceId = current.InstanceID
	strategies := map[string]sarama.BalanceStrategy{"cooperative_sticky": sarama.NewBalanceStrategyCooperativeSticky(), "sticky": sarama.NewBalanceStrategySticky(), "round_robin": sarama.NewBalanceStrategyRoundRobin(), "range": sarama.NewBalanceStrategyRange()}
	result.Consumer.Group.Rebalance.GroupStrategies = []sarama.BalanceStrategy{strategies[current.BalanceStrategy]}
	result.Consumer.Group.ResetInvalidOffsets = current.ResetInvalidOffsets
	result.Consumer.Offsets.AutoCommit.Enable = true
	result.Consumer.Offsets.AutoCommit.Interval = current.AutoCommitInterval.Duration()
	if current.InitialOffset == "oldest" {
		result.Consumer.Offsets.Initial = sarama.OffsetOldest
	} else {
		result.Consumer.Offsets.Initial = sarama.OffsetNewest
	}
	if current.IsolationLevel == "read_committed" {
		result.Consumer.IsolationLevel = sarama.ReadCommitted
	} else {
		result.Consumer.IsolationLevel = sarama.ReadUncommitted
	}
	if current.FetchMinSize.Bytes() > math.MaxInt32 || current.FetchDefaultPartitionSize.Bytes() > math.MaxInt32 || current.FetchMaxPartitionSize.Bytes() > math.MaxInt32 || current.FetchMaxTotalSize.Bytes() > math.MaxInt32 {
		return nil, invalidConfig("kafkamodule Consumer Fetch 容量超过 Sarama int32 上限")
	}
	result.Consumer.Fetch.Min = int32(current.FetchMinSize.Bytes())
	result.Consumer.Fetch.Default = int32(current.FetchDefaultPartitionSize.Bytes())
	result.Consumer.Fetch.Max = int32(current.FetchMaxPartitionSize.Bytes())
	result.Consumer.Fetch.MaxBytes = int32(current.FetchMaxTotalSize.Bytes())
	result.Consumer.MaxWaitTime = current.FetchMaxWait.Duration()
	result.Consumer.MaxProcessingTime = current.MaxProcessingTime.Duration()
	result.Consumer.Return.Errors = true
	result.ChannelBufferSize = current.ChannelBufferMessages
	selected := consumerOptions{}
	for _, option := range options {
		if option == nil {
			return nil, invalidConfig("kafkamodule ConsumerOption 不能为空")
		}
		option.applyConsumer(&selected)
	}
	if err = applyHooks(result, selected.hooks); err != nil {
		return nil, err
	}
	if err = validateManagedConsumerConfig(result); err != nil {
		return nil, err
	}
	if err = result.Validate(); err != nil {
		return nil, invalidConfig("kafkamodule Sarama Consumer 配置无效: " + err.Error())
	}
	return result, nil
}

// BuildSaramaConfig 构建自由模式的公共 Sarama 基础配置；返回配置不创建网络连接。
//
// 使用者可以在 Hook 中配置事务、手工 Offset、OAuth、Rack 和 Interceptor，并自行创建、取消、
// 关闭 Sarama Client/Producer/Consumer/Admin。自由模式不接入 Origin 生命周期或 Service 协程，
// 但仍禁止 InsecureSkipVerify。常规业务优先使用 Managed Producer/Consumer Builder。
func BuildSaramaConfig(input ClusterConfig, options ...SaramaConfigOption) (*sarama.Config, error) {
	current, err := normalizeClusterConfig(input)
	if err != nil {
		return nil, err
	}
	result, err := buildClusterSaramaConfig(current)
	if err != nil {
		return nil, err
	}
	selected := saramaOptions{}
	for _, option := range options {
		if option == nil {
			return nil, invalidConfig("kafkamodule SaramaConfigOption 不能为空")
		}
		option.applySarama(&selected)
	}
	if err = applyHooks(result, selected.hooks); err != nil {
		return nil, err
	}
	if result.Net.TLS.Config != nil && result.Net.TLS.Config.InsecureSkipVerify {
		return nil, invalidConfig("kafkamodule 禁止跳过 TLS 服务端证书校验")
	}
	if err = result.Validate(); err != nil {
		return nil, invalidConfig("kafkamodule 自由 Sarama 配置无效: " + err.Error())
	}
	return result, nil
}

// BuildAdminSaramaConfig 构建自由模式 Admin 基础配置；它是语义明确的 BuildSaramaConfig 别名。
func BuildAdminSaramaConfig(input ClusterConfig, options ...SaramaConfigOption) (*sarama.Config, error) {
	return BuildSaramaConfig(input, options...)
}

func buildClusterSaramaConfig(current ClusterConfig) (*sarama.Config, error) {
	version, err := sarama.ParseKafkaVersion(current.Version)
	if err != nil {
		return nil, invalidConfig("kafkamodule cluster.version 无效")
	}
	result := sarama.NewConfig()
	result.Version = version
	result.ClientID = current.ClientID
	result.Net.DialTimeout = current.DialTimeout.Duration()
	result.Net.ReadTimeout = current.ReadTimeout.Duration()
	result.Net.WriteTimeout = current.WriteTimeout.Duration()
	result.Net.KeepAlive = current.KeepAlive.Duration()
	result.Metadata.Timeout = current.MetadataTimeout.Duration()
	result.Metadata.RefreshFrequency = current.MetadataRefreshInterval.Duration()
	result.Metadata.Retry.Max = current.MetadataRetryMax
	result.Metadata.Retry.Backoff = current.MetadataRetryBackoff.Duration()
	result.Metadata.AllowAutoTopicCreation = current.AllowAutoTopicCreation
	if current.TLS.Enable {
		tlsConfig, tlsErr := loadTLSConfig(current.TLS)
		if tlsErr != nil {
			return nil, tlsErr
		}
		result.Net.TLS.Enable = true
		result.Net.TLS.Config = tlsConfig
	}
	if current.SASL.Enable {
		result.Net.SASL.Enable = true
		result.Net.SASL.User = current.SASL.Username
		result.Net.SASL.Password = current.SASL.Password
		result.Net.SASL.Handshake = true
		switch current.SASL.Mechanism {
		case "plain":
			result.Net.SASL.Mechanism = sarama.SASLTypePlaintext
		case "scram_sha_256":
			result.Net.SASL.Mechanism = sarama.SASLTypeSCRAMSHA256
			result.Net.SASL.SCRAMClientGeneratorFunc = func() sarama.SCRAMClient { return &xdgSCRAMClient{hash: scram.SHA256} }
		case "scram_sha_512":
			result.Net.SASL.Mechanism = sarama.SASLTypeSCRAMSHA512
			result.Net.SASL.SCRAMClientGeneratorFunc = func() sarama.SCRAMClient { return &xdgSCRAMClient{hash: scram.SHA512} }
		}
	}
	return result, nil
}

func loadTLSConfig(current TLSConfig) (*tls.Config, error) {
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	if current.CAFile != "" {
		pem, readErr := os.ReadFile(current.CAFile)
		if readErr != nil {
			return nil, invalidConfig("kafkamodule 无法读取 TLS CA 文件")
		}
		if !pool.AppendCertsFromPEM(pem) {
			return nil, invalidConfig("kafkamodule TLS CA 文件不包含有效 PEM 证书")
		}
	}
	result := &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: pool, ServerName: current.ServerName}
	if current.CertFile != "" {
		certificate, loadErr := tls.LoadX509KeyPair(current.CertFile, current.KeyFile)
		if loadErr != nil {
			return nil, invalidConfig("kafkamodule 无法加载 TLS 客户端证书和私钥")
		}
		result.Certificates = []tls.Certificate{certificate}
	}
	return result, nil
}

func applyHooks(target *sarama.Config, hooks []SaramaConfigHook) (err error) {
	defer func() {
		if recover() != nil {
			err = invalidConfig("kafkamodule Sarama Hook panic")
		}
	}()
	for _, hook := range hooks {
		if hook == nil {
			return invalidConfig("kafkamodule Sarama Hook 不能为空")
		}
		if err := hook(target); err != nil {
			return invalidConfig("kafkamodule Sarama Hook 失败: " + err.Error())
		}
	}
	return nil
}

func validateManagedProducerConfig(current *sarama.Config) error {
	if !current.Producer.Return.Successes || !current.Producer.Return.Errors {
		return invalidConfig("kafkamodule 受管 Producer 必须开启 Successes 和 Errors")
	}
	if current.Producer.Transaction.ID != "" {
		return invalidConfig("kafkamodule 受管 Producer 不支持事务")
	}
	if current.Producer.Retry.MaxBufferLength <= 0 || current.Producer.Retry.MaxBufferBytes <= 0 {
		return invalidConfig("kafkamodule 受管 Producer Retry Buffer 必须有界")
	}
	if current.Producer.Idempotent && (current.Producer.RequiredAcks != sarama.WaitForAll || current.Net.MaxOpenRequests != 1 || current.Producer.Retry.Max <= 0) {
		return invalidConfig("kafkamodule Sarama Hook 破坏了幂等 Producer 不变量")
	}
	if current.Net.TLS.Config != nil && current.Net.TLS.Config.InsecureSkipVerify {
		return invalidConfig("kafkamodule 禁止跳过 TLS 服务端证书校验")
	}
	return nil
}

func validateManagedConsumerConfig(current *sarama.Config) error {
	if !current.Consumer.Return.Errors || !current.Consumer.Offsets.AutoCommit.Enable {
		return invalidConfig("kafkamodule 受管 Consumer 必须开启错误通道和 AutoCommit")
	}
	if current.Net.TLS.Config != nil && current.Net.TLS.Config.InsecureSkipVerify {
		return invalidConfig("kafkamodule 禁止跳过 TLS 服务端证书校验")
	}
	return nil
}

type xdgSCRAMClient struct {
	hash         scram.HashGeneratorFcn
	client       *scram.Client
	conversation *scram.ClientConversation
}

func (client *xdgSCRAMClient) Begin(userName, password, authzID string) error {
	value, err := client.hash.NewClient(userName, password, authzID)
	if err != nil {
		return err
	}
	client.client = value
	client.conversation = value.NewConversation()
	return nil
}
func (client *xdgSCRAMClient) Step(challenge string) (string, error) {
	if client.conversation == nil {
		return "", invalidConfig("kafkamodule SCRAM 尚未 Begin")
	}
	return client.conversation.Step(challenge)
}
func (client *xdgSCRAMClient) Done() bool {
	return client.conversation != nil && client.conversation.Done()
}
