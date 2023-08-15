package log

import (
	"fmt"
	jsoniter "github.com/json-iterator/go"
	"io"
	"os"
	"path"
	"time"
	"log/slog"
	"context"
	"sync/atomic"
)

var json = jsoniter.ConfigCompatibleWithStandardLibrary
var OpenConsole bool
var LogSize int64
var gLogger, _ = NewTextLogger(LevelDebug, "", "",true)

// levels
const (
	LevelTrace          	= slog.Level(-8)
	LevelDebug        	= slog.LevelDebug
	LevelInfo       	= slog.LevelInfo
	LevelWarning    	= slog.LevelWarn
	LevelError        	= slog.LevelError
	LevelStack   			= slog.Level(12)
	LevelDump               = slog.Level(16)
	LevelFatal 				= slog.Level(20)
)

type Logger struct {
	Slogger *slog.Logger
	filePath    string
	fileprefix 	string
	fileDay     int
	fileCreateTime int64 //second
	ioWriter IoWriter

	sBuff Buffer
}

type IoWriter struct {
	outFile    io.Writer  // destination for output
	outConsole io.Writer  //os.Stdout
	writeBytes  int64
}

func (iw *IoWriter) Close() error {
	if iw.outFile!= nil {
		return iw.outFile.(io.Closer).Close()
	}

	return nil
}

func (iw *IoWriter) writeFile(p []byte) (n int, err error){
	if iw.outFile != nil {
		n,err = iw.outFile.Write(p)
		if n > 0 {
			atomic.AddInt64(&iw.writeBytes,int64(n))
		}
	}

	return 0,nil
}

func (iw *IoWriter) Write(p []byte) (n int, err error){
	n,err = iw.writeFile(p)

	if iw.outConsole != nil {
		return iw.outConsole.Write(p)
	}

	return
}

func (logger *Logger) isFull() bool {
	if LogSize == 0 {
		return false
	}

	return atomic.LoadInt64(&logger.ioWriter.writeBytes) >= LogSize
}


func (logger *Logger) setIo() error{
	now := time.Now()

	if logger.fileCreateTime == now.Unix() {
		return nil
	}

	if logger.fileDay == now.Day() && logger.isFull() == false {
		return nil
	}

	if logger.Slogger != nil {
		logger.Slogger.Handler().(IOriginHandler).Lock()
		defer logger.Slogger.Handler().(IOriginHandler).UnLock()
	}

	if logger.filePath != "" {
		var err error
		fileName := fmt.Sprintf("%s%d%02d%02d_%02d_%02d_%02d.log",
			logger.fileprefix,
			now.Year(),
			now.Month(),
			now.Day(),
			now.Hour(),
			now.Minute(),
			now.Second())

		filePath := path.Join(logger.filePath, fileName)

		logger.ioWriter.outFile,err = os.Create(filePath)
		if err != nil {
			return err
		}
		logger.fileDay = now.Day()
		logger.fileCreateTime = now.Unix()
		atomic.StoreInt64(&logger.ioWriter.writeBytes,0)
		if OpenConsole == true {
			logger.ioWriter.outConsole = os.Stdout
		}
	}else{
		logger.ioWriter.outConsole = os.Stdout
	}

	return nil
}

func NewTextLogger(level slog.Level,pathName string,filePrefix string,addSource bool) (*Logger,error){
	var logger Logger
	logger.filePath = pathName
	logger.fileprefix = filePrefix

	err := logger.setIo()
	if err != nil {
		return nil,err
	}
	logger.Slogger = slog.New(NewOriginTextHandler(level,&logger.ioWriter,addSource,defaultReplaceAttr))

	return &logger,nil
}

func NewJsonLogger(level slog.Level,pathName string,filePrefix string,addSource bool) (*Logger,error){
	var logger Logger
	logger.filePath = pathName
	logger.fileprefix = filePrefix

	err := logger.setIo()
	if err != nil {
		return nil,err
	}
	logger.Slogger = slog.New(NewOriginJsonHandler(level,&logger.ioWriter,true,defaultReplaceAttr))

	return &logger,nil
}

// It's dangerous to call the method on logging
func (logger *Logger) Close() {
	logger.ioWriter.Close()
}

