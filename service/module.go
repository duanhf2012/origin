package service

import (
	"fmt"
	"github.com/duanhf2012/origin/event"
	"github.com/duanhf2012/origin/log"
	"github.com/duanhf2012/origin/util/timer"
	"reflect"
	"runtime"
	"time"
)

const InitModuleId = 1e17


type IModule interface {
	SetModuleId(moduleId int64) bool
	GetModuleId() int64
	AddModule(module IModule) (int64,error)
	GetModule(moduleId int64) IModule
	GetAncestor()IModule
	ReleaseModule(moduleId int64)
	NewModuleId() int64
	GetParent()IModule
	OnInit() error
	OnRelease()
	getBaseModule() IModule
	GetService() IService
	GetModuleName() string
	GetEventProcessor()event.IEventProcessor
	NotifyEvent(ev *event.Event)
}



//1.管理各模块树层关系
//2.提供定时器常用工具
type Module struct {
	moduleId int64
	parent IModule        //父亲
	self IModule        //自己
	child map[int64]IModule //孩子们
	mapActiveTimer map[*timer.Timer]interface{}
	mapActiveCron map[*timer.Cron]interface{}

	dispatcher         *timer.Dispatcher //timer

	//根结点
	ancestor IModule      //始祖
	seedModuleId int64    //模块id种子
	descendants map[int64]IModule//始祖的后裔们

	//事件管道
	moduleName string
	eventHandler event.IEventHandler
	//eventHandler event.EventHandler
}


func (slf *Module) SetModuleId(moduleId int64) bool{
	if slf.moduleId > 0 {
		return false
	}

	slf.moduleId = moduleId
	return true
}

func (slf *Module) GetModuleId() int64{
	return slf.moduleId
}

func (slf *Module) GetModuleName() string{
	return slf.moduleName
}

func (slf *Module) OnInit() error{
//	slf.eventHandler = event.NewEventHandler()
 	return nil
}

func (slf *Module) AddModule(module IModule) (int64,error){
	//没有事件处理器不允许加入其他模块
	if slf.GetEventProcessor() == nil {
		return 0,fmt.Errorf("module %+v is not Event Processor is nil",slf.self)
	}
	pAddModule := module.getBaseModule().(*Module)
	if pAddModule.GetModuleId()==0 {
		pAddModule.moduleId = slf.NewModuleId()
	}

	if slf.child == nil {
		slf.child = map[int64]IModule{}
	}
	_,ok := slf.child[module.GetModuleId()]
	if ok == true {
		return 0,fmt.Errorf("Exists module id %d",module.GetModuleId())
	}

	pAddModule.self = module
	pAddModule.parent = slf.self
	pAddModule.dispatcher = slf.GetAncestor().getBaseModule().(*Module).dispatcher
	pAddModule.ancestor = slf.ancestor
	pAddModule.moduleName = reflect.Indirect(reflect.ValueOf(module)).Type().Name()
	pAddModule.eventHandler = event.NewEventHandler()
	pAddModule.eventHandler.Init(slf.eventHandler.GetEventProcessor())
	err := module.OnInit()
	if err != nil {
		return 0,err
	}

	slf.child[module.GetModuleId()] = module
	slf.ancestor.getBaseModule().(*Module).descendants[module.GetModuleId()] = module

	log.Debug("Add module %s completed",slf.GetModuleName())
	return module.GetModuleId(),nil
}

func (slf *Module) ReleaseModule(moduleId int64){
	pModule := slf.GetModule(moduleId).getBaseModule().(*Module)

	//释放子孙
	for id,_ := range pModule.child {
		slf.ReleaseModule(id)
	}

	pModule.GetEventHandler().Desctory()
	pModule.self.OnRelease()
	log.Debug("Release module %s.",slf.GetModuleName())
	for pTimer,_ := range pModule.mapActiveTimer {
		pTimer.Stop()
	}

	for pCron,_ := range pModule.mapActiveCron {
		pCron.Stop()
	}

	delete(slf.child,moduleId)
	delete (slf.ancestor.getBaseModule().(*Module).descendants,moduleId)

	//清理被删除的Module
	pModule.self = nil
	pModule.parent = nil
	pModule.child = nil
	pModule.mapActiveTimer = nil
	pModule.mapActiveCron = nil
	pModule.dispatcher = nil
	pModule.ancestor = nil
	pModule.descendants = nil
}

func (slf *Module) NewModuleId() int64{
	slf.ancestor.getBaseModule().(*Module).seedModuleId+=1
	return slf.ancestor.getBaseModule().(*Module).seedModuleId
}

func (slf *Module) GetAncestor()IModule{
	return slf.ancestor
}

func (slf *Module) GetModule(moduleId int64) IModule{
	iModule,ok := slf.GetAncestor().getBaseModule().(*Module).descendants[moduleId]
	if ok == false{
		return nil
	}
	return iModule
}

func (slf *Module) getBaseModule() IModule{
	return slf
}


func (slf *Module) GetParent()IModule{
	return slf.parent
}

func (slf *Module) AfterFunc(d time.Duration, cb func()) *timer.Timer {
	if slf.mapActiveTimer == nil {
		slf.mapActiveTimer =map[*timer.Timer]interface{}{}
	}

	funName :=  runtime.FuncForPC(reflect.ValueOf(cb).Pointer()).Name()
	 tm := slf.dispatcher.AfterFuncEx(funName,d,func(t *timer.Timer){
		cb()
		delete(slf.mapActiveTimer,t)
	 })

	 slf.mapActiveTimer[tm] = nil
	 return tm
}

func (slf *Module) CronFunc(cronExpr *timer.CronExpr, cb func()) *timer.Cron {
	if slf.mapActiveCron == nil {
		slf.mapActiveCron =map[*timer.Cron]interface{}{}
	}

	cron := slf.dispatcher.CronFuncEx(cronExpr, func(cron *timer.Cron) {
		cb()
	})

	slf.mapActiveCron[cron] = nil
	return cron
}

func (slf *Module) OnRelease(){
}

func (slf *Module) GetService() IService {
	return slf.GetAncestor().(IService)
}

func (slf *Module) GetEventProcessor() event.IEventProcessor{
	return slf.eventHandler.GetEventProcessor()
}

func (slf *Module) NotifyEvent(ev *event.Event){
	slf.eventHandler.NotifyEvent(ev)
}

func (slf *Module) GetEventHandler() event.IEventHandler{
	return slf.eventHandler
}