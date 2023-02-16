package service

import "errors"

//本地所有的service
var mapServiceName map[string]IService
var setupServiceList []IService

type RegRpcEventFunType func(serviceName string)
type RegDiscoveryServiceEventFunType func(serviceName string)
var RegRpcEventFun RegRpcEventFunType
var UnRegRpcEventFun RegRpcEventFunType

var RegDiscoveryServiceEventFun RegDiscoveryServiceEventFunType
var UnRegDiscoveryServiceEventFun RegDiscoveryServiceEventFunType

func init(){
	mapServiceName = map[string]IService{}
	setupServiceList = []IService{}
}

func Init() {
	for _,s := range setupServiceList {
		err := s.OnInit()
		if err != nil {
			errs := errors.New("Failed to initialize "+s.GetName()+" service:"+err.Error())
			panic(errs)
		}
	}
}

func Setup(s IService) bool {
	_,ok := mapServiceName[s.GetName()]
	if ok == true {
		return false
	}

	mapServiceName[s.GetName()] = s
	setupServiceList = append(setupServiceList, s)
	return true
}

func GetService(serviceName string) IService {
	s,ok := mapServiceName[serviceName]
	if ok == false {
		return nil
	}

	return s
}

func Start(){
	for _,s := range setupServiceList {
		s.Start()
	}
}

func StopAllService(){
	for i := len(setupServiceList) - 1; i >= 0; i-- {
		setupServiceList[i].Stop()
	}
}
