package zaplog

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/duanhf2012/origin/v3/errs"
	originlog "github.com/duanhf2012/origin/v3/log"
	"github.com/duanhf2012/origin/v3/log/internal/rotate"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// outputState 保存一个输出端的启动配置和可并发更新的当前状态。
type outputState struct {
	available   bool
	configLevel originlog.Level
	enabled     atomic.Bool
	level       atomic.Uint32
}

// consoleOutput 把 stdout/stderr 视为同一个可控 Console，Error 只写 stderr。
type consoleOutput struct {
	state  outputState
	format originlog.Format
	fields originlog.ContextFieldsConfig
	stdout io.Writer
	stderr io.Writer
	// 非 text 格式继续复用 Zap Encoder；两个 Writer 各自拥有独立 Encoder 状态。
	stdoutCore zapcore.Core
	stderrCore zapcore.Core
}

// fileOutput 保存活动文件及其单个 Encoder；rotate.Writer 是唯一文件资源所有者。
type fileOutput struct {
	state  outputState
	format originlog.Format
	fields originlog.ContextFieldsConfig
	writer *rotate.Writer
	core   zapcore.Core
}

// handler 把 Origin Record 同步写入独立可控的 Console 和 File 输出。
type handler struct {
	console consoleOutput
	file    fileOutput

	// closed 让控制冷路径和 Enabled 在资源释放后立即返回稳定状态。
	closed atomic.Bool
	// closeOnce 和 closeErr 固化首次资源释放结果。
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

	result := &handler{}
	result.console = consoleOutput{
		format: config.Console.Format,
		fields: config.Console.ContextFields,
		stdout: settings.stdout,
		stderr: settings.stderr,
	}
	result.console.state.initialize(config.Console.Enabled, config.Console.Level)
	if config.Console.Enabled && config.Console.Format != originlog.TextFormat {
		stdoutEncoder, err := newEncoder(config.Console.Format, settings)
		if err != nil {
			return nil, err
		}
		stderrEncoder, err := newEncoder(config.Console.Format, settings)
		if err != nil {
			return nil, err
		}
		result.console.stdoutCore = newCore(stdoutEncoder, settings.stdout)
		result.console.stderrCore = newCore(stderrEncoder, settings.stderr)
	}

	result.file = fileOutput{
		format: config.File.Format,
		fields: config.File.ContextFields,
	}
	result.file.state.initialize(config.File.Enabled, config.File.Level)
	if config.File.Enabled {
		fileWriter, err := rotate.New(rotateConfig(config.File))
		if err != nil {
			return nil, errs.Wrap(errs.CodeLogOutputFailed, err)
		}
		result.file.writer = fileWriter
		if config.File.Format != originlog.TextFormat {
			fileEncoder, encoderErr := newEncoder(config.File.Format, settings)
			if encoderErr != nil {
				_ = fileWriter.Close()
				return nil, encoderErr
			}
			result.file.core = newCore(fileEncoder, fileWriter)
		}
	}
	return result, nil
}

// initialize 在最终字段地址上建立输出状态，禁止复制已经使用过的 atomic.noCopy 值。
func (state *outputState) initialize(available bool, level originlog.Level) {
	state.available = available
	state.configLevel = level
	state.enabled.Store(available)
	state.level.Store(uint32(level))
}

// newCore 只负责非 text 格式的编码与写入；级别和启停已由 handler 统一判定。
func newCore(encoder zapcore.Encoder, writer io.Writer) zapcore.Core {
	return zapcore.NewCore(
		encoder,
		writerSyncer{Writer: writer},
		zap.LevelEnablerFunc(func(zapcore.Level) bool { return true }),
	)
}

// Enabled 报告当前至少一个可用且已启用输出是否接收指定级别。
func (handler *handler) Enabled(level originlog.Level) bool {
	if handler == nil || handler.closed.Load() || !validLevel(level) {
		return false
	}
	return handler.console.state.accepts(level) || handler.file.state.accepts(level)
}

// Write 把一条 Origin Record 按各输出端当前状态、格式和字段掩码分别写出。
func (handler *handler) Write(record originlog.Record, fields []originlog.Field) error {
	if handler == nil || handler.closed.Load() {
		return errs.ErrLogClosed
	}
	var result error
	if handler.console.state.accepts(record.Level) {
		result = errors.Join(result, handler.console.write(record, fields))
	}
	if handler.file.state.accepts(record.Level) {
		result = errors.Join(result, handler.file.write(record, fields))
	}
	return result
}

