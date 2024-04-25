package cluster

import (
	"errors"
	"github.com/duanhf2012/origin/v2/log"
	"github.com/duanhf2012/origin/v2/rpc"
	"github.com/duanhf2012/origin/v2/service"
	"github.com/duanhf2012/origin/v2/util/timer"
	"google.golang.org/protobuf/proto"
	"time"
)

const OriginDiscoveryMasterName = "DiscoveryMaster"
const OriginDiscoveryClientName = "DiscoveryClient"
const RegServiceDiscover = OriginDiscoveryMasterName + ".RPC_RegServiceDiscover"
const SubServiceDiscover = OriginDiscoveryClientName + ".RPC_SubServiceDiscover"
const AddSubServiceDiscover = OriginDiscoveryMasterName + ".RPC_AddSubServiceDiscover"
const NodeRetireRpcMethod = OriginDiscoveryMasterName+".RPC_NodeRetire"
const RpcPingMethod = OriginDiscoveryMasterName+".RPC_Ping"
const UnRegServiceDiscover = OriginDiscoveryMasterName+".RPC_UnRegServiceDiscover"

type OriginDiscoveryMaster struct {
	service.Service

	mapNodeInfo map[string]struct{}
	nodeInfo    []*rpc.NodeInfo

	nsTTL nodeSetTTL
}

type OriginDiscoveryClient struct {
	service.Service

	funDelNode FunDelNode
	funSetNode FunSetNode
	localNodeId   string

	mapDiscovery map[string]map[string]struct{} //map[masterNodeId]map[nodeId]struct{}
	mapMasterNetwork map[string]string
	bRetire bool
}

var masterService OriginDiscoveryMaster
var clientService OriginDiscoveryClient

func getOriginDiscovery() IServiceDiscovery {
	return &clientService
}

func init() {
	masterService.SetName(OriginDiscoveryMasterName)
	clientService.SetName(OriginDiscoveryClientName)
}

func (ds *OriginDiscoveryMaster) isRegNode(nodeId string) bool {
	_, ok := ds.mapNodeInfo[nodeId]
	return ok
}

func (ds *OriginDiscoveryMaster) updateNodeInfo(nInfo *rpc.NodeInfo) {
	if _,ok:= ds.mapNodeInfo[nInfo.NodeId];ok == false {
		return
	}

	nodeInfo := proto.Clone(nInfo).(*rpc.NodeInfo)
	for i:=0;i<len(ds.nodeInfo);i++ {
		if ds.nodeInfo[i].NodeId == nodeInfo.NodeId {
			ds.nodeInfo[i]  = nodeInfo
			break
		}
	}
}

func (ds *OriginDiscoveryMaster) addNodeInfo(nInfo *rpc.NodeInfo) {
	if len(nInfo.PublicServiceList) == 0 {
		return
	}

	_, ok := ds.mapNodeInfo[nInfo.NodeId]
	if ok == true {
		return
	}
	ds.mapNodeInfo[nInfo.NodeId] = struct{}{}

	nodeInfo := proto.Clone(nInfo).(*rpc.NodeInfo)
	ds.nodeInfo = append(ds.nodeInfo, nodeInfo)
}

