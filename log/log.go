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
	"sync/atomic"
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
	buf            []Buffer

	outFile    io.Writer  // destination for output
	outConsole io.Writer  //os.Stdout

	mu     sync.Mutex // ensures atomic writes; protects the following fields
	buffIndex uint32
	buffNum uint32
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

func New(strLevel string, pathName string, filePre string, flag int,buffNum uint32) (*Logger, error) {
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

	// new111
	logger := new(Logger)
	logger.level = level
	logger.filePath = pathName
	logger.filepre = filePre
	logger.flag = flag
	logger.buf = make([]Buffer,buffNum)
	logger.buffNum = buffNum

	for i:=uint32(0);i<buffNum;i++{
		logger.buf[i].Init()
	}

	now := time.Now()
	err := logger.GenDayFile(&now)
	if err != nil {
		return nil,err
	}

	return logger, nil
}


func (logger *Logger) nextBuff() *Buffer{
	return &logger.buf[atomic.AddUint32(&logger.buffIndex,1)%logger.buffNum]
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


	buf := logger.nextBuff()
	buf.Locker()

	buf.Reset()
	logger.formatHeader(buf,3,&now)
	buf.AppendString(printLevel)
	buf.AppendString(fmt.Sprintf(format, a...))
	buf.AppendByte('\n')

	logger.mu.Lock()
	logger.GenDayFile(&now)

	if logger.outFile!= nil {
		logger.outFile.Write(buf.Bytes())
	}
	if logger.outConsole!= nil {
		logger.outConsole.Write(buf.Bytes())
	}
	logger.mu.Unlock()
	buf.UnLocker()
	if level == fatalLevel {
		os.Exit(1)
	}
}


