package log

import (
	"context"
	"fmt"
	"github.com/duanhf2012/origin/v2/util/bytespool"
	"io"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

var OpenConsole bool
var LogSize int64
var LogChannelCap int
var LogPath string
var LogLevel = LevelTrace

var gLogger, _ = NewTextLogger(LevelDebug, "", "", true, LogChannelCap)
var isSetLogger bool
var memPool = bytespool.NewMemAreaPool()

// levels
const (
	LevelTrace   = slog.Level(-8)
	LevelDebug   = slog.LevelDebug
	LevelInfo    = slog.LevelInfo
	LevelWarning = slog.LevelWarn
	LevelError   = slog.LevelError
	LevelStack   = slog.Level(12)
	LevelDump    = slog.Level(16)
	LevelFatal   = slog.Level(20)
)

type ILogger interface {
	Trace(msg string, args ...any)
	Debug(msg string, args ...any)
	Info(msg string, args ...any)
	Warning(msg string, args ...any)
	Error(msg string, args ...any)
	Stack(msg string, args ...any)
	Dump(msg string, args ...any)
	Fatal(msg string, args ...any)

	DoSPrintf(level slog.Level, a []interface{})
	FormatHeader(buf *Buffer, level slog.Level, callDepth int)
	Close()
}

type Logger struct {
	SLogger *slog.Logger

	ioWriter IoWriter

	sBuff Buffer
}

type IoWriter struct {
	outFile    io.Writer // destination for output
	writeBytes int64
	logChannel chan []byte
	wg         sync.WaitGroup
	closeSig   chan struct{}

	lockWrite sync.Mutex

	filePath       string
	filePrefix     string
	fileDay        int
	fileCreateTime int64 //second
}

func (iw *IoWriter) Close() error {
	iw.lockWrite.Lock()
	defer iw.lockWrite.Unlock()

	iw.close()

	return nil
}

func (iw *IoWriter) close() error {
	if iw.closeSig != nil {
		close(iw.closeSig)
		iw.closeSig = nil
	}
	iw.wg.Wait()

	if iw.outFile != nil {
		err := iw.outFile.(io.Closer).Close()
		iw.outFile = nil
		return err
	}

	return nil
}

func (iw *IoWriter) writeFile(p []byte) (n int, err error) {
	//switch log file
	iw.switchFile()

	if iw.outFile != nil {
		n, err = iw.outFile.Write(p)
		if n > 0 {
			atomic.AddInt64(&iw.writeBytes, int64(n))
		}
	}

	return 0, nil
}

func (iw *IoWriter) Write(p []byte) (n int, err error) {
	iw.lockWrite.Lock()
	defer iw.lockWrite.Unlock()

	if iw.logChannel == nil {
		return iw.writeIo(p)
	}

	copyBuff := memPool.MakeBytes(len(p))
	if copyBuff == nil {
		return 0, fmt.Errorf("MakeByteSlice failed")
	}
	copy(copyBuff, p)

	iw.logChannel <- copyBuff

	return
}

func (iw *IoWriter) writeIo(p []byte) (n int, err error) {
	n, err = iw.writeFile(p)

	if OpenConsole {
		n, err = os.Stdout.Write(p)
	}

	return
}

func (iw *IoWriter) setLogChannel(logChannelNum int) (err error) {
	iw.lockWrite.Lock()
	defer iw.lockWrite.Unlock()
	iw.close()

	if logChannelNum == 0 {
		return nil
	}

	//copy iw.logChannel
	var logInfo []byte
	logChannel := make(chan []byte, logChannelNum)
	for i := 0; i < logChannelNum && i < len(iw.logChannel); i++ {
		logInfo = <-iw.logChannel
		logChannel <- logInfo
	}
	iw.logChannel = logChannel

	iw.closeSig = make(chan struct{})
	iw.wg.Add(1)
	go iw.run()

	return nil
}

func (iw *IoWriter) run() {
	defer iw.wg.Done()

Loop:
	for {
		select {
		case <-iw.closeSig:
			break Loop
		case logs := <-iw.logChannel:
			iw.writeIo(logs)
			memPool.ReleaseBytes(logs)
		}
	}

	for len(iw.logChannel) > 0 {
		logs := <-iw.logChannel
		iw.writeIo(logs)
		memPool.ReleaseBytes(logs)
	}
}

func (iw *IoWriter) isFull() bool {
	if LogSize == 0 {
		return false
	}

	return atomic.LoadInt64(&iw.writeBytes) >= LogSize
}

func (logger *Logger) setLogChannel(logChannel int) (err error) {
	return logger.ioWriter.setLogChannel(logChannel)
}

func (iw *IoWriter) switchFile() error {
	now := time.Now()
	if iw.fileCreateTime == now.Unix() {
		return nil
	}

	if iw.fileDay == now.Day() && iw.isFull() == false {
		return nil
	}

	if iw.filePath != "" {
		var err error
		fileName := fmt.Sprintf("%s%d%02d%02d_%02d_%02d_%02d.log",
			iw.filePrefix,
			now.Year(),
			now.Month(),
			now.Day(),
			now.Hour(),
			now.Minute(),
			now.Second())

		filePath := path.Join(iw.filePath, fileName)

		iw.outFile, err = os.Create(filePath)
		if err != nil {
			return err
		}
		iw.fileDay = now.Day()
		iw.fileCreateTime = now.Unix()
		atomic.StoreInt64(&iw.writeBytes, 0)
	}

	return nil
}

func GetDefaultHandler() IOriginHandler {
	return gLogger.(*Logger).SLogger.Handler().(IOriginHandler)
}

func NewTextLogger(level slog.Level, pathName string, filePrefix string, addSource bool, logChannelCap int) (ILogger, error) {
	var logger Logger
	logger.ioWriter.filePath = pathName
	logger.ioWriter.filePrefix = filePrefix

	logger.SLogger = slog.New(NewOriginTextHandler(level, &logger.ioWriter, addSource, defaultReplaceAttr))
	logger.setLogChannel(logChannelCap)
	err := logger.ioWriter.switchFile()
	if err != nil {
		return nil, err
	}

	return &logger, nil
}

func NewJsonLogger(level slog.Level, pathName string, filePrefix string, addSource bool, logChannelCap int) (ILogger, error) {
	var logger Logger
	logger.ioWriter.filePath = pathName
	logger.ioWriter.filePrefix = filePrefix

	logger.SLogger = slog.New(NewOriginJsonHandler(level, &logger.ioWriter, true, defaultReplaceAttr))
	logger.setLogChannel(logChannelCap)
	err := logger.ioWriter.switchFile()
	if err != nil {
		return nil, err
	}

	return &logger, nil
}

// Close It's dangerous to call the method on logging
func (logger *Logger) Close() {
	logger.ioWriter.Close()
}

func (logger *Logger) Trace(msg string, args ...any) {
	logger.SLogger.Log(context.Background(), LevelTrace, msg, args...)
}

func (logger *Logger) Debug(msg string, args ...any) {

	logger.SLogger.Log(context.Background(), LevelDebug, msg, args...)
}

func (logger *Logger) Info(msg string, args ...any) {
	logger.SLogger.Log(context.Background(), LevelInfo, msg, args...)
}

func (logger *Logger) Warning(msg string, args ...any) {
	logger.SLogger.Log(context.Background(), LevelWarning, msg, args...)
}

func (logger *Logger) Error(msg string, args ...any) {
	logger.SLogger.Log(context.Background(), LevelError, msg, args...)
}

func (logger *Logger) Stack(msg string, args ...any) {
	logger.SLogger.Log(context.Background(), LevelStack, msg, args...)
}

func (logger *Logger) Dump(msg string, args ...any) {
	logger.SLogger.Log(context.Background(), LevelDump, msg, args...)
}

func (logger *Logger) Fatal(msg string, args ...any) {
	logger.SLogger.Log(context.Background(), LevelFatal, msg, args...)
	os.Exit(1)
}

// SetLogger It's non-thread-safe
func SetLogger(logger ILogger) {
	if logger != nil && isSetLogger == false {
		gLogger = logger
		isSetLogger = true
	}
}

func GetLogger() ILogger {
	return gLogger
}

func Trace(msg string, args ...any) {
	gLogger.Trace(msg, args...)
}

func Debug(msg string, args ...any) {
	gLogger.Debug(msg, args...)
}

func Info(msg string, args ...any) {
	gLogger.Info(msg, args...)
}

func Warning(msg string, args ...any) {
	gLogger.Warning(msg, args...)
}

func Error(msg string, args ...any) {
	gLogger.Error(msg, args...)
}

func Stack(msg string, args ...any) {
	gLogger.Stack(msg, args...)
}

func Dump(dump string, args ...any) {
	gLogger.Dump(dump, args...)
}

func Fatal(msg string, args ...any) {
	gLogger.Fatal(msg, args...)
}

func Close() {
	gLogger.Close()
}

func ErrorAttr(key string, value error) slog.Attr {
	if value == nil {
		return slog.Attr{Key: key, Value: slog.StringValue("nil")}
	}

	return slog.Attr{Key: key, Value: slog.StringValue(value.Error())}
}

func String(key, value string) slog.Attr {
	return slog.Attr{Key: key, Value: slog.StringValue(value)}
}

func Int(key string, value int) slog.Attr {
	return slog.Attr{Key: key, Value: slog.Int64Value(int64(value))}
}

func Int64(key string, value int64) slog.Attr {
	return slog.Attr{Key: key, Value: slog.Int64Value(value)}
}

func Int32(key string, value int32) slog.Attr {
	return slog.Attr{Key: key, Value: slog.Int64Value(int64(value))}
}

func Int16(key string, value int16) slog.Attr {
	return slog.Attr{Key: key, Value: slog.Int64Value(int64(value))}
}

func Int8(key string, value int8) slog.Attr {
	return slog.Attr{Key: key, Value: slog.Int64Value(int64(value))}
}

func Uint(key string, value uint) slog.Attr {
	return slog.Attr{Key: key, Value: slog.Uint64Value(uint64(value))}
}

func Uint64(key string, v uint64) slog.Attr {
	return slog.Attr{Key: key, Value: slog.Uint64Value(v)}
}

func Uint32(key string, value uint32) slog.Attr {
	return slog.Attr{Key: key, Value: slog.Uint64Value(uint64(value))}
}

func Uint16(key string, value uint16) slog.Attr {
	return slog.Attr{Key: key, Value: slog.Uint64Value(uint64(value))}
}

func Uint8(key string, value uint8) slog.Attr {
	return slog.Attr{Key: key, Value: slog.Uint64Value(uint64(value))}
}

func Float64(key string, v float64) slog.Attr {
	return slog.Attr{Key: key, Value: slog.Float64Value(v)}
}

func Bool(key string, v bool) slog.Attr {
	return slog.Attr{Key: key, Value: slog.BoolValue(v)}
}

func Time(key string, v time.Time) slog.Attr {
	return slog.Attr{Key: key, Value: slog.TimeValue(v)}
}

func Duration(key string, v time.Duration) slog.Attr {
	return slog.Attr{Key: key, Value: slog.DurationValue(v)}
}

func Any(key string, value any) slog.Attr {
	return slog.Attr{Key: key, Value: slog.AnyValue(value)}
}

func Group(key string, args ...any) slog.Attr {
	return slog.Group(key, args...)
}

func (logger *Logger) DoSPrintf(level slog.Level, a []interface{}) {
	if logger.SLogger.Enabled(context.Background(), level) == false {
		return
	}

	logger.SLogger.Handler().(IOriginHandler).Lock()
	defer logger.SLogger.Handler().(IOriginHandler).UnLock()

	logger.sBuff.Reset()

	logger.FormatHeader(&logger.sBuff, level, 3)

	for _, s := range a {
		logger.sBuff.AppendString(slog.AnyValue(s).String())
	}
	logger.sBuff.AppendString("\"\n")
	logger.ioWriter.Write(logger.sBuff.Bytes())
}

func (logger *Logger) STrace(a ...interface{}) {
	logger.DoSPrintf(LevelTrace, a)
}

func (logger *Logger) SDebug(a ...interface{}) {
	logger.DoSPrintf(LevelDebug, a)
}

func (logger *Logger) SInfo(a ...interface{}) {
	logger.DoSPrintf(LevelInfo, a)
}

func (logger *Logger) SWarning(a ...interface{}) {
	logger.DoSPrintf(LevelWarning, a)
}

func (logger *Logger) SError(a ...interface{}) {
	logger.DoSPrintf(LevelError, a)
}

func STrace(a ...interface{}) {
	gLogger.DoSPrintf(LevelTrace, a)
}

func SDebug(a ...interface{}) {
	gLogger.DoSPrintf(LevelDebug, a)
}

func SInfo(a ...interface{}) {
	gLogger.DoSPrintf(LevelInfo, a)
}

func SWarning(a ...interface{}) {
	gLogger.DoSPrintf(LevelWarning, a)
}

func SError(a ...interface{}) {
	gLogger.DoSPrintf(LevelError, a)
}

func (logger *Logger) FormatHeader(buf *Buffer, level slog.Level, callDepth int) {
	t := time.Now()
	var file string
	var line int

	// Release lock while getting caller info - it's expensive.
	var ok bool
	_, file, line, ok = runtime.Caller(callDepth)
	if !ok {
		file = "???"
		line = 0
	}
	file = filepath.Base(file)

	buf.AppendString("time=\"")
	buf.AppendString(t.Format("2006/01/02 15:04:05"))
	buf.AppendString("\"")
	logger.sBuff.AppendString(" level=")
	logger.sBuff.AppendString(getStrLevel(level))
	logger.sBuff.AppendString(" source=")

	buf.AppendString(file)
	buf.AppendByte(':')
	buf.AppendInt(int64(line))
	buf.AppendString(" msg=\"")
}
