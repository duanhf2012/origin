package cluster

import (
	"fmt"
	"github.com/duanhf2012/origin/log"
	"github.com/duanhf2012/origin/rpc"
	"github.com/duanhf2012/origin/service"
	"time"
)

const maxTryCount = 30             //最大重试次数
const perTrySecond = 2*time.Second //每次重试间隔2秒
const DynamicDiscoveryMasterName = "DiscoveryMaster"
const DynamicDiscoveryClientName = "DiscoveryClient"
const RegServiceDiscover = DynamicDiscoveryMasterName+".RPC_RegServiceDiscover"
const SubServiceDiscover = DynamicDiscoveryClientName+".RPC_SubServiceDiscover"
const AddSubServiceDiscover = DynamicDiscoveryMasterName+".RPC_AddSubServiceDiscover"

type DynamicDiscoveryMaster struct {
	service.Service

	mapNodeInfo map[int32]struct{}
	nodeInfo             []*rpc.NodeInfo
}

type DynamicDiscoveryClient struct {
	service.Service

	funDelService FunDelNode
	funSetService FunSetNodeInfo
	localNodeId int
}


var masterService DynamicDiscoveryMaster
var clientService DynamicDiscoveryClient

func getDynamicDiscovery() IServiceDiscovery{
	return &clientService
}

func init(){
	masterService.SetName(DynamicDiscoveryMasterName)
	clientService.SetName(DynamicDiscoveryClientName)
}

func (ds *DynamicDiscoveryMaster) addNodeInfo(nodeInfo *rpc.NodeInfo){
	if len(nodeInfo.PublicServiceList)==0 {
		return
	}

	_,ok := ds.mapNodeInfo[nodeInfo.NodeId]
	if ok == true {
		return
	}
	ds.mapNodeInfo[nodeInfo.NodeId] = struct{}{}
	ds.nodeInfo = append(ds.nodeInfo,nodeInfo)
}

func (ds *DynamicDiscoveryMaster) RPC_AddSubServiceDiscover(nodeInfo *rpc.NodeInfo,ret *rpc.Empty) error{
	ds.addNodeInfo(nodeInfo)
	return nil
}

func (ds *DynamicDiscoveryMaster) OnInit() error{
	ds.mapNodeInfo = make(map[int32] struct{},20)
	ds.RegRpcListener(ds)

	return nil
}

func (ds *DynamicDiscoveryMaster) OnStart(){
	var nodeInfo rpc.NodeInfo
	localNodeInfo := cluster.GetLocalNodeInfo()
	if localNodeInfo.Private == true {
		return
	}

	nodeInfo.NodeId = int32(localNodeInfo.NodeId)
	nodeInfo.NodeName = localNodeInfo.NodeName
	nodeInfo.ListenAddr = localNodeInfo.ListenAddr
	nodeInfo.PublicServiceList = localNodeInfo.PublicServiceList

	ds.addNodeInfo(&nodeInfo)
}

func (ds *DynamicDiscoveryMaster) OnNodeConnected(nodeId int){
	//向它发布所有服务列表信息
	var notifyDiscover rpc.SubscribeDiscoverNotify
	notifyDiscover.IsFull = true
	notifyDiscover.NodeInfo = ds.nodeInfo
	notifyDiscover.MasterNodeId = int32(cluster.GetLocalNodeInfo().NodeId)
	ds.GoNode(nodeId,SubServiceDiscover,&notifyDiscover)
}

func (ds *DynamicDiscoveryMaster) OnNodeDisconnect(nodeId int){
	var notifyDiscover rpc.SubscribeDiscoverNotify
	notifyDiscover.MasterNodeId = int32(cluster.GetLocalNodeInfo().NodeId)
	notifyDiscover.DelNodeId = int32(nodeId)
	//删除结点
	cluster.DelNode(nodeId,true)
	ds.CastGo(SubServiceDiscover,&notifyDiscover)
}

