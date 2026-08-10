// Package admin 定义 Application 与 Service 共同使用的 Admin HTTP 值模型。
package admin

import (
	"context"
	"net/http"
	"time"

	"github.com/duanhf2012/origin/v3/errs"
)

const (
	// maxEndpointNameBytes 限制单段路由标识，避免业务名称扩大 URL、路由表和审计字段。
	maxEndpointNameBytes = 63
	// DefaultTimeout 是没有单独设置时每个 Admin Endpoint 的执行上限。
	// 首版 Option 只允许在这个框架硬上限内缩短预算。
	DefaultTimeout = 15 * time.Second
	// DefaultMaxBodyBytes 是 POST Endpoint 默认允许的请求体上限。
	// 首版 Option 只允许在这个框架硬上限内缩小请求体。
	DefaultMaxBodyBytes int64 = 1 << 20
	// DefaultMaxResponseBytes 是 Endpoint 编码结果默认允许的上限。
	// 首版 Option 只允许在这个框架硬上限内缩小响应体。
	DefaultMaxResponseBytes int64 = 4 << 20
)

// Handler 处理已经完成认证、授权和输入复制的 Admin 请求。
type Handler func(context.Context, Request) (Response, error)

// Provider 提供当前实例注册的 Admin Endpoint。
type Provider interface {
	AdminEndpoints() []Endpoint
}

// Option 只配置 Endpoint 的执行边界；配置错误会由 Validate 返回。
type Option func(*endpointOptions) error

type endpointOptions struct {
	timeout          time.Duration
	maxBodyBytes     int64
	maxResponseBytes int64
	successStatus    int
}

// Endpoint 是创建后不再变化的 Admin HTTP Endpoint 定义。
type Endpoint struct {
	name    string
	method  string
	handler Handler

	timeout          time.Duration
	maxBodyBytes     int64
	maxResponseBytes int64
	successStatus    int
	optionErr        error
}

// Get 创建只接受无请求体的 GET Endpoint。
func Get(name string, handler Handler, options ...Option) Endpoint {
	return newEndpoint(name, http.MethodGet, handler, options)
}

// Post 创建接受 JSON 请求体的 POST Endpoint。
func Post(name string, handler Handler, options ...Option) Endpoint {
	return newEndpoint(name, http.MethodPost, handler, options)
}

func newEndpoint(name, method string, handler Handler, options []Option) Endpoint {
	configured := endpointOptions{
		timeout:          DefaultTimeout,
		maxResponseBytes: DefaultMaxResponseBytes,
		successStatus:    http.StatusOK,
	}
	if method == http.MethodPost {
		configured.maxBodyBytes = DefaultMaxBodyBytes
		configured.successStatus = http.StatusNoContent
	}

	var optionErr error
	for _, option := range options {
		if option == nil {
			if optionErr == nil {
				optionErr = errs.NewMessage(errs.CodeInvalidArgument, "Admin Endpoint Option 不能为空")
			}
			continue
		}
		if err := option(&configured); err != nil && optionErr == nil {
			optionErr = err
		}
	}
	return Endpoint{
		name:             name,
		method:           method,
		handler:          handler,
		timeout:          configured.timeout,
		maxBodyBytes:     configured.maxBodyBytes,
		maxResponseBytes: configured.maxResponseBytes,
		successStatus:    configured.successStatus,
		optionErr:        optionErr,
	}
}

// WithTimeout 设置 Handler 的最长执行时间。
func WithTimeout(value time.Duration) Option {
	return func(options *endpointOptions) error {
		options.timeout = value
		return nil
	}
}

// WithMaxBodyBytes 设置 POST 请求体的最大字节数。
func WithMaxBodyBytes(value int64) Option {
	return func(options *endpointOptions) error {
		options.maxBodyBytes = value
		return nil
	}
}

// WithMaxResponseBytes 设置已编码响应的最大字节数。
func WithMaxResponseBytes(value int64) Option {
	return func(options *endpointOptions) error {
		options.maxResponseBytes = value
		return nil
	}
}

