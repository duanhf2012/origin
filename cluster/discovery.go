package cluster

import (
	"errors"
	"github.com/duanhf2012/origin/v2/service"
)

func (cls *Cluster) setupDiscovery(localNodeId string, setupServiceFun SetupServiceFun) error{
	if cls.discoveryInfo.getDiscoveryType() == OriginType { //origin类型服务发现
		return cls.setupOriginDiscovery(localNodeId,setupServiceFun)
	}else if cls.discoveryInfo.getDiscoveryType() ==  EtcdType{//etcd类型服务发现
		return cls.setupEtcdDiscovery(localNodeId,setupServiceFun)
	}

	return cls.setupConfigDiscovery(localNodeId,setupServiceFun)
}

func (cls *Cluster) setupOriginDiscovery(localNodeId string, setupServiceFun SetupServiceFun) error{
	if cls.serviceDiscovery != nil {
		return errors.New("service discovery has been setup")
	}

	//1.如果没有配置DiscoveryNode配置，则使用默认配置文件发现服务
	localMaster, hasMaster := cls.checkOriginDiscovery(localNodeId)
	if hasMaster == false {
		return errors.New("no master node config")
	}

	cls.serviceDiscovery = getOriginDiscovery()
	//2.如果为动态服务发现安装本地发现服务
	if localMaster == true {
		setupServiceFun(&masterService)
		cls.AddDiscoveryService(OriginDiscoveryMasterName, false)
	}

	setupServiceFun(&clientService)
	cls.AddDiscoveryService(OriginDiscoveryClientName, true)


	return nil
}

func (cls *Cluster) setupEtcdDiscovery(localNodeId string, setupServiceFun SetupServiceFun) error{
	if cls.serviceDiscovery != nil {
		return errors.New("service discovery has been setup")
	}

	//setup etcd service
	cls.serviceDiscovery = getEtcdDiscovery()
	setupServiceFun(cls.serviceDiscovery.(service.IService))
	
	cls.AddDiscoveryService(cls.serviceDiscovery.(service.IService).GetName(),false)
	return nil
}

func (cls *Cluster) setupConfigDiscovery(localNodeId string, setupServiceFun SetupServiceFun) error{
	if cls.serviceDiscovery != nil {
		return errors.New("service discovery has been setup")
	}

	cls.serviceDiscovery = &ConfigDiscovery{}
	return nil
}

func (cls *Cluster) GetOriginDiscoveryNodeList() []NodeInfo {
	return cls.discoveryInfo.Origin
}

func (cls *Cluster) GetEtcdDiscovery() *EtcdDiscovery {
	return cls.discoveryInfo.Etcd
}
