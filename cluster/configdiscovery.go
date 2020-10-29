package cluster


type ConfigDiscovery struct {
	funDelService FunDelNode
	funSetService FunSetNodeInfo
	localNodeId int
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
