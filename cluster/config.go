package cluster

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"strings"
)

//负载均衡信息
type BalancingInfo struct {
	NodeId      int    //我的nodeId
	ServiceName string //负载均衡的ServiceName

	TotalNum int   //总共有多少个协同Node
	MyIndex  int   //负责的index [0, TotalNum)
	NodeList []int //所有协同的node列表 按NodeId升序排列
}

//判断hash后的Id是否命中我的NodeId
func (slf *BalancingInfo) Hit(hashId int) bool {
	if hashId >= 0 && slf.TotalNum > 0 && slf.MyIndex >= 0 {
		return hashId%slf.TotalNum == slf.MyIndex
	}
	return false
}

//判断命中的NodeId，-1表示无法取得
func (slf *BalancingInfo) GetHitNodeId(hashId int) int {
	if hashId >= 0 && slf.TotalNum > 0 {
		if idx := hashId % slf.TotalNum; idx >= 0 && idx < len(slf.NodeList) {
			return slf.NodeList[idx]
		}
	}
	return -1
}

type CNodeCfg struct {
	NodeID      int
	NodeName    string
	ServiceList []string
	ClusterNode []string
}

type CNode struct {
	SubNetName  string
	NodeID      int
	NodeName    string //SubNetName.NodeName
	ServerAddr  string
	ServiceList map[string]bool
}

type SubNetNodeInfo struct {
	SubNetMode        string
	SubNetName        string
	PublicServiceList []string
	NodeList          []CNodeCfg //配置列表

	//mapClusterNodeService map[string][]CNode //map[nodename] []CNode
	//mapClusterServiceNode map[string][]CNode //map[servicename] []CNode
}

type ClusterConfig struct {
	SubNet []SubNetNodeInfo
	//CurrentSubNetIdx int
	mapIdNode   map[int]CNode
	currentNode CNode //当前node

	mapClusterNodeService map[string][]CNode //map[nodename] []CNode
	mapClusterServiceNode map[string][]CNode //map[servicename] []CNode
}

func GenNodeName(subnetName string, nodename string) string {
	parts := strings.Split(nodename, ".")
	if len(parts) < 2 {
		return subnetName + "." + nodename
	}

	return nodename
}

func AddCluster(clusterNodeNameList *[]string, nodename string) bool {
	for _, n := range *clusterNodeNameList {
		if n == nodename {
			return false
		}
	}

	*clusterNodeNameList = append(*clusterNodeNameList, nodename)
	return true
}

func ReadCfg(path string, nodeid int, mapNodeData map[int]NodeData) (*ClusterConfig, error) {
	clsCfg := &ClusterConfig{}
	clsCfg.mapIdNode = map[int]CNode{}
	clsCfg.mapClusterNodeService = make(map[string][]CNode, 1)
	clsCfg.mapClusterServiceNode = make(map[string][]CNode, 1)

	//1.加载解析配置
	d, err := ioutil.ReadFile(path)
	if err != nil {
		fmt.Printf("Read File %s Error!", path)
		return nil, err
	}

	err = json.Unmarshal(d, clsCfg)
	if err != nil {
		fmt.Printf("Read File %s ,%s Error!", path, err)
		return nil, err
	}

	//存储所有的nodeid对应cnode信息
	var clusterNodeNameList []string
	for _, c := range clsCfg.SubNet {
		for _, v := range c.NodeList {
			mapservice := make(map[string]bool, 1)
			for _, s := range v.ServiceList {
				mapservice[s] = true
			}
			nodeData, ok := mapNodeData[v.NodeID]
			if ok == false {
				return nil, errors.New(fmt.Sprintf("Cannot find node id %d in nodeconfig.json file!", v.NodeID))
			}

			node := CNode{c.SubNetName, v.NodeID, c.SubNetName + "." + v.NodeName, nodeData.NodeAddr, mapservice}
			clsCfg.mapIdNode[v.NodeID] = node

			if v.NodeID == nodeid {
				clsCfg.currentNode = node
				for _, servicename := range c.PublicServiceList {
					clsCfg.currentNode.ServiceList[servicename] = true
				}

				for _, nodename := range v.ClusterNode {
					AddCluster(&clusterNodeNameList, GenNodeName(c.SubNetName, nodename))
				}
				AddCluster(&clusterNodeNameList, GenNodeName(c.SubNetName, v.NodeName))
			}
		}
	}

	if clsCfg.currentNode.NodeID == 0 {
		return nil, errors.New(fmt.Sprintf("Cannot find NodeId %d in cluster.json!", nodeid))
	}

	//如果集群是FULL模式
	if strings.ToUpper(clsCfg.GetClusterMode()) == "FULL" {
		for _, subnet := range clsCfg.SubNet {
			if subnet.SubNetName == clsCfg.currentNode.SubNetName {
				for _, nodes := range subnet.NodeList {
					AddCluster(&clusterNodeNameList, GenNodeName(subnet.SubNetName, nodes.NodeName))
				}
			}
		}
	}

	for _, clusternodename := range clusterNodeNameList {
		for _, c := range clsCfg.SubNet {
			for _, nodecfg := range c.NodeList {
				if clusternodename != c.SubNetName+"."+nodecfg.NodeName {
					continue
				}
				n, ok := clsCfg.mapIdNode[nodecfg.NodeID]
				if ok == false {
					return nil, errors.New(fmt.Sprintf("Cannot find NodeId %d in cluster.json!", nodecfg.NodeID))
				}
				clsCfg.mapClusterNodeService[nodecfg.NodeName] = append(clsCfg.mapClusterNodeService[nodecfg.NodeName], n)
				for _, sname := range nodecfg.ServiceList {
					clsCfg.mapClusterServiceNode[sname] = append(clsCfg.mapClusterServiceNode[sname], n)
				}
			}
		}
	}

	return clsCfg, nil
}

