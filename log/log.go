package log

import (
	"errors"
	"fmt"
	jsoniter "github.com/json-iterator/go"
	"io"
	"log"
	syslog "log"
	"os"
	"path"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"time"
)

var json = jsoniter.ConfigCompatibleWithStandardLibrary
var OpenConsole bool

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
	filepre 	string
	
	//logTime    time.Time
	fileDay        int
	level      int
	flag       int
	buf            Buffer

	outFile    io.Writer  // destination for output
	outConsole io.Writer  //os.Stdout

	mu     sync.Mutex // ensures atomic writes; protects the following fields
}

func (logger *Logger) GenDayFile(now *time.Time) error {
	if logger.fileDay == now.Day() {
		return nil
	}

	filename := fmt.Sprintf("%d%02d%02d_%02d_%02d_%02d.log",
		now.Year(),
		now.Month(),
		now.Day(),
		now.Hour(),
		now.Minute(),
		now.Second())

	if logger.filePath != "" {
		var err error
		logger.outFile,err = os.Create(path.Join(logger.filePath, logger.filepre+filename))
		if err != nil {
			return err
		}
		logger.fileDay = now.Day()
		if OpenConsole == true {
			logger.outConsole = os.Stdout
		}
	}else{
		logger.outConsole = os.Stdout
	}

	return nil
}

func New(strLevel string, pathName string, filePre string, flag int) (*Logger, error) {
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

	// new
	logger := new(Logger)
	logger.level = level
	logger.filePath = pathName
	logger.filepre = filePre
	logger.flag = flag
	logger.buf.Init()
	now := time.Now()
	err := logger.GenDayFile(&now)
	if err != nil {
		return nil,err
	}

	return logger, nil
}

// It's dangerous to call the method on logging
func (logger *Logger) Close() {
	if logger.outFile != nil {
		logger.outFile.(io.Closer).Close()
		logger.outFile = nil
	}
}

func (logger *Logger) doPrintf(level int, printLevel string, format string, a ...interface{}) {
	if level < logger.level {
		return
	}
	now := time.Now()

	logger.mu.Lock()
	logger.GenDayFile(&now)

	logger.buf.Reset()
	logger.formatHeader(3,&now)
	logger.buf.AppendString(printLevel)
	logger.buf.AppendString(fmt.Sprintf(format, a...))
	logger.buf.AppendByte('\n')
	if logger.outFile!= nil {
		logger.outFile.Write(logger.buf.Bytes())
	}
	if logger.outConsole!= nil {
		logger.outConsole.Write(logger.buf.Bytes())
	}
	logger.mu.Unlock()
	if level == fatalLevel {
		os.Exit(1)
	}
}


