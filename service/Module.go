package service

import (
	"fmt"
	"os"
	"runtime/debug"
	"sync"
	"sync/atomic"

	"github.com/duanhf2012/origin/util"
)

const (
	//ModuleNone ...
	MAX_ALLOW_SET_MODULE_ID = iota + 100000000
	INIT_AUTO_INCREMENT
)

type IModule interface {
	OnInit() error //Module初始化时调用
	OnRun() bool   //Module运行时调用
	OnEndRun()
	SetModuleId(moduleId uint32)           //手动设置ModuleId
	GetModuleId() uint32                   //获取ModuleId
	GetModuleById(moduleId uint32) IModule //通过ModuleId获取Module
	AddModule(module IModule) uint32       //添加Module
	ReleaseModule(moduleId uint32) bool    //释放Module

	SetSelf(module IModule)            //设置保存自己interface
	GetSelf() IModule                  //获取自己interface
	SetOwner(module IModule)           //设置父Module
	GetOwner() IModule                 //获取父Module
	SetOwnerService(iservice IService) //设置拥有者服务
	GetOwnerService() IService         //获取拥有者服务
	GetRoot() IModule                  //获取Root根Module

	RunModule(module IModule)                                    //手动运行Module
	InitModule(exit chan bool, pwaitGroup *sync.WaitGroup) error //手动初始化Module
	getBaseModule() *BaseModule                                  //获取BaseModule指针
	IsInit() bool
}

type BaseModule struct {
	moduleId uint32

	ownerService IService
	mapModule    map[uint32]IModule
	ownerModule  IModule
	selfModule   IModule

	CurrMaxModuleId uint32
	corouterstatus  int32 //0表示运行状态 //1释放消亡状态

	rwModuleLocker *sync.RWMutex
	ExitChan       chan bool
	WaitGroup      *sync.WaitGroup
	bInit          bool

	recoverCount int8
}

func (slf *BaseModule) GetRoot() IModule {
	currentOwner := slf.GetSelf()

	for {
		owner := currentOwner.GetOwner()
		if owner == currentOwner {
			return owner
		}
		currentOwner = owner
	}
}

func (slf *BaseModule) getLocker() *sync.RWMutex {
	return slf.rwModuleLocker
}

func (slf *BaseModule) SetModuleId(moduleId uint32) {
	slf.moduleId = moduleId
}

func (slf *BaseModule) GetModuleId() uint32 {
	return slf.moduleId
}

func (slf *BaseModule) GetModuleById(moduleId uint32) IModule {
	locker := slf.GetRoot().getBaseModule().getLocker()
	locker.Lock()

	ret, ok := slf.mapModule[moduleId]
	if ok == false {

		locker.Unlock()
		return nil
	}

	locker.Unlock()
	return ret
}

func (slf *BaseModule) GetModuleCount() int {
	locker := slf.GetRoot().getBaseModule().getLocker()
	locker.Lock()
	defer locker.Unlock()

	return len(slf.mapModule)
}

func (slf *BaseModule) genModuleId() uint32 {
	slf.CurrMaxModuleId++
	moduleId := slf.CurrMaxModuleId

	return moduleId
}

func (slf *BaseModule) deleteModule(moduleId uint32) bool {
	module, ok := slf.mapModule[moduleId]
	if ok == false {
		GetLogger().Printf(LEVER_WARN, "RemoveModule fail %d...", moduleId)
		return false
	}

	//协程退出
	atomic.AddInt32(&module.getBaseModule().corouterstatus, 1)
	module.getBaseModule().ownerService = nil
	module.getBaseModule().mapModule = nil
	module.getBaseModule().ownerModule = nil
	module.getBaseModule().selfModule = nil

	delete(slf.mapModule, moduleId)

	return true
}

func (slf *BaseModule) releaseModule(moduleId uint32) bool {
	module, ok := slf.mapModule[moduleId]
	if ok == false {
		GetLogger().Printf(LEVER_FATAL, "RemoveModule fail %d...", moduleId)
		return false
	}

	for submoduleId, _ := range module.getBaseModule().mapModule {
		module.getBaseModule().releaseModule(submoduleId)
	}

	slf.deleteModule(moduleId)

	return true
}

func (slf *BaseModule) ReleaseModule(moduleId uint32) bool {
	locker := slf.GetRoot().getBaseModule().getLocker()
	locker.Lock()
	slf.releaseModule(moduleId)
	locker.Unlock()
	return true
}

func (slf *BaseModule) IsRoot() bool {
	return slf.GetOwner() == slf.GetSelf()
}

func (slf *BaseModule) GetSelf() IModule {
	if slf.selfModule == nil {
		return slf
	}

	return slf.selfModule
}

