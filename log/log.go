package log

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path"
	"runtime/debug"
	"strings"
	"time"
)

var OpenConsole bool = true

// levels
const (
	debugLevel   = 0
	releaseLevel = 1
	warningLevel = 2
	errorLevel   = 3
	stackLevel   = 4
	fatalLevel   = 5
)

const (
	printDebugLevel   = "[debug  ] "
	printReleaseLevel = "[release] "
	printWarningLevel = "[warning] "
	printErrorLevel   = "[error  ] "
	printStackLevel   = "[stack  ] "
	printFatalLevel   = "[fatal  ] "
)

type Logger struct {
	filePath   string
	logTime    time.Time
	level      int
	stdLogger   *log.Logger
	baseLogger *log.Logger
	baseFile   *os.File
	flag       int
}

func New(strLevel string, pathname string, flag int) (*Logger, error) {
	// level
	var level int
	switch strings.ToLower(strLevel) {
	case "debug":
		level = debugLevel
	case "release":
		level = releaseLevel
	case "warning":
		level = warningLevel
	case "error":
		level = errorLevel
	case "stack":
		level = stackLevel
	case "fatal":
		level = fatalLevel
	default:
		return nil, errors.New("unknown level: " + strLevel)
	}

	// logger
	var baseLogger *log.Logger
	var baseFile *os.File
	now := time.Now()
	if pathname != "" {
		filename := fmt.Sprintf("%d%02d%02d_%02d_%02d_%02d.log",
			now.Year(),
			now.Month(),
			now.Day(),
			now.Hour(),
			now.Minute(),
			now.Second())

		file, err := os.Create(path.Join(pathname, filename))
		if err != nil {
			return nil, err
		}

		baseLogger = log.New(file, "", flag)
		baseFile = file
	} else {
		baseLogger = log.New(os.Stdout, "", flag)
		OpenConsole = false
	}
	// new
	logger := new(Logger)
	logger.level = level
	logger.stdLogger = log.New(os.Stdout, "", flag)
	logger.baseLogger = baseLogger
	logger.baseFile = baseFile
	logger.logTime = now
	logger.filePath = pathname
	logger.flag = flag

	return logger, nil
}

// It's dangerous to call the method on logging
func (logger *Logger) Close() {
	if logger.baseFile != nil {
		logger.baseFile.Close()
	}

	logger.baseLogger = nil
	logger.baseFile = nil
}

func (logger *Logger) doPrintf(level int, printLevel string, format string, a ...interface{}) {
	if level < logger.level {
		return
	}
	if logger.baseLogger == nil {
		panic("logger closed")
	}

	if logger.baseFile != nil {
		now := time.Now()
		if now.Day() != logger.logTime.Day() {
			filename := fmt.Sprintf("%d%02d%02d_%02d_%02d_%02d.log",
				now.Year(),
				now.Month(),
				now.Day(),
				now.Hour(),
				now.Minute(),
				now.Second())

			file, err := os.Create(path.Join(logger.filePath, filename))
			if err == nil {
				logger.baseFile.Close()
				logger.baseLogger = log.New(file, "", logger.flag)
				logger.baseFile = file
				logger.logTime = now
			}
		}
	}

	format = printLevel + format
	logger.baseLogger.Output(3, fmt.Sprintf(format, a...))
	if OpenConsole == true {
		logger.stdLogger.Output(3, fmt.Sprintf(format, a...))
	}
	if level == fatalLevel {
		os.Exit(1)
	}
}

func (logger *Logger) Debug(format string, a ...interface{}) {
	logger.doPrintf(debugLevel, printDebugLevel, format, a...)
}

func (logger *Logger) Release(format string, a ...interface{}) {
	logger.doPrintf(releaseLevel, printReleaseLevel, format, a...)
}

func (logger *Logger) Warning(format string, a ...interface{}) {
	logger.doPrintf(warningLevel, printWarningLevel, format, a...)
}

func (logger *Logger) Error(format string, a ...interface{}) {
	logger.doPrintf(errorLevel, printErrorLevel, format, a...)
}

func (logger *Logger) Stack(format string, a ...interface{}) {
	logger.doPrintf(stackLevel, printStackLevel, format, a...)
}

func (logger *Logger) Fatal(format string, a ...interface{}) {
	logger.doPrintf(fatalLevel, printFatalLevel, format, a...)
}

var gLogger, _ = New("debug", "", log.LstdFlags|log.Lshortfile)

// It's dangerous to call the method on logging
func Export(logger *Logger) {
	if logger != nil {
		gLogger = logger
	}
}

func Debug(format string, a ...interface{}) {
	gLogger.doPrintf(debugLevel, printDebugLevel, format, a...)
}

func Release(format string, a ...interface{}) {
	gLogger.doPrintf(releaseLevel, printReleaseLevel, format, a...)
}

func Warning(format string, a ...interface{}) {
	gLogger.doPrintf(warningLevel, printWarningLevel, format, a...)
}

func Error(format string, a ...interface{}) {
	gLogger.doPrintf(errorLevel, printErrorLevel, format, a...)
}

func Stack(format string, a ...interface{}) {
	s := string(debug.Stack())
	gLogger.doPrintf(stackLevel, printStackLevel, s+"\n"+format, a...)
}

func Fatal(format string, a ...interface{}) {
	gLogger.doPrintf(fatalLevel, printFatalLevel, format, a...)
}

func Close() {
	gLogger.Close()
}
