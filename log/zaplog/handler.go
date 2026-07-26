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

// handler 把 Origin Record 同步写入一个或多个 Zap Core。
type handler struct {
	// cores 分别代表 stdout、stderr 和文件输出；Write 按级别选择。
	cores []zapcore.Core
	// file 由 handler 独占，用于停止滚动维护协程。
	file *rotate.Writer

	// closeOnce 和 closeErr 固化首次资源释放结果。
	closeOnce sync.Once
	closeErr  error
}

// New 使用默认 Zap Handler 创建日志 Runtime。
func New(config originlog.Config, supplied ...Option) (*originlog.Runtime, error) {
	// 先完整构造同步 Handler，失败时没有 Runtime 协程需要回收。
	logHandler, err := NewHandler(config, supplied...)
	if err != nil {
		return nil, err
	}
	// Handler 成功后交给 Runtime 接管串行写入和生命周期。
	runtime, err := originlog.NewRuntime(config, logHandler)
	if err != nil {
		// Runtime 未接管时由当前函数立即释放 Handler 已创建资源。
		_ = logHandler.Close()
		return nil, err
	}
	// 成功后 Runtime 成为 Handler 的唯一生命周期所有者。
	return runtime, nil
}

// NewHandler 创建不含第二套事件队列的同步 Zap Handler。
func NewHandler(config originlog.Config, supplied ...Option) (originlog.Handler, error) {
	// 默认 Zap Handler 依赖完整输出配置，必须在创建文件前校验。
	if err := config.Validate(); err != nil {
		return nil, err
	}

	// 为每次构造建立独立选项状态，默认连接真实标准输出。
	settings := options{
		encoders: make(map[string]EncoderFactory),
		stdout:   os.Stdout,
		stderr:   os.Stderr,
	}
	// 按调用顺序应用选项；任一失败时尚未创建外部资源。
	for _, option := range supplied {
		if option == nil {
			return nil, invalidOption("nil option")
		}
		if err := option.apply(&settings); err != nil {
			return nil, err
		}
	}

	// 最多建立 stdout、stderr 和 file 三个 Core。
	cores := make([]zapcore.Core, 0, 3)
	if config.Console.Enabled {
		// 每个 Core 必须拥有独立 Encoder，避免内部缓冲或 Clone 状态共享。
		stdoutEncoder, err := newEncoder(config.Console.Format, settings)
		if err != nil {
			return nil, err
		}
		stderrEncoder, err := newEncoder(config.Console.Format, settings)
		if err != nil {
			return nil, err
		}
		// 控制台最低级别只计算一次，并在两个 Enabler 闭包中只读使用。
		minimum := toZapLevel(config.Console.Level)
		// Debug～Warn 写 stdout；Error 及以上写 stderr，避免一条日志重复。
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

	// 先建立空结果，后续文件 Writer 成功后才提交到字段。
	result := &handler{}
	if config.File.Enabled {
		// 文件可以选择与控制台不同的格式和最低级别。
		fileEncoder, err := newEncoder(config.File.Format, settings)
		if err != nil {
			return nil, err
		}
		// rotate.Writer 同步写文件并独占一个归档维护协程。
		fileWriter, err := rotate.New(rotateConfig(config.File))
		if err != nil {
			return nil, errs.Wrap(errs.CodeLogOutputFailed, err)
		}
		// Handler 从此负责在 Close 中停止并释放 fileWriter。
		result.file = fileWriter
		cores = append(cores, zapcore.NewCore(
			fileEncoder,
			zapcore.AddSync(fileWriter),
			zap.LevelEnablerFunc(func(level zapcore.Level) bool {
				return level >= toZapLevel(config.File.Level)
			}),
		))
	}

	// 最后一次性发布 Core 列表，返回后配置保持只读。
	result.cores = cores
	return result, nil
}

// Enabled 报告任意 Core 是否接收指定 Origin 级别。
func (handler *handler) Enabled(level originlog.Level) bool {
	// 先执行稳定级别映射，再逐 Core 查询各自 Enabler。
	zapLevel := toZapLevel(level)
	for _, core := range handler.cores {
		if core.Enabled(zapLevel) {
			// 找到一个输出端即可提前返回。
			return true
		}
	}
	return false
}

// Write 把一条 Origin Record 和字段同步写到全部匹配的 Zap Core。
func (handler *handler) Write(record originlog.Record, fields []originlog.Field) error {
	// 先转换公共元数据，避免为每个 Core 重复构造 Entry。
	entry := zapcore.Entry{
		Level:   toZapLevel(record.Level),
		Time:    record.Time,
		Message: record.Message,
		Stack:   record.Stack,
	}
	// Caller 为空时保持 Zap 的 Defined=false，避免输出伪造位置。
	if record.Caller.File != "" {
		entry.Caller = zapcore.EntryCaller{
			Defined: true,
			PC:      record.Caller.PC,
			File:    record.Caller.File,
			Line:    record.Caller.Line,
		}
	}

	// 常见日志在栈上容纳 16 个字段，超出时才分配切片。
	var local [16]zapcore.Field
	converted := local[:0]
	if len(fields) > len(local) {
		converted = make([]zapcore.Field, 0, len(fields))
	}
	// 字段按 Origin 顺序转换，保留稳定字段在动态字段之前的契约。
	for _, field := range fields {
		converted = append(converted, toZapField(field))
	}
	// 逐个写入接收该级别的 Core，并汇总所有输出错误。
	var result error
	for _, core := range handler.cores {
		if core.Enabled(entry.Level) {
			result = errors.Join(result, core.Write(entry, converted))
		}
	}
	return result
}

// Sync 刷新全部 Zap Core，并保留每个输出端的错误。
func (handler *handler) Sync() error {
	// Runtime 保证 Sync 与 Write 串行，因此无需额外锁。
	var result error
	for _, core := range handler.cores {
		result = errors.Join(result, core.Sync())
	}
	return result
}

// Close 刷新并关闭 Handler 独占的文件 Writer，重复调用安全。
func (handler *handler) Close() error {
	// 只有首次调用执行实际释放，后续返回固化结果。
	handler.closeOnce.Do(func() {
		// 先刷新所有 Core，再停止文件滚动和维护协程。
		syncErr := handler.Sync()
		var fileErr error
		if handler.file != nil {
			fileErr = handler.file.Close()
		}
		// 两个阶段都要执行并合并错误，不能因刷新失败跳过资源关闭。
		handler.closeErr = errors.Join(syncErr, fileErr)
	})
	return handler.closeErr
}

// toZapField 把无 interface{} 装箱的 Origin Field 映射为 Zap Field。
func toZapField(field originlog.Field) zapcore.Field {
	// Key 对全部合法 Kind 通用，先读取一次。
	key := field.Key()
	// Kind 决定使用哪个类型安全访问器和 Zap 构造函数。
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
		// 无效或未来未知 Kind 使用 Skip，避免错误解释底层存储。
		return zap.Skip()
	}
}

// toZapLevel 把 Origin 稳定级别映射到 Zap 内部级别。
func toZapLevel(level originlog.Level) zapcore.Level {
	// 显式 switch 隔离第三方枚举数值，不能依赖强制类型转换。
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
		// 未知值映射到 Invalid，由 Core Enabler 拒绝。
		return zapcore.InvalidLevel
	}
}

// rotateConfig 把公开文件配置转换为内部 Writer 使用的字节和时长单位。
func rotateConfig(config originlog.FileConfig) rotate.Config {
	// 所有乘法范围已由 Config.Validate 检查，此处只做确定转换。
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

// writerSyncer 为控制台 io.Writer 补齐 Zap 所需的 Sync 方法。
type writerSyncer struct {
	io.Writer
}

// Sync 对普通控制台 Writer 无可执行刷新操作。
func (writer writerSyncer) Sync() error {
	// writer 字段仅用于满足接口，返回 nil 保持 stdout/stderr 可替换。
	return nil
}

// 编译期确认 handler 完整实现公开 Handler 边界。
var _ originlog.Handler = (*handler)(nil)
