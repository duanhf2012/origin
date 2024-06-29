package cluster

import (
	"fmt"
	"github.com/duanhf2012/origin/v2/log"
	"github.com/duanhf2012/origin/v2/rpc"
	"github.com/duanhf2012/origin/v2/service"
	"strings"
	"sync"
	"github.com/duanhf2012/origin/v2/event"
	"errors"
	"reflect"
)

var configDir = "./config/"

type SetupServiceFun func(s ...service.IService)

type NodeStatus int

const (
	Normal  NodeStatus = 0 //正常
	Discard NodeStatus = 1 //丢弃
)

type DiscoveryService struct {
	MasterNodeId string //要筛选的主结点Id，如果不配置或者配置成0，表示针对所有的主结点
	NetworkName string  //如果是etcd，指定要筛选的网络名中的服务，不配置，表示所有的网络
	ServiceList  []string  //只发现的服务列表
}

type NodeInfo struct {
	NodeId            string
	Private           bool
	ListenAddr        string
	MaxRpcParamLen    uint32   //最大Rpc参数长度
	CompressBytesLen  int   //超过字节进行压缩的长度
	ServiceList  	  []string //所有的有序服务列表
	PublicServiceList []string //对外公开的服务列表
	DiscoveryService  []DiscoveryService //筛选发现的服务，如果不配置，不进行筛选
	status            NodeStatus
	Retire bool

	NetworkName string
}

type NodeRpcInfo struct {
	nodeInfo NodeInfo
	client   *rpc.Client
}

var cluster Cluster

