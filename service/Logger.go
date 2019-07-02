package service

import (
	"fmt"
)

const (
	LEVER_UNKNOW = 0
	LEVER_DEBUG  = 1
	LEVER_INFO   = 2
	LEVER_WARN   = 3
	LEVER_ERROR  = 4
	LEVER_FATAL  = 5
	LEVEL_MAX    = 6
)

var defaultLogger = &LoggerFmt{}

type ILogger interface {
	Printf(level uint, format string, v ...interface{})
	Print(level uint, v ...interface{})
	SetLogLevel(level uint)
}

type LoggerFmt struct {
}

func (slf *LoggerFmt) Printf(level uint, format string, v ...interface{}) {
	fmt.Printf(format, v...)
	fmt.Println("")
}
func (slf *LoggerFmt) Print(level uint, v ...interface{}) {
	fmt.Println(v...)
}
func (slf *LoggerFmt) SetLogLevel(level uint) {
	//do nothing
}
