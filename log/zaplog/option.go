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

type optionFunc func(*options) error

func (function optionFunc) apply(options *options) error {
	return function(options)
}

type options struct {
	encoders map[string]EncoderFactory
	stdout   io.Writer
	stderr   io.Writer
}

// WithEncoder 注册仅属于本次 Handler 的自定义 Encoder。
func WithEncoder(name string, factory EncoderFactory) Option {
	return optionFunc(func(options *options) error {
		if name == "" {
			return invalidOption("encoder name is empty")
		}
		if name == "json" || name == "text" {
			return invalidOption("encoder name is reserved")
		}
		if factory == nil {
			return invalidOption("encoder factory is nil")
		}
		if _, exists := options.encoders[name]; exists {
			return invalidOption("encoder name is duplicated")
		}
		options.encoders[name] = factory
		return nil
	})
}

func withConsoleWriters(stdout, stderr io.Writer) Option {
	return optionFunc(func(options *options) error {
		if stdout == nil || stderr == nil {
			return invalidOption("console writer is nil")
		}
		options.stdout = stdout
		options.stderr = stderr
		return nil
	})
}

func invalidOption(message string) error {
	return errs.Wrap(errs.CodeInvalidConfig, fmt.Errorf("%s", message))
}
