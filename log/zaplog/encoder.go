package zaplog

import (
	"fmt"

	originlog "github.com/duanhf2012/origin/v3/log"
	"go.uber.org/zap/zapcore"
)

func newEncoder(format originlog.Format, options options) (zapcore.Encoder, error) {
	config := encoderConfig(format)
	switch format {
	case originlog.JSONFormat:
		return zapcore.NewJSONEncoder(config), nil
	case originlog.TextFormat:
		return zapcore.NewConsoleEncoder(config), nil
	default:
		factory, exists := options.encoders[string(format)]
		if !exists {
			return nil, invalidOption(fmt.Sprintf("unknown encoder %q", format))
		}
		encoder, err := factory(config)
		if err != nil {
			return nil, invalidOption(fmt.Sprintf("create encoder %q: %v", format, err))
		}
		if encoder == nil {
			return nil, invalidOption(fmt.Sprintf("encoder %q returned nil", format))
		}
		return encoder, nil
	}
}

func encoderConfig(format originlog.Format) zapcore.EncoderConfig {
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
	if format == originlog.TextFormat {
		config.EncodeLevel = zapcore.CapitalLevelEncoder
	} else {
		config.EncodeLevel = zapcore.LowercaseLevelEncoder
	}
	return config
}
