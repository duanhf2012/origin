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

type SetupServiceFun func(s ...service.IService)

type NodeStatus int

const (
	Normal  NodeStatus = 0 //正常
	Discard NodeStatus = 1 //丢弃
)

type MasterDiscoveryService struct {
	MasterNodeId int32 //要筛选的主结点Id，如果不配置或者配置成0，表示针对所有的主结点
	DiscoveryService  []string  //只发现的服务列表
}

type NodeInfo struct {
	NodeId            int
	NodeName          string
	Private           bool
	ListenAddr        string
	MaxRpcParamLen    uint32   //最大Rpc参数长度
	CompressBytesLen  int   //超过字节进行压缩的长度
	ServiceList  	  []string //所有的有序服务列表
	PublicServiceList []string //对外公开的服务列表
	MasterDiscoveryService  []MasterDiscoveryService //筛选发现的服务，如果不配置，不进行筛选
	status            NodeStatus
	Retire bool
}

type NodeRpcInfo struct {
	nodeInfo NodeInfo
	client   *rpc.Client
}

var cluster Cluster

type Cluster struct {
	localNodeInfo           NodeInfo    //本结点配置信息
	masterDiscoveryNodeList []NodeInfo  //配置发现Master结点
	globalCfg               interface{} //全局配置

	localServiceCfg  map[string]interface{} //map[serviceName]配置数据*
	serviceDiscovery IServiceDiscovery      //服务发现接口


	locker         sync.RWMutex                //结点与服务关系保护锁
	mapRpc           map[int]*NodeRpcInfo    //nodeId
	//mapIdNode      map[int]NodeInfo            //map[NodeId]NodeInfo
	mapServiceNode map[string]map[int]struct{} //map[serviceName]map[NodeId]

	rpcServer                rpc.Server
	rpcEventLocker           sync.RWMutex        //Rpc事件监听保护锁
	mapServiceListenRpcEvent map[string]struct{} //ServiceName
	mapServiceListenDiscoveryEvent map[string]struct{} //ServiceName
}

func GetCluster() *Cluster {
	return &cluster
}

func SetConfigDir(cfgDir string) {
	configDir = cfgDir
}

func SetServiceDiscovery(serviceDiscovery IServiceDiscovery) {
	cluster.serviceDiscovery = serviceDiscovery
}

func (cls *Cluster) Start() {
	cls.rpcServer.Start(cls.localNodeInfo.ListenAddr, cls.localNodeInfo.MaxRpcParamLen,cls.localNodeInfo.CompressBytesLen)
}

func (cls *Cluster) Stop() {
	cls.serviceDiscovery.OnNodeStop()
}

func (cls *Cluster) DiscardNode(nodeId int) {
	cls.locker.Lock()
	nodeInfo, ok := cls.mapRpc[nodeId]
	bDel := (ok == true) &&  nodeInfo.nodeInfo.status == Discard
	cls.locker.Unlock()

	if bDel {
		cls.DelNode(nodeId, true)
	}
}

func (cls *Cluster) DelNode(nodeId int, immediately bool) {
	//MasterDiscover结点与本地结点不删除
	if cls.GetMasterDiscoveryNodeInfo(nodeId) != nil || nodeId == cls.localNodeInfo.NodeId {
		return
	}
	cls.locker.Lock()
	defer cls.locker.Unlock()

	rpc, ok := cls.mapRpc[nodeId]
	if ok == false {
		return
	}

	if immediately ==false {
		//正在连接中不主动断开，只断开没有连接中的
		if rpc.client.IsConnected() {
			rpc.nodeInfo.status = Discard
			log.Info("Discard node",log.Int("nodeId",rpc.nodeInfo.NodeId),log.String("ListenAddr", rpc.nodeInfo.ListenAddr))
			return
		}
	}

	for _, serviceName := range rpc.nodeInfo.ServiceList {
		cls.delServiceNode(serviceName, nodeId)
	}

	delete(cls.mapRpc, nodeId)
	if ok == true {
		rpc.client.Close(false)
	}

	log.Info("remove node ",log.Int("NodeId", rpc.nodeInfo.NodeId),log.String("ListenAddr", rpc.nodeInfo.ListenAddr))
}

