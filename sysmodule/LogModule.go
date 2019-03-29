package sysmodule

import (
	"fmt"
	"log"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/duanhf2012/origin/service"
)

//等级从低到高
const (
	LEVER_UNKNOW = 0
	LEVER_DEBUG  = 1
	LEVER_INFO   = 2
	LEVER_WARN   = 3
	LEVER_ERROR  = 4
	LEVER_FATAL  = 5
	LEVEL_MAX    = 6
)

var LogPrefix = [LEVEL_MAX]string{"[UNKNOW]", "[DEBUG]", "[INFO ]", "[WARN ]", "[ERROR]", "[FATAL]"}

type ILogger interface {
	Printf(level uint, format string, v ...interface{})
	Print(level uint, v ...interface{})
	SetLogLevel(level uint)
}

type FunListenLog func(uint, string)

type LogModule struct {
	service.BaseModule
	currentDay  int
	logfilename string
	logger      [LEVEL_MAX]*log.Logger
	logFile     *os.File
	openLevel   uint
	locker      sync.Mutex
	calldepth   int
	listenFun   FunListenLog
}

func (slf *LogModule) GetCurrentFileName() string {
	return slf.logfilename + "-" + time.Now().Format("2006-01-02") + ".log"
}

func (slf *LogModule) CheckAndGenFile() {

	slf.locker.Lock()
	if time.Now().Day() != slf.currentDay {

		if time.Now().Day() == slf.currentDay {
			slf.locker.Unlock()
			return
		}

		slf.currentDay = time.Now().Day()
		if slf.logFile != nil {
			slf.logFile.Close()
		}

		var err error
		slf.logFile, err = os.OpenFile(slf.GetCurrentFileName(), os.O_RDWR|os.O_CREATE|os.O_APPEND, 0766)
		if err != nil {
			fmt.Printf("create log file %+v error!", slf.GetCurrentFileName())
			slf.locker.Unlock()
			return
		}

		for level := 0; level < LEVEL_MAX; level++ {
			slf.logger[level] = log.New(slf.logFile, LogPrefix[level], log.Lshortfile|log.LstdFlags)
		}

	}
	slf.locker.Unlock()
}

func (slf *LogModule) Init(logfilename string, openLevel uint) {
	slf.currentDay = 0
	slf.logfilename = logfilename
	slf.openLevel = openLevel
	slf.calldepth = 3
}

func (slf *LogModule) SetListenLogFunc(listenFun FunListenLog) {
	slf.listenFun = listenFun
}

func (slf *LogModule) GetLoggerByLevel(level uint) *log.Logger {
	if level >= LEVEL_MAX {
		level = 0
	}
	return slf.logger[level]
}

func (slf *LogModule) Printf(level uint, format string, v ...interface{}) {
	if level < slf.openLevel {
		return
	}

	if slf.openLevel == LEVER_DEBUG || slf.listenFun != nil {
		strlog := fmt.Sprintf(format, v...)
		if slf.openLevel == LEVER_DEBUG {
			fmt.Println(LogPrefix[level], time.Now().Format("2006/1/2 15:04:05"), strlog)
		}

		if slf.listenFun != nil {
			var file string
			var line int
			var ok bool
			_, file, line, ok = runtime.Caller(slf.calldepth - 1)
			if !ok {
				file = "???"
				line = 0
			}
			parts := strings.Split(file, "/")
			if len(parts) > 0 {
				file = parts[len(parts)-1]
			}

			format = LogPrefix[level] + time.Now().Format("2006/1/2 15:04:05") + fmt.Sprintf(" %s:%d:", file, line) + format
			slf.listenFun(level, fmt.Sprintf(format, v...))
		}
	}

	slf.CheckAndGenFile()
	slf.GetLoggerByLevel(level).Output(slf.calldepth, fmt.Sprintf(format, v...))
}

func (slf *LogModule) Print(level uint, v ...interface{}) {
	if level < slf.openLevel {
		return
	}

	if slf.openLevel == LEVER_DEBUG || slf.listenFun != nil {
		strlog := fmt.Sprint(v...)
		if slf.openLevel == LEVER_DEBUG {
			fmt.Println(LogPrefix[level], strlog)
		}

		if slf.listenFun != nil {
			slf.listenFun(level, fmt.Sprint(v...))
		}
	}

	slf.CheckAndGenFile()
	slf.GetLoggerByLevel(level).Output(slf.calldepth, fmt.Sprint(v...))
}

func (slf *LogModule) AppendCallDepth(calldepth int) {
	slf.calldepth += calldepth
}

func (slf *LogModule) SetLogLevel(level uint) {
	slf.openLevel = level
}
