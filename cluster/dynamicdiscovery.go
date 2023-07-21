package cluster

import (
	"errors"
	"github.com/duanhf2012/origin/log"
	"github.com/duanhf2012/origin/rpc"
	"github.com/duanhf2012/origin/service"
	"time"
	"github.com/duanhf2012/origin/util/timer"
)

const DynamicDiscoveryMasterName = "DiscoveryMaster"
const DynamicDiscoveryClientName = "DiscoveryClient"
const RegServiceDiscover = DynamicDiscoveryMasterName + ".RPC_RegServiceDiscover"
const SubServiceDiscover = DynamicDiscoveryClientName + ".RPC_SubServiceDiscover"
const AddSubServiceDiscover = DynamicDiscoveryMasterName + ".RPC_AddSubServiceDiscover"

type DynamicDiscoveryMaster struct {
	service.Service

	mapNodeInfo map[int32]struct{}
	nodeInfo    []*rpc.NodeInfo
}

type DynamicDiscoveryClient struct {
	service.Service

	funDelService FunDelNode
	funSetService FunSetNodeInfo
	localNodeId   int

	mapDiscovery map[int32]map[int32]struct{} //map[masterNodeId]map[nodeId]struct{}
}

var masterService DynamicDiscoveryMaster
var clientService DynamicDiscoveryClient

func getDynamicDiscovery() IServiceDiscovery {
	return &clientService
}

func init() {
	masterService.SetName(DynamicDiscoveryMasterName)
	clientService.SetName(DynamicDiscoveryClientName)
}

func (ds *DynamicDiscoveryMaster) isRegNode(nodeId int32) bool {
	_, ok := ds.mapNodeInfo[nodeId]
	return ok
}

func (ds *DynamicDiscoveryMaster) addNodeInfo(nodeInfo *rpc.NodeInfo) {
	if len(nodeInfo.PublicServiceList) == 0 {
		return
	}

	_, ok := ds.mapNodeInfo[nodeInfo.NodeId]
	if ok == true {
		return
	}
	ds.mapNodeInfo[nodeInfo.NodeId] = struct{}{}
	ds.nodeInfo = append(ds.nodeInfo, nodeInfo)
}

func (ds *DynamicDiscoveryMaster) removeNodeInfo(nodeId int32) {
	if _,ok:= ds.mapNodeInfo[nodeId];ok == false {
		return
	}

	for i:=0;i<len(ds.nodeInfo);i++ {
		if ds.nodeInfo[i].NodeId == nodeId {
			ds.nodeInfo = append(ds.nodeInfo[:i],ds.nodeInfo[i+1:]...)
			break
		}
	}

	delete(ds.mapNodeInfo,nodeId)
}

func (ds *DynamicDiscoveryMaster) OnInit() error {
	ds.mapNodeInfo = make(map[int32]struct{}, 20)
	ds.RegRpcListener(ds)

	return nil
}

func (ds *DynamicDiscoveryMaster) OnStart() {
	var nodeInfo rpc.NodeInfo
	localNodeInfo := cluster.GetLocalNodeInfo()
	if localNodeInfo.Private == true {
		return
	}

	nodeInfo.NodeId = int32(localNodeInfo.NodeId)
	nodeInfo.NodeName = localNodeInfo.NodeName
	nodeInfo.ListenAddr = localNodeInfo.ListenAddr
	nodeInfo.PublicServiceList = localNodeInfo.PublicServiceList
	nodeInfo.MaxRpcParamLen = localNodeInfo.MaxRpcParamLen
	
	ds.addNodeInfo(&nodeInfo)
}

func (ds *DynamicDiscoveryMaster) OnNodeConnected(nodeId int) {
	//没注册过结点不通知
	if ds.isRegNode(int32(nodeId)) == false {
		return
	}

	//向它发布所有服务列表信息
	var notifyDiscover rpc.SubscribeDiscoverNotify
	notifyDiscover.IsFull = true
	notifyDiscover.NodeInfo = ds.nodeInfo
	notifyDiscover.MasterNodeId = int32(cluster.GetLocalNodeInfo().NodeId)

	ds.GoNode(nodeId, SubServiceDiscover, &notifyDiscover)
}

