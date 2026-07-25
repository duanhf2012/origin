package log

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const baseCallerSkip = 5

// Logger 是轻量、可复制的结构化日志入口。
type Logger struct {
	runtime    *Runtime
	fields     []Field
	callerSkip int
}

// NewNop 返回不创建协程且不产生输出的 Logger。
func NewNop() Logger {
	return Logger{}
}

// Enabled 报告至少一个输出端是否接收指定级别。
func (logger Logger) Enabled(level Level) bool {
	return logger.runtime != nil && logger.runtime.enabled(level)
}

// Log 记录运行时确定级别的日志。
func (logger Logger) Log(level Level, message string, fields ...Field) {
	logger.write(level, message, false, fields)
}

// Debug 记录 Debug 日志。
func (logger Logger) Debug(message string, fields ...Field) {
	logger.write(DebugLevel, message, false, fields)
}

// Info 记录 Info 日志。
func (logger Logger) Info(message string, fields ...Field) {
	logger.write(InfoLevel, message, false, fields)
}

// Warn 记录 Warn 日志。
func (logger Logger) Warn(message string, fields ...Field) {
	logger.write(WarnLevel, message, false, fields)
}

// Error 记录不带完整堆栈的 Error 日志。
func (logger Logger) Error(message string, fields ...Field) {
	logger.write(ErrorLevel, message, false, fields)
}

// ErrorStack 记录带调用堆栈并在一秒内等待写出的 Error 日志。
func (logger Logger) ErrorStack(message string, fields ...Field) {
	logger.write(ErrorLevel, message, true, fields)
}

// With 返回带有固定字段的不可变派生 Logger。
func (logger Logger) With(fields ...Field) Logger {
	if logger.runtime == nil || len(fields) == 0 {
		return logger
	}

	combined := make([]Field, 0, len(logger.fields)+len(fields))
	combined = append(combined, logger.fields...)
	combined = appendValidFields(combined, fields)
	logger.fields = combined
	return logger
}

// WithCallerSkip 返回额外跳过调用栈层数的不可变派生 Logger。
func (logger Logger) WithCallerSkip(skip int) Logger {
	if skip > 0 {
		logger.callerSkip += skip
	}
	return logger
}

func (logger Logger) write(level Level, message string, stack bool, fields []Field) {
	if logger.runtime == nil {
		return
	}
	logger.runtime.write(logger, level, message, stack, fields)
}

func appendValidFields(target []Field, fields []Field) []Field {
	for _, field := range fields {
		if field.key == "" || field.kind == InvalidField {
			continue
		}
		if reservedField(field.key) {
			continue
		}
		target = append(target, field)
	}
	return target
}

func reservedField(key string) bool {
	switch key {
	case "time", "level", "message", "msg", "caller", "stack":
		return true
	default:
		return false
	}
}

func captureCaller(extraSkip int) Caller {
	pc, file, line, ok := runtime.Caller(baseCallerSkip + extraSkip)
	if !ok {
		return Caller{}
	}
	return Caller{
		PC:   pc,
		File: shortFile(file),
		Line: line,
	}
}

func captureStack(extraSkip int) string {
	pcs := make([]uintptr, 64)
	count := runtime.Callers(baseCallerSkip+1+extraSkip, pcs)
	if count == 0 {
		return ""
	}

	var builder strings.Builder
	frames := runtime.CallersFrames(pcs[:count])
	for {
		frame, more := frames.Next()
		fmt.Fprintf(&builder, "%s\n\t%s:%d\n", frame.Function, shortFile(frame.File), frame.Line)
		if !more {
			break
		}
	}
	return builder.String()
}

func shortFile(file string) string {
	file = filepath.ToSlash(file)
	last := strings.LastIndexByte(file, '/')
	if last < 0 {
		return file
	}
	previous := strings.LastIndexByte(file[:last], '/')
	if previous < 0 {
		return file[last+1:]
	}
	return file[previous+1:]
}

func newRecord(level Level, message string, callerSkip int, stack bool) Record {
	record := Record{
		Time:    time.Now(),
		Level:   level,
		Message: message,
		Caller:  captureCaller(callerSkip),
	}
	if stack {
		record.Stack = captureStack(callerSkip)
	}
	return record
}
