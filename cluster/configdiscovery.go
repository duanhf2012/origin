package cluster

import "github.com/duanhf2012/origin/v2/rpc"

type ConfigDiscovery struct {
	funDelService FunDelNode
	funSetService FunSetNodeInfo
	localNodeId string
}


func (discovery *ConfigDiscovery) InitDiscovery(localNodeId string,funDelNode FunDelNode,funSetNodeInfo FunSetNodeInfo) error{
	discovery.localNodeId = localNodeId
	discovery.funDelService = funDelNode
	discovery.funSetService = funSetNodeInfo

	//解析本地其他服务配置
	_,nodeInfoList,err := GetCluster().readLocalClusterConfig(rpc.NodeIdNull)
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