func (logger *Logger) doSPrintf(level int, printLevel string, a []interface{}) {
	if level < logger.level {
		return
	}
	now := time.Now()
	logger.mu.Lock()
	logger.GenDayFile(&now)
	logger.buf.Reset()
	logger.formatHeader(3,&now)
	logger.buf.AppendString(printLevel)
	for _,s := range a {
		switch s.(type) {
		//case error:
		//	logger.buf.AppendString(s.(error).Error())
		case []string:
			strSlice := s.([]string)
			logger.buf.AppendByte('[')
			for _,str := range strSlice {
				logger.buf.AppendString(str)
				logger.buf.AppendByte(',')
			}

			lastIdx := logger.buf.Len()-1
			if logger.buf.Bytes()[lastIdx] == ',' {
				logger.buf.Bytes()[lastIdx] = ']'
			}else{
				logger.buf.AppendByte(']')
			}

		case int:
			logger.buf.AppendInt(int64(s.(int)))
		case []int:
			intSlice := s.([]int)
			logger.buf.AppendByte('[')
			for _,v := range intSlice {
				logger.buf.AppendInt(int64(v))
				logger.buf.AppendByte(',')
			}

			lastIdx := logger.buf.Len()-1
			if logger.buf.Bytes()[lastIdx] == ',' {
				logger.buf.Bytes()[lastIdx] = ']'
			}else{
				logger.buf.AppendByte(']')
			}
		case int8:
			logger.buf.AppendInt(int64(s.(int8)))
		case []int8:
			intSlice := s.([]int8)
			logger.buf.AppendByte('[')
			for _,v := range intSlice {
				logger.buf.AppendInt(int64(v))
				logger.buf.AppendByte(',')
			}

			lastIdx := logger.buf.Len()-1
			if logger.buf.Bytes()[lastIdx] == ',' {
				logger.buf.Bytes()[lastIdx] = ']'
			}else{
				logger.buf.AppendByte(']')
			}
		case int16:
			logger.buf.AppendInt(int64(s.(int16)))
		case []int16:
			intSlice := s.([]int16)
			logger.buf.AppendByte('[')
			for _,v := range intSlice {
				logger.buf.AppendInt(int64(v))
				logger.buf.AppendByte(',')
			}

			lastIdx := logger.buf.Len()-1
			if logger.buf.Bytes()[lastIdx] == ',' {
				logger.buf.Bytes()[lastIdx] = ']'
			}else{
				logger.buf.AppendByte(']')
			}
		case int32:
			logger.buf.AppendInt(int64(s.(int32)))
		case []int32:
			intSlice := s.([]int32)
			logger.buf.AppendByte('[')
			for _,v := range intSlice {
				logger.buf.AppendInt(int64(v))
				logger.buf.AppendByte(',')
			}

			lastIdx := logger.buf.Len()-1
			if logger.buf.Bytes()[lastIdx] == ',' {
				logger.buf.Bytes()[lastIdx] = ']'
			}else{
				logger.buf.AppendByte(']')
			}
		case int64:
			logger.buf.AppendInt(s.(int64))
		case []int64:
			intSlice := s.([]int64)
			logger.buf.AppendByte('[')
			for _,v := range intSlice {
				logger.buf.AppendInt(v)
				logger.buf.AppendByte(',')
			}

			lastIdx := logger.buf.Len()-1
			if logger.buf.Bytes()[lastIdx] == ',' {
				logger.buf.Bytes()[lastIdx] = ']'
			}else{
				logger.buf.AppendByte(']')
			}
		case uint:
			logger.buf.AppendUint(uint64(s.(uint)))

		case []uint:
			intSlice := s.([]uint)
			logger.buf.AppendByte('[')
			for _,v := range intSlice {
				logger.buf.AppendUint(uint64(v))
				logger.buf.AppendByte(',')
			}

			lastIdx := logger.buf.Len()-1
			if logger.buf.Bytes()[lastIdx] == ',' {
				logger.buf.Bytes()[lastIdx] = ']'
			}else{
				logger.buf.AppendByte(']')
			}

		case uint8:
			logger.buf.AppendUint(uint64(s.(uint8)))
		case []uint8:
			intSlice := s.([]uint8)
			logger.buf.AppendByte('[')
			for _,v := range intSlice {
				logger.buf.AppendUint(uint64(v))
				logger.buf.AppendByte(',')
			}

			lastIdx := logger.buf.Len()-1
			if logger.buf.Bytes()[lastIdx] == ',' {
				logger.buf.Bytes()[lastIdx] = ']'
			}else{
				logger.buf.AppendByte(']')
			}

		case uint16:
			logger.buf.AppendUint(uint64(s.(uint16)))
		case []uint16:
			intSlice := s.([]uint16)
			logger.buf.AppendByte('[')
			for _,v := range intSlice {
				logger.buf.AppendUint(uint64(v))
				logger.buf.AppendByte(',')
			}

			lastIdx := logger.buf.Len()-1
			if logger.buf.Bytes()[lastIdx] == ',' {
				logger.buf.Bytes()[lastIdx] = ']'
			}else{
				logger.buf.AppendByte(']')
			}
		case uint32:
			logger.buf.AppendUint(uint64(s.(uint32)))
		case []uint32:
			intSlice := s.([]uint32)
			logger.buf.AppendByte('[')
			for _,v := range intSlice {
				logger.buf.AppendUint(uint64(v))
				logger.buf.AppendByte(',')
			}

			lastIdx := logger.buf.Len()-1
			if logger.buf.Bytes()[lastIdx] == ',' {
				logger.buf.Bytes()[lastIdx] = ']'
			}else{
				logger.buf.AppendByte(']')
			}
		case uint64:
			logger.buf.AppendUint(s.(uint64))
		case []uint64:
			intSlice := s.([]uint64)
			logger.buf.AppendByte('[')
			for _,v := range intSlice {
				logger.buf.AppendUint(v)
				logger.buf.AppendByte(',')
			}
			lastIdx := logger.buf.Len()-1
			if logger.buf.Bytes()[lastIdx] == ',' {
				logger.buf.Bytes()[lastIdx] = ']'
			}else{
				logger.buf.AppendByte(']')
			}
		case float32:
			logger.buf.AppendFloat(float64(s.(float32)),32)
		case []float32:
			intSlice := s.([]float32)
			logger.buf.AppendByte('[')
			for _,v := range intSlice {
				logger.buf.AppendFloat(float64(v),32)
				logger.buf.AppendByte(',')
			}
			lastIdx := logger.buf.Len()-1
			if logger.buf.Bytes()[lastIdx] == ',' {
				logger.buf.Bytes()[lastIdx] = ']'
			}else{
				logger.buf.AppendByte(']')
			}
		case float64:
			logger.buf.AppendFloat(s.(float64),64)
		case []float64:
			intSlice := s.([]float64)
			logger.buf.AppendByte('[')
			for _,v := range intSlice {
				logger.buf.AppendFloat(v,64)
				logger.buf.AppendByte(',')
			}
			lastIdx := logger.buf.Len()-1
			if logger.buf.Bytes()[lastIdx] == ',' {
				logger.buf.Bytes()[lastIdx] = ']'
			}else{
				logger.buf.AppendByte(']')
			}
		case bool:
			logger.buf.AppendBool(s.(bool))
		case []bool:
			intSlice := s.([]bool)
			logger.buf.AppendByte('[')
			for _,v := range intSlice {
				logger.buf.AppendBool(v)
				logger.buf.AppendByte(',')
			}
			lastIdx := logger.buf.Len()-1
			if logger.buf.Bytes()[lastIdx] == ',' {
				logger.buf.Bytes()[lastIdx] = ']'
			}else{
				logger.buf.AppendByte(']')
			}
		case string:
			logger.buf.AppendString(s.(string))
		case *int:
			val := s.(*int)
			if val != nil {
				logger.buf.AppendInt(int64(*val))
			}else{
				logger.buf.AppendString("nil<*int>")
			}
		case *int8:
			val := s.(*int8)
			if val != nil {
				logger.buf.AppendInt(int64(*val))
			}else{
				logger.buf.AppendString("nil<*int8>")
			}
		case *int16:
			val := s.(*int16)
			if val != nil {
				logger.buf.AppendInt(int64(*val))
			}else{
				logger.buf.AppendString("nil<*int16>")
			}
		case *int32:
			val := s.(*int32)
			if val != nil {
				logger.buf.AppendInt(int64(*val))
			}else{
				logger.buf.AppendString("nil<*int32>")
			}
		case *int64:
			val := s.(*int64)
			if val != nil {
				logger.buf.AppendInt(int64(*val))
			}else{
				logger.buf.AppendString("nil<*int64>")
			}
		case *uint:
			val := s.(*uint)
			if val != nil {
				logger.buf.AppendUint(uint64(*val))
			}else{
				logger.buf.AppendString("nil<*uint>")
			}
		case *uint8:
			val := s.(*uint8)
			if val != nil {
				logger.buf.AppendUint(uint64(*val))
			}else{
				logger.buf.AppendString("nil<*uint8>")
			}
		case *uint16:
			val := s.(*uint16)
			if val != nil {
				logger.buf.AppendUint(uint64(*val))
			}else{
				logger.buf.AppendString("nil<*uint16>")
			}
		case *uint32:
			val := s.(*uint32)
			if val != nil {
				logger.buf.AppendUint(uint64(*val))
			}else{
				logger.buf.AppendString("nil<*uint32>")
			}
		case *uint64:
			val := s.(*uint64)
			if val != nil {
				logger.buf.AppendUint(uint64(*val))
			}else{
				logger.buf.AppendString("nil<*uint64>")
			}
		case *float32:
			val := s.(*float32)
			if val != nil {
				logger.buf.AppendFloat(float64(*val),32)
			}else{
				logger.buf.AppendString("nil<*float32>")
			}
		case *float64:
			val := s.(*float32)
			if val != nil {
				logger.buf.AppendFloat(float64(*val),64)
			}else{
				logger.buf.AppendString("nil<*float64>")
			}
		case *bool:
			val := s.(*bool)
			if val != nil {
				logger.buf.AppendBool(*val)
			}else{
				logger.buf.AppendString("nil<*bool>")
			}
		case *string:
			val := s.(*string)
			if val != nil {
				logger.buf.AppendString(*val)
			}else{
				logger.buf.AppendString("nil<*string>")
			}
		//case []byte:
		//	logger.buf.AppendBytes(s.([]byte))
		default:
			//b,err := json.MarshalToString(s)
			//if err != nil {
			logger.buf.AppendString("<unknown type>")
			//}else{
				//logger.buf.AppendBytes(b)
			//}
		}
	}
	logger.buf.AppendByte('\n')
	if logger.outFile!= nil {
		logger.outFile.Write(logger.buf.Bytes())
	}
	if logger.outConsole!= nil {
		logger.outConsole.Write(logger.buf.Bytes())
	}
	logger.mu.Unlock()
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

var gLogger, _ = New("debug", "", "", log.LstdFlags|log.Lshortfile)

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

func SDebug(a ...interface{}) {
	gLogger.doSPrintf(debugLevel, printDebugLevel, a)
}

func SRelease(a ...interface{}) {
	gLogger.doSPrintf(releaseLevel, printReleaseLevel, a)
}

func SWarning(a ...interface{}) {
	gLogger.doSPrintf(warningLevel, printWarningLevel,  a)
}

func SError(a ...interface{}) {
	gLogger.doSPrintf(errorLevel, printErrorLevel, a)
}

func SStack(a ...interface{}) {
	gLogger.doSPrintf(stackLevel, printStackLevel, a)
	gLogger.doSPrintf(stackLevel, printStackLevel, []interface{}{string(debug.Stack())})
}

func SFatal(a ...interface{}) {
	gLogger.doSPrintf(fatalLevel, printFatalLevel,  a)
}

const timeFlag = syslog.Ldate|syslog.Ltime|syslog.Lmicroseconds
func (logger *Logger) formatHeader(calldepth int,t *time.Time) {
	var file string
	var line int
	if logger.flag&(syslog.Lshortfile|syslog.Llongfile) != 0 {
		// Release lock while getting caller info - it's expensive.
		var ok bool
		_, file, line, ok = runtime.Caller(calldepth)
		if !ok {
			file = "???"
			line = 0
		}
	}

	if logger.flag&syslog.Lmsgprefix != 0 {
		logger.buf.AppendString(logger.filepre)
	}
	if logger.flag&timeFlag != 0 {
		if logger.flag&syslog.Ldate != 0 {
			year, month, day := t.Date()
			logger.buf.AppendInt(int64(year))
			logger.buf.AppendByte('/')
			logger.buf.AppendInt(int64(month))
			logger.buf.AppendByte('/')
			logger.buf.AppendInt(int64(day))
			logger.buf.AppendByte(' ')
		}

		if logger.flag&(syslog.Ltime|syslog.Lmicroseconds) != 0 {
			hour, min, sec := t.Clock()
			logger.buf.AppendInt(int64(hour))
			logger.buf.AppendByte(':')
			logger.buf.AppendInt(int64(min))
			logger.buf.AppendByte(':')

			logger.buf.AppendInt(int64(sec))

			if logger.flag&syslog.Lmicroseconds != 0 {
				logger.buf.AppendByte('.')
				logger.buf.AppendInt(int64(t.Nanosecond()/1e3))
			}
			logger.buf.AppendByte(' ')
		}
	}
	if logger.flag&(syslog.Lshortfile|syslog.Llongfile) != 0 {
		if logger.flag&syslog.Lshortfile != 0 {
			short := file
			for i := len(file) - 1; i > 0; i-- {
				if file[i] == '/' {
					short = file[i+1:]
					break
				}
			}
			file = short
		}
		logger.buf.AppendString(file)
		logger.buf.AppendByte(':')
		logger.buf.AppendInt(int64(line))
		logger.buf.AppendString(": ")
	}

	if logger.flag&syslog.Lmsgprefix != 0 {
		logger.buf.AppendString(logger.filepre)
	}
}