func (ds *DynamicDiscoveryMaster) OnNodeDisconnect(nodeId int) {
	if ds.isRegNode(int32(nodeId)) == false {
		return
	}

	ds.removeNodeInfo(int32(nodeId))

	var notifyDiscover rpc.SubscribeDiscoverNotify
	notifyDiscover.MasterNodeId = int32(cluster.GetLocalNodeInfo().NodeId)
	notifyDiscover.DelNodeId = int32(nodeId)
	//删除结点
	cluster.DelNode(nodeId, true)

	//无注册过的结点不广播，避免非当前Master网络中的连接断开时通知到本网络
	ds.CastGo(SubServiceDiscover, &notifyDiscover)
}

func (ds *DynamicDiscoveryMaster) RpcCastGo(serviceMethod string, args interface{}) {
	for nodeId, _ := range ds.mapNodeInfo {
		ds.GoNode(int(nodeId), serviceMethod, args)
	}
}

// 收到注册过来的结点
func (ds *DynamicDiscoveryMaster) RPC_RegServiceDiscover(req *rpc.ServiceDiscoverReq, res *rpc.Empty) error {
	if req.NodeInfo == nil {
		err := errors.New("RPC_RegServiceDiscover req is error.")
		log.SError(err.Error())

		return err
	}

	//广播给其他所有结点
	var notifyDiscover rpc.SubscribeDiscoverNotify
	notifyDiscover.MasterNodeId = int32(cluster.GetLocalNodeInfo().NodeId)
	notifyDiscover.NodeInfo = append(notifyDiscover.NodeInfo, req.NodeInfo)
	ds.RpcCastGo(SubServiceDiscover, &notifyDiscover)

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
	nodeInfo.MaxRpcParamLen = req.NodeInfo.MaxRpcParamLen
	//主动删除已经存在的结点,确保先断开，再连接
	cluster.serviceDiscoveryDelNode(nodeInfo.NodeId, true)

	//加入到本地Cluster模块中，将连接该结点
	cluster.serviceDiscoverySetNodeInfo(&nodeInfo)

	return nil
}

func (dc *DynamicDiscoveryClient) OnInit() error {
	dc.RegRpcListener(dc)
	dc.mapDiscovery = map[int32]map[int32]struct{}{}
	return nil
}

func (dc *DynamicDiscoveryClient) addMasterNode(masterNodeId int32, nodeId int32) {
	_, ok := dc.mapDiscovery[masterNodeId]
	if ok == false {
		dc.mapDiscovery[masterNodeId] = map[int32]struct{}{}
	}
	dc.mapDiscovery[masterNodeId][nodeId] = struct{}{}
}

func (dc *DynamicDiscoveryClient) removeMasterNode(masterNodeId int32, nodeId int32) {
	mapNodeId, ok := dc.mapDiscovery[masterNodeId]
	if ok == false {
		return
	}

	delete(mapNodeId, nodeId)
}

func (dc *DynamicDiscoveryClient) findNodeId(nodeId int32) bool {
	for _, mapNodeId := range dc.mapDiscovery {
		_, ok := mapNodeId[nodeId]
		if ok == true {
			return true
		}
	}

	return false
}

func (dc *DynamicDiscoveryClient) OnStart() {
	//2.添加并连接发现主结点
	dc.addDiscoveryMaster()
}

func (dc *DynamicDiscoveryClient) addDiscoveryMaster() {
	discoveryNodeList := cluster.GetDiscoveryNodeList()
	for i := 0; i < len(discoveryNodeList); i++ {
		if discoveryNodeList[i].NodeId == cluster.GetLocalNodeInfo().NodeId {
			continue
		}
		dc.funSetService(&discoveryNodeList[i])
	}
}

