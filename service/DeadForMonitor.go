package service

import (
	"fmt"
	"github.com/duanhf2012/origin/util"
	"runtime/pprof"
	"time"
)

type ModuleMontior struct {
	mapModule *util.MapEx
}

type ModuleInfo struct {
	enterStartTm int64
	mNameInfo string
}

var moduleMontior ModuleMontior



func MonitorEnter(uuid string,strMonitorInfo string){
	if moduleMontior.mapModule == nil {
		return
	}
	moduleMontior.mapModule.Set(uuid, &ModuleInfo{enterStartTm:time.Now().Unix(),mNameInfo:strMonitorInfo})
}

func  MonitorLeave(uuid string){
	if moduleMontior.mapModule == nil {
		return
	}

	moduleMontior.mapModule.Del(uuid)
}



func ReportDeadFor(){
	if moduleMontior.mapModule == nil {
		return
	}
	moduleMontior.mapModule.RLockRange(func(key interface{}, value interface{}) {
		if value != nil {
			pModuleInfo := value.(*ModuleInfo)
			//超过5分钟认为dead for
			if time.Now().Unix() - pModuleInfo.enterStartTm > 300 {
				GetLogger().Printf(LEVER_FATAL, "module is %s, Dead cycle\n", pModuleInfo.mNameInfo)
			}
		}
	})
}

func EnableDeadForMonitor(checkInterval time.Duration){
	moduleMontior.mapModule = util.NewMapEx()
	var tmInval util.Timer
	tmInval.SetupTimer(int32(checkInterval.Milliseconds()))
	go func(){
		for {
			time.Sleep(time.Second*5)
			if tmInval.CheckTimeOut(){
				ReportDeadFor()
				ReportPprof()
			}
		}
	}()
}


func ReportPprof(){
	strReport := ""
	for _, p := range pprof.Profiles() {
		strReport += fmt.Sprintf("Name %s,count %d\n",p.Name(),p.Count())
	}

	GetLogger().Printf(LEVER_INFO, "PProf %s\n", strReport)
}