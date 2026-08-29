package ginmodule

import originconfig "github.com/duanhf2012/origin/v3/config"

// ServerConfig 是可以从 Service 配置严格解码的 Gin HTTP Server 配置。
//
// TLS 证书和 SafeErrorMapper 等运行期对象不进入 YAML，应在 Options 转换后通过代码注入。
type ServerConfig struct {
	// Address 是包含端口的监听地址；生产环境应按实际暴露范围设置主机部分。
	Address string `json:"address"`
	// RequestTimeout 是单个请求 Context 的总预算。
	RequestTimeout originconfig.Duration `json:"request_timeout"`
	// ReadHeaderTimeout 是读取完整请求 Header 的最长时间。
	ReadHeaderTimeout originconfig.Duration `json:"read_header_timeout"`
	// ReadTimeout 是读取完整请求（包括 Body）的最长时间。
	ReadTimeout originconfig.Duration `json:"read_timeout"`
	// WriteTimeout 是写出响应的最长时间，必须大于 RequestTimeout。
	WriteTimeout originconfig.Duration `json:"write_timeout"`
	// IdleTimeout 是 HTTP Keep-Alive 空闲连接的保留时间。
	IdleTimeout originconfig.Duration `json:"idle_timeout"`
	// MaxHeaderBytes 是请求 Header 以及 Safe 响应 Header 的字节上限。
	MaxHeaderBytes originconfig.ByteSize `json:"max_header_bytes"`
	// MaxRequestBodySize 是单个请求 Body 的字节上限。
	MaxRequestBodySize originconfig.ByteSize `json:"max_request_body_size"`
	// MaxSafeResponseBodySize 是 Safe Handler 缓冲响应 Body 的字节上限。
	MaxSafeResponseBodySize originconfig.ByteSize `json:"max_safe_response_body_size"`
	// MaxActiveRequests 是当前 Module 同时处理的在途请求硬上限。
	MaxActiveRequests int `json:"max_active_requests"`
	// TrustedProxies 是允许提供转发客户端地址的代理 IP 或 CIDR；空列表表示不信任代理。
	TrustedProxies []string `json:"trusted_proxies"`
}

// DefaultServerConfig 返回与 DefaultServerOptions 完全一致的默认配置。
func DefaultServerConfig() ServerConfig {
	options := DefaultServerOptions()
	return ServerConfig{
		Address:                 "0.0.0.0:19093",
		RequestTimeout:          originconfig.Duration(options.RequestTimeout),
		ReadHeaderTimeout:       originconfig.Duration(options.ReadHeaderTimeout),
		ReadTimeout:             originconfig.Duration(options.ReadTimeout),
		WriteTimeout:            originconfig.Duration(options.WriteTimeout),
		IdleTimeout:             originconfig.Duration(options.IdleTimeout),
		MaxHeaderBytes:          originconfig.ByteSize(options.MaxHeaderBytes),
		MaxRequestBodySize:      originconfig.ByteSize(options.MaxRequestBodySize),
		MaxSafeResponseBodySize: originconfig.ByteSize(options.MaxSafeResponseBodySize),
		MaxActiveRequests:       options.MaxActiveRequests,
		TrustedProxies:          []string{},
	}
}

// Options 把可序列化配置转换为已经完整校验的运行期 Options。
func (configured ServerConfig) Options() (ServerOptions, error) {
	if err := validateAddress(configured.Address); err != nil {
		return ServerOptions{}, err
	}
	options := DefaultServerOptions()
	options.RequestTimeout = configured.RequestTimeout.Duration()
	options.ReadHeaderTimeout = configured.ReadHeaderTimeout.Duration()
	options.ReadTimeout = configured.ReadTimeout.Duration()
	options.WriteTimeout = configured.WriteTimeout.Duration()
	options.IdleTimeout = configured.IdleTimeout.Duration()
	options.MaxHeaderBytes = int(configured.MaxHeaderBytes.Bytes())
	options.MaxRequestBodySize = configured.MaxRequestBodySize.Bytes()
	options.MaxSafeResponseBodySize = configured.MaxSafeResponseBodySize.Bytes()
	options.MaxActiveRequests = configured.MaxActiveRequests
	options.TrustedProxies = append([]string(nil), configured.TrustedProxies...)
	if err := validateServerOptions(options); err != nil {
		return ServerOptions{}, err
	}
	return options, nil
}
