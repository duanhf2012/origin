package cluster

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
)

type CNodeCfg struct {
	NodeID      int
	NodeName    string
	ServerAddr  string
	ServiceList []string
	ClusterNode []string
}

type CNode struct {
	NodeID      int
	NodeName    string
	ServerAddr  string
	ServiceList map[string]bool
}

type ClusterConfig struct {
	PublicServiceList []string
	NodeList          []CNodeCfg //配置列表
	currentNode       CNode      //当前node

	mapIdNode             map[int]CNode      //map[nodeid] CNode
	mapClusterNodeService map[string][]CNode //map[nodename] []CNode
	mapClusterServiceNode map[string][]CNode //map[servicename] []CNode
}

func ReadCfg(path string, nodeid int) (*ClusterConfig, error) {
	c := &ClusterConfig{}

	//1.加载解析配置
	d, err := ioutil.ReadFile(path)
	if err != nil {
		fmt.Printf("Read File %s Error!", path)
		return nil, err
	}

	err = json.Unmarshal(d, c)
	if err != nil {
		fmt.Printf("Read File %s ,%s Error!", path, err)
		return nil, err
	}

	c.mapIdNode = make(map[int]CNode, 1)
	c.mapClusterNodeService = make(map[string][]CNode, 1)
	c.mapClusterServiceNode = make(map[string][]CNode, 1)

	//2.组装mapIdNode
	var custerNodeName []string
	for _, v := range c.NodeList {
		mapservice := make(map[string]bool, 1)
		for _, s := range v.ServiceList {
			mapservice[s] = true
		}

		node := CNode{v.NodeID, v.NodeName, v.ServerAddr, mapservice}
		c.mapIdNode[v.NodeID] = node

		if v.NodeID == nodeid {
			//保存当前结点
			c.currentNode = node
			custerNodeName = v.ClusterNode
		}
	}

	if c.currentNode.NodeID == 0 {
		return nil, errors.New(fmt.Sprintf("Cannot find NodeId %d in cluster.json!", nodeid))
	}

	//3.存入当前Node服务名
	c.mapClusterNodeService[c.currentNode.NodeName] = append(c.mapClusterNodeService[c.currentNode.NodeName], c.currentNode)

	//4.组装mapClusterNodeService
	for _, cn := range custerNodeName {
		for _, n := range c.mapIdNode {
			if n.NodeName == cn {
				nodeList := c.mapClusterNodeService[n.NodeName]
				if IsExistsNode(nodeList, &n) == false {
					c.mapClusterNodeService[n.NodeName] = append(c.mapClusterNodeService[n.NodeName], n)
				}
			}
		}
	}

	//5.组装mapClusterServiceNode
	for _, nodelist := range c.mapClusterNodeService { //[]Node
		for _, node := range nodelist { //Node
			for s := range node.ServiceList {
				c.mapClusterServiceNode[s] = append(c.mapClusterServiceNode[s], node)
			}
		}
	}

	//向c.currentNode中加入公共服务
	for _, servicename := range c.PublicServiceList {
		c.currentNode.ServiceList[servicename] = true
	}
	return c, nil
}

func (slf *ClusterConfig) GetIdByService(serviceName string) []int {
	var nodeidlist []int
	nodeidlist = make([]int, 0)

	nodeList, ok := slf.mapClusterServiceNode[serviceName]
	if ok == true {
		for _, v := range nodeList {
			nodeidlist = append(nodeidlist, v.NodeID)
		}
	}

	return nodeidlist
}

func (slf *ClusterConfig) GetIdByNodeService(NodeName string, serviceName string) []int {
	var nodeidlist []int
	nodeidlist = make([]int, 0)

	if NodeName == slf.currentNode.NodeName {
		nodeidlist = append(nodeidlist, slf.currentNode.NodeID)
	}

	v, ok := slf.mapClusterNodeService[NodeName]
	if ok == false {
		return nodeidlist
	}

	for _, n := range v {
		_, ok = n.ServiceList[serviceName]
		if ok == true {
			nodeidlist = append(nodeidlist, n.NodeID)
		}
	}

	return nodeidlist
}

func (slf *ClusterConfig) HasLocalService(serviceName string) bool {
	_, ok := slf.currentNode.ServiceList[serviceName]
	return ok == true
}

func IsExistsNode(nodelist []CNode, pNode *CNode) bool {
	for _, node := range nodelist {
		if node.NodeID == pNode.NodeID {
			return true
		}
	}

	return false
}

func (slf *ClusterConfig) GetNodeNameByNodeId(nodeid int) string {
	node, ok := slf.mapIdNode[nodeid]
	if ok == false {
		return ""
	}

	return node.NodeName
}
