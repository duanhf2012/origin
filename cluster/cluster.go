package cluster

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/duanhf2012/origin/sysmodule"

	"github.com/duanhf2012/origin/service"

	"github.com/duanhf2012/origin/rpc"
)

type RpcClient struct {
	nodeid     int
	pclient    *rpc.Client
	serverAddr string
}

func (slf *RpcClient) IsConnected() bool {
	return (slf.pclient != nil) && (slf.pclient.IsClosed() == false)
}

type CCluster struct {
	port       int
	cfg        *ClusterConfig
	nodeclient map[int]*RpcClient

	reader net.Conn
	writer net.Conn

	LocalRpcClient        *rpc.Client
	innerLocalServiceList map[string]bool
}

func (slf *CCluster) ReadNodeInfo(nodeid int) error {
	var err error
	slf.cfg, err = ReadCfg("./config/cluster.json", nodeid)
	if err != nil {
		return err
	}

	return nil
}

func (slf *CCluster) GetClusterClient(id int) *rpc.Client {
	if id == GetNodeId() {
		return slf.LocalRpcClient
	}

	v, ok := slf.nodeclient[id]
	if ok == false {
		return nil
	}

	return v.pclient
}

func (slf *CCluster) GetBindUrl() string {
	return slf.cfg.currentNode.ServerAddr
}

func (slf *CCluster) AcceptRpc(tpcListen *net.TCPListener) error {
	for {
		conn, err := tpcListen.Accept()
		if err != nil {
			service.GetLogger().Printf(sysmodule.LEVER_ERROR, "tpcListen.Accept error:%v", err)
			return err
		}
		go rpc.ServeConn(conn)
	}

	return nil
}

func (slf *CCluster) ListenService() error {

	bindStr := slf.GetBindUrl()
	parts := strings.Split(bindStr, ":")
	if len(parts) < 2 {
		service.GetLogger().Printf(sysmodule.LEVER_FATAL, "ListenService address %s is error.", bindStr)
		os.Exit(1)
	}
	bindStr = "0.0.0.0:" + parts[1]

	//
	tcpaddr, err := net.ResolveTCPAddr("tcp4", bindStr)
	if err != nil {
		service.GetLogger().Printf(sysmodule.LEVER_FATAL, "ResolveTCPAddr error:%v", err)
		os.Exit(1)
		return err
	}

	tcplisten, err2 := net.ListenTCP("tcp", tcpaddr)
	if err2 != nil {
		service.GetLogger().Printf(sysmodule.LEVER_FATAL, "ListenTCP error:%v", err2)
		os.Exit(1)
		return err2
	}
	go slf.AcceptRpc(tcplisten)
	slf.ReSetLocalRpcClient()
	return nil
}

func (slf *CCluster) ReSetLocalRpcClient() {
	slf.reader, slf.writer = net.Pipe()
	go rpc.ServeConn(slf.reader)
	slf.LocalRpcClient = rpc.NewClient(slf.writer)
}

type CPing struct {
	TimeStamp int64
}

type CPong struct {
	TimeStamp int64
}

func (slf *CPing) Ping(ping *CPing, pong *CPong) error {
	pong.TimeStamp = ping.TimeStamp
	return nil
}

func (slf *CCluster) ConnService() error {
	ping := CPing{0}
	pong := CPong{0}
	rpc.RegisterName("CPing", "", &ping)

	//连接集群服务器
	for _, nodeList := range slf.cfg.mapClusterNodeService {
		for _, node := range nodeList {
			if node.NodeID == slf.cfg.currentNode.NodeID {
				continue
			}
			slf.nodeclient[node.NodeID] = &RpcClient{node.NodeID, nil, node.ServerAddr}
		}
	}

	for {
		for _, rpcClient := range slf.nodeclient {

			//连接状态发送ping
			if rpcClient.IsConnected() == true {
				ping.TimeStamp = 0
				err := rpcClient.pclient.Call("CPing.Ping", &ping, &pong)

				if err != nil {
					rpcClient.pclient.Close()
					rpcClient.pclient = nil
					continue
				}

				continue
			}

			//非连接状态重新连接
			if rpcClient.pclient != nil {
				rpcClient.pclient.Close()
				rpcClient.pclient = nil
			}

			client, err := rpc.Dial("tcp", rpcClient.serverAddr)
			if err != nil {
				service.GetLogger().Printf(sysmodule.LEVER_WARN, "Connect nodeid:%d,address:%s fail", rpcClient.nodeid, rpcClient.serverAddr)
				continue
			}
			service.GetLogger().Printf(sysmodule.LEVER_INFO, "Connect nodeid:%d,address:%s succ", rpcClient.nodeid, rpcClient.serverAddr)

			v, _ := slf.nodeclient[rpcClient.nodeid]
			v.pclient = client
		}

		if slf.LocalRpcClient.IsClosed() {
			slf.ReSetLocalRpcClient()
		}
		time.Sleep(time.Second * 4)
	}

	return nil
}

