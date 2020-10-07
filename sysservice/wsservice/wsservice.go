package wsservice

import (
	"fmt"
	"github.com/duanhf2012/origin/event"
	"github.com/duanhf2012/origin/log"
	"github.com/duanhf2012/origin/network"
	"github.com/duanhf2012/origin/network/processor"
	"github.com/duanhf2012/origin/service"
	"sync"
)

type WSService struct {
	service.Service
	wsServer network.WSServer

	mapClientLocker sync.RWMutex
	mapClient       map[uint64] *WSClient
	initClientId    uint64
	process         processor.Processor
}

type WSPackType int8
const(
	WPT_Connected WSPackType = 0
	WPT_DisConnected WSPackType = 1
	WPT_Pack WSPackType = 2
	WPT_UnknownPack WSPackType = 3
)

type WSPack struct {
	Type         WSPackType //0表示连接 1表示断开 2表示数据
	MsgProcessor processor.Processor
	ClientId     uint64
	Data         interface{}
}


const Default_WS_MaxConnNum = 3000
const Default_WS_PendingWriteNum = 10000
const Default_WS_MaxMsgLen = 65535


func (slf *WSService) OnInit() error{
	iConfig := slf.GetServiceCfg()
	if iConfig == nil {
		return fmt.Errorf("%s service config is error!",slf.GetName())
	}
	wsCfg := iConfig.(map[string]interface{})
	addr,ok := wsCfg["ListenAddr"]
	if ok == false {
		return fmt.Errorf("%s service config is error!",slf.GetName())
	}

	slf.wsServer.Addr = addr.(string)
	slf.wsServer.MaxConnNum = Default_WS_MaxConnNum
	slf.wsServer.PendingWriteNum = Default_WS_PendingWriteNum
	slf.wsServer.MaxMsgLen = Default_WS_MaxMsgLen
	MaxConnNum,ok := wsCfg["MaxConnNum"]
	if ok == true {
		slf.wsServer.MaxConnNum = int(MaxConnNum.(float64))
	}
	PendingWriteNum,ok := wsCfg["PendingWriteNum"]
	if ok == true {
		slf.wsServer.PendingWriteNum = int(PendingWriteNum.(float64))
	}

	MaxMsgLen,ok := wsCfg["MaxMsgLen"]
	if ok == true {
		slf.wsServer.MaxMsgLen = uint32(MaxMsgLen.(float64))
	}

	slf.mapClient = make( map[uint64] *WSClient,slf.wsServer.MaxConnNum)
	slf.wsServer.NewAgent =slf.NewWSClient
	slf.wsServer.Start()
	return nil
}

func (slf *WSService) WSEventHandler(ev *event.Event) {
	pack := ev.Data.(*WSPack)
	switch pack.Type {
	case WPT_Connected:
		pack.MsgProcessor.ConnectedRoute(pack.ClientId)
	case WPT_DisConnected:
		pack.MsgProcessor.DisConnectedRoute(pack.ClientId)
	case WPT_UnknownPack:
		pack.MsgProcessor.UnknownMsgRoute(pack.Data,pack.ClientId)
	case WPT_Pack:
		pack.MsgProcessor.MsgRoute(pack.Data, pack.ClientId)
	}
}

func (slf *WSService) SetProcessor(process processor.Processor,handler event.IEventHandler){
	slf.process = process
	slf.RegEventReciverFunc(event.Sys_Event_WebSocket,handler,slf.WSEventHandler)
}

func (slf *WSService) NewWSClient(conn *network.WSConn) network.Agent {
	slf.mapClientLocker.Lock()
	defer slf.mapClientLocker.Unlock()

	for {
		slf.initClientId+=1
		_,ok := slf.mapClient[slf.initClientId]
		if ok == true {
			continue
		}

		pClient := &WSClient{wsConn:conn, id:slf.initClientId}
		pClient.wsService = slf
		slf.mapClient[slf.initClientId] = pClient
		return pClient
	}

	return nil
}

type WSClient struct {
	id uint64
	wsConn *network.WSConn
	wsService *WSService
}

func (slf *WSClient) GetId() uint64 {
	return slf.id
}

func (slf *WSClient) Run() {
	slf.wsService.NotifyEvent(&event.Event{Type:event.Sys_Event_WebSocket,Data:&WSPack{ClientId:slf.id,Type:WPT_Connected,MsgProcessor:slf.wsService.process}})
	for{
		bytes,err := slf.wsConn.ReadMsg()
		if err != nil {
			log.Debug("read client id %d is error:%+v",slf.id,err)
			break
		}
		data,err:=slf.wsService.process.Unmarshal(bytes)
		if err != nil {
			slf.wsService.NotifyEvent(&event.Event{Type:event.Sys_Event_WebSocket,Data:&WSPack{ClientId:slf.id,Type:WPT_UnknownPack,Data:bytes,MsgProcessor:slf.wsService.process}})
			continue
		}
		slf.wsService.NotifyEvent(&event.Event{Type:event.Sys_Event_WebSocket,Data:&WSPack{ClientId:slf.id,Type:WPT_Pack,Data:data,MsgProcessor:slf.wsService.process}})
	}
}

func (slf *WSClient) OnClose(){
	slf.wsService.NotifyEvent(&event.Event{Type:event.Sys_Event_WebSocket,Data:&WSPack{ClientId:slf.id,Type:WPT_DisConnected,MsgProcessor:slf.wsService.process}})
	slf.wsService.mapClientLocker.Lock()
	defer slf.wsService.mapClientLocker.Unlock()
	delete (slf.wsService.mapClient,slf.GetId())
}

func (slf *WSService) SendMsg(clientid uint64,msg interface{}) error{
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
	return client.wsConn.WriteMsg(bytes)
}

func (slf *WSService) Close(clientid uint64) {
	slf.mapClientLocker.Lock()
	defer slf.mapClientLocker.Unlock()

	client,ok := slf.mapClient[clientid]
	if ok == false{
		return
	}

	if client.wsConn!=nil {
		client.wsConn.Close()
	}

	return
}

