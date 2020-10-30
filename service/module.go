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
	moduleId int64                              //模块Id
	moduleName string                           //模块名称
	parent IModule        						//父亲
	self IModule        						//自己
	child map[int64]IModule 					//孩子们
	mapActiveTimer map[*timer.Timer]interface{}
	mapActiveCron map[*timer.Cron]interface{}
	dispatcher         *timer.Dispatcher 		//timer

	//根结点
	ancestor IModule      						//始祖
	seedModuleId int64    						//模块id种子
	descendants map[int64]IModule				//始祖的后裔们

	//事件管道
	eventHandler event.IEventHandler
}

func (m *Module) SetModuleId(moduleId int64) bool{
	if m.moduleId > 0 {
		return false
	}

	m.moduleId = moduleId
	return true
}

func (m *Module) GetModuleId() int64{
	return m.moduleId
}

func (m *Module) GetModuleName() string{
	return m.moduleName
}

func (m *Module) OnInit() error{
 	return nil
}

func (m *Module) AddModule(module IModule) (int64,error){
	//没有事件处理器不允许加入其他模块
	if m.GetEventProcessor() == nil {
		return 0,fmt.Errorf("module %+v is not Event Processor is nil", m.self)
	}
	pAddModule := module.getBaseModule().(*Module)
	if pAddModule.GetModuleId()==0 {
		pAddModule.moduleId = m.NewModuleId()
	}

	if m.child == nil {
		m.child = map[int64]IModule{}
	}
	_,ok := m.child[module.GetModuleId()]
	if ok == true {
		return 0,fmt.Errorf("Exists module id %d",module.GetModuleId())
	}

	pAddModule.self = module
	pAddModule.parent = m.self
	pAddModule.dispatcher = m.GetAncestor().getBaseModule().(*Module).dispatcher
	pAddModule.ancestor = m.ancestor
	pAddModule.moduleName = reflect.Indirect(reflect.ValueOf(module)).Type().Name()
	pAddModule.eventHandler = event.NewEventHandler()
	pAddModule.eventHandler.Init(m.eventHandler.GetEventProcessor())
	err := module.OnInit()
	if err != nil {
		return 0,err
	}

	m.child[module.GetModuleId()] = module
	m.ancestor.getBaseModule().(*Module).descendants[module.GetModuleId()] = module

	log.Debug("Add module %s completed", m.GetModuleName())
	return module.GetModuleId(),nil
}

func (m *Module) ReleaseModule(moduleId int64){
	pModule := m.GetModule(moduleId).getBaseModule().(*Module)

	//释放子孙
	for id,_ := range pModule.child {
		m.ReleaseModule(id)
	}

	pModule.GetEventHandler().Destroy()
	pModule.self.OnRelease()
	log.Debug("Release module %s.", m.GetModuleName())
	for pTimer,_ := range pModule.mapActiveTimer {
		pTimer.Close()
	}

	for pCron,_ := range pModule.mapActiveCron {
		pCron.Close()
	}

	delete(m.child,moduleId)
	delete (m.ancestor.getBaseModule().(*Module).descendants,moduleId)

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

func (m *Module) NewModuleId() int64{
	m.ancestor.getBaseModule().(*Module).seedModuleId+=1
	return m.ancestor.getBaseModule().(*Module).seedModuleId
}

func (m *Module) GetAncestor()IModule{
	return m.ancestor
}

func (m *Module) GetModule(moduleId int64) IModule{
	iModule,ok := m.GetAncestor().getBaseModule().(*Module).descendants[moduleId]
	if ok == false{
		return nil
	}
	return iModule
}

func (m *Module) getBaseModule() IModule{
	return m
}

func (m *Module) GetParent()IModule{
	return m.parent
}

func (m *Module) AfterFunc(d time.Duration, cb func()) *timer.Timer {
	if m.mapActiveTimer == nil {
		m.mapActiveTimer =map[*timer.Timer]interface{}{}
	}

	funName :=  runtime.FuncForPC(reflect.ValueOf(cb).Pointer()).Name()
	 tm := m.dispatcher.AfterFuncEx(funName,d,func(t *timer.Timer){
		cb()
		delete(m.mapActiveTimer,t)
	 })

	 m.mapActiveTimer[tm] = nil
	 return tm
}

func (m *Module) CronFunc(cronExpr *timer.CronExpr, cb func()) *timer.Cron {
	if m.mapActiveCron == nil {
		m.mapActiveCron =map[*timer.Cron]interface{}{}
	}

	cron := m.dispatcher.CronFuncEx(cronExpr, func(cron *timer.Cron) {
		cb()
	})

	m.mapActiveCron[cron] = nil
	return cron
}

func (m *Module) OnRelease(){
}

func (m *Module) GetService() IService {
	return m.GetAncestor().(IService)
}

func (m *Module) GetEventProcessor() event.IEventProcessor{
	return m.eventHandler.GetEventProcessor()
}

func (m *Module) NotifyEvent(ev *event.Event){
	m.eventHandler.NotifyEvent(ev)
}

func (m *Module) GetEventHandler() event.IEventHandler{
	return m.eventHandler
}