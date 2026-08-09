// Package origin 实现不依赖外部中间件的 Origin 内置服务发现。
package origin

import (
	"strings"
	"time"

	originconfig "github.com/duanhf2012/origin/v3/config"
	publicprovider "github.com/duanhf2012/origin/v3/discovery/provider"
	"github.com/duanhf2012/origin/v3/errs"
)

const (
	defaultTTL = 15 * time.Second
	minTTL     = 3 * time.Second
	maxTTL     = 5 * time.Minute
)

// Config 是 Origin Provider 的冻结配置。
type Config struct {
	TTL    time.Duration
	Server ServerConfig
}

// ServerConfig 只标识承载 DiscoveryService 的 Node。实际 TCP 地址或 NATS 连接参数
// 从 Application 的 rpc 和 nodes 配置推导，避免在 Discovery 中重复声明第二份端口。
type ServerConfig struct {
	Node string
}

type configMirror struct {
	TTL    *originconfig.Duration `json:"ttl"`
	Server serverConfigMirror     `json:"server"`
}

type serverConfigMirror struct {
	Node string `json:"node"`
}

// DecodeConfig 严格解码并验证 Origin Provider 配置。
func DecodeConfig(config publicprovider.Config) (Config, error) {
	var mirror configMirror
	if err := config.Decode(&mirror); err != nil {
		return Config{}, err
	}
	result := Config{
		TTL: defaultTTL,
		Server: ServerConfig{
			Node: strings.TrimSpace(mirror.Server.Node),
		},
	}
	if mirror.TTL != nil {
		result.TTL = mirror.TTL.Duration()
	}
	if result.TTL < minTTL || result.TTL > maxTTL {
		return Config{}, invalidConfig("discovery.origin.ttl 必须位于 3s～5m")
	}
	if !validKebab(result.Server.Node) {
		return Config{}, invalidConfig(
			"discovery.origin.server.node 必须是 63 字节以内的小写 kebab-case",
		)
	}
	return result, nil
}

func validKebab(value string) bool {
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

func invalidConfig(message string) error {
	return errs.NewMessage(errs.CodeInvalidConfig, message)
}