// write 按 Error 分流控制台 Writer，并保证同一条日志不会重复。
func (output *consoleOutput) write(record originlog.Record, fields []originlog.Field) error {
	if output.format == originlog.TextFormat {
		writer := output.stdout
		if record.Level == originlog.ErrorLevel {
			writer = output.stderr
		}
		return writeAll(writer, encodeText(record, fields, output.fields))
	}
	core := output.stdoutCore
	if record.Level == originlog.ErrorLevel {
		core = output.stderrCore
	}
	return writeCore(core, record, fields, output.fields)
}

// write 使用活动文件 Writer；text 直接编码，JSON/自定义格式继续复用 Zap Encoder。
func (output *fileOutput) write(record originlog.Record, fields []originlog.Field) error {
	if output.format == originlog.TextFormat {
		return writeAll(output.writer, encodeText(record, fields, output.fields))
	}
	return writeCore(output.core, record, fields, output.fields)
}

// writeCore 在单个输出端应用字段掩码后调用其独占 Zap Core。
func writeCore(
	core zapcore.Core,
	record originlog.Record,
	fields []originlog.Field,
	mask originlog.ContextFieldsConfig,
) error {
	entry := toZapEntry(record)
	var local [16]zapcore.Field
	converted := local[:0]
	if len(fields) > len(local) {
		converted = make([]zapcore.Field, 0, len(fields))
	}
	for _, field := range fields {
		if !fieldVisible(field, mask) {
			continue
		}
		converted = append(converted, toZapField(field))
	}
	return core.Write(entry, converted)
}

// toZapEntry 把公共 Record 转为 JSON 或自定义 Zap Encoder 使用的元数据。
func toZapEntry(record originlog.Record) zapcore.Entry {
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
	return entry
}

// fieldVisible 在编码前应用当前输出端独立的框架归属字段掩码。
func fieldVisible(field originlog.Field, mask originlog.ContextFieldsConfig) bool {
	switch field.Key() {
	case "node_id":
		return mask.NodeID
	case "service_name":
		return mask.ServiceName
	default:
		return true
	}
}

// encodeText 生成单行人工可读格式：TIME LEVEL [SCOPE] CALLER MESSAGE key=value。
func encodeText(
	record originlog.Record,
	fields []originlog.Field,
	mask originlog.ContextFieldsConfig,
) []byte {
	result := make([]byte, 0, 256)
	result = record.Time.In(time.Local).AppendFormat(result, "2006-01-02T15:04:05.000")
	result = append(result, ' ')
	result = append(result, strings.ToUpper(record.Level.String())...)

	nodeID, serviceName := scopeValues(fields, mask)
	if nodeID != "" || serviceName != "" {
		result = append(result, ' ', '[')
		if nodeID != "" {
			result = append(result, nodeID...)
		}
		if nodeID != "" && serviceName != "" {
			result = append(result, '/')
		}
		if serviceName != "" {
			result = append(result, serviceName...)
		}
		result = append(result, ']')
	}
	if record.Caller.File != "" {
		result = append(result, ' ')
		result = append(result, record.Caller.File...)
		result = append(result, ':')
		result = strconv.AppendInt(result, int64(record.Caller.Line), 10)
	}
	result = append(result, ' ')
	result = appendMessage(result, record.Message)
	for _, field := range fields {
		if !fieldVisible(field, mask) || field.Key() == "node_id" || field.Key() == "service_name" {
			continue
		}
		result = append(result, ' ')
		result = appendTextKey(result, field.Key())
		result = append(result, '=')
		result = appendTextValue(result, field)
	}
	if record.Stack != "" {
		result = append(result, " stack="...)
		result = strconv.AppendQuote(result, record.Stack)
	}
	return append(result, '\n')
}

// appendTextKey 在字段名会破坏 key=value 边界时使用 Go 引号保留原始 Key。
func appendTextKey(target []byte, key string) []byte {
	for offset := 0; offset < len(key); {
		current, size := utf8.DecodeRuneInString(key[offset:])
		if (current == utf8.RuneError && size == 1) ||
			unicode.IsSpace(current) || unicode.IsControl(current) || current == '=' ||
			current == '"' || current == '\\' {
			return strconv.AppendQuote(target, key)
		}
		offset += size
	}
	return append(target, key...)
}

