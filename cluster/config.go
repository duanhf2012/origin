package cluster

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
)

type CNodeCfg struct {
	NodeID   int
	NodeName string

	ServerAddr  string
	ServiceList []string
	ClusterNode []string
}

type CNode struct {
	NodeID   int
	NodeName string

	ServerAddr  string
	ServiceList map[string]bool
}

type ClusterConfig struct {
	NodeList []CNodeCfg

	//通过id获取结点
	mapIdNode map[int]CNode

	//map[nodename][ {CNode} ]
	mapClusterNodeService map[string][]CNode
	mapClusterServiceNode map[string][]CNode
	mapLocalService       map[string]bool

	currentNode CNode
}

// ReadCfg ...
func ReadCfg(path string, nodeid int) (*ClusterConfig, error) {
	c := &ClusterConfig{}

	d, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, err
	}
	err = json.Unmarshal(d, c)
	if err != nil {
		return nil, err
	}

	c.mapIdNode = make(map[int]CNode, 1)
	c.mapClusterNodeService = make(map[string][]CNode, 1)
	c.mapLocalService = make(map[string]bool)
	c.mapClusterServiceNode = make(map[string][]CNode, 1)

	//组装mapIdNode
	var clusterNode []string
	for _, v := range c.NodeList {

		//1.取所有结点
		mapservice := make(map[string]bool, 1)
		c.mapIdNode[v.NodeID] = CNode{v.NodeID, v.NodeName, v.ServerAddr, mapservice}

		if nodeid == v.NodeID {
			c.currentNode = c.mapIdNode[v.NodeID]

			//2.mapServiceNode map[string][]string
			for _, s := range v.ServiceList {
				mapservice[s] = true
				c.mapLocalService[s] = true
			}

			for _, c := range v.ClusterNode {
				clusterNode = append(clusterNode, c)
			}
		}
	}

	//组装mapClusterNodeService
	for _, kc := range clusterNode {
		for _, v := range c.NodeList {
			if kc == v.NodeName {
				//将自有连接的结点取详细信息
				mapservice := make(map[string]bool, 1)
				curNode := CNode{v.NodeID, v.NodeName, v.ServerAddr, mapservice}
				for _, s := range v.ServiceList {
					mapservice[s] = true
					c.mapClusterServiceNode[s] = append(c.mapClusterServiceNode[s], curNode)
				}
				c.mapClusterNodeService[v.NodeName] = append(c.mapClusterNodeService[v.NodeName], curNode)
			}

		}
	}

	fmt.Println(c.mapIdNode)
	fmt.Println(c.mapClusterNodeService)
	fmt.Println(c.mapClusterServiceNode)
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
	_, ok := slf.mapLocalService[serviceName]
	return ok == true
}
