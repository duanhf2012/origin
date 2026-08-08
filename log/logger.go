package log

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// baseCallerSkip 跳过 Logger 公共方法、内部转发和 runtime.Callers 自身栈帧。
const baseCallerSkip = 5

// Logger 是轻量、可复制的结构化日志入口。
type Logger struct {
	// runtime 指向唯一拥有队列和 Handler 的实例；nil 表示 Nop Logger。
	runtime *Runtime
	// fields 是派生 Logger 预绑定且独占的稳定字段切片。
	fields []Field
	// callerSkip 记录业务包装层要求额外跳过的栈帧数。
	callerSkip int
}

// NewNop 返回不创建协程且不产生输出的 Logger。
func NewNop() Logger {
	// Logger 的零值天然无输出，因此无需分配 Runtime 或 Handler。
	return Logger{}
}

// Enabled 报告至少一个输出端是否接收指定级别。
func (logger Logger) Enabled(level Level) bool {
	// Nop Logger 直接关闭；其余判断交给 Runtime 统一处理生命周期和级别。
	return logger.runtime != nil && logger.runtime.enabled(level)
}

// Log 记录运行时确定级别的日志。
func (logger Logger) Log(level Level, message string, fields ...Field) {
	// 动态级别仍复用统一写入路径，保持字段过滤和调用者定位一致。
	logger.write(level, message, false, fields)
}

// Debug 记录 Debug 日志。
func (logger Logger) Debug(message string, fields ...Field) {
	// 固定级别方法只负责选择 Level，不复制调度逻辑。
	logger.write(DebugLevel, message, false, fields)
}

// Info 记录 Info 日志。
func (logger Logger) Info(message string, fields ...Field) {
	// 固定级别方法只负责选择 Level，不复制调度逻辑。
	logger.write(InfoLevel, message, false, fields)
}

// Warn 记录 Warn 日志。
func (logger Logger) Warn(message string, fields ...Field) {
	// 固定级别方法只负责选择 Level，不复制调度逻辑。
	logger.write(WarnLevel, message, false, fields)
}

// Error 记录不带完整堆栈的 Error 日志。
func (logger Logger) Error(message string, fields ...Field) {
	// 普通 Error 保持与其他普通日志相同的同步或异步策略。
	logger.write(ErrorLevel, message, false, fields)
}

// ErrorStack 记录带调用堆栈并在一秒内等待写出的 Error 日志。
func (logger Logger) ErrorStack(message string, fields ...Field) {
	// 带堆栈错误走可靠写入路径，避免异步队列满时直接丢失关键诊断。
	logger.write(ErrorLevel, message, true, fields)
}

// With 返回带有固定字段的不可变派生 Logger。
func (logger Logger) With(fields ...Field) Logger {
	// Nop Logger 或没有新字段时直接返回值副本，不产生分配。
	if logger.runtime == nil || len(fields) == 0 {
		return logger
	}

	// 创建新的独占切片，确保派生 Logger 不会修改父 Logger 的稳定字段。
	combined := make([]Field, 0, len(logger.fields)+len(fields))
	combined = append(combined, logger.fields...)
	combined = appendValidFields(combined, fields)
	// Logger 是值类型，只更新返回副本即可保持不可变派生语义。
	logger.fields = combined
	return logger
}

// WithScope 返回由框架装配期绑定 NodeID 和 ServiceName 的不可变派生 Logger。
//
// 普通业务字段中的 app_name、node_id、service_name 会被过滤；本方法是跨包构造 Service
// Runtime 时唯一显式写入归属字段的入口。空值表示当前层级没有对应归属。
func (logger Logger) WithScope(nodeID, serviceName string) Logger {
	if logger.runtime == nil {
		return logger
	}

	// 先写新的唯一作用域，再保留原 Logger 的普通固定字段；已有作用域字段必须被替换，
	// 否则从 Node Logger 派生 Service Logger 会输出重复 Key。
	combined := make([]Field, 0, len(logger.fields)+2)
	if nodeID != "" {
		combined = append(combined, String("node_id", nodeID))
	}
	if serviceName != "" {
		combined = append(combined, String("service_name", serviceName))
	}
	for _, field := range logger.fields {
		if field.key == "app_name" || field.key == "node_id" || field.key == "service_name" {
			continue
		}
		combined = append(combined, field)
	}
	logger.fields = combined
	return logger
}