// scopeValues 从已经由框架固定在切片前部的字段中提取当前输出可见的作用域。
func scopeValues(
	fields []originlog.Field,
	mask originlog.ContextFieldsConfig,
) (nodeID, serviceName string) {
	for _, field := range fields {
		switch field.Key() {
		case "node_id":
			if mask.NodeID {
				nodeID = field.StringValue()
			}
		case "service_name":
			if mask.ServiceName {
				serviceName = field.StringValue()
			}
		}
	}
	return nodeID, serviceName
}

// appendMessage 保留普通空格，仅在控制字符会破坏单行边界时使用 Go 字符串转义。
func appendMessage(target []byte, message string) []byte {
	for offset := 0; offset < len(message); {
		value, size := utf8.DecodeRuneInString(message[offset:])
		if (value == utf8.RuneError && size == 1) || unicode.IsControl(value) {
			return strconv.AppendQuote(target, message)
		}
		offset += size
	}
	return append(target, message...)
}

// appendTextValue 按 FieldKind 生成不会破坏 key=value 边界的紧凑文本。
func appendTextValue(target []byte, field originlog.Field) []byte {
	switch field.Kind() {
	case originlog.StringField, originlog.ErrorField:
		return appendTextString(target, field.StringValue())
	case originlog.BoolField:
		return strconv.AppendBool(target, field.BoolValue())
	case originlog.IntField, originlog.Int32Field, originlog.Int64Field:
		return strconv.AppendInt(target, field.Int64Value(), 10)
	case originlog.UintField, originlog.Uint32Field, originlog.Uint64Field:
		return strconv.AppendUint(target, field.Uint64Value(), 10)
	case originlog.Float32Field:
		return strconv.AppendFloat(target, float64(field.Float32Value()), 'g', -1, 32)
	case originlog.Float64Field:
		return strconv.AppendFloat(target, field.Float64Value(), 'g', -1, 64)
	case originlog.DurationField:
		return append(target, field.DurationValue().String()...)
	case originlog.TimeField:
		return strconv.AppendQuote(target, field.TimeValue().Format(time.RFC3339Nano))
	case originlog.BytesField:
		return base64.StdEncoding.AppendEncode(target, field.BytesValue())
	case originlog.AnyField:
		return append(target, field.BytesValue()...)
	default:
		return append(target, "null"...)
	}
}

// appendTextString 仅在空白、引号、反斜杠或控制字符存在时增加引号与转义。
func appendTextString(target []byte, value string) []byte {
	if value == "" {
		return append(target, `""`...)
	}
	for offset := 0; offset < len(value); {
		current, size := utf8.DecodeRuneInString(value[offset:])
		if (current == utf8.RuneError && size == 1) ||
			unicode.IsSpace(current) || unicode.IsControl(current) || current == '"' || current == '\\' {
			return strconv.AppendQuote(target, value)
		}
		offset += size
	}
	return append(target, value...)
}

// writeAll 把一条完整日志写到输出端，并把短写转换为标准错误。
func writeAll(writer io.Writer, content []byte) error {
	written, err := writer.Write(content)
	if err != nil {
		return err
	}
	if written != len(content) {
		return io.ErrShortWrite
	}
	return nil
}

// Sync 刷新非 text Zap Core 和文件 Writer，并保留所有输出错误。
func (handler *handler) Sync() error {
	if handler == nil {
		return nil
	}
	var result error
	if handler.console.stdoutCore != nil {
		result = errors.Join(result, handler.console.stdoutCore.Sync())
	}
	if handler.console.stderrCore != nil {
		result = errors.Join(result, handler.console.stderrCore.Sync())
	}
	if handler.file.core != nil {
		result = errors.Join(result, handler.file.core.Sync())
	} else if handler.file.writer != nil {
		result = errors.Join(result, handler.file.writer.Sync())
	}
	return result
}

// Close 停止新写入、刷新并关闭 Handler 独占的文件 Writer，重复调用安全。
func (handler *handler) Close() error {
	if handler == nil {
		return nil
	}
	handler.closeOnce.Do(func() {
		handler.closed.Store(true)
		handler.console.state.enabled.Store(false)
		handler.file.state.enabled.Store(false)
		syncErr := handler.Sync()
		var fileErr error
		if handler.file.writer != nil {
			fileErr = handler.file.writer.Close()
		}
		handler.closeErr = errors.Join(syncErr, fileErr)
	})
	return handler.closeErr
}

// SetConsoleLevel 修改当前控制台最低级别。
func (handler *handler) SetConsoleLevel(level originlog.Level) error {
	return handler.setLevel(&handler.console.state, level)
}