func (slf *BaseModule) AddModule(module IModule) uint32 {
	//消亡状态不允许加入模块
	if atomic.LoadInt32(&slf.corouterstatus) != 0 {
		GetLogger().Printf(LEVER_ERROR, "%T Cannot AddModule %T", slf.GetSelf(), module.GetSelf())
		return 0
	}

	//用户设置的id不允许大于MAX_ALLOW_SET_MODULE_ID
	if module.GetModuleId() > MAX_ALLOW_SET_MODULE_ID {
		GetLogger().Printf(LEVER_ERROR, "Module Id %d is error  %T", module.GetModuleId(), module.GetSelf())
		return 0
	}

	if slf.IsRoot() {
		//构建Root结点
		slf.rwModuleLocker = &sync.RWMutex{}
	}

	pModule := slf.GetModuleById(module.GetModuleId())
	if pModule != nil {
		GetLogger().Printf(LEVER_ERROR, "%T Cannot AddModule %T,moduleid %d is  repeat!", slf.GetSelf(), module.GetSelf(), module.GetModuleId())
		return 0
	}

	locker := slf.GetRoot().getBaseModule().getLocker()
	locker.Lock()
	defer locker.Unlock()

	//如果没有设置，自动生成ModuleId
	if module.GetModuleId() == 0 {
		module.SetModuleId(slf.genModuleId())
	}

	module.getBaseModule().selfModule = module
	if slf.GetOwner() != nil {
		if slf.IsRoot() {
			//root owner为自己
			module.SetOwner(slf.GetOwner())
		} else {
			module.SetOwner(slf.GetSelf())
		}
	}

	//设置模块退出信号捕获
	module.InitModule(slf.ExitChan, slf.WaitGroup)

	//存入父模块中
	if slf.mapModule == nil {
		slf.mapModule = make(map[uint32]IModule)
	}
	_, ok := slf.mapModule[module.GetModuleId()]
	if ok == true {
		return 0
	}

	slf.mapModule[module.GetModuleId()] = module

	//运行模块
	GetLogger().Printf(LEVER_INFO, "Start Init module %T.", module)
	err := module.OnInit()
	if err != nil {
		GetLogger().Printf(LEVER_ERROR, "End Init module %T id is %d is fail,reason:%v...", module, module.GetModuleId(), err)
		os.Exit(-1)
	}
	module.getBaseModule().OnInit()
	GetLogger().Printf(LEVER_INFO, "End Init module %T.", module)

	go module.RunModule(module)
	return module.GetModuleId()
}

func (slf *BaseModule) OnInit() error {
	slf.bInit = true
	return nil
}

func (slf *BaseModule) OnRun() bool {
	return false
}

func (slf *BaseModule) OnEndRun() {
}

func (slf *BaseModule) SetOwner(ownerModule IModule) {
	slf.ownerModule = ownerModule
}

func (slf *BaseModule) SetSelf(module IModule) {
	slf.selfModule = module
}

func (slf *BaseModule) GetOwner() IModule {

	if slf.ownerModule == nil {
		return slf
	}
	return slf.ownerModule
}

func (slf *BaseModule) GetOwnerService() IService {
	return slf.ownerService
}

func (slf *BaseModule) SetOwnerService(iservice IService) {
	slf.ownerService = iservice
}

func (slf *BaseModule) InitModule(exit chan bool, pwaitGroup *sync.WaitGroup) error {
	slf.CurrMaxModuleId = INIT_AUTO_INCREMENT
	slf.WaitGroup = pwaitGroup
	slf.ExitChan = exit
	return nil
}

func (slf *BaseModule) getBaseModule() *BaseModule {
	return slf
}

func (slf *BaseModule) IsInit() bool {
	return slf.bInit
}

func (slf *BaseModule) RunModule(module IModule) {
	GetLogger().Printf(LEVER_INFO, "Start Run module %T ...", module)

	defer func() {
		if r := recover(); r != nil {
			var coreInfo string
			coreInfo = string(debug.Stack())

			coreInfo += "\n" + fmt.Sprintf("Core module is %T, try count %d. core information is %v\n", module, slf.recoverCount, r)
			GetLogger().Printf(LEVER_FATAL, coreInfo)
			slf.recoverCount += 1

			//重试3次
			if slf.recoverCount < 10 {
				go slf.RunModule(slf.GetSelf())
			} else {
				GetLogger().Printf(LEVER_FATAL, "Routine %T.OnRun has exited!", module)
			}
		}
	}()

	//运行所有子模块
	timer := util.Timer{}
	timer.SetupTimer(1000)
	slf.WaitGroup.Add(1)
	defer slf.WaitGroup.Done()
	for {

		if atomic.LoadInt32(&slf.corouterstatus) != 0 {
			module.OnEndRun()
			GetLogger().Printf(LEVER_INFO, "OnEndRun module %T ...", module)
			break
		}

		//每500ms检查退出
		if timer.CheckTimeOut() {

			select {
			case <-slf.ExitChan:
				module.OnEndRun()
				GetLogger().Printf(LEVER_INFO, "OnEndRun module %T...", module)
				return
			default:
			}
		}

		if module.OnRun() == false {
			module.OnEndRun()
			GetLogger().Printf(LEVER_INFO, "OnEndRun module %T...", module)
			return
		}

	}

}
