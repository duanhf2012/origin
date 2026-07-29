// Package origin 实现不依赖外部中间件的 Origin 内置服务发现。
package origin

import (
	"net"
	"strconv"
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

// ServerConfig 同时描述 DiscoveryService 监听地址和客户端引导地址。
type ServerConfig struct {
	Node    string
	Listen  string
	Address string
}

type configMirror struct {
	TTL    *originconfig.Duration `json:"ttl"`
	Server serverConfigMirror     `json:"server"`
}

type serverConfigMirror struct {
	Node    string `json:"node"`
	Listen  string `json:"listen"`
	Address string `json:"address"`
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
			Node:    strings.TrimSpace(mirror.Server.Node),
			Listen:  strings.TrimSpace(mirror.Server.Listen),
			Address: strings.TrimSpace(mirror.Server.Address),
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
	if err := validateListenAddress(result.Server.Listen); err != nil {
		return Config{}, err
	}
	if err := validateDialAddress(result.Server.Address); err != nil {
		return Config{}, err
	}
	return result, nil
}

func validateListenAddress(address string) error {
	_, portText, err := net.SplitHostPort(address)
	if err != nil {
		return invalidConfig("discovery.origin.server.listen 必须是 host:port")
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port <= 0 || port > 65535 {
		return invalidConfig("discovery.origin.server.listen 端口必须位于 1～65535")
	}
	return nil
}

func validateDialAddress(address string) error {
	host, portText, err := net.SplitHostPort(address)
	if err != nil {
		return invalidConfig("discovery.origin.server.address 必须是 host:port")
	}
	host = strings.TrimSpace(host)
	if host == "" || host == "0.0.0.0" || host == "::" {
		return invalidConfig("discovery.origin.server.address 不能使用空主机或通配地址")
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port <= 0 || port > 65535 {
		return invalidConfig("discovery.origin.server.address 端口必须位于 1～65535")
	}
	return nil
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