// ResetConsoleLevel 恢复控制台启动配置级别。
func (handler *handler) ResetConsoleLevel() error {
	return handler.resetLevel(&handler.console.state)
}

// SetFileLevel 修改当前文件最低级别。
func (handler *handler) SetFileLevel(level originlog.Level) error {
	return handler.setLevel(&handler.file.state, level)
}

// ResetFileLevel 恢复文件启动配置级别。
func (handler *handler) ResetFileLevel() error {
	return handler.resetLevel(&handler.file.state)
}

// SetConsoleEnabled 暂停或恢复已在启动时建立的控制台输出。
func (handler *handler) SetConsoleEnabled(enabled bool) error {
	return handler.setEnabled(&handler.console.state, enabled)
}

// SetFileEnabled 暂停或恢复已在启动时建立的文件输出。
func (handler *handler) SetFileEnabled(enabled bool) error {
	return handler.setEnabled(&handler.file.state, enabled)
}

// Status 返回 Console/File 同一时刻附近的原子状态快照。
func (handler *handler) Status() originlog.Status {
	if handler == nil {
		return originlog.Status{}
	}
	return originlog.Status{
		Console: handler.console.state.status(),
		File:    handler.file.state.status(),
	}
}

// setLevel 校验 Handler 生命周期、输出可用性和公开 Level 后原子提交新级别。
func (handler *handler) setLevel(state *outputState, level originlog.Level) error {
	if handler == nil || handler.closed.Load() {
		return errs.ErrLogClosed
	}
	if !validLevel(level) {
		return errs.ErrInvalidArgument
	}
	if !state.available {
		return errs.ErrLogOutputUnavailable
	}
	state.level.Store(uint32(level))
	return nil
}

// resetLevel 恢复当前输出启动时的独立配置级别。
func (handler *handler) resetLevel(state *outputState) error {
	if handler == nil || handler.closed.Load() {
		return errs.ErrLogClosed
	}
	if !state.available {
		return errs.ErrLogOutputUnavailable
	}
	state.level.Store(uint32(state.configLevel))
	return nil
}

// setEnabled 只改变逻辑准入；底层 Writer 一直保留到 Application 停止。
func (handler *handler) setEnabled(state *outputState, enabled bool) error {
	if handler == nil || handler.closed.Load() {
		return errs.ErrLogClosed
	}
	if !state.available {
		return errs.ErrLogOutputUnavailable
	}
	state.enabled.Store(enabled)
	return nil
}

// accepts 是日志热路径使用的两次原子读取，不获取互斥锁。
func (state *outputState) accepts(level originlog.Level) bool {
	return state != nil && state.available && state.enabled.Load() &&
		uint32(level) >= state.level.Load()
}

// status 复制配置常量和当前原子值；快照允许来自相邻 CPU 时刻。
func (state *outputState) status() originlog.OutputStatus {
	if state == nil {
		return originlog.OutputStatus{}
	}
	return originlog.OutputStatus{
		Available:   state.available,
		Enabled:     state.available && state.enabled.Load(),
		Level:       originlog.Level(state.level.Load()),
		ConfigLevel: state.configLevel,
	}
}

// validLevel 隔离 log 包的内部校验实现，避免依赖第三方 Zap 枚举连续性。
func validLevel(level originlog.Level) bool {
	switch level {
	case originlog.DebugLevel, originlog.InfoLevel, originlog.WarnLevel, originlog.ErrorLevel:
		return true
	default:
		return false
	}
}

// toZapField 把无 interface{} 装箱的 Origin Field 映射为 Zap Field。
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

// toZapLevel 把 Origin 稳定级别映射到 Zap 内部级别。
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

// rotateConfig 把公开文件配置转换为内部 Writer 使用的字节和时长单位。
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

// writerSyncer 为普通 io.Writer 补齐 Zap Core 所需的 Sync 方法。
type writerSyncer struct {
	io.Writer
}

// Sync 在 Writer 自身支持刷新时委托，否则控制台 Buffer 无需操作。
func (writer writerSyncer) Sync() error {
	if syncer, ok := writer.Writer.(interface{ Sync() error }); ok {
		return syncer.Sync()
	}
	return nil
}

// 编译期确认默认 Handler 同时实现固定写入和可选运行时控制边界。
var (
	_ originlog.Handler    = (*handler)(nil)
	_ originlog.Controller = (*handler)(nil)
)
