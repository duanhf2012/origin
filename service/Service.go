package service

import (
	"fmt"
	"log"

	"os"
	"reflect"
	"strings"
	"sync"
	"time"
)

type MethodInfo struct {
	Fun       reflect.Value
	ParamList []reflect.Value
	types     reflect.Type
}

type IModule interface {
	SetModuleId(moduleId uint32) bool
	GetModuleId() uint32
	GetModuleById(moduleId uint32) IModule
	AddModule(module IModule) uint32
	//DynamicAddModule(module IModule) uint32

	RunModule(module IModule) error
	InitModule(exit chan bool, pwaitGroup *sync.WaitGroup) error
	OnInit() error
	OnRun() bool

	GetOwnerService() IService
	SetOwnerService(iservice IService)

	SetOwner(module IModule)
	GetOwner() IModule

	getBaseModule() *BaseModule
}

type IService interface {
	Init(Iservice IService) error
	OnInit() error
	OnRun() bool
	OnFetchService(iservice IService) error
	OnSetupService(iservice IService)  //其他服务被安装
	OnRemoveService(iservice IService) //其他服务被安装

	GetServiceName() string
	SetServiceName(serviceName string) bool
	GetServiceId() int
	IsTimeOutTick(microSecond int64) bool

	GetStatus() int
}

var Log *log.Logger
var logFile *os.File

func InitLog() {
	fileName := "system-" + time.Now().Format("2006-01-02") + ".log"
	var err error
	logFile, err = os.Create(fileName)

	if err != nil {
		log.Fatalln("open file error")
	}
	Log = log.New(logFile, "", log.Lshortfile|log.LstdFlags)
}

type BaseService struct {
	BaseModule

	serviceid   int
	servicename string
	Status      int
}

type BaseModule struct {
	moduleId uint32

	ownerService IService
	tickTime     int64

	ExitChan  chan bool
	WaitGroup *sync.WaitGroup

	mapModule       map[uint32]IModule
	CurrMaxModuleId uint32
	rwModuleLocker  sync.RWMutex

	selfModule  IModule
	ownerModule IModule
}

func (slf *BaseService) GetServiceId() int {
	return slf.serviceid
}

func (slf *BaseService) GetServiceName() string {
	return slf.servicename
}

func (slf *BaseService) SetServiceName(serviceName string) bool {
	slf.servicename = serviceName
	return true
}

func (slf *BaseService) GetStatus() int {
	return slf.Status
}

func (slf *BaseService) OnFetchService(iservice IService) error {
	return nil
}

func (slf *BaseService) OnSetupService(iservice IService) {

	return
}

func (slf *BaseService) OnRemoveService(iservice IService) {
	return
}

func (slf *BaseService) Init(iservice IService) error {
	slf.ownerService = iservice
	slf.servicename = fmt.Sprintf("%T", iservice)
	parts := strings.Split(slf.servicename, ".")
	if len(parts) != 2 {
		GetLogger().Printf(LEVER_ERROR, "BaseService.Init: service name is error: %q", slf.servicename)
		err := fmt.Errorf("BaseService.Init: service name is error: %q", slf.servicename)
		return err
	}

	slf.servicename = parts[1]
	slf.serviceid = InstanceServiceMgr().GenServiceID()

	return nil
}

func (slf *BaseService) RPC_CheckServiceTickTimeOut(microSecond int64) error {

	if slf.IsTimeOutTick(microSecond) == true {
		//	Log.Printf("service:%s is timeout,state:%d", slf.GetServiceName(), slf.GetStatus())
	}

	return nil
}

func (slf *BaseService) IsTimeOutTick(microSecond int64) bool {

	nowtm := time.Now().UnixNano() / 1e6
	return nowtm-slf.tickTime >= microSecond
}

func (slf *BaseModule) SetModuleId(moduleId uint32) bool {

	slf.moduleId = moduleId
	return true
}

func (slf *BaseModule) GetModuleId() uint32 {
	return slf.moduleId
}

