package redismodule

import (
	"crypto/tls"
	"reflect"

	"github.com/redis/go-redis/v9"
)

// Option 是 Redis Module 的封闭高级构造选项。
type Option interface{ apply(*moduleOptions) error }

type optionFunc func(*moduleOptions) error

func (fn optionFunc) apply(options *moduleOptions) error { return fn(options) }

type moduleOptions struct {
	tlsConfig *tls.Config
	hooks     []redis.Hook
	factory   runtimeFactory
}

// WithTLSConfig 使用调用方提供的高级 TLS 配置并立即克隆快照。
//
// config 不能为 nil，不能启用 InsecureSkipVerify，不能与 Config.TLSCAFile 同时使用；
// Config.TLS 必须为 true。调用返回后修改原配置不会影响 Module。
func WithTLSConfig(config *tls.Config) Option {
	return optionFunc(func(options *moduleOptions) error {
		if config == nil {
			return invalidConfig("redismodule TLS 配置不能为空")
		}
		if options.tlsConfig != nil {
			return invalidConfig("redismodule WithTLSConfig 只能设置一次")
		}
		if config.InsecureSkipVerify {
			return invalidConfig("redismodule 禁止跳过 TLS 证书校验")
		}
		options.tlsConfig = config.Clone()
		return nil
	})
}

// WithHook 按参数和 Option 调用顺序安装 go-redis Hook。
//
// Hook 在启动 Ping 前生效，必须非 nil 且由调用方保证并发安全；Hook 不能无差别记录可能包含
// 密码、Token、会话或玩家数据的命令参数。
func WithHook(hooks ...redis.Hook) Option {
	return optionFunc(func(options *moduleOptions) error {
		if len(hooks) == 0 {
			return invalidConfig("redismodule Hook 不能为空")
		}
		for _, hook := range hooks {
			if hook == nil || isNilHook(hook) {
				return invalidConfig("redismodule Hook 不能为空")
			}
			options.hooks = append(options.hooks, hook)
		}
		return nil
	})
}

func isNilHook(hook redis.Hook) bool {
	value := reflect.ValueOf(hook)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

func withRuntimeFactoryForTest(factory runtimeFactory) Option {
	return optionFunc(func(options *moduleOptions) error {
		if factory == nil {
			return invalidConfig("redismodule Runtime Factory 不能为空")
		}
		options.factory = factory
		return nil
	})
}
