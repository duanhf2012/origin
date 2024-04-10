package cluster

type OperType int

type FunDelNode func (nodeId string,immediately bool)
type FunSetNodeInfo func(nodeInfo *NodeInfo)

type IServiceDiscovery interface {
	InitDiscovery(localNodeId string,funDelNode FunDelNode,funSetNodeInfo FunSetNodeInfo) error
	OnNodeStop()
}

