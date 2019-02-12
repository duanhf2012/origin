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
	SetModuleType(moduleType uint32)
	GetModuleType() uint32
	RunModule(module IModule, exit chan bool, pwaitGroup *sync.WaitGroup) error
	InitModule(module IModule) error
	OnInit() error
	OnRun() bool
	AddModule(module IModule) bool
	GetModuleByType(moduleType uint32) IModule
	GetOwnerService() IService
	SetOwnerService(iservice IService)
}

type IService interface {
	Init(Iservice IService, servicetype int) error
	OnInit() error
	OnEndInit() error
	OnRun() bool
	OnDestory() error
	OnFetchService(iservice IService) error
	OnSetupService(iservice IService)  //其他服务被安装
	OnRemoveService(iservice IService) //其他服务被安装

	GetServiceType() int
	GetServiceName() string
	SetServiceName(serviceName string) bool
	GetServiceId() int
	IsTimeOutTick(microSecond int64) bool

	GetStatus() int
}

var Log *log.Logger
var logFile *os.File

func InitLog() {
	fileName := "log.log"
	var err error
	logFile, err = os.Create(fileName)

	if err != nil {
		log.Fatalln("open file error")
	}
	Log = log.New(logFile, "", log.LstdFlags)
}

type BaseService struct {
	BaseModule

	serviceid   int
	servicename string
	servicetype int

	Status int
}

type BaseModule struct {
	moduleType uint32
	mapModule  map[uint32]IModule

	ownerService IService
	tickTime     int64
}

func (slf *BaseService) GetServiceId() int {
	return slf.serviceid
}

func (slf *BaseService) GetServiceType() int {
	return slf.servicetype
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

func (slf *BaseService) OnEndInit() error {
	return nil
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

func (slf *BaseService) Init(iservice IService, servicetype int) error {
	slf.ownerService = iservice
	slf.servicename = fmt.Sprintf("%T", iservice)
	parts := strings.Split(slf.servicename, ".")
	if len(parts) != 2 {
		err := fmt.Errorf("BaseService.Init: service name is error: %q", slf.servicename)
		return err
	}

	slf.servicename = parts[1]
	slf.servicetype = servicetype
	slf.serviceid = InstanceServiceMgr().GenServiceID()

	return nil
}

func (slf *BaseService) RPC_CheckServiceTickTimeOut(microSecond int64) error {

	if slf.IsTimeOutTick(microSecond) == true {
		Log.Printf("service:%s is timeout,state:%d", slf.GetServiceName(), slf.GetStatus())
	}

	return nil
}

func (slf *BaseService) IsTimeOutTick(microSecond int64) bool {

	nowtm := time.Now().UnixNano() / 1e6
	return nowtm-slf.tickTime >= microSecond

}

func (slf *BaseModule) SetModuleType(moduleType uint32) {
	slf.moduleType = moduleType
}

func (slf *BaseModule) GetModuleType() uint32 {
	return slf.moduleType
}

//OnInit() error
//OnRun() error
func (slf *BaseModule) AddModule(module IModule) bool {
	if module.GetModuleType() == 0 {
		return false
	}

	module.SetOwnerService(slf.ownerService)

	if slf.mapModule == nil {
		slf.mapModule = make(map[uint32]IModule)
	}

	_, ok := slf.mapModule[module.GetModuleType()]
	if ok == true {
		return false
	}

	slf.mapModule[module.GetModuleType()] = module
	return true
}

func (slf *BaseModule) GetModuleByType(moduleType uint32) IModule {
	ret, ok := slf.mapModule[moduleType]
	if ok == false {
		return nil
	}

	return ret
}

func (slf *BaseModule) OnInit() error {
	return fmt.Errorf("not implement OnInit moduletype %d ", slf.GetModuleType())
}

func (slf *BaseModule) OnRun() bool {
	return false
}

func (slf *BaseModule) GetOwnerService() IService {
	return slf.ownerService
}

func (slf *BaseModule) SetOwnerService(iservice IService) {
	slf.ownerService = iservice
}

func (slf *BaseModule) InitModule(module IModule) error {
	module.OnInit()
	for _, subModule := range slf.mapModule {
		go subModule.OnInit()
	}

	return nil
}

func (slf *BaseModule) RunModule(module IModule, exit chan bool, pwaitGroup *sync.WaitGroup) error {
	//运行所有子模块
	for _, subModule := range slf.mapModule {
		go subModule.RunModule(subModule, exit, pwaitGroup)
	}

	pwaitGroup.Add(1)
	defer pwaitGroup.Done()
	for {
		select {
		case <-exit:
			fmt.Println("stopping module %s...", fmt.Sprintf("%T", slf))
			return nil
		default:
		}
		if module.OnRun() == false {
			break
		}
		slf.tickTime = time.Now().UnixNano() / 1e6
	}

	return nil
}
