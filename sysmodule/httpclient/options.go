// Package httpclient 提供可并发复用、具有生产边界的 HTTP Client。
package httpclient

import (
	"crypto/tls"
	"net/http"
	"net/url"
	"time"

	"github.com/duanhf2012/origin/v3/errs"
)

const (
	defaultTimeout                = 30 * time.Second
	defaultMaxResponseBodySize    = 4 << 20
	defaultDialTimeout            = 5 * time.Second
	defaultDialKeepAlive          = 30 * time.Second
	defaultTLSHandshakeTimeout    = 10 * time.Second
	defaultResponseHeaderTimeout  = 15 * time.Second
	defaultIdleConnTimeout        = 90 * time.Second
	defaultMaxIdleConns           = 128
	defaultMaxIdleConnsPerHost    = 16
	defaultMaxConnsPerHost        = 64
	defaultMaxResponseHeaderBytes = 1 << 20
)

// Options 配置请求总预算、有界完整读取和标准 http.Client 扩展点。
type Options struct {
	// Timeout 覆盖连接、重定向和读取响应 Body 的总时间。
	Timeout time.Duration
	// MaxResponseBodySize 是 DoBytes 允许读取的解压后 Body 字节上限。
	MaxResponseBodySize int64
	// Transport 为 nil 时创建当前 Client 私有的默认连接池；非 nil 时由调用方管理共享语义。
	Transport http.RoundTripper
	// CheckRedirect 由 http.Client 在发起下一次跳转前调用；nil 使用标准最多十次跳转策略。
	CheckRedirect func(*http.Request, []*http.Request) error
	// Jar 保存跨请求 Cookie；nil 表示不自动持久化 Cookie。
	Jar http.CookieJar
}

// TransportOptions 配置生产环境经常调整的拨号、TLS、响应 Header 与连接池边界。
type TransportOptions struct {
	// DialTimeout 是 DNS/TCP 单次建连预算。
	DialTimeout time.Duration
	// DialKeepAlive 是 TCP KeepAlive 探测周期，不是 HTTP 连接池空闲时间。
	DialKeepAlive time.Duration
	// TLSHandshakeTimeout 是 TLS 握手预算。
	TLSHandshakeTimeout time.Duration
	// ResponseHeaderTimeout 是写完请求后等待响应 Header 的预算。
	ResponseHeaderTimeout time.Duration
	// IdleConnTimeout 是 HTTP Keep-Alive 空闲连接的保留时间。
	IdleConnTimeout time.Duration
	// MaxIdleConns 是全部目标合计的空闲连接上限。
	MaxIdleConns int
	// MaxIdleConnsPerHost 是单目标保留的空闲连接上限。
	MaxIdleConnsPerHost int
	// MaxConnsPerHost 是单目标拨号中、活动和空闲连接总上限。
	MaxConnsPerHost int
	// MaxResponseHeaderBytes 是单次响应 Header 的字节上限。
	MaxResponseHeaderBytes int64
	// Proxy 为 nil 时禁用代理；默认值使用 http.ProxyFromEnvironment。
	Proxy func(*http.Request) (*url.URL, error)
	// TLSConfig 为 nil 时使用系统根证书；非 nil 时由构造器克隆。
	TLSConfig *tls.Config
}

// DefaultOptions 返回普通服务间 HTTP API 可直接使用的有界默认值。
func DefaultOptions() Options {
	return Options{
		Timeout:             defaultTimeout,
		MaxResponseBodySize: defaultMaxResponseBodySize,
	}
}

// DefaultTransportOptions 返回启用连接复用、TLS 校验和 HTTP/2 的默认 Transport 配置。
func DefaultTransportOptions() TransportOptions {
	return TransportOptions{
		DialTimeout:            defaultDialTimeout,
		DialKeepAlive:          defaultDialKeepAlive,
		TLSHandshakeTimeout:    defaultTLSHandshakeTimeout,
		ResponseHeaderTimeout:  defaultResponseHeaderTimeout,
		IdleConnTimeout:        defaultIdleConnTimeout,
		MaxIdleConns:           defaultMaxIdleConns,
		MaxIdleConnsPerHost:    defaultMaxIdleConnsPerHost,
		MaxConnsPerHost:        defaultMaxConnsPerHost,
		MaxResponseHeaderBytes: defaultMaxResponseHeaderBytes,
		Proxy:                  http.ProxyFromEnvironment,
	}
}

func validateOptions(options Options) error {
	if options.Timeout <= 0 {
		return errs.NewMessage(errs.CodeInvalidConfig, "httpclient.timeout 必须大于零")
	}
	if options.MaxResponseBodySize <= 0 {
		return errs.NewMessage(errs.CodeInvalidConfig, "httpclient.max_response_body_size 必须大于零")
	}
	return nil
}

func validateTransportOptions(options TransportOptions) error {
	if options.DialTimeout <= 0 || options.DialKeepAlive <= 0 ||
		options.TLSHandshakeTimeout <= 0 || options.ResponseHeaderTimeout <= 0 ||
		options.IdleConnTimeout <= 0 {
		return errs.NewMessage(errs.CodeInvalidConfig, "httpclient Transport 的所有超时必须大于零")
	}
	if options.MaxIdleConns <= 0 || options.MaxIdleConnsPerHost <= 0 ||
		options.MaxConnsPerHost <= 0 || options.MaxResponseHeaderBytes <= 0 {
		return errs.NewMessage(errs.CodeInvalidConfig, "httpclient Transport 的所有容量必须大于零")
	}
	if options.MaxIdleConns < options.MaxIdleConnsPerHost {
		return errs.NewMessage(
			errs.CodeInvalidConfig,
			"httpclient.max_idle_conns 不能小于 max_idle_conns_per_host",
		)
	}
	if options.MaxConnsPerHost < options.MaxIdleConnsPerHost {
		return errs.NewMessage(
			errs.CodeInvalidConfig,
			"httpclient.max_conns_per_host 不能小于 max_idle_conns_per_host",
		)
	}
	if options.TLSConfig != nil && options.TLSConfig.InsecureSkipVerify {
		return errs.NewMessage(errs.CodeInvalidConfig, "httpclient 禁止 TLS InsecureSkipVerify")
	}
	return nil
}
