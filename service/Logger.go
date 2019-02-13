package service

const (
	LEVER_UNKNOW = 0
	LEVER_DEBUG  = 1
	LEVER_INFO   = 2
	LEVER_WARN   = 3
	LEVER_ERROR  = 4
	LEVER_FATAL  = 5
	LEVEL_MAX    = 6
)

type ILogger interface {
	Printf(level uint, format string, v ...interface{})
	Print(level uint, v ...interface{})
}