func (logger *Logger) Trace(msg string, args ...any) {
	logger.setIo()
	logger.Slogger.Log(context.Background(),LevelTrace,msg,args...)
}

func (logger *Logger) Debug(msg string, args ...any) {
	logger.setIo()
	logger.Slogger.Log(context.Background(),LevelDebug,msg,args...)
}

func (logger *Logger) Info(msg string, args ...any) {
	logger.setIo()
	logger.Slogger.Log(context.Background(),LevelInfo,msg,args...)
}

func (logger *Logger) Warning(msg string, args ...any) {
	logger.setIo()
	logger.Slogger.Log(context.Background(),LevelWarning,msg,args...)
}

func (logger *Logger) Error(msg string, args ...any) {
	logger.setIo()
	logger.Slogger.Log(context.Background(),LevelError,msg,args...)
}

func (logger *Logger) Stack(msg string, args ...any) {
	logger.setIo()
	logger.Slogger.Log(context.Background(),LevelStack,msg,args...)
}

func (logger *Logger) Dump(msg string, args ...any) {
	logger.setIo()
	logger.Slogger.Log(context.Background(),LevelDump,msg,args...)
}

func (logger *Logger) Fatal(msg string, args ...any) {
	logger.setIo()
	logger.Slogger.Log(context.Background(),LevelFatal,msg,args...)
	os.Exit(1)
}

// It's dangerous to call the method on logging
func Export(logger *Logger) {
	if logger != nil {
		gLogger = logger
	}
}

func Trace(msg string, args ...any){
	gLogger.Trace(msg, args...)
}

func Debug(msg string, args ...any){
	gLogger.Debug(msg,args...)
}

func Info(msg string, args ...any){
	gLogger.Info(msg,args...)
}

func Warning(msg string, args ...any){
	gLogger.Warning(msg,args...)
}

func Error(msg string, args ...any){
	gLogger.Error(msg,args...)
}

func Stack(msg string, args ...any){
	gLogger.Stack(msg,args...)
}

func Dump(dump string, args ...any){
	gLogger.Dump(dump,args...)
}

func Fatal(msg string, args ...any){
	gLogger.Fatal(msg,args...)
}

func Close() {
	gLogger.Close()
}

func ErrorAttr(key string,value error) slog.Attr{
	return slog.Attr{key, slog.StringValue(value.Error())}
}

func String(key, value string) slog.Attr {
	return slog.Attr{key, slog.StringValue(value)}
}

func Int(key string, value int) slog.Attr {
	return slog.Attr{key, slog.Int64Value(int64(value))}
}

func Int64(key string, value int64) slog.Attr {
	return slog.Attr{key, slog.Int64Value(value)}
}

func Int32(key string, value int32) slog.Attr {
	return slog.Attr{key, slog.Int64Value(int64(value))}
}

func Int16(key string, value int16) slog.Attr {
	return slog.Attr{key, slog.Int64Value(int64(value))}
}

func Int8(key string, value int8) slog.Attr {
	return slog.Attr{key, slog.Int64Value(int64(value))}
}

func Uint(key string, value uint) slog.Attr {
	return slog.Attr{key, slog.Uint64Value(uint64(value))}
}

func Uint64(key string, v uint64) slog.Attr {
	return slog.Attr{key, slog.Uint64Value(v)}
}

func Uint32(key string, value uint32) slog.Attr {
	return slog.Attr{key, slog.Uint64Value(uint64(value))}
}

func Uint16(key string, value uint16) slog.Attr {
	return slog.Attr{key, slog.Uint64Value(uint64(value))}
}

func Uint8(key string, value uint8) slog.Attr {
	return slog.Attr{key, slog.Uint64Value(uint64(value))}
}

func Float64(key string, v float64) slog.Attr {
	return slog.Attr{key, slog.Float64Value(v)}
}

func Bool(key string, v bool) slog.Attr {
	return slog.Attr{key, slog.BoolValue(v)}
}

func Time(key string, v time.Time) slog.Attr {
	return slog.Attr{key, slog.TimeValue(v)}
}

func Duration(key string, v time.Duration) slog.Attr {
	return slog.Attr{key, slog.DurationValue(v)}
}