func (cls *Cluster) serviceDiscoveryDelNode(nodeId int, immediately bool) {
	if nodeId == 0 {
		return
	}

	cls.DelNode(nodeId, immediately)
}

func (cls *Cluster) delServiceNode(serviceName string, nodeId int) {
	if nodeId == cls.localNodeInfo.NodeId {
		return
	}

	mapNode := cls.mapServiceNode[serviceName]
	delete(mapNode, nodeId)
	if len(mapNode) == 0 {
		delete(cls.mapServiceNode, serviceName)
	}
}

func (cls *Cluster) serviceDiscoverySetNodeInfo(nodeInfo *NodeInfo) {
	//本地结点不加入
	if nodeInfo.NodeId == cls.localNodeInfo.NodeId {
		return
	}

	cls.locker.Lock()
	defer cls.locker.Unlock()

	//先清一次的NodeId对应的所有服务清理
	lastNodeInfo, ok := cls.mapRpc[nodeInfo.NodeId]
	if ok == true {
		for _, serviceName := range lastNodeInfo.nodeInfo.ServiceList {
			cls.delServiceNode(serviceName, nodeInfo.NodeId)
		}
	}

	//再重新组装
	mapDuplicate := map[string]interface{}{} //预防重复数据
	for _, serviceName := range nodeInfo.PublicServiceList {
		if _, ok := mapDuplicate[serviceName]; ok == true {
			//存在重复
			log.Error("Bad duplicate Service Cfg.")
			continue
		}
		mapDuplicate[serviceName] = nil
		if _, ok := cls.mapServiceNode[serviceName]; ok == false {
			cls.mapServiceNode[serviceName] = make(map[int]struct{}, 1)
		}
		cls.mapServiceNode[serviceName][nodeInfo.NodeId] = struct{}{}
	}

	if lastNodeInfo != nil {
		log.Info("Discovery nodeId",log.Int("NodeId", nodeInfo.NodeId),log.Any("services:", nodeInfo.PublicServiceList),log.Bool("Retire",nodeInfo.Retire))
		lastNodeInfo.nodeInfo = *nodeInfo
		return
	}

	//不存在时，则建立连接
	rpcInfo := NodeRpcInfo{}
	rpcInfo.nodeInfo = *nodeInfo
	rpcInfo.client =rpc.NewRClient(nodeInfo.NodeId, nodeInfo.ListenAddr, nodeInfo.MaxRpcParamLen,cls.localNodeInfo.CompressBytesLen,cls.triggerRpcEvent)
	cls.mapRpc[nodeInfo.NodeId] = &rpcInfo
	log.Info("Discovery nodeId and new rpc client",log.Int("NodeId", nodeInfo.NodeId),log.Any("services:", nodeInfo.PublicServiceList),log.Bool("Retire",nodeInfo.Retire),log.String("nodeListenAddr",nodeInfo.ListenAddr))
}



func (cls *Cluster) Init(localNodeId int, setupServiceFun SetupServiceFun) error {
	//1.初始化配置
	err := cls.InitCfg(localNodeId)
	if err != nil {
		return err
	}

	cls.rpcServer.Init(cls)

	//2.安装服务发现结点
	cls.SetupServiceDiscovery(localNodeId, setupServiceFun)
	service.RegRpcEventFun = cls.RegRpcEvent
	service.UnRegRpcEventFun = cls.UnRegRpcEvent
	service.RegDiscoveryServiceEventFun = cls.RegDiscoveryEvent
	service.UnRegDiscoveryServiceEventFun = cls.UnReDiscoveryEvent

	err = cls.serviceDiscovery.InitDiscovery(localNodeId, cls.serviceDiscoveryDelNode, cls.serviceDiscoverySetNodeInfo)
	if err != nil {
		return err
	}

	return nil
}

