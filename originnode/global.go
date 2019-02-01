package originnode

import (
	_ "net/http/pprof"

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
