package sysmodule

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/duanhf2012/origin/service"
)

const (
	maxLinesInLog = 100000 //一个日志文件最多只写这么多行
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
	currentDay  int64
	lines       int64
	logfilename string
	logger      [LEVEL_MAX]*log.Logger
	logFile     *os.File
	openLevel   uint
	locker      sync.Mutex
	calldepth   int
	listenFun   FunListenLog
}

func (slf *LogModule) GetCurrentFileName() string {
	now := time.Now()
	fpath := filepath.Join("logs")
	os.MkdirAll(fpath, os.ModePerm)
	fname := slf.logfilename + "-" + now.Format("20060102-150405") + ".log"
	ret := filepath.Join(fpath, fname)
	return ret
}

//检查是否需要切换新的日志文件
func (slf *LogModule) CheckAndGenFile(fileline string) (newFile bool) {
	now := time.Now()
	nowDate := int64(now.Day())

	slf.locker.Lock()

	isNewDay := nowDate != slf.currentDay
	slf.lines++
	if isNewDay || slf.lines > maxLinesInLog {
		// if time.Now().Day() == slf.currentDay {
		// 	slf.locker.Unlock()
		// 	return
		// }
		//fmt.Println("new log file", slf.currentDay, nowDate, isNewDay, slf.lines, maxLinesInLog)

		slf.currentDay = nowDate
		slf.lines = 1
		newFile = true
		if slf.logFile != nil {
			slf.logFile.Close()
		}

		var err error
		slf.logFile, err = os.OpenFile(slf.GetCurrentFileName(), os.O_RDWR|os.O_CREATE|os.O_APPEND, os.ModePerm)
		if err != nil {
			fmt.Printf("create log file %+v error!", slf.GetCurrentFileName())
			slf.locker.Unlock()
			return false
		}

		for level := 0; level < LEVEL_MAX; level++ {
			slf.logger[level] = log.New(slf.logFile, LogPrefix[level], log.Lshortfile|log.LstdFlags)
		}
	}

	slf.locker.Unlock()
	return newFile
}

func (slf *LogModule) Init(logFilePrefixName string, openLevel uint) {
	slf.currentDay = 0
	slf.logfilename = logFilePrefixName
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

	_, file, line, ok := runtime.Caller(slf.calldepth - 1)
	if !ok {
		file = "???"
		line = 0
	}
	fileLine := fmt.Sprintf(" %s:%d: ", file, line)
	slf.CheckAndGenFile(fileLine)

	logContents := fmt.Sprintf(format, v...)
	slf.doPutLog(level, fileLine, logContents)
}

func (slf *LogModule) Print(level uint, v ...interface{}) {
	if level < slf.openLevel {
		return
	}

	_, file, line, ok := runtime.Caller(slf.calldepth - 1)
	if !ok {
		file = "???"
		line = 0
	}
	fileLine := fmt.Sprintf(" %s:%d: ", file, line)
	slf.CheckAndGenFile(fileLine)

	logContents := fmt.Sprint(v...)
	slf.doPutLog(level, fileLine, logContents)
}

//最终写日志的接口
func (slf *LogModule) doPutLog(level uint, fileLine, logContents string) {
	if slf.openLevel == LEVER_DEBUG || slf.listenFun != nil {
		strlog := fmt.Sprintf("%s %s %s", LogPrefix[level], time.Now().Format("2006-01-02 15:04:05"), logContents)
		if slf.openLevel == LEVER_DEBUG {
			fmt.Println(strlog)
		}

		if slf.listenFun != nil {
			fline := fileLine
			if idx := strings.LastIndex(fileLine, "/"); idx >= 0 {
				fline = fileLine[idx+1:]
			}

			ft := fline + " " + strlog
			slf.listenFun(level, fmt.Sprintf(ft))
		}
	}

	slf.GetLoggerByLevel(level).Output(slf.calldepth+1, logContents)
}

func (slf *LogModule) AppendCallDepth(calldepth int) {
	slf.calldepth += calldepth
}

func (slf *LogModule) SetLogLevel(level uint) {
	slf.openLevel = level
}
