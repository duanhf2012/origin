package log

import "time"

// Caller 是日志原始调用位置。
type Caller struct {
	PC   uintptr
	File string
	Line int
}

// Record 是一条日志的公共元数据。
type Record struct {
	Time    time.Time
	Level   Level
	Message string
	Caller  Caller
	Stack   string
}

// Handler 是底层日志实现的最小替换边界。
type Handler interface {
	Enabled(level Level) bool
	Write(record Record, fields []Field) error
	Sync() error
	Close() error
}
