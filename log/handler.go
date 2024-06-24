package log

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sync"
)

const defaultSkip = 7
type IOriginHandler interface {
	slog.Handler
	Lock()
	UnLock()
	SetSkip(skip int)
	GetSkip() int
}

type BaseHandler struct {
	addSource bool
	w io.Writer
	locker sync.Mutex
	skip int
}

type OriginTextHandler struct {
	BaseHandler
	*slog.TextHandler
}

type OriginJsonHandler struct {
	BaseHandler
	*slog.JSONHandler
}

func (bh *BaseHandler) SetSkip(skip int){
	bh.skip = skip
}

func (bh *BaseHandler) GetSkip() int{
	return bh.skip
}

func getStrLevel(level slog.Level) string{
	switch level {
	case LevelTrace:
		return "Trace"
	case LevelDebug:
		return "Debug"
	case LevelInfo:
		return "Info"
	case LevelWarning:
		return "Warning"
	case LevelError:
		return "Error"
	case LevelStack:
		return "Stack"
	case LevelDump:
		return "Dump"
	case LevelFatal:
		return "Fatal"
	}

	return ""
}

func defaultReplaceAttr(groups []string, a slog.Attr) slog.Attr {
	if a.Key == slog.LevelKey {
		level := a.Value.Any().(slog.Level)
		a.Value = slog.StringValue(getStrLevel(level))
	}else if a.Key == slog.TimeKey && len(groups) == 0 {
		a.Value = slog.StringValue(a.Value.Time().Format("2006/01/02 15:04:05"))
	}else if a.Key == slog.SourceKey {
		source := a.Value.Any().(*slog.Source)
		source.File = filepath.Base(source.File)
	}
	return a
}

func NewOriginTextHandler(level slog.Level,w io.Writer,addSource bool,replaceAttr func([]string,slog.Attr) slog.Attr) slog.Handler{
	var textHandler OriginTextHandler
	textHandler.addSource = addSource
	textHandler.w = w
	textHandler.TextHandler = slog.NewTextHandler(w,&slog.HandlerOptions{
		AddSource:   addSource,
		Level:       level,
		ReplaceAttr: replaceAttr,
	})

	textHandler.skip  = defaultSkip
	return &textHandler
}

func (oh *OriginTextHandler) Handle(context context.Context, record slog.Record) error{
	oh.Fill(context,&record)
	oh.locker.Lock()
	defer oh.locker.Unlock()
	
	if record.Level == LevelStack || record.Level == LevelFatal{
		err := oh.TextHandler.Handle(context, record)
		oh.logStack(&record)
		return err
	}else if record.Level == LevelDump {
		strDump := record.Message
		record.Message = "dump info"
		err := oh.TextHandler.Handle(context, record)
		oh.w.Write([]byte(strDump))
		return err
	}


	return oh.TextHandler.Handle(context, record)
}

func (b *BaseHandler) logStack(record *slog.Record){
	b.w.Write(debug.Stack())
}

func (b *BaseHandler) Lock(){
	b.locker.Lock()
}

func (b *BaseHandler) UnLock(){
	b.locker.Unlock()
}

func NewOriginJsonHandler(level slog.Level,w io.Writer,addSource bool,replaceAttr func([]string,slog.Attr) slog.Attr) slog.Handler{
	var jsonHandler OriginJsonHandler
	jsonHandler.addSource = addSource
	jsonHandler.w = w
	jsonHandler.JSONHandler = slog.NewJSONHandler(w,&slog.HandlerOptions{
		AddSource:   addSource,
		Level:       level,
		ReplaceAttr: replaceAttr,
	})

	jsonHandler.skip = defaultSkip
	return &jsonHandler
}

func (oh *OriginJsonHandler) Handle(context context.Context, record slog.Record) error{
	oh.Fill(context,&record)
	if record.Level == LevelStack || record.Level == LevelFatal || record.Level == LevelDump{
		record.Add("stack",debug.Stack())
	}

	oh.locker.Lock()
	defer oh.locker.Unlock()
	return oh.JSONHandler.Handle(context, record)
}

func (b *BaseHandler) Fill(context context.Context, record *slog.Record) {
	if b.addSource {
		var pcs [1]uintptr
		runtime.Callers(b.skip, pcs[:])
		record.PC = pcs[0]
	}
}
