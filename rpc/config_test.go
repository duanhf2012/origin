package rpc

import (
	"errors"
	"testing"
	"time"

	"github.com/duanhf2012/origin/v3/errs"
)

// TestTCPConfigDefaults 锁定 TCP 对外配置名称、默认值和严格传输块规则。
func TestTCPConfigDefaults(t *testing.T) {
	config := DefaultConfig()
	config.TCP.Listen = "127.0.0.1:7101"
	config.TCP.Advertise = "127.0.0.1:7101"

	// 默认配置只启用 TCP；业务 payload、发送队列和读空闲超时沿用已确认值。
	if err := config.Validate(); err != nil {
		t.Fatalf("默认 TCP 配置应有效: %v", err)
	}
	if DefaultMaxPayloadSize != 32*1024*1024 {
		t.Fatalf("DefaultMaxPayloadSize = %d，期望 32M", DefaultMaxPayloadSize)
	}
	if config.MaxPayloadSize != DefaultMaxPayloadSize ||
		config.MaxBroadcastSize != DefaultMaxBroadcastSize ||
		config.TCP.SendQueueMessages != 16_384 ||
		config.TCP.ReadIdleTimeout != 15*time.Second ||
		config.TCP.WriteTimeout != 15*time.Second ||
		config.NATS != nil {
		t.Fatalf("默认 TCP 配置错误: %+v", config)
	}
}

// TestNATSConfigDefaults 锁定 NATS 最小公开配置和接收队列默认值。
func TestNATSConfigDefaults(t *testing.T) {
	config := Config{
		Transport:        TransportNATS,
		MaxPayloadSize:   DefaultMaxPayloadSize,
		MaxBroadcastSize: DefaultMaxBroadcastSize,
		NATS:             DefaultNATSConfig(),
	}
	config.NATS.Namespace = "game-prod"
	config.NATS.URLs = []string{"nats://127.0.0.1:4222"}

	// NATS 配置不应要求 TCP 地址，也不公开底层重连、Ping 或字节 Pending 选项。
	if err := config.Validate(); err != nil {
		t.Fatalf("默认 NATS 配置应有效: %v", err)
	}
	if config.NATS.ReceiveQueueMessages != 16_384 || config.TCP != nil {
		t.Fatalf("默认 NATS 配置错误: %+v", config)
	}
}

// TestConfigRejectsInvalidValues 覆盖两种传输的互斥、容量、地址与认证边界。
func TestConfigRejectsInvalidValues(t *testing.T) {
	tcpConfig := DefaultConfig()
	tcpConfig.TCP.Listen = "127.0.0.1:7101"
	tcpConfig.TCP.Advertise = "127.0.0.1:7101"

	natsConfig := Config{
		Transport:        TransportNATS,
		MaxPayloadSize:   DefaultMaxPayloadSize,
		MaxBroadcastSize: DefaultMaxBroadcastSize,
		NATS:             DefaultNATSConfig(),
	}
	natsConfig.NATS.Namespace = "game-prod"
	natsConfig.NATS.URLs = []string{"nats://127.0.0.1:4222"}

	cases := []Config{
		func() Config { value := tcpConfig; value.Transport = "udp"; return value }(),
		func() Config { value := tcpConfig; value.MaxPayloadSize = 0; return value }(),
		func() Config {
			value := tcpConfig
			value.MaxPayloadSize = DefaultMaxPayloadSize + 1
			return value
		}(),
		func() Config { value := tcpConfig; value.MaxBroadcastSize = 0; return value }(),
		func() Config {
			value := tcpConfig
			value.MaxBroadcastSize = MaxBroadcastSize + 1
			return value
		}(),
		func() Config { value := tcpConfig; value.TCP = nil; return value }(),
		func() Config {
			value := tcpConfig
			value.NATS = DefaultNATSConfig()
			return value
		}(),
		func() Config {
			value := tcpConfig
			copy := *value.TCP
			copy.SendQueueMessages = 0
			value.TCP = &copy
			return value
		}(),
		func() Config {
			value := tcpConfig
			copy := *value.TCP
			copy.SendQueueMessages = 65_537
			value.TCP = &copy
			return value
		}(),
		func() Config {
			value := tcpConfig
			copy := *value.TCP
			copy.ReadIdleTimeout = -1
			value.TCP = &copy
			return value
		}(),
		func() Config {
			value := tcpConfig
			copy := *value.TCP
			copy.WriteTimeout = 0
			value.TCP = &copy
			return value
		}(),
		func() Config {
			value := tcpConfig
			copy := *value.TCP
			copy.Listen = "bad"
			value.TCP = &copy
			return value
		}(),
		func() Config {
			value := tcpConfig
			copy := *value.TCP
			copy.Advertise = "0.0.0.0:7101"
			value.TCP = &copy
			return value
		}(),
		func() Config { value := natsConfig; value.NATS = nil; return value }(),
		func() Config {
			value := natsConfig
			value.TCP = DefaultTCPConfig()
			return value
		}(),
		func() Config {
			value := natsConfig
			copy := *value.NATS
			copy.Namespace = "Game.Prod"
			value.NATS = &copy
			return value
		}(),
		func() Config {
			value := natsConfig
			copy := *value.NATS
			copy.URLs = nil
			value.NATS = &copy
			return value
		}(),
		func() Config {
			value := natsConfig
			copy := *value.NATS
			copy.ReceiveQueueMessages = 65_537
			value.NATS = &copy
			return value
		}(),
		func() Config {
			value := natsConfig
			copy := *value.NATS
			copy.Auth.Username = "game"
			copy.Auth.Token = "secret"
			value.NATS = &copy
			return value
		}(),
		func() Config {
			value := natsConfig
			copy := *value.NATS
			copy.TLS.CertFile = "client.pem"
			value.NATS = &copy
			return value
		}(),
	}
	for index, config := range cases {
		err := config.Validate()
		if !errors.Is(err, errs.ErrInvalidConfig) {
			t.Fatalf("case %d 错误=%v，期望 CodeInvalidConfig", index, err)
		}
	}
}
