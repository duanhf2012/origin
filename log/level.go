// Package log 提供 Origin 组件共享的结构化日志 API。
package log

import "strings"

// Level 是日志严重级别。
type Level uint8

const (
	LevelInvalid Level = iota
	DebugLevel
	InfoLevel
	WarnLevel
	ErrorLevel
)

// String 返回稳定的小写级别名称。
func (level Level) String() string {
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
		return "invalid"
	}
}

// ParseLevel 解析不区分大小写的日志级别。
func ParseLevel(value string) (Level, bool) {
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
		return LevelInvalid, false
	}
}

func (level Level) valid() bool {
	return level >= DebugLevel && level <= ErrorLevel
}
