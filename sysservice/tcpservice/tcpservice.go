package tcpservice

import (
	"fmt"
	"github.com/duanhf2012/origin/event"
	"github.com/duanhf2012/origin/log"
	"github.com/duanhf2012/origin/network"
	"github.com/duanhf2012/origin/service"
	"sync"
)

type TcpService struct {
	tcpServer network.TCPServer
	service.Service

	mapClientLocker sync.RWMutex
	mapClient       map[uint64] *Client
	initClientId    uint64
	process         network.Processor
}

type TcpPackType int8
const(
	TPT_Connected TcpPackType = 0
	TPT_DisConnected TcpPackType = 1
	TPT_Pack TcpPackType = 2
	TPT_UnknownPack TcpPackType = 3
)

type TcpPack struct {
	Type TcpPackType //0表示连接 1表示断开 2表示数据
	MsgProcessor network.Processor
	ClientId uint64
	Data interface{}
}

const Default_MaxConnNum = 3000
const Default_PendingWriteNum = 10000
const Default_LittleEndian = false
const Default_MinMsgLen = 2
const Default_MaxMsgLen = 65535

func (slf *TcpService) OnInit() error{
	iConfig := slf.GetServiceCfg()
	if iConfig == nil {
		return fmt.Errorf("%s service config is error!",slf.GetName())
	}
	tcpCfg := iConfig.(map[string]interface{})
	addr,ok := tcpCfg["ListenAddr"]
	if ok == false {
		return fmt.Errorf("%s service config is error!",slf.GetName())
	}
	slf.tcpServer.Addr = addr.(string)
	slf.tcpServer.MaxConnNum = Default_MaxConnNum
	slf.tcpServer.PendingWriteNum = Default_PendingWriteNum
	slf.tcpServer.LittleEndian = Default_LittleEndian
	slf.tcpServer.MinMsgLen = Default_MinMsgLen
	slf.tcpServer.MaxMsgLen = Default_MaxMsgLen
	MaxConnNum,ok := tcpCfg["MaxConnNum"]
	if ok == true {
		slf.tcpServer.MaxConnNum = int(MaxConnNum.(float64))
	}
	PendingWriteNum,ok := tcpCfg["PendingWriteNum"]
	if ok == true {
		slf.tcpServer.PendingWriteNum = int(PendingWriteNum.(float64))
	}
	LittleEndian,ok := tcpCfg["LittleEndian"]
	if ok == true {
		slf.tcpServer.LittleEndian = LittleEndian.(bool)
	}
	MinMsgLen,ok := tcpCfg["MinMsgLen"]
	if ok == true {
		slf.tcpServer.MinMsgLen = uint32(MinMsgLen.(float64))
	}
	MaxMsgLen,ok := tcpCfg["MaxMsgLen"]
	if ok == true {
		slf.tcpServer.MaxMsgLen = uint32(MaxMsgLen.(float64))
	}
	slf.mapClient = make( map[uint64] *Client,slf.tcpServer.MaxConnNum)
	slf.tcpServer.NewAgent =slf.NewClient
	slf.tcpServer.Start()
	return nil
}

func (slf *TcpService) TcpEventHandler(ev *event.Event) {
	pack := ev.Data.(*TcpPack)
	switch pack.Type {
	case TPT_Connected:
		pack.MsgProcessor.ConnectedRoute(pack.ClientId)
	case TPT_DisConnected:
		pack.MsgProcessor.DisConnectedRoute(pack.ClientId)
	case TPT_UnknownPack:
		pack.MsgProcessor.UnknownMsgRoute(pack.Data,pack.ClientId)
	case TPT_Pack:
		pack.MsgProcessor.MsgRoute(pack.Data, pack.ClientId)
	}
}

func (slf *TcpService) SetProcessor(process network.Processor,handler event.IEventHandler){
	slf.process = process
	slf.RegEventReciverFunc(event.Sys_Event_Tcp,handler,slf.TcpEventHandler)
}

func (slf *TcpService) NewClient(conn *network.TCPConn) network.Agent {
	slf.mapClientLocker.Lock()
	defer slf.mapClientLocker.Unlock()

	for {
		slf.initClientId+=1
		_,ok := slf.mapClient[slf.initClientId]
		if ok == true {
			continue
		}

		pClient := &Client{tcpConn:conn, id:slf.initClientId}
		pClient.tcpService = slf
		slf.mapClient[slf.initClientId] = pClient
		return pClient
	}

	return nil
}

type Client struct {
	id uint64
	tcpConn *network.TCPConn
	tcpService *TcpService
}

func (slf *Client) GetId() uint64 {
	return slf.id
}

func (slf *Client) Run() {
	slf.tcpService.NotifyEvent(&event.Event{Type:event.Sys_Event_Tcp,Data:&TcpPack{ClientId:slf.id,Type:TPT_Connected,MsgProcessor:slf.tcpService.process}})
	for{
		bytes,err := slf.tcpConn.ReadMsg()
		if err != nil {
			log.Debug("read client id %d is error:%+v",slf.id,err)
			break
		}
		data,err:=slf.tcpService.process.Unmarshal(bytes)
		if err != nil {
			slf.tcpService.NotifyEvent(&event.Event{Type:event.Sys_Event_Tcp,Data:&TcpPack{ClientId:slf.id,Type:TPT_UnknownPack,Data:bytes,MsgProcessor:slf.tcpService.process}})
			continue
		}
		slf.tcpService.NotifyEvent(&event.Event{Type:event.Sys_Event_Tcp,Data:&TcpPack{ClientId:slf.id,Type:TPT_Pack,Data:data,MsgProcessor:slf.tcpService.process}})
	}
}

func (slf *Client) OnClose(){
	slf.tcpService.NotifyEvent(&event.Event{Type:event.Sys_Event_Tcp,Data:&TcpPack{ClientId:slf.id,Type:TPT_DisConnected,MsgProcessor:slf.tcpService.process}})
	slf.tcpService.mapClientLocker.Lock()
	defer slf.tcpService.mapClientLocker.Unlock()
	delete (slf.tcpService.mapClient,slf.GetId())
}

func (slf *TcpService) SendMsg(clientid uint64,msg interface{}) error{
	slf.mapClientLocker.Lock()
	client,ok := slf.mapClient[clientid]
	if ok == false{
		slf.mapClientLocker.Unlock()
		return fmt.Errorf("client %d is disconnect!",clientid)
	}

	slf.mapClientLocker.Unlock()
	bytes,err := slf.process.Marshal(msg)
	if err != nil {
		return err
	}
	return client.tcpConn.WriteMsg(bytes)
}

func (slf *TcpService) Close(clientid uint64) {
	slf.mapClientLocker.Lock()
	defer slf.mapClientLocker.Unlock()

	client,ok := slf.mapClient[clientid]
	if ok == false{
		return
	}

	if client.tcpConn!=nil {
		client.tcpConn.Close()
	}

	return
}

func (slf *TcpService) GetClientIp(clientid uint64) string{
	slf.mapClientLocker.Lock()
	defer slf.mapClientLocker.Unlock()
	pClient,ok := slf.mapClient[clientid]
	if ok == false{
		return ""
	}

	return pClient.tcpConn.GetRemoteIp()
}