func (slf *CCluster) Init() error {
	if len(os.Args) < 2 {

		return fmt.Errorf("Param error not find NodeId=number")
	}

	parts := strings.Split(os.Args[1], "=")
	if len(parts) < 2 {
		return fmt.Errorf("Param error not find NodeId=number")
	}

	if parts[0] != "NodeId" {

		return fmt.Errorf("Param error not find NodeId=number")
	}

	slf.nodeclient = make(map[int]*RpcClient)

	//读取配置
	ret, err := strconv.Atoi(parts[1])
	if err != nil {
		return err
	}

	return slf.ReadNodeInfo(ret)
}

func (slf *CCluster) Start() error {
	service.InstanceServiceMgr().FetchService(slf.OnFetchService)

	//监听服务
	slf.ListenService()

	//集群
	go slf.ConnService()

	return nil
}

//servicename.methodname
//_servicename.methodname
func (slf *CCluster) Call(NodeServiceMethod string, args interface{}, reply interface{}) error {
	var callServiceName string
	var serviceName string
	nodeidList := slf.GetNodeList(NodeServiceMethod, &callServiceName, &serviceName)
	if len(nodeidList) > 1 || len(nodeidList) < 1 {
		service.GetLogger().Printf(sysmodule.LEVER_ERROR, "CCluster.Call(%s) find nodes count %d is error.", NodeServiceMethod, len(nodeidList))
		return fmt.Errorf("CCluster.Call(%s) find nodes count %d is error.", NodeServiceMethod, len(nodeidList))
	}

	nodeid := nodeidList[0]
	if nodeid == GetNodeId() {
		//判断服务是否已经完成初始化
		iService := service.InstanceServiceMgr().FindService(serviceName)
		if iService == nil {
			service.GetLogger().Printf(sysmodule.LEVER_ERROR, "CCluster.Call(%s): NodeId %d cannot find.", NodeServiceMethod, nodeid)
			return fmt.Errorf("CCluster.Call(%s): NodeId %d cannot find..", NodeServiceMethod, nodeid)
		}
		if iService.IsInit() == false {
			service.GetLogger().Printf(sysmodule.LEVER_ERROR, "CCluster.Call(%s): NodeId %d is not init.", NodeServiceMethod, nodeid)
			return fmt.Errorf("CCluster.Call(%s): NodeId %d is not init.", NodeServiceMethod, nodeid)
		}
		return slf.LocalRpcClient.Call(callServiceName, args, reply)
	} else {
		pclient := slf.GetClusterClient(nodeid)
		if pclient == nil {
			service.GetLogger().Printf(sysmodule.LEVER_ERROR, "CCluster.Call(%s): NodeId %d is not find.", NodeServiceMethod, nodeid)
			return fmt.Errorf("CCluster.Call(%s): NodeId %d is not find.", NodeServiceMethod, nodeid)
		}
		err := pclient.Call(callServiceName, args, reply)
		if err != nil {
			service.GetLogger().Printf(sysmodule.LEVER_ERROR, "CCluster.Call(%s) is fail:%v.", callServiceName, err)
		}
		return err
	}

	service.GetLogger().Printf(sysmodule.LEVER_ERROR, "CCluster.Call(%s) fail.", NodeServiceMethod)
	return fmt.Errorf("CCluster.Call(%s) fail.", NodeServiceMethod)
}

func (slf *CCluster) GetNodeList(NodeServiceMethod string, rpcServerMethod *string, rpcServiceName *string) []int {
	var nodename string
	var servicename string
	var methodname string
	var nodeidList []int

	parts := strings.Split(NodeServiceMethod, ".")
	if len(parts) == 2 {
		servicename = parts[0]
		methodname = parts[1]
	} else if len(parts) == 3 {
		nodename = parts[0]
		servicename = parts[1]
		methodname = parts[2]
	} else {
		return nodeidList
	}

	if nodename == "" {
		nodeidList = make([]int, 0)
		if servicename[:1] == "_" {
			servicename = servicename[1:]
			nodeidList = append(nodeidList, GetNodeId())
		} else {
			nodeidList = slf.cfg.GetIdByService(servicename)
		}
	} else {
		nodeidList = slf.GetIdByNodeService(nodename, servicename)
	}

	if rpcServiceName != nil {
		*rpcServiceName = servicename
	}

	if rpcServerMethod != nil {
		*rpcServerMethod = servicename + "." + methodname
	}

	return nodeidList
}

