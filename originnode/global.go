package originnode

import (
	_ "net/http/pprof"
	"sync"

	"github.com/duanhf2012/origin/sysmodule"

	"github.com/duanhf2012/origin/service"
)

type GlobalModule struct {
	service.BaseModule
}

var g_module GlobalModule

func AddModule(module service.IModule) bool {
	if module.GetModuleType() == 0 {
		return false
	}

	return g_module.AddModule(module)
}

func GetModuleByType(moduleType uint32) service.IModule {
	return g_module.GetModuleByType(moduleType)
}

func GetLog(logmodule uint32) sysmodule.ILogger {
	module := g_module.GetModuleByType(logmodule)
	if nil == module {
		return nil
	}

	return module.(sysmodule.ILogger)
}

func InitGlobalModule() {
	g_module.InitModule(&g_module)
}

func RunGlobalModule(exit chan bool, pwaitGroup *sync.WaitGroup) {
	g_module.RunModule(&g_module, exit, pwaitGroup)
}
