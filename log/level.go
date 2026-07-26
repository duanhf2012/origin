// Package log 提供 Origin 组件共享的结构化日志 API。
package log

import "strings"

// Level 是日志严重级别。
type Level uint8

const (
	// LevelInvalid 是零值占位，不允许写入。
	LevelInvalid Level = iota
	// DebugLevel 用于开发和低层诊断。
	DebugLevel
	// InfoLevel 用于正常运行信息。
	InfoLevel
	// WarnLevel 用于可恢复异常或风险提示。
	WarnLevel
	// ErrorLevel 用于已经发生的错误。
	ErrorLevel
)

// String 返回稳定的小写级别名称。
func (level Level) String() string {
	// 显式映射保证日志协议文本不会随第三方库变化。
	switch level {
	case DebugLevel:
		return "debug"
	case InfoLevel:
		return "info"
	case WarnLevel:
		return "warn"
	case ErrorLevel:
		return "error"
	default:
		// 未知扩展值统一显示 invalid，避免伪装成合法级别。
		return "invalid"
	}
}

// ParseLevel 解析不区分大小写的日志级别。
func ParseLevel(value string) (Level, bool) {
	// 配置和命令行输入允许大小写差异，输出仍固定为小写。
	switch strings.ToLower(value) {
	case "debug":
		return DebugLevel, true
	case "info":
		return InfoLevel, true
	case "warn":
		return WarnLevel, true
	case "error":
		return ErrorLevel, true
	default:
		// 返回 Invalid 和 false，让调用方决定错误上下文。
		return LevelInvalid, false
	}
}

// valid 报告 Level 是否属于当前公开的连续合法范围。
func (level Level) valid() bool {
	// 合法值连续排列，范围判断比重复 switch 更直接。
	return level >= DebugLevel && level <= ErrorLevel
}
