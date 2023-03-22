package tcpservice

import (
	"fmt"
	"github.com/duanhf2012/origin/event"
	"github.com/duanhf2012/origin/log"
	"github.com/duanhf2012/origin/network"
	"github.com/duanhf2012/origin/network/processor"
	"github.com/duanhf2012/origin/node"
	"github.com/duanhf2012/origin/service"
	"sync/atomic"
	"sync"
	"time"
	"runtime"
)

type TcpService struct {
	tcpServer network.TCPServer
	service.Service

	mapClientLocker sync.RWMutex
	mapClient       map[uint64] *Client
	process processor.IProcessor
}

type TcpPackType int8
const(
	TPT_Connected TcpPackType = 0
	TPT_DisConnected TcpPackType = 1
	TPT_Pack TcpPackType = 2
	TPT_UnknownPack TcpPackType = 3
)

const (
	MaxNodeId = 1<<14 - 1  //最大值 16383
	MaxSeed   = 1<<19 - 1  //最大值 524287
	MaxTime   = 1<<31 - 1  //最大值 2147483647
)

var seed uint32

type TcpPack struct {
	Type         TcpPackType //0表示连接 1表示断开 2表示数据
	ClientId     uint64
	Data         interface{}
}

type Client struct {
	id uint64
	tcpConn *network.TCPConn
	tcpService *TcpService
}

func (tcpService *TcpService) genId() uint64 {
	if node.GetNodeId()>MaxNodeId{
		panic("nodeId exceeds the maximum!")
	}

	newSeed := atomic.AddUint32(&seed,1) % MaxSeed
	nowTime := uint64(time.Now().Unix())%MaxTime
	return (uint64(node.GetNodeId())<<50)|(nowTime<<19)|uint64(newSeed)
}


func GetNodeId(agentId uint64) int {
	return int(agentId>>50)
}

func (tcpService *TcpService) OnInit() error{
	iConfig := tcpService.GetServiceCfg()
	if iConfig == nil {
		return fmt.Errorf("%s service config is error!", tcpService.GetName())
	}
	tcpCfg := iConfig.(map[string]interface{})
	addr,ok := tcpCfg["ListenAddr"]
	if ok == false {
		return fmt.Errorf("%s service config is error!", tcpService.GetName())
	}

	tcpService.tcpServer.Addr = addr.(string)
	MaxConnNum,ok := tcpCfg["MaxConnNum"]
	if ok == true {
		tcpService.tcpServer.MaxConnNum = int(MaxConnNum.(float64))
	}
	PendingWriteNum,ok := tcpCfg["PendingWriteNum"]
	if ok == true {
		tcpService.tcpServer.PendingWriteNum = int(PendingWriteNum.(float64))
	}
	LittleEndian,ok := tcpCfg["LittleEndian"]
	if ok == true {
		tcpService.tcpServer.LittleEndian = LittleEndian.(bool)
	}
	LenMsgLen,ok := tcpCfg["LenMsgLen"]
	if ok == true {
		tcpService.tcpServer.LenMsgLen = int(LenMsgLen.(float64))
	}
	MinMsgLen,ok := tcpCfg["MinMsgLen"]
	if ok == true {
		tcpService.tcpServer.MinMsgLen = uint32(MinMsgLen.(float64))
	}
	MaxMsgLen,ok := tcpCfg["MaxMsgLen"]
	if ok == true {
		tcpService.tcpServer.MaxMsgLen = uint32(MaxMsgLen.(float64))
	}

	readDeadline,ok := tcpCfg["ReadDeadline"]
	if ok == true {
		tcpService.tcpServer.ReadDeadline = time.Second*time.Duration(readDeadline.(float64))
	}

	writeDeadline,ok := tcpCfg["WriteDeadline"]
	if ok == true {
		tcpService.tcpServer.WriteDeadline = time.Second*time.Duration(writeDeadline.(float64))
	}

	tcpService.mapClient = make( map[uint64] *Client, tcpService.tcpServer.MaxConnNum)
	tcpService.tcpServer.NewAgent = tcpService.NewClient
	tcpService.tcpServer.Start()

	return nil
}

func (tcpService *TcpService) TcpEventHandler(ev event.IEvent) {
	pack := ev.(*event.Event).Data.(TcpPack)
	switch pack.Type {
	case TPT_Connected:
		tcpService.process.ConnectedRoute(pack.ClientId)
	case TPT_DisConnected:
		tcpService.process.DisConnectedRoute(pack.ClientId)
	case TPT_UnknownPack:
		tcpService.process.UnknownMsgRoute(pack.ClientId,pack.Data)
	case TPT_Pack:
		tcpService.process.MsgRoute(pack.ClientId,pack.Data)
	}
}

func (tcpService *TcpService) SetProcessor(process processor.IProcessor,handler event.IEventHandler){
	tcpService.process = process
	tcpService.RegEventReceiverFunc(event.Sys_Event_Tcp,handler, tcpService.TcpEventHandler)
}

