package cluster

import (
	"fmt"
	"github.com/duanhf2012/origin/rpc"
	"github.com/duanhf2012/origin/service"
	"strings"
)

var configdir = "./config/"

type SubNet struct {
	SubNetName string
	NodeList []NodeInfo
}

type NodeInfo struct {
	NodeId int
	ListenAddr string
	NodeName string
	ServiceList []string
}

type NodeRpcInfo struct {
	nodeinfo NodeInfo
	client *rpc.Client
}



var cluster Cluster

type Cluster struct {
	localsubnet SubNet         //本子网
	mapSubNetInfo map[string] SubNet //子网名称，子网信息

	mapSubNetNodeInfo map[string]map[int]NodeInfo //map[子网名称]map[NodeId]NodeInfo
	localSubNetMapNode map[int]NodeInfo           //本子网内 map[NodeId]NodeInfo
	localSubNetMapService map[string][]NodeInfo   //本子网内所有ServiceName对应的结点列表
	localNodeMapService map[string]interface{}    //本Node支持的服务
	localNodeInfo NodeInfo

	localServiceCfg map[string]interface{} //map[servicename]数据
	localNodeServiceCfg map[int]map[string]interface{}  //map[nodeid]map[servicename]数据

	mapRpc map[int] NodeRpcInfo//nodeid

	rpcServer rpc.Server
}


func SetConfigDir(cfgdir string){
	configdir = cfgdir
}

func (slf *Cluster) Init(currentNodeId int) error{
	//1.初始化配置
	err := slf.InitCfg(currentNodeId)
	if err != nil {
		return err
	}

	slf.rpcServer.Init(slf)

	//2.建议rpc连接
	slf.mapRpc = map[int] NodeRpcInfo{}
	for _,nodeinfo := range slf.localSubNetMapNode {
		rpcinfo := NodeRpcInfo{}
		rpcinfo.nodeinfo = nodeinfo
		rpcinfo.client = &rpc.Client{}
		if nodeinfo.NodeId == currentNodeId {
			rpcinfo.client.Connect("")
			//rpcinfo.client.Connect(nodeinfo.ListenAddr)
		}else{
			rpcinfo.client.Connect(nodeinfo.ListenAddr)
		}
		slf.mapRpc[nodeinfo.NodeId] = rpcinfo
	}

	return nil
}

func (slf *Cluster) FindRpcHandler(servicename string) rpc.IRpcHandler {
	pService := service.GetService(servicename)
	if pService == nil {
		return nil
	}

	return pService.GetRpcHandler()
}

func (slf *Cluster) Start() {
	slf.rpcServer.Start(slf.localNodeInfo.ListenAddr)
}


func GetCluster() *Cluster{
	return &cluster
}

func (slf *Cluster) GetRpcClient(nodeid int) *rpc.Client {
	c,ok := slf.mapRpc[nodeid]
	if ok == false {
		return nil
	}

	return c.client
}

func GetRpcClient(nodeId int,serviceMethod string,clientList *[]*rpc.Client) error {
	if nodeId>0 {
		pClient := GetCluster().GetRpcClient(nodeId)
		if pClient==nil {
			return fmt.Errorf("cannot find  nodeid %d!",nodeId)
		}
		*clientList = append(*clientList,pClient)
		return nil
	}

	serviceAndMethod := strings.Split(serviceMethod,".")
	if len(serviceAndMethod)!=2 {
		return fmt.Errorf("servicemethod param  %s is error!",serviceMethod)
	}

	//1.找到对应的rpcnodeid
	GetCluster().GetNodeIdByService(serviceAndMethod[0],clientList)
	return nil
}

func GetRpcServer() *rpc.Server{
	return &cluster.rpcServer
}

func (slf *Cluster) IsNodeConnected (nodeId int) bool {
	pClient := slf.GetRpcClient(nodeId)
	return pClient!=nil && pClient.IsConnected()
}
