package cluster

type OperType int

type FunDelNode func (nodeId int)
type FunSetNodeInfo func(nodeInfo *NodeInfo)

type IServiceDiscovery interface {
	Init(localNodeId int) error
	OnNodeStop()
	RegFunDelNode(funDelNode FunDelNode)
	RegFunSetNode(funSetNodeInfo FunSetNodeInfo)
}