func (ds *OriginDiscoveryMaster) removeNodeInfo(nodeId string) {
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

func (ds *OriginDiscoveryMaster) OnInit() error {
	ds.mapNodeInfo = make(map[string]struct{}, 20)
	ds.RegNodeConnListener(ds)
	ds.RegNatsConnListener(ds)

	ds.nsTTL.init(time.Duration(cluster.GetOriginDiscovery().TTLSecond)*time.Second)

	return nil
}

func (ds *OriginDiscoveryMaster) checkTTL(){
	if cluster.IsNatsMode() == false {
		return
	}

	interval := time.Duration(cluster.GetOriginDiscovery().TTLSecond)*time.Second
	interval = interval /3 /2
	if interval < time.Second {
		interval = time.Second
	}

	ds.NewTicker(interval,func(t *timer.Ticker){
		ds.nsTTL.checkTTL(func(nodeIdList []string) {
			for _,nodeId := range nodeIdList {
				log.Debug("TTL expiry",log.String("nodeId",nodeId))
				ds.OnNodeDisconnect(nodeId)
			}
		})
	})
}

func (ds *OriginDiscoveryMaster) OnStart() {
	var nodeInfo rpc.NodeInfo
	localNodeInfo := cluster.GetLocalNodeInfo()
	nodeInfo.NodeId = localNodeInfo.NodeId
	nodeInfo.ListenAddr = localNodeInfo.ListenAddr
	nodeInfo.PublicServiceList = localNodeInfo.PublicServiceList
	nodeInfo.MaxRpcParamLen = localNodeInfo.MaxRpcParamLen
	nodeInfo.Private = localNodeInfo.Private
	nodeInfo.Retire = localNodeInfo.Retire
	ds.addNodeInfo(&nodeInfo)

	ds.checkTTL()
}

func (dc *OriginDiscoveryMaster) OnNatsConnected(){
	//向所有的节点同步服务发现信息
	var notifyDiscover rpc.SubscribeDiscoverNotify
	notifyDiscover.IsFull = true
	notifyDiscover.NodeInfo = dc.nodeInfo
	notifyDiscover.MasterNodeId = cluster.GetLocalNodeInfo().NodeId
	dc.RpcCastGo(SubServiceDiscover, &notifyDiscover)
}

func (dc *OriginDiscoveryMaster)  OnNatsDisconnect(){
}

func (ds *OriginDiscoveryMaster) OnNodeConnected(nodeId string) {
}

func (ds *OriginDiscoveryMaster) OnNodeDisconnect(nodeId string) {
	if ds.isRegNode(nodeId) == false {
		return
	}

	ds.removeNodeInfo(nodeId)

	//主动删除已经存在的结点,确保先断开，再连接
	var notifyDiscover rpc.SubscribeDiscoverNotify
	notifyDiscover.MasterNodeId = cluster.GetLocalNodeInfo().NodeId
	notifyDiscover.DelNodeId = nodeId

	//删除结点
	cluster.DelNode(nodeId, true)

	//无注册过的结点不广播，避免非当前Master网络中的连接断开时通知到本网络
	ds.CastGo(SubServiceDiscover, &notifyDiscover)
}

func (ds *OriginDiscoveryMaster) RpcCastGo(serviceMethod string, args interface{}) {
	for nodeId, _ := range ds.mapNodeInfo {
		ds.GoNode(nodeId, serviceMethod, args)
	}
}

func (ds *OriginDiscoveryMaster) RPC_Ping(req *rpc.Ping, res *rpc.Pong) error {
	if ds.isRegNode(req.NodeId) == false{
		res.Ok = false
		return nil
	}

	return nil
	res.Ok = true
	ds.nsTTL.addAndRefreshNode(req.NodeId)
	return nil
}

func (ds *OriginDiscoveryMaster) RPC_NodeRetire(req *rpc.NodeRetireReq, res *rpc.Empty) error {
	log.Info("node is retire",log.String("nodeId",req.NodeInfo.NodeId),log.Bool("retire",req.NodeInfo.Retire))

	ds.updateNodeInfo(req.NodeInfo)

	var notifyDiscover rpc.SubscribeDiscoverNotify
	notifyDiscover.MasterNodeId = cluster.GetLocalNodeInfo().NodeId
	notifyDiscover.NodeInfo = append(notifyDiscover.NodeInfo, req.NodeInfo)
	ds.RpcCastGo(SubServiceDiscover, &notifyDiscover)

	return nil
}

// 收到注册过来的结点
func (ds *OriginDiscoveryMaster) RPC_RegServiceDiscover(req *rpc.RegServiceDiscoverReq, res *rpc.SubscribeDiscoverNotify) error {
	if req.NodeInfo == nil {
		err := errors.New("RPC_RegServiceDiscover req is error.")
		log.Error(err.Error())

		return err
	}

	if req.NodeInfo.NodeId != cluster.GetLocalNodeInfo().NodeId {
		ds.nsTTL.addAndRefreshNode(req.NodeInfo.NodeId)
	}

	//广播给其他所有结点
	var notifyDiscover rpc.SubscribeDiscoverNotify
	notifyDiscover.MasterNodeId = cluster.GetLocalNodeInfo().NodeId
	notifyDiscover.NodeInfo = append(notifyDiscover.NodeInfo, req.NodeInfo)
	ds.RpcCastGo(SubServiceDiscover, &notifyDiscover)

	//存入本地
	ds.addNodeInfo(req.NodeInfo)

	//初始化结点信息
	var nodeInfo NodeInfo
	nodeInfo.NodeId = req.NodeInfo.NodeId
	nodeInfo.Private = req.NodeInfo.Private
	nodeInfo.ServiceList = req.NodeInfo.PublicServiceList
	nodeInfo.PublicServiceList = req.NodeInfo.PublicServiceList
	nodeInfo.ListenAddr = req.NodeInfo.ListenAddr
	nodeInfo.MaxRpcParamLen = req.NodeInfo.MaxRpcParamLen
	nodeInfo.Retire = req.NodeInfo.Retire

	//主动删除已经存在的结点,确保先断开，再连接
	cluster.serviceDiscoveryDelNode(nodeInfo.NodeId, true)

	//加入到本地Cluster模块中，将连接该结点
	cluster.serviceDiscoverySetNodeInfo(&nodeInfo)


	res.IsFull = true
	res.NodeInfo = ds.nodeInfo
	res.MasterNodeId = cluster.GetLocalNodeInfo().NodeId
	return nil
}

func (dc *OriginDiscoveryClient) OnInit() error {
	dc.RegNodeConnListener(dc)
	dc.RegNatsConnListener(dc)

	dc.mapDiscovery = map[string]map[string]struct{}{}
	dc.mapMasterNetwork = map[string]string{}

	return nil
}

func (dc *OriginDiscoveryClient) addMasterNode(masterNodeId string, nodeId string) {
	_, ok := dc.mapDiscovery[masterNodeId]
	if ok == false {
		dc.mapDiscovery[masterNodeId] = map[string]struct{}{}
	}
	dc.mapDiscovery[masterNodeId][nodeId] = struct{}{}
}

func (dc *OriginDiscoveryClient) removeMasterNode(masterNodeId string, nodeId string) {
	mapNodeId, ok := dc.mapDiscovery[masterNodeId]
	if ok == false {
		return
	}

	delete(mapNodeId, nodeId)
}

func (dc *OriginDiscoveryClient) findNodeId(nodeId string) bool {
	for _, mapNodeId := range dc.mapDiscovery {
		_, ok := mapNodeId[nodeId]
		if ok == true {
			return true
		}
	}

	return false
}

func (dc *OriginDiscoveryClient) ping(){
	if cluster.IsNatsMode() == false {
		return
	}

	interval := time.Duration(cluster.GetOriginDiscovery().TTLSecond)*time.Second
	interval = interval /3
	if interval < time.Second {
		interval = time.Second
	}

	dc.NewTicker(interval,func(t *timer.Ticker){
		var ping rpc.Ping
		ping.NodeId = cluster.GetLocalNodeInfo().NodeId
		masterNodes := GetCluster().GetOriginDiscovery().MasterNodeList
		for i:=0;i<len(masterNodes);i++ {
			if masterNodes[i].NodeId == cluster.GetLocalNodeInfo().NodeId {
				continue
			}

			masterNodeId := masterNodes[i].NodeId
			dc.AsyncCallNode(masterNodeId,RpcPingMethod,&ping, func(empty *rpc.Pong,err error) {
				if err == nil && empty.Ok == false{
					//断开master重
					dc.regServiceDiscover(masterNodeId)
				}
			})
		}
	})
}


func (dc *OriginDiscoveryClient) OnStart() {
	//2.添加并连接发现主结点
	dc.addDiscoveryMaster()
	dc.ping()
}

func (dc *OriginDiscoveryClient) addDiscoveryMaster() {
	discoveryNodeList := cluster.GetOriginDiscovery()

	for i := 0; i < len(discoveryNodeList.MasterNodeList); i++ {
		if discoveryNodeList.MasterNodeList[i].NodeId == cluster.GetLocalNodeInfo().NodeId {
			continue
		}
		dc.funSetNode(&discoveryNodeList.MasterNodeList[i])
	}
}

func (dc *OriginDiscoveryClient) fullCompareDiffNode(masterNodeId string, mapNodeInfo map[string]*rpc.NodeInfo) []string {
	if mapNodeInfo == nil {
		return nil
	}

	diffNodeIdSlice := make([]string, 0, len(mapNodeInfo))
	mapNodeId := map[string]struct{}{}
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
func (dc *OriginDiscoveryClient) RPC_SubServiceDiscover(req *rpc.SubscribeDiscoverNotify) error {
	mapNodeInfo := map[string]*rpc.NodeInfo{}
	for _, nodeInfo := range req.NodeInfo {
		//不对本地结点或者不存在任何公开服务的结点
		if nodeInfo.NodeId == dc.localNodeId {
			continue
		}

		if cluster.IsOriginMasterDiscoveryNode(cluster.GetLocalNodeInfo().NodeId) == false && len(nodeInfo.PublicServiceList) == 1 &&
			nodeInfo.PublicServiceList[0] == OriginDiscoveryClientName {
			continue
		}

		//遍历所有的公开服务，并筛选之
		for _, serviceName := range nodeInfo.PublicServiceList {
			nInfo := mapNodeInfo[nodeInfo.NodeId]
			if nInfo == nil {
				nInfo = &rpc.NodeInfo{}
				nInfo.NodeId = nodeInfo.NodeId
				nInfo.ListenAddr = nodeInfo.ListenAddr
				nInfo.MaxRpcParamLen = nodeInfo.MaxRpcParamLen
				nInfo.Retire = nodeInfo.Retire
				nInfo.Private = nodeInfo.Private

				mapNodeInfo[nodeInfo.NodeId] = nInfo
			}

			nInfo.PublicServiceList = append(nInfo.PublicServiceList, serviceName)
		}
	}

	//如果为完整同步，则找出差异的结点
	var willDelNodeId []string
	if req.IsFull == true {
		diffNode := dc.fullCompareDiffNode(req.MasterNodeId, mapNodeInfo)
		if len(diffNode) > 0 {
			willDelNodeId = append(willDelNodeId, diffNode...)
		}
	}

	//指定删除结点
	if req.DelNodeId != rpc.NodeIdNull && req.DelNodeId != dc.localNodeId {
		willDelNodeId = append(willDelNodeId, req.DelNodeId)
	}

	//删除不必要的结点
	for _, nodeId := range willDelNodeId {
		cluster.TriggerDiscoveryEvent(false,nodeId,nil)
		dc.removeMasterNode(req.MasterNodeId, nodeId)
		if dc.findNodeId(nodeId) == false {
			dc.funDelNode(nodeId, false)
		}
	}

	//设置新结点
	for _, nodeInfo := range mapNodeInfo {
		bSet := dc.setNodeInfo(req.MasterNodeId,nodeInfo)
		if bSet == false {
			continue
		}

		cluster.TriggerDiscoveryEvent(true,nodeInfo.NodeId,nodeInfo.PublicServiceList)
	}

	return nil
}

func (dc *OriginDiscoveryClient) OnNodeConnected(nodeId string) {
	dc.regServiceDiscover(nodeId)
}

func (dc *OriginDiscoveryClient) OnRelease(){
	//取消注册
	var nodeRetireReq rpc.UnRegServiceDiscoverReq
	nodeRetireReq.NodeId = cluster.GetLocalNodeInfo().NodeId

	masterNodeList := cluster.GetOriginDiscovery()
	for i:=0;i<len(masterNodeList.MasterNodeList);i++{
		if masterNodeList.MasterNodeList[i].NodeId == cluster.GetLocalNodeInfo().NodeId {
			continue
		}

		err := dc.CallNodeWithTimeout(3*time.Second,masterNodeList.MasterNodeList[i].NodeId,UnRegServiceDiscover,&nodeRetireReq,&rpc.Empty{})
		if err!= nil {
			log.Error("call "+UnRegServiceDiscover+" is fail",log.ErrorAttr("err",err))
		}
	}
}

func (dc *OriginDiscoveryClient) OnRetire(){
	dc.bRetire = true

	masterNodeList := cluster.GetOriginDiscovery()
	for i:=0;i<len(masterNodeList.MasterNodeList);i++{
		var nodeRetireReq rpc.NodeRetireReq

		nodeRetireReq.NodeInfo = &rpc.NodeInfo{}
		nodeRetireReq.NodeInfo.NodeId = cluster.localNodeInfo.NodeId
		nodeRetireReq.NodeInfo.ListenAddr = cluster.localNodeInfo.ListenAddr
		nodeRetireReq.NodeInfo.MaxRpcParamLen = cluster.localNodeInfo.MaxRpcParamLen
		nodeRetireReq.NodeInfo.PublicServiceList =  cluster.localNodeInfo.PublicServiceList
		nodeRetireReq.NodeInfo.Retire = dc.bRetire
		nodeRetireReq.NodeInfo.Private = cluster.localNodeInfo.Private

		err := dc.GoNode(masterNodeList.MasterNodeList[i].NodeId,NodeRetireRpcMethod,&nodeRetireReq)
		if err!= nil {
			log.Error("call "+NodeRetireRpcMethod+" is fail",log.ErrorAttr("err",err))
		}
	}
}

func (dc *OriginDiscoveryClient) tryRegServiceDiscover(nodeId string){
	dc.AfterFunc(time.Second*3, func(timer *timer.Timer) {
		dc.regServiceDiscover(nodeId)
	})
}

func (dc *OriginDiscoveryClient) regServiceDiscover(nodeId string){
	if nodeId == cluster.GetLocalNodeInfo().NodeId {
		return
	}
	nodeInfo := cluster.getOriginMasterDiscoveryNodeInfo(nodeId)
	if nodeInfo == nil {
		return
	}

	var req rpc.RegServiceDiscoverReq
	req.NodeInfo = &rpc.NodeInfo{}
	req.NodeInfo.NodeId = cluster.localNodeInfo.NodeId
	req.NodeInfo.ListenAddr = cluster.localNodeInfo.ListenAddr
	req.NodeInfo.MaxRpcParamLen = cluster.localNodeInfo.MaxRpcParamLen
	req.NodeInfo.PublicServiceList =  cluster.localNodeInfo.PublicServiceList
	req.NodeInfo.Retire = dc.bRetire
	req.NodeInfo.Private = cluster.localNodeInfo.Private
	log.Debug("regServiceDiscover",log.String("nodeId",nodeId))
	//向Master服务同步本Node服务信息
	_,err := dc.AsyncCallNodeWithTimeout(3*time.Second,nodeId, RegServiceDiscover, &req, func(res *rpc.SubscribeDiscoverNotify, err error) {
		if err != nil {
			log.Error("call "+RegServiceDiscover+" is fail :"+ err.Error())
			dc.tryRegServiceDiscover(nodeId)
			return
		}

		dc.RPC_SubServiceDiscover(res)
	})

	if err != nil {
		log.Error("call "+ RegServiceDiscover+" is fail :"+ err.Error())
		dc.tryRegServiceDiscover(nodeId)
	}
}

func (dc *OriginDiscoveryClient) setNodeInfo(masterNodeId string,nodeInfo *rpc.NodeInfo) bool{
	if nodeInfo == nil || nodeInfo.Private == true || nodeInfo.NodeId == dc.localNodeId {
		return false
	}

	//筛选关注的服务
	var discoverServiceSlice = make([]string, 0, 24)
	for _, pubService := range nodeInfo.PublicServiceList {
		if cluster.CanDiscoveryService(masterNodeId,pubService) == true {
			discoverServiceSlice = append(discoverServiceSlice,pubService)
		}
	}

	if len(discoverServiceSlice) == 0 {
		return false
	}

	var nInfo NodeInfo
	nInfo.ServiceList = discoverServiceSlice
	nInfo.PublicServiceList = discoverServiceSlice
	nInfo.NodeId = nodeInfo.NodeId
	nInfo.ListenAddr = nodeInfo.ListenAddr
	nInfo.MaxRpcParamLen = nodeInfo.MaxRpcParamLen
	nInfo.Retire = nodeInfo.Retire
	nInfo.Private = nodeInfo.Private

	dc.funSetNode(&nInfo)

	return true
}

func (dc *OriginDiscoveryClient) OnNodeDisconnect(nodeId string) {
	log.Debug("OnNodeDisconnect",log.String("nodeId",nodeId))
	//将Discard结点清理
	cluster.DiscardNode(nodeId)
}

func (dc *OriginDiscoveryClient) InitDiscovery(localNodeId string, funDelNode FunDelNode, funSetNode FunSetNode) error {
	dc.localNodeId = localNodeId
	dc.funDelNode = funDelNode
	dc.funSetNode = funSetNode

	return nil
}

func (cls *Cluster) checkOriginDiscovery(localNodeId string) (bool, bool) {
	var localMaster bool //本结点是否为Master结点
	var hasMaster bool   //是否配置Master服务

	//遍历所有结点
	for _, nodeInfo := range cls.discoveryInfo.Origin.MasterNodeList {
		if nodeInfo.NodeId == localNodeId {
			localMaster = true
		}
		hasMaster = true
	}

	//返回查询结果
	return localMaster, hasMaster
}

func (cls *Cluster) AddDiscoveryService(serviceName string, bPublicService bool) {
	addServiceList := append([]string{},serviceName)
	cls.localNodeInfo.ServiceList = append(addServiceList,cls.localNodeInfo.ServiceList...)
	if bPublicService {
		cls.localNodeInfo.PublicServiceList = append(cls.localNodeInfo.PublicServiceList, serviceName)
	}

	if _, ok := cls.mapServiceNode[serviceName]; ok == false {
		cls.mapServiceNode[serviceName] = map[string]struct{}{}
	}
	cls.mapServiceNode[serviceName][cls.localNodeInfo.NodeId] = struct{}{}
}


func (cls *Cluster) IsOriginMasterDiscoveryNode(nodeId string) bool {
	//return cls.GetMasterDiscoveryNodeInfo(cls.GetLocalNodeInfo().NodeId) != nil
	return cls.getOriginMasterDiscoveryNodeInfo(nodeId) != nil
}

func (cls *Cluster) getOriginMasterDiscoveryNodeInfo(nodeId string) *NodeInfo {
	if cls.discoveryInfo.Origin == nil {
		return nil
	}

	for i := 0; i < len(cls.discoveryInfo.Origin.MasterNodeList); i++ {
		if cls.discoveryInfo.Origin.MasterNodeList[i].NodeId == nodeId {
			return &cls.discoveryInfo.Origin.MasterNodeList[i]
		}
	}

	return nil
}

func (dc *OriginDiscoveryClient) OnNatsConnected(){
	log.Debug("OnNatsConnected")
	masterNodes := GetCluster().GetOriginDiscovery().MasterNodeList
	for i:=0;i<len(masterNodes);i++ {
		dc.regServiceDiscover(masterNodes[i].NodeId)
	}
}

func (dc *OriginDiscoveryClient)  OnNatsDisconnect(){

}