func (tcpService *TcpService) NewClient(conn *network.TCPConn) network.Agent {
	tcpService.mapClientLocker.Lock()
	defer tcpService.mapClientLocker.Unlock()

	for {
		clientId := tcpService.genId()
		_,ok := tcpService.mapClient[clientId]
		if ok == true {
			continue
		}

		pClient := &Client{tcpConn:conn, id:clientId}
		pClient.tcpService = tcpService
		tcpService.mapClient[clientId] = pClient

		return pClient
	}

	return nil
}

func (slf *Client) GetId() uint64 {
	return slf.id
}

func (slf *Client) Run() {
	defer func() {
		if r := recover(); r != nil {
			buf := make([]byte, 4096)
			l := runtime.Stack(buf, false)
			errString := fmt.Sprint(r)
			log.SError("core dump info[",errString,"]\n",string(buf[:l]))
		}
	}()

	slf.tcpService.NotifyEvent(&event.Event{Type:event.Sys_Event_Tcp,Data:TcpPack{ClientId:slf.id,Type:TPT_Connected}})
	for{
		if slf.tcpConn == nil {
			break
		}

		slf.tcpConn.SetReadDeadline(slf.tcpService.tcpServer.ReadDeadline)
		bytes,err := slf.tcpConn.ReadMsg()
		if err != nil {
			log.SDebug("read client id ",slf.id," is error:",err.Error())
			break
		}
		data,err:=slf.tcpService.process.Unmarshal(slf.id,bytes)

		if err != nil {
			slf.tcpService.NotifyEvent(&event.Event{Type:event.Sys_Event_Tcp,Data:TcpPack{ClientId:slf.id,Type:TPT_UnknownPack,Data:bytes}})
			continue
		}
		slf.tcpService.NotifyEvent(&event.Event{Type:event.Sys_Event_Tcp,Data:TcpPack{ClientId:slf.id,Type:TPT_Pack,Data:data}})
	}
}

func (slf *Client) OnClose(){
	slf.tcpService.NotifyEvent(&event.Event{Type:event.Sys_Event_Tcp,Data:TcpPack{ClientId:slf.id,Type:TPT_DisConnected}})
	slf.tcpService.mapClientLocker.Lock()
	defer slf.tcpService.mapClientLocker.Unlock()
	delete (slf.tcpService.mapClient,slf.GetId())
}

func (tcpService *TcpService) SendMsg(clientId uint64,msg interface{}) error{
	tcpService.mapClientLocker.Lock()
	client,ok := tcpService.mapClient[clientId]
	if ok == false{
		tcpService.mapClientLocker.Unlock()
		return fmt.Errorf("client %d is disconnect!",clientId)
	}

	tcpService.mapClientLocker.Unlock()
	bytes,err := tcpService.process.Marshal(clientId,msg)
	if err != nil {
		return err
	}
	return client.tcpConn.WriteMsg(bytes)
}

func (tcpService *TcpService) Close(clientId uint64) {
	tcpService.mapClientLocker.Lock()
	defer tcpService.mapClientLocker.Unlock()

	client,ok := tcpService.mapClient[clientId]
	if ok == false{
		return
	}

	if client.tcpConn!=nil {
		client.tcpConn.Close()
	}

	return
}

func (tcpService *TcpService) GetClientIp(clientid uint64) string{
	tcpService.mapClientLocker.Lock()
	defer tcpService.mapClientLocker.Unlock()
	pClient,ok := tcpService.mapClient[clientid]
	if ok == false{
		return ""
	}

	return pClient.tcpConn.GetRemoteIp()
}


func (tcpService *TcpService) SendRawMsg(clientId uint64,msg []byte) error{
	tcpService.mapClientLocker.Lock()
	client,ok := tcpService.mapClient[clientId]
	if ok == false{
		tcpService.mapClientLocker.Unlock()
		return fmt.Errorf("client %d is disconnect!",clientId)
	}
	tcpService.mapClientLocker.Unlock()
	return client.tcpConn.WriteMsg(msg)
}

func (tcpService *TcpService) SendRawData(clientId uint64,data []byte) error{
	tcpService.mapClientLocker.Lock()
	client,ok := tcpService.mapClient[clientId]
	if ok == false{
		tcpService.mapClientLocker.Unlock()
		return fmt.Errorf("client %d is disconnect!",clientId)
	}
	tcpService.mapClientLocker.Unlock()
	return client.tcpConn.WriteRawMsg(data)
}


func (tcpService *TcpService) GetConnNum() int {
	tcpService.mapClientLocker.Lock()
	connNum := len(tcpService.mapClient)
	tcpService.mapClientLocker.Unlock()
	return connNum
}

func (server *TcpService) SetNetMempool(mempool network.INetMempool){
	server.tcpServer.SetNetMempool(mempool)
}

func (server *TcpService) GetNetMempool() network.INetMempool{
	return server.tcpServer.GetNetMempool()
}

func (server *TcpService) ReleaseNetMem(byteBuff []byte) {
	server.tcpServer.GetNetMempool().ReleaseByteSlice(byteBuff)
}
