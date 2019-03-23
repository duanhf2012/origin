package sysservice

import (
	"github.com/duanhf2012/origin/service"
	"github.com/duanhf2012/origin/sysmodule"
)

type LogService struct {
	service.BaseService
	logmodule *sysmodule.LogModule
}

func (slf *LogService) InitLog(logservicename string, openLevel uint) {
	slf.SetServiceName(logservicename)
	slf.logmodule = &sysmodule.LogModule{}
	slf.logmodule.Init(logservicename, openLevel)
}

func (slf *LogService) Printf(level uint, format string, v ...interface{}) {
	slf.logmodule.Printf(level, format, v...)
}

func (slf *LogService) Print(level uint, v ...interface{}) {
	slf.logmodule.Print(level, v...)
}

func (slf *LogService) AppendCallDepth(calldepth int) {
	slf.logmodule.AppendCallDepth(calldepth)
}

func (slf *LogService) SetLogLevel(level uint) {
	slf.logmodule.SetLogLevel(level)
}

func (slf *LogService) SetListenLogFunc(listenFun sysmodule.FunListenLog) {
	slf.logmodule.SetListenLogFunc(listenFun)
}