func (dc *DynamicDiscoveryClient) fullCompareDiffNode(masterNodeId int32, mapNodeInfo map[int32]*rpc.NodeInfo) []int32 {
	if mapNodeInfo == nil {
		return nil
	}

	diffNodeIdSlice := make([]int32, 0, len(mapNodeInfo))
	mapNodeId := map[int32]struct{}{}
	mapNodeId, ok := dc.mapDiscovery[masterNodeId]
	if ok == false {
		return nil
	}

	//本地任何Master都不存在的，放到diffNodeIdSlice
	for nodeId, _ := range mapNodeId {
		_, ok := mapNodeInfo[nodeId]
		if ok == false {
			diffNodeIdSlice = append(diffNodeIdSlice, nodeId)
		}
	}

	return diffNodeIdSlice
}

//订阅发现的服务通知
func (dc *DynamicDiscoveryClient) RPC_SubServiceDiscover(req *rpc.SubscribeDiscoverNotify) error {
	//整理当前master结点需要筛选的NeighborService
	masterDiscoveryNodeInfo := cluster.GetMasterDiscoveryNodeInfo(int(req.MasterNodeId))
	mapMasterDiscoveryService := map[string]struct{}{}
	if masterDiscoveryNodeInfo != nil {
		for i := 0; i < len(masterDiscoveryNodeInfo.NeighborService); i++ {
			mapMasterDiscoveryService[masterDiscoveryNodeInfo.NeighborService[i]] = struct{}{}
		}
	}

	mapNodeInfo := map[int32]*rpc.NodeInfo{}
	for _, nodeInfo := range req.NodeInfo {
		//不对本地结点或者不存在任何公开服务的结点
		if int(nodeInfo.NodeId) == dc.localNodeId {
			continue
		}

		if cluster.IsMasterDiscoveryNode() == false && len(nodeInfo.PublicServiceList) == 1 &&
			nodeInfo.PublicServiceList[0] == DynamicDiscoveryClientName {
			continue
		}

		//遍历所有的公开服务，并筛选之
		for _, serviceName := range nodeInfo.PublicServiceList {
			//只有存在配置时才做筛选
			if len(mapMasterDiscoveryService) > 0 {
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
				nInfo.MaxRpcParamLen = nodeInfo.MaxRpcParamLen
				mapNodeInfo[nodeInfo.NodeId] = nInfo
			}

			nInfo.PublicServiceList = append(nInfo.PublicServiceList, serviceName)
		}
	}

	//如果为完整同步，则找出差异的结点
	var willDelNodeId []int32
	//如果不是邻居结点，则做筛选
	if req.IsFull == true {
		diffNode := dc.fullCompareDiffNode(req.MasterNodeId, mapNodeInfo)
		if len(diffNode) > 0 {
			willDelNodeId = append(willDelNodeId, diffNode...)
		}
	}

	//指定删除结点
	if req.DelNodeId > 0 && req.DelNodeId != int32(dc.localNodeId) {
		willDelNodeId = append(willDelNodeId, req.DelNodeId)
	}

	//删除不必要的结点
	for _, nodeId := range willDelNodeId {
		nodeInfo,_ := cluster.GetNodeInfo(int(nodeId))
		cluster.TriggerDiscoveryEvent(false,int(nodeId),nodeInfo.PublicServiceList)
		dc.removeMasterNode(req.MasterNodeId, int32(nodeId))
		if dc.findNodeId(nodeId) == false {
			dc.funDelService(int(nodeId), false)
		}
	}

	//设置新结点
	for _, nodeInfo := range mapNodeInfo {
		dc.addMasterNode(req.MasterNodeId, nodeInfo.NodeId)
		dc.setNodeInfo(nodeInfo)

		if len(nodeInfo.PublicServiceList) == 0 {
			continue
		}

		cluster.TriggerDiscoveryEvent(true,int(nodeInfo.NodeId),nodeInfo.PublicServiceList)
	}

	return nil
}

