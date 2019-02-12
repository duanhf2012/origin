package service

import (
	"fmt"
	"sync"
	"time"
)

type IServiceManager interface {
	Setup(s IService) bool
	Init() bool
	Start() bool
	CreateServiceID() int
}

type CServiceManager struct {
	genserviceid    int
	localserviceMap map[string]IService
}

func (slf *CServiceManager) Setup(s IService) bool {

	slf.localserviceMap[s.GetServiceName()] = s
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

func (slf *CServiceManager) Init() bool {

	for _, s := range slf.localserviceMap {
		(s.(IModule)).InitModule(s.(IModule))
	}

	// 初始化结束
	for _, s := range slf.localserviceMap {
		if s.OnEndInit() != nil {
			return false
		}
	}

	return true
}

func (slf *CServiceManager) Start(exit chan bool, pwaitGroup *sync.WaitGroup) bool {
	for _, s := range slf.localserviceMap {
		(s.(IModule)).RunModule(s.(IModule), exit, pwaitGroup)
	}

	pwaitGroup.Add(1)
	go slf.CheckServiceTimeTimeout(exit, pwaitGroup)
	return true
}

func (slf *CServiceManager) CheckServiceTimeTimeout(exit chan bool, pwaitGroup *sync.WaitGroup) {
	defer pwaitGroup.Done()
	for {
		select {
		case <-exit:
			fmt.Println("CheckServiceTimeTimeout stopping...")
			return
		default:
		}

		for _, s := range slf.localserviceMap {

			if s.IsTimeOutTick(20000) == true {
				Log.Printf("service:%s is timeout,state:%d", s.GetServiceName(), s.GetStatus())
			}
		}
		time.Sleep(2 * time.Second)
	}

}

func (slf *CServiceManager) GenServiceID() int {
	slf.genserviceid += 1
	return slf.genserviceid
}

func (slf *CServiceManager) Get() bool {
	for _, s := range slf.localserviceMap {
		go s.OnRun()
	}

	return true
}

var _self *CServiceManager

func InstanceServiceMgr() *CServiceManager {
	if _self == nil {
		_self = new(CServiceManager)
		_self.localserviceMap = make(map[string]IService)
		return _self
	}
	return _self
}
