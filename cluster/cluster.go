package cluster

import (
	"fmt"
	"github.com/duanhf2012/origin/log"
	"github.com/duanhf2012/origin/rpc"
	"github.com/duanhf2012/origin/service"
	"strings"
	"sync"
)

var configDir = "./config/"


type NodeInfo struct {
	NodeId int
	NodeName string
	Private bool
	ListenAddr string
	ServiceList []string
}

type NodeRpcInfo struct {
	nodeInfo NodeInfo
	client *rpc.Client
}

var cluster Cluster
type Cluster struct {
	localNodeInfo NodeInfo
	localServiceCfg map[string]interface{} //map[serviceName]配置数据*
	mapRpc map[int] NodeRpcInfo            //nodeId
	serviceDiscovery IServiceDiscovery     //服务发现接口
	mapIdNode map[int]NodeInfo             //map[NodeId]NodeInfo
	mapServiceNode map[string][]int        //map[serviceName]NodeInfo
	locker sync.RWMutex
	rpcServer rpc.Server

	rpcListerList []rpc.IRpcListener
}

func SetConfigDir(cfgDir string){
	configDir = cfgDir
}

func SetServiceDiscovery(serviceDiscovery IServiceDiscovery) {
	cluster.serviceDiscovery = serviceDiscovery
}

func (cls *Cluster) serviceDiscoveryDelNode (nodeId int){
	cls.locker.Lock()
	defer cls.locker.Unlock()

	cls.delNode(nodeId)
}

func (cls *Cluster) delNode(nodeId int){
	//删除rpc连接关系
	rpc,ok := cls.mapRpc[nodeId]
	if ok == true {
		delete(cls.mapRpc,nodeId)
		rpc.client.Close(false)
	}

	nodeInfo,ok := cls.mapIdNode[nodeId]
	if ok == false {
		return
	}

	for _,serviceName := range nodeInfo.ServiceList{
		cls.delServiceNode(serviceName,nodeId)
	}

	delete(cls.mapIdNode,nodeId)
}

func (cls *Cluster) delServiceNode(serviceName string,nodeId int){
	nodeList := cls.mapServiceNode[serviceName]
	for idx,nId := range nodeList {
		if nId == nodeId {
			cls.mapServiceNode[serviceName] = append(nodeList[:idx],nodeList[idx+1:]...)
			return
		}
	}
}

func (cls *Cluster) serviceDiscoverySetNodeInfo (nodeInfo *NodeInfo){
	if nodeInfo.NodeId == cls.localNodeInfo.NodeId || len(nodeInfo.ServiceList)==0 || nodeInfo.Private == true {
		return
	}

	cls.locker.Lock()
	defer cls.locker.Unlock()

	//先清理删除
	cls.delNode(nodeInfo.NodeId)

	//再重新组装
	mapDuplicate := map[string]interface{}{} //预防重复数据
	for _,serviceName := range nodeInfo.ServiceList {
		if _,ok :=  mapDuplicate[serviceName];ok == true {
			//存在重复
			log.Error("Bad duplicate Service Cfg.")
			continue
		}
		mapDuplicate[serviceName] = nil
		cls.mapServiceNode[serviceName] = append(cls.mapServiceNode[serviceName],nodeInfo.NodeId)
	}

	cls.mapIdNode[nodeInfo.NodeId] = *nodeInfo
	rpcInfo := NodeRpcInfo{}
	rpcInfo.nodeInfo = *nodeInfo
	rpcInfo.client = &rpc.Client{}
	rpcInfo.client.TriggerRpcEvent = cls.triggerRpcEvent
	rpcInfo.client.Connect(nodeInfo.NodeId,nodeInfo.ListenAddr)
	cls.mapRpc[nodeInfo.NodeId] = rpcInfo
}

func (cls *Cluster) buildLocalRpc(){
	rpcInfo := NodeRpcInfo{}
	rpcInfo.nodeInfo = cls.localNodeInfo
	rpcInfo.client = &rpc.Client{}
	rpcInfo.client.Connect(rpcInfo.nodeInfo.NodeId,"")

	cls.mapRpc[cls.localNodeInfo.NodeId] = rpcInfo
}

func (cls *Cluster) Init(localNodeId int) error{
	cls.locker.Lock()

	//1.处理服务发现接口
	if cls.serviceDiscovery == nil {
		cls.serviceDiscovery = &ConfigDiscovery{}
	}

	//2.初始化配置
	err := cls.InitCfg(localNodeId)
	if err != nil {
		cls.locker.Unlock()
		return err
	}

	cls.rpcServer.Init(cls)
	cls.buildLocalRpc()

	cls.serviceDiscovery.RegFunDelNode(cls.serviceDiscoveryDelNode)
	cls.serviceDiscovery.RegFunSetNode(cls.serviceDiscoverySetNodeInfo)
	cls.locker.Unlock()

	err = cls.serviceDiscovery.Init(localNodeId)
	if err != nil {
		return err
	}

	return nil
}

func (cls *Cluster) FindRpcHandler(serviceName string) rpc.IRpcHandler {
	pService := service.GetService(serviceName)
	if pService == nil {
		return nil
	}

	return pService.GetRpcHandler()
}

func (cls *Cluster) Start() {
	cls.rpcServer.Start(cls.localNodeInfo.ListenAddr)
}

func (cls *Cluster) Stop() {
	cls.serviceDiscovery.OnNodeStop()
}

func GetCluster() *Cluster{
	return &cluster
}

func (cls *Cluster) GetRpcClient(nodeId int) *rpc.Client {
	cls.locker.RLock()
	defer cls.locker.RUnlock()
	c,ok := cls.mapRpc[nodeId]
	if ok == false {
		return nil
	}

	return c.client
}

func GetRpcClient(nodeId int,serviceMethod string,clientList []*rpc.Client) (error,int) {
	if nodeId>0 {
		pClient := GetCluster().GetRpcClient(nodeId)
		if pClient==nil {
			return fmt.Errorf("cannot find  nodeid %d!",nodeId),0
		}
		clientList[0] = pClient
		return nil,1
	}


	findIndex := strings.Index(serviceMethod,".")
	if findIndex==-1 {
		return fmt.Errorf("servicemethod param  %s is error!",serviceMethod),0
	}
	serviceName := serviceMethod[:findIndex]

	//1.找到对应的rpcNodeid
	return GetCluster().GetNodeIdByService(serviceName,clientList,true)
}

func GetRpcServer() *rpc.Server{
	return &cluster.rpcServer
}

func (cls *Cluster) IsNodeConnected (nodeId int) bool {
	pClient := cls.GetRpcClient(nodeId)
	return pClient!=nil && pClient.IsConnected()
}

func (cls *Cluster) RegisterRpcListener (rpcLister rpc.IRpcListener) {
	cls.rpcListerList = append(cls.rpcListerList,rpcLister)
}

func (cls *Cluster) triggerRpcEvent (bConnect bool,nodeId int) {
	for _,lister := range cls.rpcListerList {
		if bConnect {
			lister.OnRpcConnected(nodeId)
		}else{
			lister.OnRpcDisconnect(nodeId)
		}
	}
}

