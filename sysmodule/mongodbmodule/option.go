package mongodbmodule

import (
	"crypto/tls"
	"time"

	mongooptions "go.mongodb.org/mongo-driver/v2/mongo/options"
)

// Option 是 MongoDB Module 的高级构造选项。
//
// 普通连接参数优先写入 URI；只有 URI 无法表达的官方 Driver 能力才使用
// WithDriverOptions。Option 由本包封闭，防止外部实现绕过配置冻结和安全校验。
type Option interface {
	apply(*moduleOptions) error
}

type optionFunc func(*moduleOptions) error

func (fn optionFunc) apply(options *moduleOptions) error { return fn(options) }

type moduleOptions struct {
	tlsConfig     *tls.Config
	driverOptions []*mongooptions.ClientOptions
	factory       runtimeFactory
}

// WithTLSConfig 使用调用方提供的 TLS 配置，并在配置阶段克隆快照。
//
// config 不能为 nil，且不能启用 InsecureSkipVerify。它不能与 Config.TLSCAFile 或 URI 中的
// tlsCAFile、tlsCertificateKeyFile 同时使用。调用返回后修改原 config 不会影响 Module。
func WithTLSConfig(config *tls.Config) Option {
	return optionFunc(func(options *moduleOptions) error {
		if config == nil {
			return invalidConfig("mongodbmodule TLS 配置不能为空")
		}
		if options.tlsConfig != nil {
			return invalidConfig("mongodbmodule WithTLSConfig 只能设置一次")
		}
		if config.InsecureSkipVerify {
			return invalidConfig("mongodbmodule 禁止跳过 TLS 证书校验")
		}
		options.tlsConfig = config.Clone()
		return nil
	})
}

// WithDriverOptions 按参数顺序追加 URI 无法表达的官方 Driver 高级选项。
//
// options 中的每一项都不能为 nil，也不能再次 ApplyURI 或设置 TLSConfig。后传入的选项覆盖
// 先传入的同名字段。CommandMonitor、PoolMonitor、Registry 等高级引用对象由调用方负责在
// Module 生命周期内保持有效且不再修改。
func WithDriverOptions(options ...*mongooptions.ClientOptions) Option {
	return optionFunc(func(moduleOptions *moduleOptions) error {
		if len(options) == 0 {
			return invalidConfig("mongodbmodule Driver Options 不能为空")
		}
		for _, current := range options {
			if current == nil {
				return invalidConfig("mongodbmodule Driver Option 不能为空")
			}
			if current.GetURI() != "" || len(current.Hosts) != 0 {
				return invalidConfig("mongodbmodule Driver Option 不能再次设置 URI 或 Hosts")
			}
			if current.TLSConfig != nil {
				return invalidConfig("mongodbmodule Driver Option 不能设置 TLSConfig")
			}
			if err := current.Validate(); err != nil {
				return invalidConfig("mongodbmodule Driver Option 无效")
			}
			// 普通值与切片形成独立快照；Monitor、Registry、HTTPClient 等高级对象按官方
			// Options 语义保留共享引用，由调用方负责在 Module 生命周期内不再修改。
			snapshot := cloneClientOptions(current)
			moduleOptions.driverOptions = append(moduleOptions.driverOptions, snapshot)
		}
		return nil
	})
}

func cloneClientOptions(source *mongooptions.ClientOptions) *mongooptions.ClientOptions {
	result := *source
	result.Hosts = append([]string(nil), source.Hosts...)
	result.Compressors = append([]string(nil), source.Compressors...)
	result.AppName = clonePointer(source.AppName)
	result.ConnectTimeout = clonePointer(source.ConnectTimeout)
	result.Direct = clonePointer(source.Direct)
	result.DisableOCSPEndpointCheck = clonePointer(source.DisableOCSPEndpointCheck)
	result.HeartbeatInterval = clonePointer(source.HeartbeatInterval)
	result.LoadBalanced = clonePointer(source.LoadBalanced)
	result.LocalThreshold = clonePointer(source.LocalThreshold)
	result.MaxConnIdleTime = clonePointer(source.MaxConnIdleTime)
	result.MaxPoolSize = clonePointer(source.MaxPoolSize)
	result.MinPoolSize = clonePointer(source.MinPoolSize)
	result.MaxConnecting = clonePointer(source.MaxConnecting)
	result.ReplicaSet = clonePointer(source.ReplicaSet)
	result.RetryReads = clonePointer(source.RetryReads)
	result.RetryWrites = clonePointer(source.RetryWrites)
	result.ServerMonitoringMode = clonePointer(source.ServerMonitoringMode)
	result.ServerSelectionTimeout = clonePointer(source.ServerSelectionTimeout)
	result.SRVMaxHosts = clonePointer(source.SRVMaxHosts)
	result.SRVServiceName = clonePointer(source.SRVServiceName)
	result.Timeout = clonePointer(source.Timeout)
	result.ZlibLevel = clonePointer(source.ZlibLevel)
	result.ZstdLevel = clonePointer(source.ZstdLevel)
	result.MaxAdaptiveRetries = clonePointer(source.MaxAdaptiveRetries)
	result.EnableOverloadRetargeting = clonePointer(source.EnableOverloadRetargeting)
	if source.Auth != nil {
		auth := *source.Auth
		auth.AuthMechanismProperties = cloneMap(source.Auth.AuthMechanismProperties)
		result.Auth = &auth
	}
	if source.DriverInfo != nil {
		driverInfo := *source.DriverInfo
		result.DriverInfo = &driverInfo
	}
	if source.BSONOptions != nil {
		bsonOptions := *source.BSONOptions
		result.BSONOptions = &bsonOptions
	}
	return &result
}

func clonePointer[T ~bool | ~int | ~uint | ~uint64 | ~string | time.Duration](source *T) *T {
	if source == nil {
		return nil
	}
	value := *source
	return &value
}

func cloneMap[K comparable, V any](source map[K]V) map[K]V {
	if source == nil {
		return nil
	}
	result := make(map[K]V, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

// withRuntimeFactoryForTest 只为包内测试替换实际 Driver Runtime，不构成公开扩展点。
func withRuntimeFactoryForTest(factory runtimeFactory) Option {
	return optionFunc(func(options *moduleOptions) error {
		if factory == nil {
			return invalidConfig("mongodbmodule Runtime Factory 不能为空")
		}
		options.factory = factory
		return nil
	})
}
