package zaplog

import (
	"fmt"

	originlog "github.com/duanhf2012/origin/v3/log"
	"go.uber.org/zap/zapcore"
)

// newEncoder 为单个输出端创建独立 Encoder，禁止多个 Core 共享有状态编码器。
func newEncoder(format originlog.Format, options options) (zapcore.Encoder, error) {
	// 先生成 Origin 统一字段名和编码行为，再选择具体格式实现。
	config := encoderConfig(format)
	switch format {
	case originlog.JSONFormat:
		// JSON 用于日志平台采集，级别保持小写。
		return zapcore.NewJSONEncoder(config), nil
	case originlog.TextFormat:
		// Console Encoder 用于人工阅读，级别使用大写。
		return zapcore.NewConsoleEncoder(config), nil
	default:
		// 非内置名称必须由本次构造显式注册。
		factory, exists := options.encoders[string(format)]
		if !exists {
			return nil, invalidOption(fmt.Sprintf("unknown encoder %q", format))
		}
		// 工厂错误在启动阶段转换为配置错误，不能延迟到首次写日志。
		encoder, err := factory(config)
		if err != nil {
			return nil, invalidOption(fmt.Sprintf("create encoder %q: %v", format, err))
		}
		if encoder == nil {
			return nil, invalidOption(fmt.Sprintf("encoder %q returned nil", format))
		}
		// 返回当前输出端独占的 Encoder 实例。
		return encoder, nil
	}
}

// encoderConfig 建立 Origin 所有 Zap 输出共享的字段契约。
func encoderConfig(format originlog.Format) zapcore.EncoderConfig {
	// 显式指定字段名和编码器，避免依赖 Zap 的环境默认值。
	config := zapcore.EncoderConfig{
		MessageKey:     "msg",
		LevelKey:       "level",
		TimeKey:        "time",
		CallerKey:      "caller",
		StacktraceKey:  "stack",
		LineEnding:     zapcore.DefaultLineEnding,
		EncodeTime:     zapcore.ISO8601TimeEncoder,
		EncodeDuration: zapcore.StringDurationEncoder,
		EncodeCaller:   zapcore.ShortCallerEncoder,
	}
	// 文本强调可读性使用大写级别，JSON 使用常见的小写机器格式。
	if format == originlog.TextFormat {
		config.EncodeLevel = zapcore.CapitalLevelEncoder
	} else {
		config.EncodeLevel = zapcore.LowercaseLevelEncoder
	}
	// 每次返回值副本，调用方可以安全交给不同 Encoder。
	return config
}
