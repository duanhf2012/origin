// Package etcd implements the built-in etcd discovery Provider.
package etcd

import (
	"crypto/tls"
	"crypto/x509"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	originconfig "github.com/duanhf2012/origin/v3/config"
	publicprovider "github.com/duanhf2012/origin/v3/discovery/provider"
	"github.com/duanhf2012/origin/v3/errs"
)

const (
	defaultNamespace      = "/origin"
	defaultTTL            = 15 * time.Second
	defaultDialTimeout    = 5 * time.Second
	defaultRequestTimeout = 5 * time.Second
	maxNetworks           = 64
	rangePageSize         = 32
)

// Config is the validated immutable etcd Provider configuration.
type Config struct {
	Endpoints      []string
	Namespace      string
	LocalNetwork   string
	WatchNetworks  []string
	Networks       []string
	TTL            time.Duration
	DialTimeout    time.Duration
	RequestTimeout time.Duration
	Auth           AuthConfig
	TLS            TLSConfig
}

// AuthConfig describes the two mutually exclusive etcd authentication modes.
type AuthConfig struct {
	Username string
	Password string
	Token    string
}

// TLSConfig contains resolved certificate paths and immutable verification flags.
type TLSConfig struct {
	Enabled            bool
	CAFile             string
	CertFile           string
	KeyFile            string
	ServerName         string
	InsecureSkipVerify bool
}

type configMirror struct {
	Endpoints      []string               `json:"endpoints"`
	Namespace      string                 `json:"namespace"`
	LocalNetwork   string                 `json:"local_network"`
	WatchNetworks  []string               `json:"watch_networks"`
	TTL            *originconfig.Duration `json:"ttl"`
	DialTimeout    *originconfig.Duration `json:"dial_timeout"`
	RequestTimeout *originconfig.Duration `json:"request_timeout"`
	Auth           authConfigMirror       `json:"auth"`
	TLS            tlsConfigMirror        `json:"tls"`
}

type authConfigMirror struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Token    string `json:"token"`
}

type tlsConfigMirror struct {
	CAFile             string `json:"ca_file"`
	CertFile           string `json:"cert_file"`
	KeyFile            string `json:"key_file"`
	ServerName         string `json:"server_name"`
	InsecureSkipVerify bool   `json:"insecure_skip_verify"`
}

