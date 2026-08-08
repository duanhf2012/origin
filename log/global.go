package log

import "sync/atomic"

// defaultLoggerState 是进程默认日志入口的一次不可变发布值。
//
// Logger 本身包含切片，不能直接放入 atomic.Value 后再与不同具体类型的零值混用；用指针
// 发布既保持读取无锁，也让 Runtime 关闭时可以按所有者比较并清理。
type defaultLoggerState struct {
	logger Logger
	owner  *Runtime
}

// processDefault 是经过最终确认的进程便捷外观例外：它不拥有 Runtime、Handler 或队列，
// 只原子引用某个显式 Application Runtime。多 Application 并行场景不得依赖该默认归属。
var processDefault atomic.Pointer[defaultLoggerState]

// Default 返回当前进程默认 Logger；尚未安装或所有者已关闭时返回 Nop Logger。
func Default() Logger {
	state := processDefault.Load()
	if state == nil {
		return NewNop()
	}
	return state.logger
}

// SetDefault 原子替换进程默认 Logger。
//
// 传入 Nop Logger 会清空默认入口。Application 会在日志资源就绪后安装自己的根 Logger；
// 对应 Runtime 关闭时只清理仍由自己拥有的值，不会误删后来安装的默认 Logger。
func SetDefault(logger Logger) {
	if logger.runtime == nil {
		processDefault.Store(nil)
		return
	}
	processDefault.Store(&defaultLoggerState{
		logger: logger,
		owner:  logger.runtime,
	})
}

// clearDefault 只清理指定 Runtime 当前仍拥有的默认值。
func clearDefault(owner *Runtime) {
	if owner == nil {
		return
	}
	for {
		current := processDefault.Load()
		if current == nil || current.owner != owner {
			return
		}
		if processDefault.CompareAndSwap(current, nil) {
			return
		}
	}
}

// Enabled 报告当前默认 Logger 是否至少有一个输出端接收指定级别。
func Enabled(level Level) bool {
	return Default().Enabled(level)
}

// Debug 通过当前默认 Logger 记录 Debug 日志。
func Debug(message string, fields ...Field) {
	Default().WithCallerSkip(1).Debug(message, fields...)
}

// Info 通过当前默认 Logger 记录 Info 日志。
func Info(message string, fields ...Field) {
	Default().WithCallerSkip(1).Info(message, fields...)
}

// Warn 通过当前默认 Logger 记录 Warn 日志。
func Warn(message string, fields ...Field) {
	Default().WithCallerSkip(1).Warn(message, fields...)
}

// Error 通过当前默认 Logger 记录不带完整堆栈的 Error 日志。
func Error(message string, fields ...Field) {
	Default().WithCallerSkip(1).Error(message, fields...)
}

// ErrorStack 通过当前默认 Logger 记录带完整堆栈的 Error 日志。
func ErrorStack(message string, fields ...Field) {
	Default().WithCallerSkip(1).ErrorStack(message, fields...)
}
