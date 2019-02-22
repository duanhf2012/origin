package service

import (
	"fmt"
	"sync/atomic"

	"sync"
)

type IModule interface {
	SetModuleId(moduleId uint32) bool
	GetModuleId() uint32
	GetModuleById(moduleId uint32) IModule
	AddModule(module IModule) uint32
	RunModule(module IModule)
	InitModule(exit chan bool, pwaitGroup *sync.WaitGroup) error

	OnInit() error
	OnRun() bool

	GetOwnerService() IService
	SetOwnerService(iservice IService)

	SetOwner(module IModule)
	GetOwner() IModule
	SetSelf(module IModule)
	GetSelf() IModule
	getBaseModule() *BaseModule
	GetRoot() IModule

	ReleaseModule(moduleId uint32) bool
}

type BaseModule struct {
	moduleId uint32

	tickTime int64

	ExitChan  chan bool
	WaitGroup *sync.WaitGroup

	ownerService IService
	mapModule    map[uint32]IModule
	ownerModule  IModule
	selfModule   IModule

	CurrMaxModuleId uint32
	rwModuleLocker  *sync.RWMutex

	corouterstatus int32 //0表示运行状态   //1释放消亡状态
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

func (slf *BaseModule) SetModuleId(moduleId uint32) bool {

	slf.moduleId = moduleId
	return true
}

func (slf *BaseModule) GetModuleId() uint32 {
	return slf.moduleId
}

func (slf *BaseModule) GetModuleById(moduleId uint32) IModule {
	locker := slf.GetRoot().getBaseModule().getLocker()
	locker.Lock()
	defer locker.Unlock()

	ret, ok := slf.mapModule[moduleId]
	if ok == false {

		return nil
	}

	return ret
}

func (slf *BaseModule) genModuleId() uint32 {
	//slf.rwModuleLocker.Lock()
	slf.CurrMaxModuleId++
	moduleId := slf.CurrMaxModuleId
	//slf.rwModuleLocker.Unlock()

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
	//module.getBaseModule().ExitChan = nil
	//module.getBaseModule().WaitGroup = nil

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
	defer locker.Unlock()

	slf.releaseModule(moduleId)

	return true
}

func (slf *BaseModule) IsRoot() bool {
	return slf.GetOwner() == slf.GetSelf()

	//	return slf.GetOwner().GetModuleById(slf.GetModuleId()) == nil
}

const (
	//ModuleNone ...
	MAX_ALLOW_SET_MODULE_ID = iota + 100000000
	INIT_AUTO_INCREMENT
)

func (slf *BaseModule) GetSelf() IModule {
	if slf.selfModule == nil {
		return slf
	}

	return slf.selfModule
}

func (slf *BaseModule) AddModule(module IModule) uint32 {
	//消亡状态不允许加入模块
	if atomic.LoadInt32(&slf.corouterstatus) != 0 {
		return 0
	}

	//用户设置的id不允许大于MAX_ALLOW_SET_MODULE_ID
	if module.GetModuleId() > MAX_ALLOW_SET_MODULE_ID {
		return 0
	}

	if slf.IsRoot() {
		//构建Root结点
		slf.rwModuleLocker = &sync.RWMutex{}
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
	go module.RunModule(module)
	return module.GetModuleId()
}

func (slf *BaseModule) OnInit() error {
	return nil
}

func (slf *BaseModule) OnRun() bool {
	return false
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

func (slf *BaseModule) RunModule(module IModule) {
	module.OnInit()

	//运行所有子模块
	slf.WaitGroup.Add(1)
	defer slf.WaitGroup.Done()
	for {
		if atomic.LoadInt32(&slf.corouterstatus) != 0 {
			break
		}

		select {
		case <-slf.ExitChan:
			GetLogger().Printf(LEVER_WARN, "Stopping module %T...", slf.GetSelf())
			fmt.Println("Stopping module %T...", slf.GetSelf())
			return
		default:
		}

		if module.OnRun() == false {
			return
		}
		//slf.tickTime = time.Now().UnixNano() / 1e6
	}
}