// DecodeConfig strictly decodes, normalizes, and validates one etcd block.
func DecodeConfig(raw publicprovider.Config, configRoot string) (Config, error) {
	var mirror configMirror
	if err := raw.Decode(&mirror); err != nil {
		return Config{}, err
	}
	result := Config{
		Namespace:      strings.TrimSpace(mirror.Namespace),
		LocalNetwork:   strings.TrimSpace(mirror.LocalNetwork),
		TTL:            defaultTTL,
		DialTimeout:    defaultDialTimeout,
		RequestTimeout: defaultRequestTimeout,
		Auth: AuthConfig{
			Username: strings.TrimSpace(mirror.Auth.Username),
			Password: mirror.Auth.Password,
			Token:    mirror.Auth.Token,
		},
		TLS: TLSConfig{
			CAFile:             strings.TrimSpace(mirror.TLS.CAFile),
			CertFile:           strings.TrimSpace(mirror.TLS.CertFile),
			KeyFile:            strings.TrimSpace(mirror.TLS.KeyFile),
			ServerName:         strings.TrimSpace(mirror.TLS.ServerName),
			InsecureSkipVerify: mirror.TLS.InsecureSkipVerify,
		},
	}
	if result.Namespace == "" {
		result.Namespace = defaultNamespace
	}
	if mirror.TTL != nil {
		result.TTL = mirror.TTL.Duration()
	}
	if mirror.DialTimeout != nil {
		result.DialTimeout = mirror.DialTimeout.Duration()
	}
	if mirror.RequestTimeout != nil {
		result.RequestTimeout = mirror.RequestTimeout.Duration()
	}
	if err := validateDurations(result); err != nil {
		return Config{}, err
	}
	if !validNamespace(result.Namespace) {
		return Config{}, invalidConfig(
			"discovery.etcd.namespace 必须是非根、无尾随斜线的绝对 kebab-case 前缀",
		)
	}
	if !validToken(result.LocalNetwork) {
		return Config{}, invalidConfig(
			"discovery.etcd.local_network 必须是 63 字节以内的小写 kebab-case",
		)
	}
	networks := make(map[string]struct{}, len(mirror.WatchNetworks)+1)
	networks[result.LocalNetwork] = struct{}{}
	for _, network := range mirror.WatchNetworks {
		network = strings.TrimSpace(network)
		if !validToken(network) {
			return Config{}, invalidConfig(
				"discovery.etcd.watch_networks 包含非法网络名",
			)
		}
		networks[network] = struct{}{}
	}
	if len(networks) > maxNetworks {
		return Config{}, invalidConfig("discovery.etcd 有效网络不能超过 64 个")
	}
	result.Networks = make([]string, 0, len(networks))
	for network := range networks {
		result.Networks = append(result.Networks, network)
	}
	slices.Sort(result.Networks)
	result.WatchNetworks = make([]string, 0, len(result.Networks)-1)
	for _, network := range result.Networks {
		if network != result.LocalNetwork {
			result.WatchNetworks = append(result.WatchNetworks, network)
		}
	}

	scheme, endpoints, err := normalizeEndpoints(mirror.Endpoints)
	if err != nil {
		return Config{}, err
	}
	result.Endpoints = endpoints
	if err := validateAuth(result.Auth); err != nil {
		return Config{}, err
	}
	result.TLS.Enabled = scheme == "https"
	if err := resolveAndValidateTLS(&result.TLS, configRoot); err != nil {
		return Config{}, err
	}
	if !result.TLS.Enabled && hasTLSFields(result.TLS) {
		return Config{}, invalidConfig("HTTP endpoint 不能配置 discovery.etcd.tls")
	}
	return result, nil
}

func validateDurations(config Config) error {
	if config.TTL < 3*time.Second || config.TTL > 5*time.Minute ||
		config.TTL%time.Second != 0 {
		return invalidConfig("discovery.etcd.ttl 必须是 3s～5m 的整数秒")
	}
	if config.DialTimeout <= 0 || config.DialTimeout > 5*time.Minute {
		return invalidConfig("discovery.etcd.dial_timeout 必须位于 0s～5m")
	}
	if config.RequestTimeout <= 0 || config.RequestTimeout > 5*time.Minute {
		return invalidConfig("discovery.etcd.request_timeout 必须位于 0s～5m")
	}
	return nil
}

func normalizeEndpoints(input []string) (string, []string, error) {
	if len(input) == 0 {
		return "", nil, invalidConfig("discovery.etcd.endpoints 不能为空")
	}
	result := make([]string, 0, len(input))
	seen := make(map[string]struct{}, len(input))
	scheme := ""
	for _, raw := range input {
		parsed, err := url.Parse(strings.TrimSpace(raw))
		if err != nil {
			return "", nil, invalidConfig("discovery.etcd.endpoints 包含非法 URL")
		}
		currentScheme := strings.ToLower(parsed.Scheme)
		if (currentScheme != "http" && currentScheme != "https") ||
			parsed.Host == "" || parsed.User != nil || parsed.Path != "" ||
			parsed.RawQuery != "" || parsed.Fragment != "" {
			return "", nil, invalidConfig(
				"discovery.etcd.endpoint 必须是无 Path、Query、Fragment、UserInfo 的 HTTP(S) URL",
			)
		}
		if scheme == "" {
			scheme = currentScheme
		} else if scheme != currentScheme {
			return "", nil, invalidConfig(
				"discovery.etcd.endpoints 不能混用 HTTP 与 HTTPS",
			)
		}
		normalized := (&url.URL{
			Scheme: currentScheme,
			Host:   strings.ToLower(parsed.Host),
		}).String()
		if _, duplicate := seen[normalized]; duplicate {
			continue
		}
		seen[normalized] = struct{}{}
		result = append(result, normalized)
	}
	return scheme, result, nil
}