func Any(key string, value any) slog.Attr {
	return slog.Attr{key, slog.AnyValue(value)}
}

func Group(key string, args ...any) slog.Attr {
	return slog.Group(key, args...)
}

func (logger *Logger) doSPrintf(a []interface{}) string{
	logger.sBuff.Reset()

	for _,s := range a {
		switch s.(type) {
		case []string:
			strSlice := s.([]string)
			logger.sBuff.AppendByte('[')
			for _,str := range strSlice {
				logger.sBuff.AppendString(str)
				logger.sBuff.AppendByte(',')
			}

			lastIdx := logger.sBuff.Len()-1
			if logger.sBuff.Bytes()[lastIdx] == ',' {
				logger.sBuff.Bytes()[lastIdx] = ']'
			}else{
				logger.sBuff.AppendByte(']')
			}

		case int:
			logger.sBuff.AppendInt(int64(s.(int)))
		case []int:
			intSlice := s.([]int)
			logger.sBuff.AppendByte('[')
			for _,v := range intSlice {
				logger.sBuff.AppendInt(int64(v))
				logger.sBuff.AppendByte(',')
			}

			lastIdx := logger.sBuff.Len()-1
			if logger.sBuff.Bytes()[lastIdx] == ',' {
				logger.sBuff.Bytes()[lastIdx] = ']'
			}else{
				logger.sBuff.AppendByte(']')
			}
		case int8:
			logger.sBuff.AppendInt(int64(s.(int8)))
		case []int8:
			intSlice := s.([]int8)
			logger.sBuff.AppendByte('[')
			for _,v := range intSlice {
				logger.sBuff.AppendInt(int64(v))
				logger.sBuff.AppendByte(',')
			}

			lastIdx := logger.sBuff.Len()-1
			if logger.sBuff.Bytes()[lastIdx] == ',' {
				logger.sBuff.Bytes()[lastIdx] = ']'
			}else{
				logger.sBuff.AppendByte(']')
			}
		case int16:
			logger.sBuff.AppendInt(int64(s.(int16)))
		case []int16:
			intSlice := s.([]int16)
			logger.sBuff.AppendByte('[')
			for _,v := range intSlice {
				logger.sBuff.AppendInt(int64(v))
				logger.sBuff.AppendByte(',')
			}

			lastIdx := logger.sBuff.Len()-1
			if logger.sBuff.Bytes()[lastIdx] == ',' {
				logger.sBuff.Bytes()[lastIdx] = ']'
			}else{
				logger.sBuff.AppendByte(']')
			}
		case int32:
			logger.sBuff.AppendInt(int64(s.(int32)))
		case []int32:
			intSlice := s.([]int32)
			logger.sBuff.AppendByte('[')
			for _,v := range intSlice {
				logger.sBuff.AppendInt(int64(v))
				logger.sBuff.AppendByte(',')
			}

			lastIdx := logger.sBuff.Len()-1
			if logger.sBuff.Bytes()[lastIdx] == ',' {
				logger.sBuff.Bytes()[lastIdx] = ']'
			}else{
				logger.sBuff.AppendByte(']')
			}
		case int64:
			logger.sBuff.AppendInt(s.(int64))
		case []int64:
			intSlice := s.([]int64)
			logger.sBuff.AppendByte('[')
			for _,v := range intSlice {
				logger.sBuff.AppendInt(v)
				logger.sBuff.AppendByte(',')
			}

			lastIdx := logger.sBuff.Len()-1
			if logger.sBuff.Bytes()[lastIdx] == ',' {
				logger.sBuff.Bytes()[lastIdx] = ']'
			}else{
				logger.sBuff.AppendByte(']')
			}
		case uint:
			logger.sBuff.AppendUint(uint64(s.(uint)))

		case []uint:
			intSlice := s.([]uint)
			logger.sBuff.AppendByte('[')
			for _,v := range intSlice {
				logger.sBuff.AppendUint(uint64(v))
				logger.sBuff.AppendByte(',')
			}

			lastIdx := logger.sBuff.Len()-1
			if logger.sBuff.Bytes()[lastIdx] == ',' {
				logger.sBuff.Bytes()[lastIdx] = ']'
			}else{
				logger.sBuff.AppendByte(']')
			}

		case uint8:
			logger.sBuff.AppendUint(uint64(s.(uint8)))
		case []uint8:
			intSlice := s.([]uint8)
			logger.sBuff.AppendByte('[')
			for _,v := range intSlice {
				logger.sBuff.AppendUint(uint64(v))
				logger.sBuff.AppendByte(',')
			}

			lastIdx := logger.sBuff.Len()-1
			if logger.sBuff.Bytes()[lastIdx] == ',' {
				logger.sBuff.Bytes()[lastIdx] = ']'
			}else{
				logger.sBuff.AppendByte(']')
			}

		case uint16:
			logger.sBuff.AppendUint(uint64(s.(uint16)))
		case []uint16:
			intSlice := s.([]uint16)
			logger.sBuff.AppendByte('[')
			for _,v := range intSlice {
				logger.sBuff.AppendUint(uint64(v))
				logger.sBuff.AppendByte(',')
			}

			lastIdx := logger.sBuff.Len()-1
			if logger.sBuff.Bytes()[lastIdx] == ',' {
				logger.sBuff.Bytes()[lastIdx] = ']'
			}else{
				logger.sBuff.AppendByte(']')
			}
		case uint32:
			logger.sBuff.AppendUint(uint64(s.(uint32)))
		case []uint32:
			intSlice := s.([]uint32)
			logger.sBuff.AppendByte('[')
			for _,v := range intSlice {
				logger.sBuff.AppendUint(uint64(v))
				logger.sBuff.AppendByte(',')
			}

			lastIdx := logger.sBuff.Len()-1
			if logger.sBuff.Bytes()[lastIdx] == ',' {
				logger.sBuff.Bytes()[lastIdx] = ']'
			}else{
				logger.sBuff.AppendByte(']')
			}
		case uint64:
			logger.sBuff.AppendUint(s.(uint64))
		case []uint64:
			intSlice := s.([]uint64)
			logger.sBuff.AppendByte('[')
			for _,v := range intSlice {
				logger.sBuff.AppendUint(v)
				logger.sBuff.AppendByte(',')
			}
			lastIdx := logger.sBuff.Len()-1
			if logger.sBuff.Bytes()[lastIdx] == ',' {
				logger.sBuff.Bytes()[lastIdx] = ']'
			}else{
				logger.sBuff.AppendByte(']')
			}
		case float32:
			logger.sBuff.AppendFloat(float64(s.(float32)),32)
		case []float32:
			intSlice := s.([]float32)
			logger.sBuff.AppendByte('[')
			for _,v := range intSlice {
				logger.sBuff.AppendFloat(float64(v),32)
				logger.sBuff.AppendByte(',')
			}
			lastIdx := logger.sBuff.Len()-1
			if logger.sBuff.Bytes()[lastIdx] == ',' {
				logger.sBuff.Bytes()[lastIdx] = ']'
			}else{
				logger.sBuff.AppendByte(']')
			}
		case float64:
			logger.sBuff.AppendFloat(s.(float64),64)
		case []float64:
			intSlice := s.([]float64)
			logger.sBuff.AppendByte('[')
			for _,v := range intSlice {
				logger.sBuff.AppendFloat(v,64)
				logger.sBuff.AppendByte(',')
			}
			lastIdx := logger.sBuff.Len()-1
			if logger.sBuff.Bytes()[lastIdx] == ',' {
				logger.sBuff.Bytes()[lastIdx] = ']'
			}else{
				logger.sBuff.AppendByte(']')
			}
		case bool:
			logger.sBuff.AppendBool(s.(bool))
		case []bool:
			intSlice := s.([]bool)
			logger.sBuff.AppendByte('[')
			for _,v := range intSlice {
				logger.sBuff.AppendBool(v)
				logger.sBuff.AppendByte(',')
			}
			lastIdx := logger.sBuff.Len()-1
			if logger.sBuff.Bytes()[lastIdx] == ',' {
				logger.sBuff.Bytes()[lastIdx] = ']'
			}else{
				logger.sBuff.AppendByte(']')
			}
		case string:
			logger.sBuff.AppendString(s.(string))
		case *int:
			val := s.(*int)
			if val != nil {
				logger.sBuff.AppendInt(int64(*val))
			}else{
				logger.sBuff.AppendString("nil<*int>")
			}
		case *int8:
			val := s.(*int8)
			if val != nil {
				logger.sBuff.AppendInt(int64(*val))
			}else{
				logger.sBuff.AppendString("nil<*int8>")
			}
		case *int16:
			val := s.(*int16)
			if val != nil {
				logger.sBuff.AppendInt(int64(*val))
			}else{
				logger.sBuff.AppendString("nil<*int16>")
			}
		case *int32:
			val := s.(*int32)
			if val != nil {
				logger.sBuff.AppendInt(int64(*val))
			}else{
				logger.sBuff.AppendString("nil<*int32>")
			}
		case *int64:
			val := s.(*int64)
			if val != nil {
				logger.sBuff.AppendInt(int64(*val))
			}else{
				logger.sBuff.AppendString("nil<*int64>")
			}
		case *uint:
			val := s.(*uint)
			if val != nil {
				logger.sBuff.AppendUint(uint64(*val))
			}else{
				logger.sBuff.AppendString("nil<*uint>")
			}
		case *uint8:
			val := s.(*uint8)
			if val != nil {
				logger.sBuff.AppendUint(uint64(*val))
			}else{
				logger.sBuff.AppendString("nil<*uint8>")
			}
		case *uint16:
			val := s.(*uint16)
			if val != nil {
				logger.sBuff.AppendUint(uint64(*val))
			}else{
				logger.sBuff.AppendString("nil<*uint16>")
			}
		case *uint32:
			val := s.(*uint32)
			if val != nil {
				logger.sBuff.AppendUint(uint64(*val))
			}else{
				logger.sBuff.AppendString("nil<*uint32>")
			}
		case *uint64:
			val := s.(*uint64)
			if val != nil {
				logger.sBuff.AppendUint(uint64(*val))
			}else{
				logger.sBuff.AppendString("nil<*uint64>")
			}
		case *float32:
			val := s.(*float32)
			if val != nil {
				logger.sBuff.AppendFloat(float64(*val),32)
			}else{
				logger.sBuff.AppendString("nil<*float32>")
			}
		case *float64:
			val := s.(*float32)
			if val != nil {
				logger.sBuff.AppendFloat(float64(*val),64)
			}else{
				logger.sBuff.AppendString("nil<*float64>")
			}
		case *bool:
			val := s.(*bool)
			if val != nil {
				logger.sBuff.AppendBool(*val)
			}else{
				logger.sBuff.AppendString("nil<*bool>")
			}
		case *string:
			val := s.(*string)
			if val != nil {
				logger.sBuff.AppendString(*val)
			}else{
				logger.sBuff.AppendString("nil<*string>")
			}
		//case []byte:
		//	logger.buf.AppendBytes(s.([]byte))
		default:
			//b,err := json.MarshalToString(s)
			//if err != nil {
			logger.sBuff.AppendString("<unknown type>")
			//}else{
			//logger.buf.AppendBytes(b)
			//}
		}
	}

	return logger.sBuff.String()
}

func SDebug(a ...interface{}) {
	gLogger.sBuff.Locker()
	defer gLogger.sBuff.UnLocker()

	gLogger.Debug(gLogger.doSPrintf(a))
}

func SInfo(a ...interface{}) {
	gLogger.sBuff.Locker()
	defer gLogger.sBuff.UnLocker()

	gLogger.Info(gLogger.doSPrintf(a))
}

func SWarning(a ...interface{}) {
	gLogger.sBuff.Locker()
	defer gLogger.sBuff.UnLocker()

	gLogger.Warning(gLogger.doSPrintf(a))
}

func SError(a ...interface{}) {
	gLogger.sBuff.Locker()
	defer gLogger.sBuff.UnLocker()

	gLogger.Error(gLogger.doSPrintf(a))
}

func SStack(a ...interface{}) {
	gLogger.sBuff.Locker()
	defer gLogger.sBuff.UnLocker()

	gLogger.Stack(gLogger.doSPrintf(a))
}

func SFatal(a ...interface{}) {
	gLogger.sBuff.Locker()
	defer gLogger.sBuff.UnLocker()

	gLogger.Fatal(gLogger.doSPrintf(a))
}