// 收到注册过来的结点
func (ds *DynamicDiscoveryMaster) RPC_RegServiceDiscover(req *rpc.ServiceDiscoverReq, res *rpc.Empty) error{
	if req.NodeInfo == nil {
		err := fmt.Errorf("RPC_RegServiceDiscover req is error.")
		log.Error(err.Error())

		return err
	}

	//广播给其他所有结点
	var notifyDiscover rpc.SubscribeDiscoverNotify
	notifyDiscover.MasterNodeId = int32(cluster.GetLocalNodeInfo().NodeId)
	notifyDiscover.NodeInfo = append(notifyDiscover.NodeInfo,req.NodeInfo)
	ds.CastGo(SubServiceDiscover,&notifyDiscover)

	//存入本地
	ds.addNodeInfo(req.NodeInfo)

	//初始化结点信息
	var nodeInfo NodeInfo
	nodeInfo.NodeId = int(req.NodeInfo.NodeId)
	nodeInfo.NodeName = req.NodeInfo.NodeName
	nodeInfo.Private = req.NodeInfo.Private
	nodeInfo.ServiceList = req.NodeInfo.PublicServiceList
	nodeInfo.PublicServiceList = req.NodeInfo.PublicServiceList
	nodeInfo.ListenAddr = req.NodeInfo.ListenAddr

	//主动删除已经存在的结点,确保先断开，再连接
	cluster.serviceDiscoveryDelNode(nodeInfo.NodeId,true)

	//加入到本地Cluster模块中，将连接该结点
	//如果本结点不为master结点，而且没有可使用的服务，不加入
	cluster.serviceDiscoverySetNodeInfo(&nodeInfo)

	return nil
}

func (dc *DynamicDiscoveryClient) OnInit() error{
	dc.RegRpcListener(dc)
	return nil
}

func (dc *DynamicDiscoveryClient) OnStart(){
	//2.添加并连接发现主结点
	dc.addDiscoveryMaster()
}

func (dc *DynamicDiscoveryClient)  addDiscoveryMaster(){
	discoveryNodeList := cluster.GetDiscoveryNodeList()
	for i:=0;i<len(discoveryNodeList);i++ {
		if discoveryNodeList[i].NodeId == cluster.GetLocalNodeInfo().NodeId {
			continue
		}
		dc.funSetService(&discoveryNodeList[i])
	}
}

//订阅发现的服务通知
func (dc *DynamicDiscoveryClient) RPC_SubServiceDiscover(req *rpc.SubscribeDiscoverNotify) error {
	DiscoveryNodeInfo := cluster.GetMasterDiscoveryNodeInfo(int(req.MasterNodeId))
	mapMasterDiscoveryService := map[string]struct{}{}
	if DiscoveryNodeInfo != nil {
		for i := 0; i < len(DiscoveryNodeInfo.NeighborService); i++ {
			mapMasterDiscoveryService[DiscoveryNodeInfo.NeighborService[i]] = struct{}{}
		}
	}

	mapNodeInfo := map[int32]*rpc.NodeInfo{}
	//如果Master没有配置发现的服务
	if len(mapMasterDiscoveryService) == 0 {
		//如果为完整同步，则找出差异的结点
		var willDelNodeId []int
		if req.IsFull {
			mapNodeId := make(map[int32]struct{}, len(req.NodeInfo))
			for _, nodeInfo := range req.NodeInfo {
				mapNodeId[nodeInfo.NodeId] = struct{}{}
			}

			cluster.FetchAllNodeId(func(nodeId int) {
				if nodeId != dc.localNodeId {
					if _, ok := mapNodeId[int32(nodeId)]; ok == false {
						willDelNodeId = append(willDelNodeId, nodeId)
					}
				}
			})
		}

		//忽略本地结点
		if req.DelNodeId != int32(dc.localNodeId) && req.DelNodeId > 0 {
			willDelNodeId = append(willDelNodeId, int(req.DelNodeId))
		}

		//删除不必要的结点
		for _, nodeId := range willDelNodeId {
			dc.funDelService(nodeId, false)
		}
	}

	for _, nodeInfo := range req.NodeInfo {
		//不对本地结点或者不存在任何公开服务的结点
		if int(nodeInfo.NodeId) == dc.localNodeId {
			continue
		}

		//遍历所有的公开服务，并筛选之
		for _, serviceName := range nodeInfo.PublicServiceList {
			//只有存在配置时才做筛选
			if len(mapMasterDiscoveryService)>0 {
				if _, ok := mapMasterDiscoveryService[serviceName]; ok == false {
					continue
				}
			}

			nInfo := mapNodeInfo[nodeInfo.NodeId]
			if nInfo == nil {
				nInfo = &rpc.NodeInfo{}
				nInfo.NodeId = nodeInfo.NodeId
				nInfo.NodeName = nodeInfo.NodeName
				nInfo.ListenAddr = nodeInfo.ListenAddr
				mapNodeInfo[nodeInfo.NodeId] = nInfo
			}

			nInfo.PublicServiceList = append(nInfo.PublicServiceList, serviceName)
		}
	}



	//设置新结点
	for _, nodeInfo := range mapNodeInfo {
		dc.setNodeInfo(nodeInfo)
	}


	return nil
}

