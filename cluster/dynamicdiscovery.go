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
const DynamicDiscoveryMasterNameRpcMethod = DynamicDiscoveryMasterName+".RPC_RegServiceDiscover"
const DynamicDiscoveryClientNameRpcMethod = DynamicDiscoveryClientName+".RPC_SubServiceDiscover"
type DynamicDiscoveryMaster struct {
	service.Service

	mapNodeInfo map[int32] *rpc.NodeInfo
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
	_,ok := ds.mapNodeInfo[nodeInfo.NodeId]
	if ok == true {
		return
	}
	ds.nodeInfo = append(ds.nodeInfo,nodeInfo)
}

func (ds *DynamicDiscoveryMaster) OnInit() error{
	ds.mapNodeInfo = make(map[int32] *rpc.NodeInfo,20)
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
	ds.GoNode(nodeId,DynamicDiscoveryClientNameRpcMethod,&notifyDiscover)
}

func (ds *DynamicDiscoveryMaster) OnNodeDisconnect(nodeId int){
	var notifyDiscover rpc.SubscribeDiscoverNotify
	notifyDiscover.DelNodeId = int32(nodeId)
	//删除结点
	cluster.DelNode(nodeId,true)
	ds.CastGo(DynamicDiscoveryClientNameRpcMethod,&notifyDiscover)
}

// 收到注册过来的结点
func (ds *DynamicDiscoveryMaster) RPC_RegServiceDiscover(req *rpc.ServiceDiscoverReq, res *rpc.ServiceDiscoverRes) error{
	if req.NodeInfo == nil {
		err := fmt.Errorf("RPC_RegServiceDiscover req is error.")
		log.Error(err.Error())

		return err
	}

	//广播给其他所有结点
	var notifyDiscover rpc.SubscribeDiscoverNotify
	notifyDiscover.NodeInfo = append(notifyDiscover.NodeInfo,req.NodeInfo)
	ds.CastGo(DynamicDiscoveryClientNameRpcMethod,&notifyDiscover)

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
	res.NodeInfo = ds.nodeInfo

	return nil
}

func (dc *DynamicDiscoveryClient) OnInit() error{
	dc.RegRpcListener(dc)
	return nil
}

func (dc *DynamicDiscoveryClient) OnStart(){
	//2.添加并连接发现主结点
	localNodeInfo := cluster.GetLocalNodeInfo()
	localNodeInfo.PublicServiceList = append(localNodeInfo.PublicServiceList,DynamicDiscoveryClientName)
	dc.addDiscoveryMaster()
}

func (dc *DynamicDiscoveryClient)  addDiscoveryMaster(){
	discoveryNodeList := cluster.GetDiscoveryNodeList()
	for i:=0;i<len(discoveryNodeList);i++ {
		if discoveryNodeList[i].NodeId == cluster.GetLocalNodeInfo().NodeId {
			continue
		}
		dc.funSetService(&discoveryNodeList[i])
		//cluster.serviceDiscoverySetNodeInfo(&discoveryNodeList[i])
	}
}

//订阅发现的服务通知
func (dc *DynamicDiscoveryClient) RPC_SubServiceDiscover(req *rpc.SubscribeDiscoverNotify) error{
	//如果为完整同步，则找出差异的结点
	var willDelNodeId []int
	if req.IsFull {
		mapNodeId := make(map[int32]struct{},len(req.NodeInfo))
		for _,nodeInfo:= range req.NodeInfo{
			mapNodeId[nodeInfo.NodeId] = struct{}{}
		}

		cluster.FetchAllNodeId(func(nodeId int){
			if nodeId != dc.localNodeId {
				if _, ok := mapNodeId[int32(nodeId)]; ok == false {
					willDelNodeId = append(willDelNodeId, nodeId)
				}
			}
		})
	}

	//忽略本地结点
	if req.DelNodeId != int32(dc.localNodeId) && req.DelNodeId>0 {
		willDelNodeId = append(willDelNodeId, int(req.DelNodeId))
	}

	//删除不必要的结点
	for _,nodeId := range willDelNodeId {
		dc.funDelService(nodeId,false)
	}

	//发现新结点
	for _, nodeInfo := range req.NodeInfo {
		dc.setNodeInfo(nodeInfo)
	}

	return nil
}

func (dc *DynamicDiscoveryClient) isDiscoverNode(nodeId int) bool{
	for i:=0;i< len(cluster.discoveryNodeList);i++{
		if cluster.discoveryNodeList[i].NodeId == nodeId {
			return true
		}
	}

	return false
}

func (dc *DynamicDiscoveryClient) OnNodeConnected(nodeId int) {
	if dc.isDiscoverNode(nodeId) == false {
		return
	}

	var req rpc.ServiceDiscoverReq
	req.NodeInfo = &rpc.NodeInfo{}
	req.NodeInfo.NodeId = int32(cluster.localNodeInfo.NodeId)
	req.NodeInfo.NodeName = cluster.localNodeInfo.NodeName
	req.NodeInfo.ListenAddr = cluster.localNodeInfo.ListenAddr
	req.NodeInfo.PublicServiceList = cluster.localNodeInfo.PublicServiceList

	//如果是连接发现主服成功，则同步服务信息
	err := dc.AsyncCallNode(nodeId, DynamicDiscoveryMasterNameRpcMethod, &req, func(res *rpc.ServiceDiscoverRes, err error) {
		if err != nil {
			cluster.DelNode(nodeId,true)
			log.Error("call %s is fail :%s", DynamicDiscoveryMasterNameRpcMethod, err.Error())
			return
		}
	})
	if err != nil {
		log.Error("call %s is fail :%s", DynamicDiscoveryMasterNameRpcMethod, err.Error())
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

