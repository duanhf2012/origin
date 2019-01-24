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

type IBaseModule interface {
	SetModuleName(modulename string) bool
	GetModuleName() string
	OnInit() error
	OnRun() error
}

type IService interface {
	Init(Iservice interface{}, servicetype int) error
	Run(service IService, exit chan bool, pwaitGroup *sync.WaitGroup) error
	OnInit() error
	OnEndInit() error
	OnRun() error
	OnDestory() error
	OnFetchService(iservice IService) error
	GetServiceType() int
	GetServiceName() string
	GetServiceId() int
	IsTimeOutTick(microSecond int64) bool

	AddModule(module IBaseModule, autorun bool) bool
	GetModule(module string) IBaseModule
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
	serviceid   int
	servicename string
	servicetype int

	tickTime int64
	statTm   int64
	Status   int

	modulelist []IBaseModule
}

type BaseModule struct {
	modulename string
}

func (slf *BaseModule) SetModuleName(modulename string) bool {
	slf.modulename = modulename
	return true
}

func (slf *BaseModule) GetModuleName() string {
	return slf.modulename
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

func (slf *BaseService) GetStatus() int {
	return slf.Status
}

func (slf *BaseService) OnEndInit() error {
	return nil
}

func (slf *BaseService) OnFetchService(iservice IService) error {
	return nil
}

func (slf *BaseService) Init(Iservice interface{}, servicetype int) error {
	slf.servicename = fmt.Sprintf("%T", Iservice)
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

func (slf *BaseService) Run(service IService, exit chan bool, pwaitGroup *sync.WaitGroup) error {
	defer pwaitGroup.Done()
	for {
		select {
		case <-exit:
			fmt.Println("stopping...")
			return nil
		default:
		}
		slf.tickTime = time.Now().UnixNano() / 1e6
		service.OnRun()
		slf.tickTime = time.Now().UnixNano() / 1e6
	}

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

func (slf *BaseService) AddModule(module IBaseModule, autorun bool) bool {
	typename := fmt.Sprintf("%v", reflect.TypeOf(module))
	parts := strings.Split(typename, ".")
	if len(parts) < 2 {
		return false
	}
	module.SetModuleName(parts[1])
	slf.modulelist = append(slf.modulelist, module)
	module.OnInit()
	if autorun == true {
		go module.OnRun()
	}

	return true
}

func (slf *BaseService) GetModule(module string) IBaseModule {
	for _, v := range slf.modulelist {
		if v.GetModuleName() == module {
			return v
		}
	}

	return nil
}
