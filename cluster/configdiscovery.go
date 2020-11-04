package cluster

import "strings"

type ConfigDiscovery struct {
	funDelService FunDelNode
	funSetService FunSetNodeInfo
	localNodeId int
}

func (discovery *ConfigDiscovery) privateService(nodeInfo *NodeInfo){
	var serviceList []string
	for _,s := range nodeInfo.ServiceList {
		if strings.HasPrefix(s,"_") {
			continue
		}
		serviceList = append(serviceList,s)
	}
	nodeInfo.ServiceList = serviceList
}

func (discovery *ConfigDiscovery) Init(localNodeId int) error{
	discovery.localNodeId = localNodeId

	//解析本地其他服务配置
	nodeInfoList,err := GetCluster().readLocalClusterConfig(0)
	if err != nil {
		return err
	}

	for _,nodeInfo := range nodeInfoList {
		if nodeInfo.NodeId == localNodeId {
			continue
		}
		//去除私有服务
		discovery.privateService(&nodeInfo)
		discovery.funSetService(&nodeInfo)
	}

	return nil
}

func (discovery *ConfigDiscovery) OnNodeStop(){
}

func (discovery *ConfigDiscovery) RegFunDelNode(funDelNode FunDelNode){
	discovery.funDelService = funDelNode
}

func (discovery *ConfigDiscovery) RegFunSetNode(funSetNodeInfo FunSetNodeInfo){
	discovery.funSetService = funSetNodeInfo
}
