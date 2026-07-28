package rpc

import (
	"errors"
	"testing"
	"time"

	"github.com/duanhf2012/origin/v3/errs"
)

// TestConfigDefaultsAndDerivedLimits 锁定公开默认值和不公开的 M5 字节保护。
func TestConfigDefaultsAndDerivedLimits(t *testing.T) {
	config := DefaultConfig()
	config.TCP.Listen = "127.0.0.1:7101"
	config.TCP.Advertise = "127.0.0.1:7101"
	if err := config.Validate(); err != nil {
		t.Fatalf("默认配置应有效: %v", err)
	}
	if config.TCP.SendQueueFrames != 16_384 ||
		config.TCP.ReadTimeout != 15*time.Second ||
		config.sendQueueBytes() != 8*1024*1024 {
		t.Fatalf("默认或派生值错误: %+v bytes=%d", config, config.sendQueueBytes())
	}
}

// TestConfigRejectsInvalidValues 覆盖所有公开配置边界。
func TestConfigRejectsInvalidValues(t *testing.T) {
	base := DefaultConfig()
	base.TCP.Listen = "127.0.0.1:7101"
	base.TCP.Advertise = "127.0.0.1:7101"
	cases := []Config{
		func() Config { value := base; value.Transport = "nats"; return value }(),
		func() Config { value := base; value.MaxMessageSize = 0; return value }(),
		func() Config { value := base; value.TCP.SendQueueFrames = 0; return value }(),
		func() Config { value := base; value.TCP.SendQueueFrames = 65_537; return value }(),
		func() Config { value := base; value.TCP.ReadTimeout = -1; return value }(),
		func() Config { value := base; value.TCP.WriteTimeout = 0; return value }(),
		func() Config { value := base; value.TCP.Listen = "bad"; return value }(),
		func() Config { value := base; value.TCP.Advertise = "0.0.0.0:7101"; return value }(),
	}
	for index, config := range cases {
		err := config.Validate()
		if !errors.Is(err, errs.ErrInvalidConfig) {
			t.Fatalf("case %d 错误=%v，期望 CodeInvalidConfig", index, err)
		}
	}
}