func validateAuth(config AuthConfig) error {
	hasUsername := config.Username != ""
	hasPassword := config.Password != ""
	if hasUsername != hasPassword {
		return invalidConfig(
			"discovery.etcd.auth.username 与 password 必须同时配置",
		)
	}
	if config.Token != "" && hasUsername {
		return invalidConfig(
			"discovery.etcd.auth.token 与 username/password 互斥",
		)
	}
	return nil
}

func resolveAndValidateTLS(config *TLSConfig, root string) error {
	for _, target := range []*string{&config.CAFile, &config.CertFile, &config.KeyFile} {
		if *target == "" || filepath.IsAbs(*target) {
			continue
		}
		absoluteRoot, err := filepath.Abs(root)
		if err != nil {
			return errs.Wrap(errs.CodeInvalidConfig, err)
		}
		*target = filepath.Join(absoluteRoot, filepath.FromSlash(*target))
	}
	if (config.CertFile == "") != (config.KeyFile == "") {
		return invalidConfig(
			"discovery.etcd.tls.cert_file 与 key_file 必须同时配置",
		)
	}
	if !config.Enabled {
		return nil
	}
	tlsConfig, err := config.load()
	if err != nil {
		return ConfigError(err)
	}
	_ = tlsConfig
	return nil
}

func (config TLSConfig) load() (*tls.Config, error) {
	if !config.Enabled {
		return nil, nil
	}
	result := &tls.Config{
		MinVersion:         tls.VersionTLS12,
		ServerName:         config.ServerName,
		InsecureSkipVerify: config.InsecureSkipVerify, //nolint:gosec -- explicit opt-in field
	}
	if config.CAFile != "" {
		pem, err := os.ReadFile(config.CAFile)
		if err != nil {
			return nil, err
		}
		roots := x509.NewCertPool()
		if !roots.AppendCertsFromPEM(pem) {
			return nil, invalidConfig("discovery.etcd.tls.ca_file 不包含有效 CA")
		}
		result.RootCAs = roots
	}
	if config.CertFile != "" {
		certificate, err := tls.LoadX509KeyPair(config.CertFile, config.KeyFile)
		if err != nil {
			return nil, err
		}
		result.Certificates = []tls.Certificate{certificate}
	}
	return result, nil
}

func hasTLSFields(config TLSConfig) bool {
	return config.CAFile != "" || config.CertFile != "" ||
		config.KeyFile != "" || config.ServerName != "" ||
		config.InsecureSkipVerify
}

func validNamespace(value string) bool {
	if value == "/" || !strings.HasPrefix(value, "/") ||
		strings.HasSuffix(value, "/") || strings.Contains(value, "//") {
		return false
	}
	for _, segment := range strings.Split(value[1:], "/") {
		if !validToken(segment) {
			return false
		}
	}
	return true
}

func validToken(value string) bool {
	if len(value) == 0 || len(value) > 63 ||
		value[0] < 'a' || value[0] > 'z' ||
		value[len(value)-1] == '-' {
		return false
	}
	previousDash := false
	for index := 1; index < len(value); index++ {
		character := value[index]
		switch {
		case character >= 'a' && character <= 'z',
			character >= '0' && character <= '9':
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

// ConfigError maps file and TLS parsing failures without exposing secret contents.
func ConfigError(cause error) error {
	if cause == nil {
		return errs.ErrInvalidConfig
	}
	return errs.NewMessage(errs.CodeInvalidConfig, "discovery.etcd TLS 配置无效")
}