// WithSuccessStatus 设置 Handler 成功时必须使用的 2xx 状态码。
func WithSuccessStatus(value int) Option {
	return func(options *endpointOptions) error {
		options.successStatus = value
		return nil
	}
}

// Validate 检查 Endpoint 名称、资源上限和执行 Handler 所需的所有不变量。
func (endpoint Endpoint) Validate() error {
	if endpoint.optionErr != nil {
		return endpoint.optionErr
	}
	if !validEndpointName(endpoint.name) {
		return errs.NewMessage(errs.CodeInvalidArgument, "Admin Endpoint 名称无效")
	}
	if endpoint.method != http.MethodGet && endpoint.method != http.MethodPost {
		return errs.NewMessage(errs.CodeInvalidArgument, "Admin Endpoint Method 无效")
	}
	if endpoint.handler == nil {
		return errs.NewMessage(errs.CodeInvalidArgument, "Admin Endpoint Handler 不能为空")
	}
	if endpoint.timeout <= 0 || endpoint.timeout > DefaultTimeout {
		return errs.NewMessage(errs.CodeInvalidArgument, "Admin Endpoint Timeout 超出框架边界")
	}
	if endpoint.maxResponseBytes <= 0 || endpoint.maxResponseBytes > DefaultMaxResponseBytes {
		return errs.NewMessage(errs.CodeInvalidArgument, "Admin Endpoint MaxResponseBytes 超出框架边界")
	}
	if endpoint.successStatus < http.StatusOK || endpoint.successStatus >= http.StatusMultipleChoices {
		return errs.NewMessage(errs.CodeInvalidArgument, "Admin Endpoint SuccessStatus 必须是 2xx")
	}
	if endpoint.method == http.MethodGet {
		if endpoint.maxBodyBytes != 0 {
			return errs.NewMessage(errs.CodeInvalidArgument, "GET Admin Endpoint 不接受请求体")
		}
	} else if endpoint.maxBodyBytes <= 0 || endpoint.maxBodyBytes > DefaultMaxBodyBytes {
		return errs.NewMessage(errs.CodeInvalidArgument, "POST Admin Endpoint MaxBodyBytes 超出框架边界")
	}
	return nil
}

func validEndpointName(name string) bool {
	if len(name) == 0 || len(name) > maxEndpointNameBytes || name[0] < 'a' || name[0] > 'z' {
		return false
	}
	previousHyphen := false
	for index := 0; index < len(name); index++ {
		value := name[index]
		if value >= 'a' && value <= 'z' || value >= '0' && value <= '9' {
			previousHyphen = false
			continue
		}
		if value == '-' && !previousHyphen && index+1 < len(name) {
			previousHyphen = true
			continue
		}
		return false
	}
	return true
}

// Name 返回 Endpoint 的固定名称。
func (endpoint Endpoint) Name() string { return endpoint.name }

// Method 返回 Endpoint 的固定 HTTP 方法。
func (endpoint Endpoint) Method() string { return endpoint.method }

// Timeout 返回 Handler 的固定执行上限。
func (endpoint Endpoint) Timeout() time.Duration { return endpoint.timeout }

// MaxBodyBytes 返回请求体的固定字节上限。
func (endpoint Endpoint) MaxBodyBytes() int64 { return endpoint.maxBodyBytes }

// MaxResponseBytes 返回响应体的固定字节上限。
func (endpoint Endpoint) MaxResponseBytes() int64 { return endpoint.maxResponseBytes }

// SuccessStatus 返回成功响应必须使用的固定 HTTP 状态码。
func (endpoint Endpoint) SuccessStatus() int { return endpoint.successStatus }

// Invoke 在唯一的 Handler 边界恢复 panic，避免业务 panic 逃离 Admin Runtime。
func (endpoint Endpoint) Invoke(ctx context.Context, request Request) (response Response, err error) {
	defer func() {
		if recover() != nil {
			response = Response{}
			err = errs.New(errs.CodeInternal)
		}
	}()
	return endpoint.handler(ctx, request)
}