func (cls *Cluster) checkDynamicDiscovery(localNodeId int) (bool, bool) {
	var localMaster bool //本结点是否为Master结点
	var hasMaster bool   //是否配置Master服务

	//遍历所有结点
	for _, nodeInfo := range cls.masterDiscoveryNodeList {
		if nodeInfo.NodeId == localNodeId {
			localMaster = true
		}
		hasMaster = true
	}

	//返回查询结果
	return localMaster, hasMaster
}

func (cls *Cluster) AddDynamicDiscoveryService(serviceName string, bPublicService bool) {
	addServiceList := append([]string{},serviceName)
	cls.localNodeInfo.ServiceList = append(addServiceList,cls.localNodeInfo.ServiceList...)
	if bPublicService {
		cls.localNodeInfo.PublicServiceList = append(cls.localNodeInfo.PublicServiceList, serviceName)
	}

	if _, ok := cls.mapServiceNode[serviceName]; ok == false {
		cls.mapServiceNode[serviceName] = map[int]struct{}{}
	}
	cls.mapServiceNode[serviceName][cls.localNodeInfo.NodeId] = struct{}{}
}

func (cls *Cluster) GetDiscoveryNodeList() []NodeInfo {
	return cls.masterDiscoveryNodeList
}

func (cls *Cluster) GetMasterDiscoveryNodeInfo(nodeId int) *NodeInfo {
	for i := 0; i < len(cls.masterDiscoveryNodeList); i++ {
		if cls.masterDiscoveryNodeList[i].NodeId == nodeId {
			return &cls.masterDiscoveryNodeList[i]
		}
	}

	return nil
}

func (cls *Cluster) IsMasterDiscoveryNode() bool {
	return cls.GetMasterDiscoveryNodeInfo(cls.GetLocalNodeInfo().NodeId) != nil
}

func (cls *Cluster) SetupServiceDiscovery(localNodeId int, setupServiceFun SetupServiceFun) {
	if cls.serviceDiscovery != nil {
		return
	}

	//1.如果没有配置DiscoveryNode配置，则使用默认配置文件发现服务
	localMaster, hasMaster := cls.checkDynamicDiscovery(localNodeId)
	if hasMaster == false {
		cls.serviceDiscovery = &ConfigDiscovery{}
		return
	}
	setupServiceFun(&masterService, &clientService)

	//2.如果为动态服务发现安装本地发现服务
	cls.serviceDiscovery = getDynamicDiscovery()
	cls.AddDynamicDiscoveryService(DynamicDiscoveryClientName, true)
	if localMaster == true {
		cls.AddDynamicDiscoveryService(DynamicDiscoveryMasterName, false)
	}
}

func (cls *Cluster) FindRpcHandler(serviceName string) rpc.IRpcHandler {
	pService := service.GetService(serviceName)
	if pService == nil {
		return nil
	}

	return pService.GetRpcHandler()
}

func (cls *Cluster) getRpcClient(nodeId int) (*rpc.Client,bool) {
	c, ok := cls.mapRpc[nodeId]
	if ok == false {
		return nil,false
	}

	return c.client,c.nodeInfo.Retire
}

func (cls *Cluster) GetRpcClient(nodeId int) (*rpc.Client,bool) {
	cls.locker.RLock()
	defer cls.locker.RUnlock()
	return cls.getRpcClient(nodeId)
}

func GetRpcClient(nodeId int, serviceMethod string,filterRetire bool, clientList []*rpc.Client) (error, int) {
	if nodeId > 0 {
		pClient,retire := GetCluster().GetRpcClient(nodeId)
		if pClient == nil {
			return fmt.Errorf("cannot find  nodeid %d!", nodeId), 0
		}

		//如果需要筛选掉退休结点
		if filterRetire == true && retire == true {
			return fmt.Errorf("cannot find  nodeid %d!", nodeId), 0
		}

		clientList[0] = pClient
		return nil, 1
	}

	findIndex := strings.Index(serviceMethod, ".")
	if findIndex == -1 {
		return fmt.Errorf("servicemethod param  %s is error!", serviceMethod), 0
	}
	serviceName := serviceMethod[:findIndex]

	//1.找到对应的rpcNodeid
	return GetCluster().GetNodeIdByService(serviceName, clientList, filterRetire)
}

