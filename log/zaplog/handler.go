package zaplog

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"sync"
	"time"

	"github.com/duanhf2012/origin/v3/errs"
	originlog "github.com/duanhf2012/origin/v3/log"
	"github.com/duanhf2012/origin/v3/log/internal/rotate"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

type handler struct {
	cores []zapcore.Core
	file  *rotate.Writer

	closeOnce sync.Once
	closeErr  error
}

// New 使用默认 Zap Handler 创建日志 Runtime。
func New(config originlog.Config, supplied ...Option) (*originlog.Runtime, error) {
	logHandler, err := NewHandler(config, supplied...)
	if err != nil {
		return nil, err
	}
	runtime, err := originlog.NewRuntime(config, logHandler)
	if err != nil {
		_ = logHandler.Close()
		return nil, err
	}
	return runtime, nil
}

// NewHandler 创建不含第二套事件队列的同步 Zap Handler。
func NewHandler(config originlog.Config, supplied ...Option) (originlog.Handler, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}

	settings := options{
		encoders: make(map[string]EncoderFactory),
		stdout:   os.Stdout,
		stderr:   os.Stderr,
	}
	for _, option := range supplied {
		if option == nil {
			return nil, invalidOption("nil option")
		}
		if err := option.apply(&settings); err != nil {
			return nil, err
		}
	}

	cores := make([]zapcore.Core, 0, 3)
	if config.Console.Enabled {
		stdoutEncoder, err := newEncoder(config.Console.Format, settings)
		if err != nil {
			return nil, err
		}
		stderrEncoder, err := newEncoder(config.Console.Format, settings)
		if err != nil {
			return nil, err
		}
		minimum := toZapLevel(config.Console.Level)
		cores = append(cores,
			zapcore.NewCore(
				stdoutEncoder,
				writerSyncer{Writer: settings.stdout},
				zap.LevelEnablerFunc(func(level zapcore.Level) bool {
					return level >= minimum && level < zapcore.ErrorLevel
				}),
			),
			zapcore.NewCore(
				stderrEncoder,
				writerSyncer{Writer: settings.stderr},
				zap.LevelEnablerFunc(func(level zapcore.Level) bool {
					return level >= minimum && level >= zapcore.ErrorLevel
				}),
			),
		)
	}

	result := &handler{}
	if config.File.Enabled {
		fileEncoder, err := newEncoder(config.File.Format, settings)
		if err != nil {
			return nil, err
		}
		fileWriter, err := rotate.New(rotateConfig(config.File))
		if err != nil {
			return nil, errs.Wrap(errs.CodeLogOutputFailed, err)
		}
		result.file = fileWriter
		cores = append(cores, zapcore.NewCore(
			fileEncoder,
			zapcore.AddSync(fileWriter),
			zap.LevelEnablerFunc(func(level zapcore.Level) bool {
				return level >= toZapLevel(config.File.Level)
			}),
		))
	}

	result.cores = cores
	return result, nil
}

func (handler *handler) Enabled(level originlog.Level) bool {
	zapLevel := toZapLevel(level)
	for _, core := range handler.cores {
		if core.Enabled(zapLevel) {
			return true
		}
	}
	return false
}

func (handler *handler) Write(record originlog.Record, fields []originlog.Field) error {
	entry := zapcore.Entry{
		Level:   toZapLevel(record.Level),
		Time:    record.Time,
		Message: record.Message,
		Stack:   record.Stack,
	}
	if record.Caller.File != "" {
		entry.Caller = zapcore.EntryCaller{
			Defined: true,
			PC:      record.Caller.PC,
			File:    record.Caller.File,
			Line:    record.Caller.Line,
		}
	}

	var local [16]zapcore.Field
	converted := local[:0]
	if len(fields) > len(local) {
		converted = make([]zapcore.Field, 0, len(fields))
	}
	for _, field := range fields {
		converted = append(converted, toZapField(field))
	}
	var result error
	for _, core := range handler.cores {
		if core.Enabled(entry.Level) {
			result = errors.Join(result, core.Write(entry, converted))
		}
	}
	return result
}

func (handler *handler) Sync() error {
	var result error
	for _, core := range handler.cores {
		result = errors.Join(result, core.Sync())
	}
	return result
}

func (handler *handler) Close() error {
	handler.closeOnce.Do(func() {
		syncErr := handler.Sync()
		var fileErr error
		if handler.file != nil {
			fileErr = handler.file.Close()
		}
		handler.closeErr = errors.Join(syncErr, fileErr)
	})
	return handler.closeErr
}

func toZapField(field originlog.Field) zapcore.Field {
	key := field.Key()
	switch field.Kind() {
	case originlog.StringField:
		return zap.String(key, field.StringValue())
	case originlog.BoolField:
		return zap.Bool(key, field.BoolValue())
	case originlog.IntField:
		return zap.Int64(key, field.Int64Value())
	case originlog.Int32Field:
		return zap.Int32(key, int32(field.Int64Value()))
	case originlog.Int64Field:
		return zap.Int64(key, field.Int64Value())
	case originlog.UintField:
		return zap.Uint64(key, field.Uint64Value())
	case originlog.Uint32Field:
		return zap.Uint32(key, uint32(field.Uint64Value()))
	case originlog.Uint64Field:
		return zap.Uint64(key, field.Uint64Value())
	case originlog.Float32Field:
		return zap.Float32(key, field.Float32Value())
	case originlog.Float64Field:
		return zap.Float64(key, field.Float64Value())
	case originlog.DurationField:
		return zap.Duration(key, field.DurationValue())
	case originlog.TimeField:
		return zap.Time(key, field.TimeValue())
	case originlog.BytesField:
		return zap.Binary(key, field.BytesValue())
	case originlog.ErrorField:
		return zap.String(key, field.StringValue())
	case originlog.AnyField:
		return zap.Reflect(key, json.RawMessage(field.BytesValue()))
	default:
		return zap.Skip()
	}
}

func toZapLevel(level originlog.Level) zapcore.Level {
	switch level {
	case originlog.DebugLevel:
		return zapcore.DebugLevel
	case originlog.InfoLevel:
		return zapcore.InfoLevel
	case originlog.WarnLevel:
		return zapcore.WarnLevel
	case originlog.ErrorLevel:
		return zapcore.ErrorLevel
	default:
		return zapcore.InvalidLevel
	}
}

func rotateConfig(config originlog.FileConfig) rotate.Config {
	return rotate.Config{
		Path:         config.Path,
		MaxSizeBytes: config.Rotation.MaxSizeMB * 1024 * 1024,
		ByDate:       config.Rotation.ByDate,
		UTC:          config.Rotation.Timezone == originlog.UTCTime,
		MaxAge:       time.Duration(config.Retention.MaxAgeDays) * 24 * time.Hour,
		MaxFiles:     config.Retention.MaxFiles,
		Compress:     config.Retention.Compress,
	}
}

type writerSyncer struct {
	io.Writer
}

func (writer writerSyncer) Sync() error {
	return nil
}

var _ originlog.Handler = (*handler)(nil)
