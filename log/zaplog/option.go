// Package zaplog 提供基于 Zap 的 Origin 默认日志 Handler。
package zaplog

import (
	"fmt"
	"io"

	"github.com/duanhf2012/origin/v3/errs"
	"go.uber.org/zap/zapcore"
)

// EncoderFactory 为一个输出端创建独立 Zap Encoder。
type EncoderFactory func(zapcore.EncoderConfig) (zapcore.Encoder, error)

// Option 配置单次 Zap Handler 构造，不修改全局状态。
type Option interface {
	apply(*options) error
}

// optionFunc 让内部闭包实现 Option，同时保持公开接口最小。
type optionFunc func(*options) error

// apply 把选项修改委托给闭包。
func (function optionFunc) apply(options *options) error {
	// options 由单次 NewHandler 构造独占，不存在跨实例共享状态。
	return function(options)
}

// options 保存一次 Handler 构造过程使用的扩展点和可测试输出端。
type options struct {
	// encoders 只属于当前实例，键是配置中使用的 Format 名称。
	encoders map[string]EncoderFactory
	// stdout 和 stderr 默认指向 os.Stdout/os.Stderr，测试可以内部替换。
	stdout io.Writer
	stderr io.Writer
}

// WithEncoder 注册仅属于本次 Handler 的自定义 Encoder。
func WithEncoder(name string, factory EncoderFactory) Option {
	// 返回延迟应用的闭包，使全部选项按调用顺序在构造阶段校验。
	return optionFunc(func(options *options) error {
		// 名称必须可作为配置 Format 使用。
		if name == "" {
			return invalidOption("encoder name is empty")
		}
		// 内置格式语义固定，禁止自定义实现悄悄覆盖。
		if name == "json" || name == "text" {
			return invalidOption("encoder name is reserved")
		}
		// nil 工厂无法产生 Encoder，必须在启动阶段失败。
		if factory == nil {
			return invalidOption("encoder factory is nil")
		}
		// 同一构造调用内的重复名称属于配置冲突。
		if _, exists := options.encoders[name]; exists {
			return invalidOption("encoder name is duplicated")
		}
		// 校验完成后登记到当前实例 Map，不修改包级状态。
		options.encoders[name] = factory
		return nil
	})
}

// withConsoleWriters 是测试专用内部选项，用于替换进程标准输出。
func withConsoleWriters(stdout, stderr io.Writer) Option {
	return optionFunc(func(options *options) error {
		// 两个输出端都必须存在，避免 NewHandler 成功后写入时 panic。
		if stdout == nil || stderr == nil {
			return invalidOption("console writer is nil")
		}
		// 写入当前构造上下文，不影响其他 Handler。
		options.stdout = stdout
		options.stderr = stderr
		return nil
	})
}

// invalidOption 把 Zap 适配器选项错误统一映射为配置错误。
func invalidOption(message string) error {
	// 使用 cause 包装保留稳定错误码和本地错误文本。
	return errs.Wrap(errs.CodeInvalidConfig, fmt.Errorf("%s", message))
}
