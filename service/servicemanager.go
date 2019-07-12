package service

import (
	"os"
	"sync"
)

type IServiceManager interface {
	Setup(s IService) bool
	Init(logger ILogger) bool
	Start() bool
	CreateServiceID() int
}

type CServiceManager struct {
	genserviceid      int
	localserviceMap   map[string]IService
	logger            ILogger
	orderLocalService []string
}

func (slf *CServiceManager) Setup(s IService) bool {

	s.(IModule).SetOwnerService(s)
	s.(IModule).SetOwner(s.(IModule))
	s.(IModule).SetSelf(s.(IModule))

	slf.localserviceMap[s.GetServiceName()] = s
	slf.orderLocalService = append(slf.orderLocalService, s.GetServiceName())


	//通知其他服务已经安装
	for _, is := range slf.localserviceMap {
		//
		is.OnSetupService(s)
		s.OnSetupService(is)
	}

	return true
}

func (slf *CServiceManager) FindService(serviceName string) IService {
	service, ok := slf.localserviceMap[serviceName]
	if ok {
		return service
	}

	return nil
}

type FetchService func(s IService) error

func (slf *CServiceManager) FetchService(s FetchService) IService {
	for _, se := range slf.localserviceMap {
		s(se)
	}

	return nil
}

func (slf *CServiceManager) Init(logger ILogger, exit chan bool, pwaitGroup *sync.WaitGroup) bool {
	slf.logger = logger
	for _, s := range slf.localserviceMap {
		err := (s.(IModule)).InitModule(exit, pwaitGroup)
		if err != nil {
			slf.logger.Print(LEVER_FATAL, err)
			return false
		}
	}

	return true
}

func (slf *CServiceManager) Start() bool {
	for _, sname := range slf.orderLocalService {
		s := slf.FindService(sname)
		GetLogger().Printf(LEVER_INFO, "Start Init module %T.", s.(IModule))
		err := s.(IModule).OnInit()
		if err != nil {
			GetLogger().Printf(LEVER_ERROR, "Init module %T id is %d is fail,reason:%v...", s.(IModule), s.(IModule).GetModuleId(), err)
			os.Exit(-1)
		}
		s.(IModule).getBaseModule().OnInit()
		GetLogger().Printf(LEVER_INFO, "End Init module %T.", s.(IModule))
	}

	for _, sname := range slf.orderLocalService {
		s := slf.FindService(sname)
		go (s.(IModule)).RunModule(s.(IModule))
	}

	return true
}

func (slf *CServiceManager) GenServiceID() int {
	slf.genserviceid += 1
	return slf.genserviceid
}

func (slf *CServiceManager) GetLogger() ILogger {
	ret := slf.logger
	if ret == nil {
		ret = defaultLogger
	}
	return ret
}

var self *CServiceManager

func InstanceServiceMgr() *CServiceManager {
	if self == nil {
		self = new(CServiceManager)
		self.localserviceMap = make(map[string]IService)
		return self
	}

	return self
}

func GetLogger() ILogger {
	return InstanceServiceMgr().GetLogger()
}

func (slf *CServiceManager) IsFinishInit() bool {
	for _, val := range slf.localserviceMap {
		if val.IsInit() == false {
			return false
		}
	}

	return true
}