type Cluster struct {
	localNodeInfo           NodeInfo    //本结点配置信息

	discoveryInfo DiscoveryInfo //服务发现配置
	rpcMode RpcMode
	globalCfg               interface{} //全局配置

	localServiceCfg  map[string]interface{} //map[serviceName]配置数据*
	serviceDiscovery IServiceDiscovery      //服务发现接口

	locker         sync.RWMutex                //结点与服务关系保护锁
	mapRpc           map[string]*NodeRpcInfo    //nodeId
	mapServiceNode map[string]map[string]struct{} //map[serviceName]map[NodeId]
	mapTemplateServiceNode map[string]map[string]struct{} //map[templateServiceName]map[serviceName]nodeId

	callSet rpc.CallSet
	rpcNats rpc.RpcNats
	rpcServer rpc.IServer

	rpcEventLocker           sync.RWMutex        //Rpc事件监听保护锁
	mapServiceListenRpcEvent map[string]struct{} //ServiceName
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

func (cls *Cluster) Start() error{
	return cls.rpcServer.Start()
}

func (cls *Cluster) Stop() {
	cls.rpcServer.Stop()
}

func (cls *Cluster) DiscardNode(nodeId string) {
	cls.locker.Lock()
	nodeInfo, ok := cls.mapRpc[nodeId]
	bDel := (ok == true) &&  nodeInfo.nodeInfo.status == Discard
	cls.locker.Unlock()

	if bDel {
		cls.DelNode(nodeId)
	}
}

func (cls *Cluster) DelNode(nodeId string) {
	//MasterDiscover结点与本地结点不删除
	if cls.IsOriginMasterDiscoveryNode(nodeId) || nodeId == cls.localNodeInfo.NodeId {
		return
	}
	cls.locker.Lock()
	defer cls.locker.Unlock()

	rpc, ok := cls.mapRpc[nodeId]
	if ok == false {
		return
	}

	cls.TriggerDiscoveryEvent(false,nodeId,rpc.nodeInfo.ServiceList)
	for _, serviceName := range rpc.nodeInfo.ServiceList {
		cls.delServiceNode(serviceName, nodeId)
	}

	delete(cls.mapRpc, nodeId)
	if ok == true {
		rpc.client.Close(false)
	}

	log.Info("remove node ",log.String("NodeId", rpc.nodeInfo.NodeId),log.String("ListenAddr", rpc.nodeInfo.ListenAddr))
}

func (cls *Cluster) serviceDiscoveryDelNode(nodeId string) {
	cls.DelNode(nodeId)
}

func (cls *Cluster) delServiceNode(serviceName string, nodeId string) {
	if nodeId == cls.localNodeInfo.NodeId {
		return
	}

	//处理模板服务
	splitServiceName := strings.Split(serviceName,":")
	if len(splitServiceName) == 2 {
		serviceName = splitServiceName[0]
		templateServiceName := splitServiceName[1]

		mapService := cls.mapTemplateServiceNode[templateServiceName]
		delete(mapService,serviceName)

		if len(cls.mapTemplateServiceNode[templateServiceName]) == 0 {
			delete(cls.mapTemplateServiceNode,templateServiceName)
		}
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

	cluster.TriggerDiscoveryEvent(true,nodeInfo.NodeId,nodeInfo.PublicServiceList)
	//再重新组装
	mapDuplicate := map[string]interface{}{} //预防重复数据
	for _, serviceName := range nodeInfo.PublicServiceList {
		if _, ok := mapDuplicate[serviceName]; ok == true {
			//存在重复
			log.Error("Bad duplicate Service Cfg.")
			continue
		}
		mapDuplicate[serviceName] = nil

		//如果是模板服务，则记录模板关系
		splitServiceName := strings.Split(serviceName,":")
		if len(splitServiceName) == 2 {
			serviceName = splitServiceName[0]
			templateServiceName := splitServiceName[1]
			//记录模板
			if _, ok = cls.mapTemplateServiceNode[templateServiceName]; ok == false {
				cls.mapTemplateServiceNode[templateServiceName]=map[string]struct{}{}
			}
			cls.mapTemplateServiceNode[templateServiceName][serviceName] = struct{}{}
		}

		if _, ok = cls.mapServiceNode[serviceName]; ok == false {
			cls.mapServiceNode[serviceName] = make(map[string]struct{}, 1)
		}
		cls.mapServiceNode[serviceName][nodeInfo.NodeId] = struct{}{}
	}

	if lastNodeInfo != nil {
		log.Info("Discovery nodeId",log.String("NodeId", nodeInfo.NodeId),log.Any("services:", nodeInfo.PublicServiceList),log.Bool("Retire",nodeInfo.Retire))
		lastNodeInfo.nodeInfo = *nodeInfo
		return
	}

	//不存在时，则建立连接
	rpcInfo := NodeRpcInfo{}
	rpcInfo.nodeInfo = *nodeInfo

	if cls.IsNatsMode() {
		rpcInfo.client = cls.rpcNats.NewNatsClient(nodeInfo.NodeId, cls.GetLocalNodeInfo().NodeId,&cls.callSet,cls.NotifyAllService)
	}else{
		rpcInfo.client =rpc.NewRClient(nodeInfo.NodeId, nodeInfo.ListenAddr, nodeInfo.MaxRpcParamLen,cls.localNodeInfo.CompressBytesLen,&cls.callSet,cls.NotifyAllService)
	}
	cls.mapRpc[nodeInfo.NodeId] = &rpcInfo
	if cls.IsNatsMode() == true || cls.discoveryInfo.discoveryType!=OriginType {
		log.Info("Discovery nodeId and new rpc client",log.String("NodeId", nodeInfo.NodeId),log.Any("services:", nodeInfo.PublicServiceList),log.Bool("Retire",nodeInfo.Retire))
	}else{
		log.Info("Discovery nodeId and new rpc client",log.String("NodeId", nodeInfo.NodeId),log.Any("services:", nodeInfo.PublicServiceList),log.Bool("Retire",nodeInfo.Retire),log.String("nodeListenAddr",nodeInfo.ListenAddr))
	}
}


func (cls *Cluster) Init(localNodeId string, setupServiceFun SetupServiceFun) error {
	//1.初始化配置
	err := cls.InitCfg(localNodeId)
	if err != nil {
		return err
	}

	cls.callSet.Init()
	if cls.IsNatsMode() {
		cls.rpcNats.Init(cls.rpcMode.Nats.NatsUrl,cls.rpcMode.Nats.NoRandomize,cls.GetLocalNodeInfo().NodeId,cls.localNodeInfo.CompressBytesLen,cls,cluster.NotifyAllService)
		cls.rpcServer = &cls.rpcNats
	}else{
		s := &rpc.Server{}
		s.Init(cls.localNodeInfo.ListenAddr,cls.localNodeInfo.MaxRpcParamLen,cls.localNodeInfo.CompressBytesLen,cls)
		cls.rpcServer = s
	}

	//2.安装服务发现结点
	err = cls.setupDiscovery(localNodeId, setupServiceFun)
	if err != nil {
		log.Error("setupDiscovery fail",log.ErrorAttr("err",err))
		return err
	}
	service.RegRpcEventFun = cls.RegRpcEvent
	service.UnRegRpcEventFun = cls.UnRegRpcEvent

	err = cls.serviceDiscovery.InitDiscovery(localNodeId, cls.serviceDiscoveryDelNode, cls.serviceDiscoverySetNodeInfo)
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

func (cls *Cluster) getRpcClient(nodeId string) (*rpc.Client,bool) {
	c, ok := cls.mapRpc[nodeId]
	if ok == false {
		return nil,false
	}

	return c.client,c.nodeInfo.Retire
}

func (cls *Cluster) GetRpcClient(nodeId string) (*rpc.Client,bool) {
	cls.locker.RLock()
	defer cls.locker.RUnlock()
	return cls.getRpcClient(nodeId)
}

func GetNodeIdByTemplateService(templateServiceName string, rpcClientList []*rpc.Client, filterRetire bool) (error, []*rpc.Client) {
	return GetCluster().GetNodeIdByTemplateService(templateServiceName, rpcClientList, filterRetire)
}

func GetRpcClient(nodeId string, serviceMethod string,filterRetire bool, clientList []*rpc.Client) (error, []*rpc.Client) {
	if nodeId != rpc.NodeIdNull {
		pClient,retire := GetCluster().GetRpcClient(nodeId)
		if pClient == nil {
			return fmt.Errorf("cannot find  nodeid %d!", nodeId), nil
		}

		//如果需要筛选掉退休结点
		if filterRetire == true && retire == true {
			return fmt.Errorf("cannot find  nodeid %d!", nodeId), nil
		}

		clientList = append(clientList,pClient)
		return nil, clientList
	}

	findIndex := strings.Index(serviceMethod, ".")
	if findIndex == -1 {
		return fmt.Errorf("servicemethod param  %s is error!", serviceMethod), nil
	}
	serviceName := serviceMethod[:findIndex]

	//1.找到对应的rpcNodeid
	return GetCluster().GetNodeIdByService(serviceName, clientList, filterRetire)
}

func GetRpcServer() rpc.IServer {
	return cluster.rpcServer
}

func (cls *Cluster) IsNodeConnected(nodeId string) bool {
	pClient,_ := cls.GetRpcClient(nodeId)
	return pClient != nil && pClient.IsConnected()
}

func (cls *Cluster) IsNodeRetire(nodeId string) bool {
	cls.locker.RLock()
	defer cls.locker.RUnlock()

	_,retire :=cls.getRpcClient(nodeId)
	return retire
}

func (cls *Cluster) NotifyAllService(event event.IEvent){
	cls.rpcEventLocker.Lock()
	defer cls.rpcEventLocker.Unlock()

	for serviceName, _ := range cls.mapServiceListenRpcEvent {
		ser := service.GetService(serviceName)
		if ser == nil {
			log.Error("cannot find service name "+serviceName)
			continue
		}

		ser.(service.IModule).NotifyEvent(event)
	}
}

func (cls *Cluster) TriggerDiscoveryEvent(bDiscovery bool, nodeId string, serviceName []string) {
	var eventData service.DiscoveryServiceEvent
	eventData.IsDiscovery = bDiscovery
	eventData.NodeId = nodeId
	eventData.ServiceName = serviceName

	cls.NotifyAllService(&eventData)
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

func HasService(nodeId string, serviceName string) bool {
	cluster.locker.RLock()
	defer cluster.locker.RUnlock()

	mapNode, _ := cluster.mapServiceNode[serviceName]
	if mapNode != nil {
		_, ok := mapNode[nodeId]
		return ok
	}

	return false
}

func GetNodeByServiceName(serviceName string) map[string]struct{} {
	cluster.locker.RLock()
	defer cluster.locker.RUnlock()

	mapNode, ok := cluster.mapServiceNode[serviceName]
	if ok == false {
		return nil
	}

	mapNodeId := map[string]struct{}{}
	for nodeId,_ := range mapNode {
		mapNodeId[nodeId] = struct{}{}
	}

	return mapNodeId
}

// GetNodeByTemplateServiceName 通过模板服务名获取服务名,返回 map[serviceName真实服务名]NodeId
func GetNodeByTemplateServiceName(templateServiceName string) map[string]string {
	cluster.locker.RLock()
	defer cluster.locker.RUnlock()

	mapServiceName := cluster.mapTemplateServiceNode[templateServiceName]
	mapNodeId := make(map[string]string,9)
	for serviceName := range mapServiceName {
		mapNode, ok := cluster.mapServiceNode[serviceName]
		if ok == false {
			return nil
		}

		for nodeId,_ := range mapNode {
			mapNodeId[serviceName] = nodeId
		}
	}

	return mapNodeId
}

func (cls *Cluster) GetGlobalCfg() interface{} {
	return cls.globalCfg
}


func (cls *Cluster) ParseGlobalCfg(cfg interface{}) error{
	if  cls.globalCfg == nil {
		return errors.New("no service configuration found")
	}

	rv := reflect.ValueOf(cls.globalCfg)
	if  rv.Kind() == reflect.Ptr && rv.IsNil() {
		return errors.New("no service configuration found")
	}

	bytes,err := json.Marshal(cls.globalCfg)
	if err != nil {
		return err
	}

	return json.Unmarshal(bytes,cfg)
}

func (cls *Cluster) GetNodeInfo(nodeId string) (NodeInfo,bool) {
	cls.locker.RLock()
	defer cls.locker.RUnlock()

	nodeInfo,ok:= cls.mapRpc[nodeId]
	if ok == false || nodeInfo == nil {
		return NodeInfo{},false
	}

	return nodeInfo.nodeInfo,true
}

func (dc *Cluster) CanDiscoveryService(fromMasterNodeId string,serviceName string) bool{
	canDiscovery := true

	splitServiceName := strings.Split(serviceName,":")
	if len(splitServiceName) == 2 {
		serviceName = splitServiceName[0]
	}

	for i:=0;i<len(dc.GetLocalNodeInfo().DiscoveryService);i++{
		masterNodeId := dc.GetLocalNodeInfo().DiscoveryService[i].MasterNodeId
		//无效的配置，则跳过
		if masterNodeId == rpc.NodeIdNull && len(dc.GetLocalNodeInfo().DiscoveryService[i].ServiceList)==0 {
			continue
		}

		canDiscovery = false
		if masterNodeId == fromMasterNodeId || masterNodeId == rpc.NodeIdNull {
			for _,discoveryService := range dc.GetLocalNodeInfo().DiscoveryService[i].ServiceList {
				if discoveryService == serviceName {
					return true
				}
			}
		}
	}

	return canDiscovery
}