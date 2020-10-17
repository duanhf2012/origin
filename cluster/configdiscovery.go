package cluster


type ConfigDiscovery struct {
	funDelService FunDelNode
	funSetService FunSetNodeInfo
	localNodeId int
}

func (slf *ConfigDiscovery) Init(localNodeId int) error{
	slf.localNodeId = localNodeId

	//解析本地其他服务配置
	nodeInfoList,err := GetCluster().readLocalClusterConfig(0)
	if err != nil {
		return err
	}

	for _,nodeInfo := range nodeInfoList {
		if nodeInfo.NodeId == localNodeId {
			continue
		}
		slf.funSetService(&nodeInfo)
	}

	return nil
}


func (slf *ConfigDiscovery) OnNodeStop(){
}


func (slf *ConfigDiscovery) RegFunDelNode(funDelNode FunDelNode){
	slf.funDelService = funDelNode
}

func (slf *ConfigDiscovery) RegFunSetNode(funSetNodeInfo FunSetNodeInfo){
	slf.funSetService = funSetNodeInfo
}
