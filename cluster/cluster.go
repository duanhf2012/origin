package cluster

import (
	"fmt"
	"github.com/duanhf2012/origin/log"
	"github.com/duanhf2012/origin/rpc"
	"github.com/duanhf2012/origin/service"
	"strings"
	"sync"
)

var configdir = "./config/"



type NodeInfo struct {
	NodeId int
	NodeName string
	ListenAddr string
	ServiceList []string
}

type NodeRpcInfo struct {
	nodeinfo NodeInfo
	client *rpc.Client
}


var cluster Cluster

type Cluster struct {
	localNodeInfo NodeInfo //×
	localServiceCfg map[string]interface{} //map[servicename]配置数据*

	mapRpc map[int] NodeRpcInfo//nodeid
	rpcServer rpc.Server
	serviceDiscovery IServiceDiscovery //服务发现接口

	mapIdNode map[int]NodeInfo           //map[NodeId]NodeInfo
	mapServiceNode map[string][]int //map[serviceName]NodeInfo

	locker sync.RWMutex
}

func SetConfigDir(cfgdir string){
	configdir = cfgdir
}

func SetServiceDiscovery(serviceDiscovery IServiceDiscovery) {
	cluster.serviceDiscovery = serviceDiscovery
}

func (slf *Cluster) serviceDiscoveryDelNode (nodeId int){
	slf.locker.Lock()
	defer slf.locker.Unlock()

	slf.delNode(nodeId)
}

func (slf *Cluster) delNode(nodeId int){
	//删除rpc连接关系
	rpc,ok := slf.mapRpc[nodeId]
	if ok == true {
		delete(slf.mapRpc,nodeId)
		rpc.client.Close(false)
	}

	nodeInfo,ok := slf.mapIdNode[nodeId]
	if ok == false {
		return
	}

	for _,serviceName := range nodeInfo.ServiceList{
		slf.delServiceNode(serviceName,nodeId)
	}

	delete(slf.mapIdNode,nodeId)
}

func (slf *Cluster) delServiceNode(serviceName string,nodeId int){
	nodeList := slf.mapServiceNode[serviceName]
	for idx,nId := range nodeList {
		if nId == nodeId {
			slf.mapServiceNode[serviceName] = append(nodeList[idx:],nodeList[idx+1:]...)
			return
		}
	}
}


func (slf *Cluster) serviceDiscoverySetNodeInfo (nodeInfo *NodeInfo){
	if nodeInfo.NodeId == slf.localNodeInfo.NodeId {
		return
	}

	slf.locker.Lock()
	defer slf.locker.Unlock()

	//先清理删除
	slf.delNode(nodeInfo.NodeId)

	//再重新组装
	mapDuplicate := map[string]interface{}{} //预防重复数据
	for _,serviceName := range nodeInfo.ServiceList {
		if _,ok :=  mapDuplicate[serviceName];ok == true {
			//存在重复
			log.Error("Bad duplicate Service Cfg.")
			continue
		}

		slf.mapServiceNode[serviceName] = append(slf.mapServiceNode[serviceName],nodeInfo.NodeId)
	}

	slf.mapIdNode[nodeInfo.NodeId] = *nodeInfo
	rpcInfo := NodeRpcInfo{}
	rpcInfo.nodeinfo = *nodeInfo
	rpcInfo.client = &rpc.Client{}
	rpcInfo.client.Connect(nodeInfo.ListenAddr)
	slf.mapRpc[nodeInfo.NodeId] = rpcInfo
}

func (slf *Cluster) buildLocalRpc(){
	rpcInfo := NodeRpcInfo{}
	rpcInfo.nodeinfo = slf.localNodeInfo
	rpcInfo.client = &rpc.Client{}
	rpcInfo.client.Connect("")

	slf.mapRpc[slf.localNodeInfo.NodeId] = rpcInfo
}

func (slf *Cluster) Init(localNodeId int) error{
	slf.locker.Lock()


	//1.处理服务发现接口
	if slf.serviceDiscovery == nil {
		slf.serviceDiscovery = &ConfigDiscovery{}
	}

	//2.初始化配置
	err := slf.InitCfg(localNodeId)
	if err != nil {
		slf.locker.Unlock()
		return err
	}

	slf.rpcServer.Init(slf)
	slf.buildLocalRpc()

	slf.serviceDiscovery.RegFunDelNode(slf.serviceDiscoveryDelNode)
	slf.serviceDiscovery.RegFunSetNode(slf.serviceDiscoverySetNodeInfo)
	slf.locker.Unlock()

	err = slf.serviceDiscovery.Init(localNodeId)
	if err != nil {
		return err
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

func (slf *Cluster) Stop() {
	slf.serviceDiscovery.OnNodeStop()
}

func GetCluster() *Cluster{
	return &cluster
}

func (slf *Cluster) GetRpcClient(nodeid int) *rpc.Client {
	slf.locker.RLock()
	defer slf.locker.RUnlock()
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