func (logger *Logger) doSPrintf(level int, printLevel string, a []interface{}) {
	if level < logger.level {
		return
	}
	now := time.Now()
	buf := logger.nextBuff()
	buf.Locker()
	buf.Reset()
	logger.formatHeader(buf,3,&now)
	buf.AppendString(printLevel)
	for _,s := range a {
		switch s.(type) {
		//case error:
		//	logger.buf.AppendString(s.(error).Error())
		case []string:
			strSlice := s.([]string)
			buf.AppendByte('[')
			for _,str := range strSlice {
				buf.AppendString(str)
				buf.AppendByte(',')
			}

			lastIdx := buf.Len()-1
			if buf.Bytes()[lastIdx] == ',' {
				buf.Bytes()[lastIdx] = ']'
			}else{
				buf.AppendByte(']')
			}

		case int:
			buf.AppendInt(int64(s.(int)))
		case []int:
			intSlice := s.([]int)
			buf.AppendByte('[')
			for _,v := range intSlice {
				buf.AppendInt(int64(v))
				buf.AppendByte(',')
			}

			lastIdx := buf.Len()-1
			if buf.Bytes()[lastIdx] == ',' {
				buf.Bytes()[lastIdx] = ']'
			}else{
				buf.AppendByte(']')
			}
		case int8:
			buf.AppendInt(int64(s.(int8)))
		case []int8:
			intSlice := s.([]int8)
			buf.AppendByte('[')
			for _,v := range intSlice {
				buf.AppendInt(int64(v))
				buf.AppendByte(',')
			}

			lastIdx := buf.Len()-1
			if buf.Bytes()[lastIdx] == ',' {
				buf.Bytes()[lastIdx] = ']'
			}else{
				buf.AppendByte(']')
			}
		case int16:
			buf.AppendInt(int64(s.(int16)))
		case []int16:
			intSlice := s.([]int16)
			buf.AppendByte('[')
			for _,v := range intSlice {
				buf.AppendInt(int64(v))
				buf.AppendByte(',')
			}

			lastIdx := buf.Len()-1
			if buf.Bytes()[lastIdx] == ',' {
				buf.Bytes()[lastIdx] = ']'
			}else{
				buf.AppendByte(']')
			}
		case int32:
			buf.AppendInt(int64(s.(int32)))
		case []int32:
			intSlice := s.([]int32)
			buf.AppendByte('[')
			for _,v := range intSlice {
				buf.AppendInt(int64(v))
				buf.AppendByte(',')
			}

			lastIdx := buf.Len()-1
			if buf.Bytes()[lastIdx] == ',' {
				buf.Bytes()[lastIdx] = ']'
			}else{
				buf.AppendByte(']')
			}
		case int64:
			buf.AppendInt(s.(int64))
		case []int64:
			intSlice := s.([]int64)
			buf.AppendByte('[')
			for _,v := range intSlice {
				buf.AppendInt(v)
				buf.AppendByte(',')
			}

			lastIdx := buf.Len()-1
			if buf.Bytes()[lastIdx] == ',' {
				buf.Bytes()[lastIdx] = ']'
			}else{
				buf.AppendByte(']')
			}
		case uint:
			buf.AppendUint(uint64(s.(uint)))

		case []uint:
			intSlice := s.([]uint)
			buf.AppendByte('[')
			for _,v := range intSlice {
				buf.AppendUint(uint64(v))
				buf.AppendByte(',')
			}

			lastIdx := buf.Len()-1
			if buf.Bytes()[lastIdx] == ',' {
				buf.Bytes()[lastIdx] = ']'
			}else{
				buf.AppendByte(']')
			}

		case uint8:
			buf.AppendUint(uint64(s.(uint8)))
		case []uint8:
			intSlice := s.([]uint8)
			buf.AppendByte('[')
			for _,v := range intSlice {
				buf.AppendUint(uint64(v))
				buf.AppendByte(',')
			}

			lastIdx := buf.Len()-1
			if buf.Bytes()[lastIdx] == ',' {
				buf.Bytes()[lastIdx] = ']'
			}else{
				buf.AppendByte(']')
			}

		case uint16:
			buf.AppendUint(uint64(s.(uint16)))
		case []uint16:
			intSlice := s.([]uint16)
			buf.AppendByte('[')
			for _,v := range intSlice {
				buf.AppendUint(uint64(v))
				buf.AppendByte(',')
			}

			lastIdx := buf.Len()-1
			if buf.Bytes()[lastIdx] == ',' {
				buf.Bytes()[lastIdx] = ']'
			}else{
				buf.AppendByte(']')
			}
		case uint32:
			buf.AppendUint(uint64(s.(uint32)))
		case []uint32:
			intSlice := s.([]uint32)
			buf.AppendByte('[')
			for _,v := range intSlice {
				buf.AppendUint(uint64(v))
				buf.AppendByte(',')
			}

			lastIdx := buf.Len()-1
			if buf.Bytes()[lastIdx] == ',' {
				buf.Bytes()[lastIdx] = ']'
			}else{
				buf.AppendByte(']')
			}
		case uint64:
			buf.AppendUint(s.(uint64))
		case []uint64:
			intSlice := s.([]uint64)
			buf.AppendByte('[')
			for _,v := range intSlice {
				buf.AppendUint(v)
				buf.AppendByte(',')
			}
			lastIdx := buf.Len()-1
			if buf.Bytes()[lastIdx] == ',' {
				buf.Bytes()[lastIdx] = ']'
			}else{
				buf.AppendByte(']')
			}
		case float32:
			buf.AppendFloat(float64(s.(float32)),32)
		case []float32:
			intSlice := s.([]float32)
			buf.AppendByte('[')
			for _,v := range intSlice {
				buf.AppendFloat(float64(v),32)
				buf.AppendByte(',')
			}
			lastIdx := buf.Len()-1
			if buf.Bytes()[lastIdx] == ',' {
				buf.Bytes()[lastIdx] = ']'
			}else{
				buf.AppendByte(']')
			}
		case float64:
			buf.AppendFloat(s.(float64),64)
		case []float64:
			intSlice := s.([]float64)
			buf.AppendByte('[')
			for _,v := range intSlice {
				buf.AppendFloat(v,64)
				buf.AppendByte(',')
			}
			lastIdx := buf.Len()-1
			if buf.Bytes()[lastIdx] == ',' {
				buf.Bytes()[lastIdx] = ']'
			}else{
				buf.AppendByte(']')
			}
		case bool:
			buf.AppendBool(s.(bool))
		case []bool:
			intSlice := s.([]bool)
			buf.AppendByte('[')
			for _,v := range intSlice {
				buf.AppendBool(v)
				buf.AppendByte(',')
			}
			lastIdx := buf.Len()-1
			if buf.Bytes()[lastIdx] == ',' {
				buf.Bytes()[lastIdx] = ']'
			}else{
				buf.AppendByte(']')
			}
		case string:
			buf.AppendString(s.(string))
		case *int:
			val := s.(*int)
			if val != nil {
				buf.AppendInt(int64(*val))
			}else{
				buf.AppendString("nil<*int>")
			}
		case *int8:
			val := s.(*int8)
			if val != nil {
				buf.AppendInt(int64(*val))
			}else{
				buf.AppendString("nil<*int8>")
			}
		case *int16:
			val := s.(*int16)
			if val != nil {
				buf.AppendInt(int64(*val))
			}else{
				buf.AppendString("nil<*int16>")
			}
		case *int32:
			val := s.(*int32)
			if val != nil {
				buf.AppendInt(int64(*val))
			}else{
				buf.AppendString("nil<*int32>")
			}
		case *int64:
			val := s.(*int64)
			if val != nil {
				buf.AppendInt(int64(*val))
			}else{
				buf.AppendString("nil<*int64>")
			}
		case *uint:
			val := s.(*uint)
			if val != nil {
				buf.AppendUint(uint64(*val))
			}else{
				buf.AppendString("nil<*uint>")
			}
		case *uint8:
			val := s.(*uint8)
			if val != nil {
				buf.AppendUint(uint64(*val))
			}else{
				buf.AppendString("nil<*uint8>")
			}
		case *uint16:
			val := s.(*uint16)
			if val != nil {
				buf.AppendUint(uint64(*val))
			}else{
				buf.AppendString("nil<*uint16>")
			}
		case *uint32:
			val := s.(*uint32)
			if val != nil {
				buf.AppendUint(uint64(*val))
			}else{
				buf.AppendString("nil<*uint32>")
			}
		case *uint64:
			val := s.(*uint64)
			if val != nil {
				buf.AppendUint(uint64(*val))
			}else{
				buf.AppendString("nil<*uint64>")
			}
		case *float32:
			val := s.(*float32)
			if val != nil {
				buf.AppendFloat(float64(*val),32)
			}else{
				buf.AppendString("nil<*float32>")
			}
		case *float64:
			val := s.(*float32)
			if val != nil {
				buf.AppendFloat(float64(*val),64)
			}else{
				buf.AppendString("nil<*float64>")
			}
		case *bool:
			val := s.(*bool)
			if val != nil {
				buf.AppendBool(*val)
			}else{
				buf.AppendString("nil<*bool>")
			}
		case *string:
			val := s.(*string)
			if val != nil {
				buf.AppendString(*val)
			}else{
				buf.AppendString("nil<*string>")
			}
		//case []byte:
		//	logger.buf.AppendBytes(s.([]byte))
		default:
			//b,err := json.MarshalToString(s)
			//if err != nil {
			buf.AppendString("<unknown type>")
			//}else{
				//logger.buf.AppendBytes(b)
			//}
		}
	}
	buf.AppendByte('\n')

	logger.mu.Lock()
	logger.GenDayFile(&now)
	if logger.outFile!= nil {
		logger.outFile.Write(buf.Bytes())
	}
	if logger.outConsole!= nil {
		logger.outConsole.Write(buf.Bytes())
	}
	logger.mu.Unlock()
	buf.UnLocker()

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

var gLogger, _ = New("debug", "", "", log.LstdFlags|log.Lshortfile,1)

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
func (logger *Logger) formatHeader(buf *Buffer,calldepth int,t *time.Time) {
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
		buf.AppendString(logger.filepre)
	}
	if logger.flag&timeFlag != 0 {
		if logger.flag&syslog.Ldate != 0 {
			year, month, day := t.Date()
			buf.AppendInt(int64(year))
			buf.AppendByte('/')
			buf.AppendInt(int64(month))
			buf.AppendByte('/')
			buf.AppendInt(int64(day))
			buf.AppendByte(' ')
		}

		if logger.flag&(syslog.Ltime|syslog.Lmicroseconds) != 0 {
			hour, min, sec := t.Clock()
			buf.AppendInt(int64(hour))
			buf.AppendByte(':')
			buf.AppendInt(int64(min))
			buf.AppendByte(':')

			buf.AppendInt(int64(sec))

			if logger.flag&syslog.Lmicroseconds != 0 {
				buf.AppendByte('.')
				buf.AppendInt(int64(t.Nanosecond()/1e3))
			}
			buf.AppendByte(' ')
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
		buf.AppendString(file)
		buf.AppendByte(':')
		buf.AppendInt(int64(line))
		buf.AppendString(": ")
	}

	if logger.flag&syslog.Lmsgprefix != 0 {
		buf.AppendString(logger.filepre)
	}
}
