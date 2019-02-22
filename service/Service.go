package service

import (
	"fmt"
	"log"

	"os"
	"reflect"
	"strings"
	"time"
)

type MethodInfo struct {
	Fun       reflect.Value
	ParamList []reflect.Value
	types     reflect.Type
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

func (slf *BaseService) IsTimeOutTick(microSecond int64) bool {

	nowtm := time.Now().UnixNano() / 1e6
	return nowtm-slf.tickTime >= microSecond
}