func (dc *DynamicDiscoveryClient) isDiscoverNode(nodeId int) bool {
	for i := 0; i < len(cluster.masterDiscoveryNodeList); i++ {
		if cluster.masterDiscoveryNodeList[i].NodeId == nodeId {
			return true
		}
	}

	return false
}

func (dc *DynamicDiscoveryClient) OnNodeConnected(nodeId int) {
	dc.regServiceDiscover(nodeId)
}

func (dc *DynamicDiscoveryClient) regServiceDiscover(nodeId int){
	nodeInfo := cluster.GetMasterDiscoveryNodeInfo(nodeId)
	if nodeInfo == nil {
		return
	}

	var req rpc.ServiceDiscoverReq
	req.NodeInfo = &rpc.NodeInfo{}
	req.NodeInfo.NodeId = int32(cluster.localNodeInfo.NodeId)
	req.NodeInfo.NodeName = cluster.localNodeInfo.NodeName
	req.NodeInfo.ListenAddr = cluster.localNodeInfo.ListenAddr
	req.NodeInfo.MaxRpcParamLen = cluster.localNodeInfo.MaxRpcParamLen

	//MasterDiscoveryNode配置中没有配置NeighborService，则同步当前结点所有服务
	if len(nodeInfo.NeighborService) == 0 {
		req.NodeInfo.PublicServiceList = cluster.localNodeInfo.PublicServiceList
	} else {
		req.NodeInfo.PublicServiceList = append(req.NodeInfo.PublicServiceList, DynamicDiscoveryClientName)
	}

	//向Master服务同步本Node服务信息
	err := dc.AsyncCallNode(nodeId, RegServiceDiscover, &req, func(res *rpc.Empty, err error) {
		if err != nil {
			log.SError("call ", RegServiceDiscover, " is fail :", err.Error())
			dc.AfterFunc(time.Second*3, func(timer *timer.Timer) {
				dc.regServiceDiscover(nodeId)
			})

			return
		}
	})
	if err != nil {
		log.SError("call ", RegServiceDiscover, " is fail :", err.Error())
	}
}

func (dc *DynamicDiscoveryClient) setNodeInfo(nodeInfo *rpc.NodeInfo) {
	if nodeInfo == nil || nodeInfo.Private == true || int(nodeInfo.NodeId) == dc.localNodeId {
		return
	}

	//筛选关注的服务
	localNodeInfo := cluster.GetLocalNodeInfo()
	if len(localNodeInfo.DiscoveryService) > 0 {
		var discoverServiceSlice = make([]string, 0, 24)
		for _, pubService := range nodeInfo.PublicServiceList {
			for _, discoverService := range localNodeInfo.DiscoveryService {
				if pubService == discoverService {
					discoverServiceSlice = append(discoverServiceSlice, pubService)
				}
			}
		}
		nodeInfo.PublicServiceList = discoverServiceSlice
	}

	if len(nodeInfo.PublicServiceList) == 0 {
		return
	}

	var nInfo NodeInfo
	nInfo.ServiceList = nodeInfo.PublicServiceList
	nInfo.PublicServiceList = nodeInfo.PublicServiceList
	nInfo.NodeId = int(nodeInfo.NodeId)
	nInfo.NodeName = nodeInfo.NodeName
	nInfo.ListenAddr = nodeInfo.ListenAddr
	nInfo.MaxRpcParamLen = nodeInfo.MaxRpcParamLen
	dc.funSetService(&nInfo)
}

func (dc *DynamicDiscoveryClient) OnNodeDisconnect(nodeId int) {
	//将Discard结点清理
	cluster.DiscardNode(nodeId)
}

func (dc *DynamicDiscoveryClient) InitDiscovery(localNodeId int, funDelNode FunDelNode, funSetNodeInfo FunSetNodeInfo) error {
	dc.localNodeId = localNodeId
	dc.funDelService = funDelNode
	dc.funSetService = funSetNodeInfo

	return nil
}

func (dc *DynamicDiscoveryClient) OnNodeStop() {
}