func (slf *BaseModule) GetModuleById(moduleId uint32) IModule {
	slf.rwModuleLocker.RLock()
	ret, ok := slf.mapModule[moduleId]
	if ok == false {
		slf.rwModuleLocker.RUnlock()
		return nil
	}

	slf.rwModuleLocker.RUnlock()
	return ret
}

func (slf *BaseModule) genModuleId() uint32 {

	slf.rwModuleLocker.Lock()
	slf.CurrMaxModuleId++
	moduleId := slf.CurrMaxModuleId
	slf.rwModuleLocker.Unlock()

	return moduleId
}

func (slf *BaseModule) RemoveModule(moduleId uint32) bool {
	slf.rwModuleLocker.Lock()
	_, ok := slf.mapModule[moduleId]
	if ok == false {
		GetLogger().Printf(LEVER_WARN, "RemoveModule fail %d...", moduleId)
		slf.rwModuleLocker.Unlock()
		return false
	}

	delete(slf.mapModule, moduleId)
	slf.rwModuleLocker.Unlock()

	return true
}

func (slf *BaseModule) IsRoot() bool {
	return slf.GetOwner().GetModuleById(slf.GetModuleId()) == nil
}

func (slf *BaseModule) AddModule(module IModule) uint32 {
	if slf.WaitGroup == nil {
		GetLogger().Printf(LEVER_FATAL, "AddModule error %s...", fmt.Sprintf("%T", module))
	}

	if module.GetModuleId() > 100000000 {
		return 0
	}

	if module.GetModuleId() == 0 {
		module.SetModuleId(slf.genModuleId())
	}

	if slf.GetOwner() != nil {
		if slf.IsRoot() {
			module.SetOwner(slf.GetOwner())
		} else {
			module.SetOwner(slf.GetOwner().GetModuleById(slf.GetModuleId()))
		}

	}

	module.InitModule(slf.ExitChan, slf.WaitGroup)

	slf.rwModuleLocker.Lock()

	if slf.mapModule == nil {
		slf.mapModule = make(map[uint32]IModule)
	}

	_, ok := slf.mapModule[module.GetModuleId()]
	if ok == true {
		slf.rwModuleLocker.Unlock()
		return 0
	}

	slf.mapModule[module.GetModuleId()] = module
	slf.rwModuleLocker.Unlock()

	go module.RunModule(module)
	return module.GetModuleId()
}

func (slf *BaseModule) OnInit() error {
	return fmt.Errorf("not implement OnInit moduletype %d ", slf.GetModuleId())
}

func (slf *BaseModule) OnRun() bool {
	return false
}

func (slf *BaseModule) SetOwner(ownerModule IModule) {
	slf.ownerModule = ownerModule
}

func (slf *BaseModule) GetOwner() IModule {

	return slf.ownerModule
}

func (slf *BaseModule) GetOwnerService() IService {
	return slf.ownerService
}

func (slf *BaseModule) SetOwnerService(iservice IService) {
	slf.ownerService = iservice
}

func (slf *BaseModule) InitModule(exit chan bool, pwaitGroup *sync.WaitGroup) error {
	slf.CurrMaxModuleId = 100000
	slf.WaitGroup = pwaitGroup
	slf.ExitChan = exit
	return nil
}

func (slf *BaseModule) getBaseModule() *BaseModule {
	return slf
}

func (slf *BaseModule) RunModule(module IModule) error {

	module.OnInit()

	//运行所有子模块
	slf.rwModuleLocker.RLock()

	/*
		for _, subModule := range slf.mapModule {
			go subModule.RunModule(subModule)
		}*/
	slf.rwModuleLocker.RUnlock()

	slf.WaitGroup.Add(1)
	defer slf.WaitGroup.Done()
	for {
		select {
		case <-slf.ExitChan:
			GetLogger().Printf(LEVER_WARN, "stopping module %s...", fmt.Sprintf("%T", slf))
			fmt.Println("stopping module %s...", fmt.Sprintf("%T", slf))
			return nil
		}
		if module.OnRun() == false {
			break
		}
		slf.tickTime = time.Now().UnixNano() / 1e6
	}

	return nil
}
