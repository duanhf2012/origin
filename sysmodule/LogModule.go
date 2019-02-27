package sysmodule

import (
	"fmt"
	"log"
	"os"
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
}

type LogModule struct {
	service.BaseModule
	currentDay  int
	logfilename string
	logger      [LEVEL_MAX]*log.Logger
	logFile     *os.File
	openLevel   uint
	locker      sync.Mutex
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

	slf.CheckAndGenFile()
	slf.GetLoggerByLevel(level).Output(3, fmt.Sprintf(format, v...))
}

func (slf *LogModule) Print(level uint, v ...interface{}) {
	if level < slf.openLevel {
		return
	}

	slf.CheckAndGenFile()
	slf.GetLoggerByLevel(level).Output(3, fmt.Sprint(v...))
}