// WithCallerSkip 返回额外跳过调用栈层数的不可变派生 Logger。
func (logger Logger) WithCallerSkip(skip int) Logger {
	// 非正数不改变定位；正数只修改值副本，不影响原 Logger。
	if skip > 0 {
		logger.callerSkip += skip
	}
	return logger
}

// write 是全部日志外观的单一内部入口。
func (logger Logger) write(level Level, message string, stack bool, fields []Field) {
	// Nop Logger 在最外层快速返回，避免字段处理和调用栈采集。
	if logger.runtime == nil {
		return
	}
	// 生命周期、级别、队列和可靠性策略统一由 Runtime 决定。
	logger.runtime.write(logger, level, message, stack, fields)
}

// appendValidFields 把合法业务字段追加到调用方拥有的目标切片。
func appendValidFields(target []Field, fields []Field) []Field {
	// 按传入顺序过滤，保持稳定字段在动态字段之前的输出顺序。
	for _, field := range fields {
		// 空 Key 和无效 Kind 没有可编码语义，直接忽略。
		if field.key == "" || field.kind == InvalidField {
			continue
		}
		// 框架保留字段由 Record 和 Encoder 生成，禁止业务覆盖。
		if reservedField(field.key) {
			continue
		}
		// Field 是值对象，追加后不再依赖调用方的 Field 切片。
		target = append(target, field)
	}
	return target
}

// reservedField 报告 Key 是否由日志框架统一维护。
func reservedField(key string) bool {
	// 显式 switch 比可变 Map 更简单，也不会引入包级状态。
	switch key {
	case "time", "level", "message", "msg", "caller", "stack",
		"app_name", "node_id", "service_name":
		return true
	default:
		return false
	}
}

// captureCaller 定位真正发起日志调用的业务代码。
func captureCaller(extraSkip int) Caller {
	// 基础跳过层数加上业务包装层数量，得到需要记录的栈帧。
	pc, file, line, ok := runtime.Caller(baseCallerSkip + extraSkip)
	if !ok {
		// 栈不足时返回零值，日志仍然可以正常输出。
		return Caller{}
	}
	// 只保留较短文件路径，兼顾定位能力和日志体积。
	return Caller{
		PC:   pc,
		File: shortFile(file),
		Line: line,
	}
}

// captureStack 构造适合文本和 JSON 字段保存的完整调用堆栈。
func captureStack(extraSkip int) string {
	// 64 个 PC 是异常日志的有界上限，避免极深调用栈造成无限分配。
	pcs := make([]uintptr, 64)
	count := runtime.Callers(baseCallerSkip+1+extraSkip, pcs)
	if count == 0 {
		return ""
	}

	// CallersFrames 正确处理内联帧；按 Go 常见堆栈格式逐帧写入。
	var builder strings.Builder
	frames := runtime.CallersFrames(pcs[:count])
	for {
		frame, more := frames.Next()
		fmt.Fprintf(&builder, "%s\n\t%s:%d\n", frame.Function, shortFile(frame.File), frame.Line)
		if !more {
			break
		}
	}
	// builder 的结果由 Record 独占，随后可安全交给日志协程。
	return builder.String()
}

// shortFile 把绝对路径缩短为最后两级，保留包目录和文件名。
func shortFile(file string) string {
	// 先统一为斜杠，确保 Windows 和 Linux 输出一致。
	file = filepath.ToSlash(file)
	last := strings.LastIndexByte(file, '/')
	if last < 0 {
		return file
	}
	// 只有一级路径时直接返回文件名。
	previous := strings.LastIndexByte(file[:last], '/')
	if previous < 0 {
		return file[last+1:]
	}
	// 常规绝对路径保留“目录/文件”，足以区分多数同名源码文件。
	return file[previous+1:]
}

// newRecord 在调用协程中捕获不可延后获取的时间、调用者和可选堆栈。
func newRecord(level Level, message string, callerSkip int, stack bool) Record {
	// 先构造所有日志都需要的固定元数据。
	record := Record{
		Time:    time.Now(),
		Level:   level,
		Message: message,
		Caller:  captureCaller(callerSkip),
	}
	// 堆栈采集成本较高，仅 ErrorStack 明确请求时执行。
	if stack {
		record.Stack = captureStack(callerSkip)
	}
	// Record 不引用临时栈状态，可以安全投递给日志协程。
	return record
}
