package log

import "time"

// Caller 是日志原始调用位置。
type Caller struct {
	// PC 是原始程序计数器，便于自定义 Handler 扩展符号信息。
	PC uintptr
	// File 是缩短后的源码路径。
	File string
	// Line 是源码行号。
	Line int
}

// Record 是一条日志的公共元数据。
type Record struct {
	// Time 在业务调用协程创建记录时捕获。
	Time time.Time
	// Level 和 Message 是日志的核心语义。
	Level   Level
	Message string
	// Caller 始终尝试采集，Stack 只在 ErrorStack 请求时填充。
	Caller Caller
	Stack  string
}

// Handler 是底层日志实现的最小替换边界。
type Handler interface {
	// Enabled 报告至少一个底层输出是否接收该级别；实现必须支持并发调用。
	Enabled(level Level) bool
	// Write 写出一条完整记录；Runtime 保证串行调用且 fields 只在调用期间有效。
	Write(record Record, fields []Field) error
	// Sync 刷新底层缓冲；Runtime 保证与 Write 串行。
	Sync() error
	// Close 释放全部输出资源；Runtime 只调用一次。
	Close() error
}