//GetNodeIdByServiceName 根据服务名查找nodeid servicename服务名 bOnline是否需要查找在线服务
func (slf *CCluster) GetNodeIdByServiceName(servicename string, bOnline bool) []int {
	nodeIDList := slf.cfg.GetIdByService(servicename)

	if bOnline {
		ret := make([]int, 0, len(nodeIDList))
		for _, nodeid := range nodeIDList {
			if slf.CheckNodeIsConnectedByID(nodeid) {
				ret = append(ret, nodeid)
			}
		}
		return ret
	}

	return nodeIDList
}

func (slf *CCluster) CheckNodeIsConnectedByID(nodeid int) bool {
	if nodeid == GetNodeId() {
		return true
	}

	pclient := slf.GetRpcClientByNodeId(nodeid)
	if pclient == nil {
		return false
	}

	return pclient.IsConnected()
}

func (slf *CCluster) GetRpcClientByNodeId(nodeid int) *RpcClient {

	pclient, ok := slf.nodeclient[nodeid]
	if ok == false {
		return nil
	}

	return pclient
}

func (slf *CCluster) Go(bCast bool, NodeServiceMethod string, args interface{}, queueModle bool) error {
	var callServiceName string
	var serviceName string
	nodeidList := slf.GetNodeList(NodeServiceMethod, &callServiceName, &serviceName)
	if len(nodeidList) < 1 {
		service.GetLogger().Printf(sysmodule.LEVER_ERROR, "CCluster.Go(%s) not find nodes.", NodeServiceMethod)
		return fmt.Errorf("CCluster.Go(%s) not find nodes.", NodeServiceMethod)
	}

	if bCast == false && len(nodeidList) > 1 {
		return fmt.Errorf("CCluster.Go(%s) find more nodes", NodeServiceMethod)
	}

	for _, nodeid := range nodeidList {
		if nodeid == GetNodeId() {
			iService := service.InstanceServiceMgr().FindService(serviceName)
			if iService.IsInit() == false {
				service.GetLogger().Printf(sysmodule.LEVER_WARN, "CCluster.Call(%s): NodeId %d is not init.", NodeServiceMethod, nodeid)
				return fmt.Errorf("CCluster.Call(%s): NodeId %d is not init.", NodeServiceMethod, nodeid)
			}

			replyCall := slf.LocalRpcClient.Go(callServiceName, args, nil, nil, queueModle)
			if replyCall.Error != nil {
				service.GetLogger().Printf(sysmodule.LEVER_ERROR, "CCluster.Go(%s) fail:%v.", NodeServiceMethod, replyCall.Error)
			}
		} else {
			pclient := slf.GetClusterClient(nodeid)
			if pclient == nil {
				service.GetLogger().Printf(sysmodule.LEVER_ERROR, "CCluster.Go(%s) NodeId %d not find client", NodeServiceMethod, nodeid)
				return fmt.Errorf("CCluster.Go(%s) NodeId %d not find client", NodeServiceMethod, nodeid)
			}
			replyCall := pclient.Go(callServiceName, args, nil, nil, queueModle)
			if replyCall.Error != nil {
				service.GetLogger().Printf(sysmodule.LEVER_ERROR, "CCluster.Go(%s) fail:%v.", NodeServiceMethod, replyCall.Error)
			}
		}
	}

	return nil
}

func (slf *CCluster) CallNode(nodeid int, servicemethod string, args interface{}, reply interface{}) error {
	pclient := slf.GetClusterClient(nodeid)
	if pclient == nil {
		service.GetLogger().Printf(sysmodule.LEVER_ERROR, "CCluster.CallNode(%d,%s) NodeId not find client", nodeid, servicemethod)
		return fmt.Errorf("Call: NodeId %d is not find.", nodeid)
	}

	err := pclient.Call(servicemethod, args, reply)
	if err != nil {
		service.GetLogger().Printf(sysmodule.LEVER_ERROR, "CCluster.CallNode(%d,%s) fail:%v", nodeid, servicemethod, err)
	}

	return err
}