func (slf *ClusterConfig) GetIdByService(serviceName, subNetName string) []int {
	var nodeidlist = []int{}

	nodeList, ok := slf.mapClusterServiceNode[serviceName]
	if ok == true {
		nodeidlist = make([]int, 0, len(nodeList))
		for _, v := range nodeList {
			if subNetName == "" || subNetName == v.SubNetName {
				nodeidlist = append(nodeidlist, v.NodeID)
			}
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

func (slf *ClusterConfig) GetClusterMode() string {
	//SubNet []SubNetNodeInfo
	for _, subnet := range slf.SubNet {
		if subnet.SubNetName == slf.currentNode.SubNetName {
			return subnet.SubNetMode
		}
	}

	return ""
}

type CACfg struct {
	CertFile string
	KeyFile  string
}

//NodeConfig ...
type NodeData struct {
	NodeID   int
	LogLevel uint
	HttpPort uint16
	WSPort   uint16
	NodeAddr string
	CAFile   []CACfg

	Environment     string
	IsListenLog     int
	IsSendErrorMail int
}

type NodeConfig struct {
	Public   NodeData
	NodeList []NodeData
}

//ReadNodeConfig ...
func ReadAllNodeConfig(path string) (map[int]NodeData, error) {
	c := &NodeConfig{}
	d, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, err
	}
	err = json.Unmarshal(d, c)
	if err != nil {
		return nil, err
	}

	var mapNodeData map[int]NodeData
	mapNodeData = map[int]NodeData{}

	//data = c.Public
	for _, v := range c.NodeList {
		mapNodeData[v.NodeID] = v
	}

	return mapNodeData, nil
}
func ReadNodeConfig(path string, nodeid int) (*NodeData, error) {

	c := &NodeConfig{}
	d, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, err
	}
	err = json.Unmarshal(d, c)
	if err != nil {
		return nil, err
	}

	var data NodeData
	data = c.Public
	for _, v := range c.NodeList {
		if v.NodeID == nodeid {
			data = v
			if v.Environment == "" && c.Public.Environment != "" {
				data.Environment = c.Public.Environment
			}
			if len(v.CAFile) == 0 && len(c.Public.CAFile) != 0 {
				data.CAFile = c.Public.CAFile
			}

			if v.HttpPort == 0 && c.Public.HttpPort != 0 {
				data.HttpPort = c.Public.HttpPort
			}
			if v.WSPort == 0 && c.Public.WSPort != 0 {
				data.WSPort = c.Public.WSPort
			}
			if v.LogLevel == 0 && c.Public.LogLevel != 0 {
				data.LogLevel = c.Public.LogLevel
			}
			if v.IsListenLog == 0 && c.Public.IsListenLog != 0 {
				data.IsListenLog = c.Public.IsListenLog
			}
			break
		}
	}

	return &data, nil
}
