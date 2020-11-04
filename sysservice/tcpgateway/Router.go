package tcpgateway

import (
	"github.com/duanhf2012/origin/log"
	"github.com/duanhf2012/origin/network"
	"github.com/duanhf2012/origin/node"
	"github.com/duanhf2012/origin/rpc"
	"github.com/duanhf2012/origin/sysservice/tcpservice"
	"strings"
)

type RouterCfg struct {
}

type Router struct {
	loadBalance ILoadBalance
	rpcHandler rpc.IRpcHandler

	mapMsgRouterInfo map[uint16]*MsgRouterInfo
	mapEventRouterInfo map[string]*EventRouterInfo

	tcpService *tcpservice.TcpService

	mapClientRouterCache map[uint64]map[string]int //map[clientid]nodeid
}

type MsgRouterInfo struct {
	Rpc string
	ServiceName string
	LoadBalanceType string
}

type EventRouterInfo struct {
	Rpc string
	ServiceName string
	LoadBalanceType string
}

func NewRouter(loadBalance ILoadBalance,rpcHandler rpc.IRpcHandler,cfg interface{}) IRouter {
	router := &Router{}
	router.loadBalance = loadBalance
	router.rpcHandler = rpcHandler
	router.tcpService =  node.GetService("TcpService").(*tcpservice.TcpService)
	router.loadCfg(cfg)
	router.mapClientRouterCache = map[uint64]map[string]int{}
	return router
}

func (r *Router) loadCfg(cfg interface{}){
	r.mapMsgRouterInfo = map[uint16]*MsgRouterInfo{}
	r.mapEventRouterInfo = map[string]*EventRouterInfo{}

	mapRouter,ok := cfg.(map[string]interface{})
	if ok == false{
		//error....
		return
	}

	//parse MsgRouter
	 routerInfo,ok := mapRouter["MsgRouter"]
	 if ok == false{
	 	//error...
	 	return
	 }

	 //ar routerList []RouterItem
	routerList,ok := routerInfo.([]interface{})
	if ok == false{
		//error...
		return
	}

	for _,v := range routerList{
		mapItem := v.(map[string]interface{})
		var iMsgId interface{}
		var iRpc interface{}
		var iLoadBalanceType interface{}

		if iMsgId,ok = mapItem["MsgId"];ok == false {
			//error ...
			continue
		}
		if iRpc,ok = mapItem["Rpc"];ok == false {
			//error ...
			continue
		}
		if iLoadBalanceType,ok = mapItem["LoadBalanceType"];ok == false {
			//error ...
			continue
		}
		msgId,ok  := iMsgId.(float64)
		if ok == false {
			//error ...
			continue
		}

		strService := strings.Split(iRpc.(string),".")
		if len(strService)!=2 {
			//error ...
			continue
		}

		r.mapMsgRouterInfo[uint16(msgId)] = &MsgRouterInfo{ServiceName: strService[0],Rpc: iRpc.(string),LoadBalanceType: iLoadBalanceType.(string)}
	}

	//parse EventRouter
	eventRouterInfo,ok := mapRouter["EventRouter"]
	if ok == false{
		//error...
		return
	}

	//ar routerList []RouterItem
	eRouterList,ok := eventRouterInfo.([]interface{})
	if ok == false{
		//error...
		return
	}

	for _,v := range eRouterList{
		mapItem := v.(map[string]interface{})
		var eventType interface{}
		var iRpc interface{}
		var iLoadBalanceType interface{}

		if eventType,ok = mapItem["EventType"];ok == false {
			//error ...
			continue
		}
		if iRpc,ok = mapItem["Rpc"];ok == false {
			//error ...
			continue
		}
		if iLoadBalanceType,ok = mapItem["LoadBalanceType"];ok == false {
			//error ...
			continue
		}
		strEventType,ok  := eventType.(string)
		if ok == false {
			//error ...
			continue
		}

		strService := strings.Split(iRpc.(string),".")
		if len(strService)!=2 {
			//error ...
			continue
		}

		r.mapEventRouterInfo[strEventType] = &EventRouterInfo{ServiceName: strService[0],Rpc: iRpc.(string),LoadBalanceType: iLoadBalanceType.(string)}
	}
}

func (r *Router) GetMsgRouterService(msgType uint16) *MsgRouterInfo{
	info,ok := r.mapMsgRouterInfo[msgType]
	if ok == false {
		return nil
	}

	return info
}

func (r *Router) GetEventRouterService(eventType string) *EventRouterInfo{
	info,ok := r.mapEventRouterInfo[eventType]
	if ok == false {
		return nil
	}

	return info
}

func (r *Router) GetRouterId(clientId uint64,serviceName *string) int {
	mapServiceRouter,ok := r.mapClientRouterCache[clientId]
	if ok == false{
		return 0
	}

	routerId,ok := mapServiceRouter[*serviceName]
	if ok == false {
		return 0
	}

	return routerId
}

func (r *Router) SetRouterId(clientId uint64,serviceName string,routerId int){
	r.mapClientRouterCache[clientId][serviceName] = routerId
}

type RawInputArgs struct {
	rawData []byte
	clientId uint64
}

var rawInputEmpty []byte
func (args RawInputArgs) GetRawData() []byte{
	if len(args.rawData) < 2 {
		return args.rawData
	}

	return args.rawData[2:]
}

func (args RawInputArgs) GetAdditionParam() interface{}{
	return args.clientId
}

func (args RawInputArgs) DoGc() {
	if len(args.rawData) < 2 {
		return
	}
	network.ReleaseByteSlice(args.rawData)
}

func (r *Router) RouterMessage(cliId uint64,msgType uint16,msg []byte) {
	routerInfo:= r.GetMsgRouterService(msgType)
	if routerInfo==nil {
		log.Error("The message type is %d with no configured route!",msgType)
		return
	}

	routerId := r.GetRouterId(cliId,&routerInfo.ServiceName)
	if routerId ==0 {
		routerId = r.loadBalance.SelectNode(routerInfo.ServiceName)
		r.SetRouterId(cliId,routerInfo.ServiceName,routerId)
	}

	if routerId>0 {
		r.rpcHandler.RawGoNode(rpc.RpcProcessorPb,routerId,routerInfo.Rpc,RawInputArgs{rawData: msg,clientId: cliId})
	}
}

func (r *Router) Load(){
}

func (r *Router) RouterEvent(clientId uint64,eventType string) bool{
	routerInfo:= r.GetEventRouterService(eventType)
	if routerInfo==nil {
		log.Error("The event type is %s with no register!",eventType)
		return false
	}

	routerId := r.GetRouterId(clientId,&routerInfo.ServiceName)
	if routerId ==0 {
		routerId = r.loadBalance.SelectNode(routerInfo.ServiceName)
		r.SetRouterId(clientId,routerInfo.ServiceName,routerId)
	}

	if routerId>0 {
		r.rpcHandler.RawGoNode(rpc.RpcProcessorPb,routerId,routerInfo.Rpc,RawInputArgs{rawData: rawInputEmpty,clientId: clientId})
		return true
	}

	return false
}


func (r *Router) OnDisconnected(clientId uint64){
	delete(r.mapClientRouterCache,clientId)
	//通知事件
	r.RouterEvent(clientId,"DisConnect")
}

func (r *Router) OnConnected(clientId uint64){
	r.mapClientRouterCache[clientId] = map[string]int{}
	r.RouterEvent(clientId,"Connect")
}