func GetRpcServer() *rpc.Server {
	return &cluster.rpcServer
}

func (cls *Cluster) IsNodeConnected(nodeId int) bool {
	pClient,_ := cls.GetRpcClient(nodeId)
	return pClient != nil && pClient.IsConnected()
}

func (cls *Cluster) triggerRpcEvent(bConnect bool, clientId uint32, nodeId int) {
	cls.locker.Lock()
	nodeInfo, ok := cls.mapRpc[nodeId]
	if ok == false || nodeInfo.client == nil || nodeInfo.client.GetClientId() != clientId {
		cls.locker.Unlock()
		return
	}
	cls.locker.Unlock()

	cls.rpcEventLocker.Lock()
	defer cls.rpcEventLocker.Unlock()
	for serviceName, _ := range cls.mapServiceListenRpcEvent {
		ser := service.GetService(serviceName)
		if ser == nil {
			log.Error("cannot find service name "+serviceName)
			continue
		}

		var eventData service.RpcConnEvent
		eventData.IsConnect = bConnect
		eventData.NodeId = nodeId
		ser.(service.IModule).NotifyEvent(&eventData)
	}
}

func (cls *Cluster) TriggerDiscoveryEvent(bDiscovery bool, nodeId int, serviceName []string) {
	cls.rpcEventLocker.Lock()
	defer cls.rpcEventLocker.Unlock()

	for sName, _ := range cls.mapServiceListenDiscoveryEvent {
		ser := service.GetService(sName)
		if ser == nil {
			log.Error("cannot find service",log.Any("services",serviceName))
			continue
		}

		var eventData service.DiscoveryServiceEvent
		eventData.IsDiscovery = bDiscovery
		eventData.NodeId = nodeId
		eventData.ServiceName = serviceName
		ser.(service.IModule).NotifyEvent(&eventData)
	}

}

func (cls *Cluster) GetLocalNodeInfo() *NodeInfo {
	return &cls.localNodeInfo
}

func (cls *Cluster) RegRpcEvent(serviceName string) {
	cls.rpcEventLocker.Lock()
	if cls.mapServiceListenRpcEvent == nil {
		cls.mapServiceListenRpcEvent = map[string]struct{}{}
	}

	cls.mapServiceListenRpcEvent[serviceName] = struct{}{}
	cls.rpcEventLocker.Unlock()
}

func (cls *Cluster) UnRegRpcEvent(serviceName string) {
	cls.rpcEventLocker.Lock()
	delete(cls.mapServiceListenRpcEvent, serviceName)
	cls.rpcEventLocker.Unlock()
}


func (cls *Cluster) RegDiscoveryEvent(serviceName string) {
	cls.rpcEventLocker.Lock()
	if cls.mapServiceListenDiscoveryEvent == nil {
		cls.mapServiceListenDiscoveryEvent = map[string]struct{}{}
	}

	cls.mapServiceListenDiscoveryEvent[serviceName] = struct{}{}
	cls.rpcEventLocker.Unlock()
}

func (cls *Cluster) UnReDiscoveryEvent(serviceName string) {
	cls.rpcEventLocker.Lock()
	delete(cls.mapServiceListenDiscoveryEvent, serviceName)
	cls.rpcEventLocker.Unlock()
}



func HasService(nodeId int, serviceName string) bool {
	cluster.locker.RLock()
	defer cluster.locker.RUnlock()

	mapNode, _ := cluster.mapServiceNode[serviceName]
	if mapNode != nil {
		_, ok := mapNode[nodeId]
		return ok
	}

	return false
}

func GetNodeByServiceName(serviceName string) map[int]struct{} {
	cluster.locker.RLock()
	defer cluster.locker.RUnlock()

	mapNode, ok := cluster.mapServiceNode[serviceName]
	if ok == false {
		return nil
	}

	mapNodeId := map[int]struct{}{}
	for nodeId,_ := range mapNode {
		mapNodeId[nodeId] = struct{}{}
	}

	return mapNodeId
}

func (cls *Cluster) GetGlobalCfg() interface{} {
	return cls.globalCfg
}

