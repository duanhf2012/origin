package kafkamodule

import (
	"errors"
	"testing"
	"time"

	"github.com/IBM/sarama"
	"github.com/duanhf2012/origin/v3/config"
)

func validClusterConfig() ClusterConfig {
	return ClusterConfig{Brokers: []string{"127.0.0.1:9092"}, Version: "4.0.0", ClientID: "origin-test"}
}

func validProducerConfig() ProducerConfig { return ProducerConfig{Cluster: validClusterConfig()} }

func validConsumerConfig() ConsumerConfig {
	return ConsumerConfig{Cluster: validClusterConfig(), GroupID: "player-events", Topics: []string{"player-events"}}
}

func TestProducerConfigDefaultsAndManagedInvariants(t *testing.T) {
	current, err := normalizeProducerConfig(validProducerConfig())
	if err != nil {
		t.Fatal(err)
	}
	if current.RequiredAcks != "all" || !current.Idempotent || current.Compression != "snappy" {
		t.Fatalf("unexpected producer defaults: %+v", current)
	}
	if current.SubmitQueueMessages != 1024 || current.SubmitQueueSize.Bytes() != 64<<20 {
		t.Fatalf("unexpected queue defaults: %+v", current)
	}
	options, err := BuildProducerSaramaConfig(validProducerConfig())
	if err != nil {
		t.Fatal(err)
	}
	if !options.Producer.Return.Successes || !options.Producer.Return.Errors || !options.Producer.Idempotent || options.Net.MaxOpenRequests != 1 {
		t.Fatalf("managed invariants lost: %+v", options.Producer)
	}
}

func TestProducerConfigRejectsInvalidCombinations(t *testing.T) {
	tests := []ProducerConfig{
		{},
		{Cluster: ClusterConfig{Brokers: []string{"missing-port"}, Version: "4.0.0"}},
		{Cluster: ClusterConfig{Brokers: []string{"127.0.0.1:9092", "127.0.0.1:9092"}, Version: "4.0.0"}},
		{Cluster: ClusterConfig{Brokers: []string{"127.0.0.1:9092"}, Version: "future"}},
		{Cluster: validClusterConfig(), RequiredAcks: "one", Idempotent: true},
		{Cluster: validClusterConfig(), RequiredAcks: "invalid"},
		{Cluster: validClusterConfig(), Compression: "invalid"},
		{Cluster: validClusterConfig(), MaxMessageSize: config.ByteSize(-1)},
		{Cluster: validClusterConfig(), SubmitQueueMessages: -1},
		{Cluster: validClusterConfig(), RetryBufferMessages: -1},
		{Cluster: validClusterConfig(), FlushMessages: 10, FlushMaxMessages: 5},
	}
	for index, input := range tests {
		if _, err := normalizeProducerConfig(input); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("case %d: %v", index, err)
		}
	}
}

func TestConsumerConfigDefaultsAndInvalidCombinations(t *testing.T) {
	current, err := normalizeConsumerConfig(validConsumerConfig(), false)
	if err != nil {
		t.Fatal(err)
	}
	if current.InitialOffset != "newest" || current.BalanceStrategy != "cooperative_sticky" || current.IsolationLevel != "read_committed" {
		t.Fatalf("unexpected consumer defaults: %+v", current)
	}
	invalid := []ConsumerConfig{
		{},
		{Cluster: validClusterConfig(), GroupID: "", Topics: []string{"a"}},
		{Cluster: validClusterConfig(), GroupID: "g", Topics: nil},
		{Cluster: validClusterConfig(), GroupID: "g", Topics: []string{"a", "a"}},
		{Cluster: validClusterConfig(), GroupID: "g", Topics: []string{"a"}, InitialOffset: "middle"},
		{Cluster: validClusterConfig(), GroupID: "g", Topics: []string{"a"}, BalanceStrategy: "random"},
		{Cluster: validClusterConfig(), GroupID: "g", Topics: []string{"a"}, SessionTimeout: config.Duration(time.Second), HeartbeatInterval: config.Duration(time.Second)},
		{Cluster: validClusterConfig(), GroupID: "g", Topics: []string{"a"}, FetchMaxPartitionSize: config.ByteSize(5 << 20), FetchMaxTotalSize: config.ByteSize(4 << 20)},
	}
	for index, input := range invalid {
		if _, err := normalizeConsumerConfig(input, false); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("case %d: %v", index, err)
		}
	}
	if _, err = normalizeConsumerConfig(validConsumerConfig(), true); err != nil {
		t.Fatal(err)
	}
}

func TestSaramaHookCannotBreakManagedInvariants(t *testing.T) {
	_, err := BuildProducerSaramaConfig(validProducerConfig(), WithProducerSaramaConfig(func(current *sarama.Config) error {
		current.Producer.Return.Successes = false
		return nil
	}))
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("broken producer hook accepted: %v", err)
	}
	_, err = BuildConsumerSaramaConfig(validConsumerConfig(), WithConsumerSaramaConfig(func(current *sarama.Config) error {
		current.Consumer.Return.Errors = false
		return nil
	}))
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("broken consumer hook accepted: %v", err)
	}
}

func TestClusterTLSAndSASLValidation(t *testing.T) {
	invalid := []ClusterConfig{
		{Brokers: []string{"127.0.0.1:9092"}, Version: "4.0.0", TLS: TLSConfig{CAFile: "ca.pem"}},
		{Brokers: []string{"127.0.0.1:9092"}, Version: "4.0.0", TLS: TLSConfig{Enable: true, CertFile: "cert.pem"}},
		{Brokers: []string{"127.0.0.1:9092"}, Version: "4.0.0", SASL: SASLConfig{Enable: true, Mechanism: "plain"}},
		{Brokers: []string{"127.0.0.1:9092"}, Version: "4.0.0", SASL: SASLConfig{Enable: true, Mechanism: "unknown", Username: "u", Password: "p"}},
	}
	for index, current := range invalid {
		if _, err := BuildAdminSaramaConfig(current); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("case %d: %v", index, err)
		}
	}
}
