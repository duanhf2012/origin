package httpclient

import (
	"net"
	"net/http"
	"time"
)

// NewTransport 创建由调用方持有的独立 HTTP 连接池。
//
// Proxy 可能被并发请求调用，TLSConfig 中的回调由 net/http 握手 goroutine 调用；两者都必须并发安全。
func NewTransport(options TransportOptions) (*http.Transport, error) {
	if err := validateTransportOptions(options); err != nil {
		return nil, err
	}
	dialer := &net.Dialer{
		Timeout:   options.DialTimeout,
		KeepAlive: options.DialKeepAlive,
	}
	tlsConfig := options.TLSConfig
	if tlsConfig != nil {
		tlsConfig = tlsConfig.Clone()
	}
	return &http.Transport{
		Proxy:                  options.Proxy,
		DialContext:            dialer.DialContext,
		ForceAttemptHTTP2:      true,
		MaxIdleConns:           options.MaxIdleConns,
		MaxIdleConnsPerHost:    options.MaxIdleConnsPerHost,
		MaxConnsPerHost:        options.MaxConnsPerHost,
		IdleConnTimeout:        options.IdleConnTimeout,
		TLSHandshakeTimeout:    options.TLSHandshakeTimeout,
		ResponseHeaderTimeout:  options.ResponseHeaderTimeout,
		ExpectContinueTimeout:  time.Second,
		MaxResponseHeaderBytes: options.MaxResponseHeaderBytes,
		TLSClientConfig:        tlsConfig,
	}, nil
}
