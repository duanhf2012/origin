package cluster

type OperType int

type FunDelNode func (nodeId int)
type FunSetNodeInfo func(nodeInfo *NodeInfo)

type IServiceDiscovery interface {
	InitDiscovery(localNodeId int,funDelNode FunDelNode,funSetNodeInfo FunSetNodeInfo) error
	OnNodeStop()
}