func (slf *CCluster) GoNode(nodeid int, args interface{}, servicemethod string, queueModle bool) error {
	pclient := slf.GetClusterClient(nodeid)
	if pclient == nil {
		service.GetLogger().Printf(sysmodule.LEVER_ERROR, "CCluster.GoNode(%d,%s) NodeId not find client", nodeid, servicemethod)
		return fmt.Errorf("CCluster.GoNode(%d,%s) NodeId not find client", nodeid, servicemethod)
	}

	replyCall := pclient.Go(servicemethod, args, nil, nil, queueModle)
	if replyCall.Error != nil {
		service.GetLogger().Printf(sysmodule.LEVER_ERROR, "CCluster.GoNode(%d,%s) fail:%v", nodeid, servicemethod, replyCall.Error)
	}

	return replyCall.Error

}

func (ws *CCluster) OnFetchService(iservice service.IService) error {
	rpc.RegisterName(iservice.GetServiceName(), "RPC_", iservice)
	return nil
}

//向远程服务器调用
//Node.servicename.methodname
//servicename.methodname
func Call(NodeServiceMethod string, args interface{}, reply interface{}) error {
	return InstanceClusterMgr().Call(NodeServiceMethod, args, reply)
}

func CallNode(NodeId int, servicemethod string, args interface{}, reply interface{}) error {
	return InstanceClusterMgr().CallNode(NodeId, servicemethod, args, reply)
}

func GoNode(NodeId int, servicemethod string, args interface{}) error {
	return InstanceClusterMgr().GoNode(NodeId, args, servicemethod, false)
}

func Go(NodeServiceMethod string, args interface{}) error {
	return InstanceClusterMgr().Go(false, NodeServiceMethod, args, false)
}

func CastGo(NodeServiceMethod string, args interface{}) error {
	return InstanceClusterMgr().Go(true, NodeServiceMethod, args, false)
}

func GoNodeQueue(NodeId int, servicemethod string, args interface{}) error {
	return InstanceClusterMgr().GoNode(NodeId, args, servicemethod, true)
}

func GoQueue(NodeServiceMethod string, args interface{}) error {
	return InstanceClusterMgr().Go(false, NodeServiceMethod, args, true)
}

func CastGoQueue(NodeServiceMethod string, args interface{}) error {
	return InstanceClusterMgr().Go(true, NodeServiceMethod, args, true)
}

//GetNodeIdByServiceName 根据服务名查找nodeid serviceName服务名 bOnline是否需要查找在线服务
func GetNodeIdByServiceName(serviceName string, bOnline bool) []int {
	return InstanceClusterMgr().GetNodeIdByServiceName(serviceName, bOnline)
}

var _self *CCluster

func InstanceClusterMgr() *CCluster {
	if _self == nil {
		_self = new(CCluster)
		_self.innerLocalServiceList = make(map[string]bool)
		return _self
	}
	return _self
}

func (slf *CCluster) GetIdByNodeService(NodeName string, serviceName string) []int {
	return slf.cfg.GetIdByNodeService(NodeName, serviceName)
}

func (slf *CCluster) HasLocalService(serviceName string) bool {
	_, ok := slf.innerLocalServiceList[serviceName]
	return slf.cfg.HasLocalService(serviceName) || ok
}

func (slf *CCluster) HasInit(serviceName string) bool {
	return slf.cfg.HasLocalService(serviceName)
}

func GetNodeId() int {
	return _self.cfg.currentNode.NodeID
}

func (slf *CCluster) AddLocalService(iservice service.IService) {
	servicename := fmt.Sprintf("%T", iservice)
	parts := strings.Split(servicename, ".")
	if len(parts) != 2 {
		service.GetLogger().Printf(service.LEVER_ERROR, "BaseService.Init: service name is error: %q", servicename)
	}

	servicename = parts[1]
	slf.innerLocalServiceList[servicename] = true
}

func GetNodeName(nodeid int) string {
	return _self.cfg.GetNodeNameByNodeId(nodeid)
}

func DynamicCall(address string, serviceMethod string, args interface{}, reply interface{}) error {
	rpcClient, err := rpc.DialTimeOut("tcp", address, time.Second*1)
	if err != nil {
		return err
	}
	defer rpcClient.Close()
	err = rpcClient.Call(serviceMethod, args, reply)
	if err != nil {
		return err
	}

	return nil
}
