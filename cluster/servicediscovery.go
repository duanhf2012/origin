package cluster


type OperType int

type FunDelNode func (nodeId string)
type FunSetNode func(nodeInfo *NodeInfo)

type IServiceDiscovery interface {
	InitDiscovery(localNodeId string,funDelNode FunDelNode,funSetNodeInfo FunSetNode) error
}

