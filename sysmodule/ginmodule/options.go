// Package ginmodule 提供由 Origin Service 托管的 Gin HTTP Module。
package ginmodule

import (
	"crypto/tls"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/duanhf2012/origin/v3/errs"
)

const (
	defaultRequestTimeout     = 15 * time.Second
	defaultReadHeaderTimeout  = 5 * time.Second
	defaultReadTimeout        = 15 * time.Second
	defaultWriteTimeout       = 20 * time.Second
	defaultIdleTimeout        = 60 * time.Second
	defaultMaxHeaderBytes     = 1 << 20
	defaultMaxRequestBodySize = 4 << 20
	defaultMaxSafeBodySize    = 4 << 20
	defaultMaxActiveRequests  = 1024
)

// Response 是 Safe Handler 冻结后由请求 goroutine 提交的有界 HTTP 响应。
type Response struct {
	StatusCode int
	Header     http.Header
	Body       []byte
}

// SafeErrorMapper 把框架调度、超时和内部契约错误映射为不泄漏细节的响应。
//
// Mapper 可能在 HTTP 请求 goroutine 调用，必须并发安全且不能访问只允许 Service 串行访问的数据。
type SafeErrorMapper func(error) Response

// ServerOptions 配置 Gin Module 的 HTTP 生命周期、安全边界和 Safe Handler 缓冲上限。
type ServerOptions struct {
	RequestTimeout          time.Duration
	ReadHeaderTimeout       time.Duration
	ReadTimeout             time.Duration
	WriteTimeout            time.Duration
	IdleTimeout             time.Duration
	MaxHeaderBytes          int
	MaxRequestBodySize      int64
	MaxSafeResponseBodySize int64
	MaxActiveRequests       int
	TrustedProxies          []string
	TLSConfig               *tls.Config
	SafeErrorMapper         SafeErrorMapper
}

// DefaultServerOptions 返回普通 JSON/PB 服务可直接使用的有界默认值。
func DefaultServerOptions() ServerOptions {
	return ServerOptions{
		RequestTimeout:          defaultRequestTimeout,
		ReadHeaderTimeout:       defaultReadHeaderTimeout,
		ReadTimeout:             defaultReadTimeout,
		WriteTimeout:            defaultWriteTimeout,
		IdleTimeout:             defaultIdleTimeout,
		MaxHeaderBytes:          defaultMaxHeaderBytes,
		MaxRequestBodySize:      defaultMaxRequestBodySize,
		MaxSafeResponseBodySize: defaultMaxSafeBodySize,
		MaxActiveRequests:       defaultMaxActiveRequests,
		TrustedProxies:          []string{},
		SafeErrorMapper:         defaultSafeErrorMapper,
	}
}

func validateAddress(address string) error {
	if strings.TrimSpace(address) == "" {
		return errs.NewMessage(errs.CodeInvalidArgument, "ginmodule.address 不能为空")
	}
	if _, _, err := net.SplitHostPort(address); err != nil {
		return errs.Wrap(errs.CodeInvalidArgument, err)
	}
	return nil
}

func validateServerOptions(options ServerOptions) error {
	if options.RequestTimeout <= 0 || options.ReadHeaderTimeout <= 0 ||
		options.ReadTimeout <= 0 || options.WriteTimeout <= 0 || options.IdleTimeout <= 0 {
		return errs.NewMessage(errs.CodeInvalidConfig, "ginmodule 所有超时必须大于零")
	}
	if options.WriteTimeout <= options.RequestTimeout {
		return errs.NewMessage(
			errs.CodeInvalidConfig,
			"ginmodule.write_timeout 必须大于 request_timeout",
		)
	}
	if options.MaxHeaderBytes <= 0 || options.MaxRequestBodySize <= 0 ||
		options.MaxSafeResponseBodySize <= 0 || options.MaxActiveRequests <= 0 {
		return errs.NewMessage(errs.CodeInvalidConfig, "ginmodule 容量上限必须大于零")
	}
	if options.SafeErrorMapper == nil {
		return errs.NewMessage(errs.CodeInvalidConfig, "ginmodule.safe_error_mapper 不能为空")
	}
	if options.TLSConfig != nil && len(options.TLSConfig.Certificates) == 0 &&
		options.TLSConfig.GetCertificate == nil && options.TLSConfig.GetConfigForClient == nil {
		return errs.NewMessage(errs.CodeInvalidConfig, "ginmodule TLSConfig 没有可用服务端证书")
	}
	return nil
}