func (dc *DynamicDiscoveryClient) isDiscoverNode(nodeId int) bool{
	for i:=0;i< len(cluster.masterDiscoveryNodeList);i++{
		if cluster.masterDiscoveryNodeList[i].NodeId == nodeId {
			return true
		}
	}

	return false
}

func (dc *DynamicDiscoveryClient) OnNodeConnected(nodeId int) {
	nodeInfo := cluster.GetMasterDiscoveryNodeInfo(nodeId)
	if nodeInfo == nil {
		return
	}

	var req rpc.ServiceDiscoverReq
	req.NodeInfo = &rpc.NodeInfo{}
	req.NodeInfo.NodeId = int32(cluster.localNodeInfo.NodeId)
	req.NodeInfo.NodeName = cluster.localNodeInfo.NodeName
	req.NodeInfo.ListenAddr = cluster.localNodeInfo.ListenAddr
	//DiscoveryNode配置中没有配置NeighborService，则同步当前结点所有服务
	if len(nodeInfo.NeighborService)==0{
		req.NodeInfo.PublicServiceList = cluster.localNodeInfo.PublicServiceList
	}else{
		req.NodeInfo.PublicServiceList = append(req.NodeInfo.PublicServiceList,DynamicDiscoveryClientName)
	}

	//向Master服务同步本Node服务信息
	err := dc.AsyncCallNode(nodeId, RegServiceDiscover, &req, func(res *rpc.Empty, err error) {
		if err != nil {
			log.Error("call %s is fail :%s", RegServiceDiscover, err.Error())
			return
		}
	})
	if err != nil {
		log.Error("call %s is fail :%s", RegServiceDiscover, err.Error())
	}
}

func (dc *DynamicDiscoveryClient) setNodeInfo(nodeInfo *rpc.NodeInfo){
	if nodeInfo==nil || nodeInfo.Private == true || int(nodeInfo.NodeId) == dc.localNodeId{
		return
	}

	//筛选关注的服务
	localNodeInfo := cluster.GetLocalNodeInfo()
	if len(localNodeInfo.DiscoveryService) >0 {
		var discoverServiceSlice  = make([]string,0,24)
		for _,pubService := range nodeInfo.PublicServiceList {
			for _, discoverService := range localNodeInfo.DiscoveryService {
				if pubService == discoverService {
					discoverServiceSlice = append(discoverServiceSlice,pubService)
				}
			}
		}
		nodeInfo.PublicServiceList = discoverServiceSlice
	}

	if len(nodeInfo.PublicServiceList)==0{
		return
	}

	if cluster.IsMasterDiscoveryNode() {
		var ret rpc.Empty
		dc.Call(AddSubServiceDiscover,nodeInfo,&ret)
	}

	var nInfo NodeInfo
	nInfo.ServiceList = nodeInfo.PublicServiceList
	nInfo.PublicServiceList = nodeInfo.PublicServiceList
	nInfo.NodeId = int(nodeInfo.NodeId)
	nInfo.NodeName = nodeInfo.NodeName
	nInfo.ListenAddr = nodeInfo.ListenAddr
	dc.funSetService(&nInfo)
}


func (dc *DynamicDiscoveryClient) OnNodeDisconnect(nodeId int){
	//将Discard结点清理
	cluster.DiscardNode(nodeId)
}

func (dc *DynamicDiscoveryClient) InitDiscovery(localNodeId int,funDelNode FunDelNode,funSetNodeInfo FunSetNodeInfo) error{
	dc.localNodeId = localNodeId
	dc.funDelService = funDelNode
	dc.funSetService = funSetNodeInfo

	return nil
}

func (dc *DynamicDiscoveryClient) OnNodeStop(){

}

