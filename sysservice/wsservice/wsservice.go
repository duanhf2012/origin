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
	process         processor.IProcessor
}

type WSPackType int8
const(
	WPT_Connected WSPackType = 0
	WPT_DisConnected WSPackType = 1
	WPT_Pack WSPackType = 2
	WPT_UnknownPack WSPackType = 3
)

const Default_WS_MaxConnNum = 3000
const Default_WS_PendingWriteNum = 10000
const Default_WS_MaxMsgLen = 65535

type WSClient struct {
	id uint64
	wsConn *network.WSConn
	wsService *WSService
}

type WSPack struct {
	Type         WSPackType //0表示连接 1表示断开 2表示数据
	MsgProcessor processor.IProcessor
	ClientId     uint64
	Data         interface{}
}

func (ws *WSService) OnInit() error{
	iConfig := ws.GetServiceCfg()
	if iConfig == nil {
		return fmt.Errorf("%s service config is error!", ws.GetName())
	}
	wsCfg := iConfig.(map[string]interface{})
	addr,ok := wsCfg["ListenAddr"]
	if ok == false {
		return fmt.Errorf("%s service config is error!", ws.GetName())
	}

	ws.wsServer.Addr = addr.(string)
	ws.wsServer.MaxConnNum = Default_WS_MaxConnNum
	ws.wsServer.PendingWriteNum = Default_WS_PendingWriteNum
	ws.wsServer.MaxMsgLen = Default_WS_MaxMsgLen
	MaxConnNum,ok := wsCfg["MaxConnNum"]
	if ok == true {
		ws.wsServer.MaxConnNum = int(MaxConnNum.(float64))
	}
	PendingWriteNum,ok := wsCfg["PendingWriteNum"]
	if ok == true {
		ws.wsServer.PendingWriteNum = int(PendingWriteNum.(float64))
	}

	MaxMsgLen,ok := wsCfg["MaxMsgLen"]
	if ok == true {
		ws.wsServer.MaxMsgLen = uint32(MaxMsgLen.(float64))
	}

	ws.mapClient = make( map[uint64] *WSClient, ws.wsServer.MaxConnNum)
	ws.wsServer.NewAgent = ws.NewWSClient
	ws.wsServer.Start()
	return nil
}

func (ws *WSService) WSEventHandler(ev event.IEvent) {
	pack := ev.(*event.Event).Data.(*WSPack)
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

func (ws *WSService) SetProcessor(process processor.IProcessor,handler event.IEventHandler){
	ws.process = process
	ws.RegEventReceiverFunc(event.Sys_Event_WebSocket,handler, ws.WSEventHandler)
}

func (ws *WSService) NewWSClient(conn *network.WSConn) network.Agent {
	ws.mapClientLocker.Lock()
	defer ws.mapClientLocker.Unlock()

	for {
		ws.initClientId+=1
		_,ok := ws.mapClient[ws.initClientId]
		if ok == true {
			continue
		}

		pClient := &WSClient{wsConn:conn, id: ws.initClientId}
		pClient.wsService = ws
		ws.mapClient[ws.initClientId] = pClient
		return pClient
	}

	return nil
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

func (ws *WSService) SendMsg(clientid uint64,msg interface{}) error{
	ws.mapClientLocker.Lock()
	client,ok := ws.mapClient[clientid]
	if ok == false{
		ws.mapClientLocker.Unlock()
		return fmt.Errorf("client %d is disconnect!",clientid)
	}

	ws.mapClientLocker.Unlock()
	bytes,err := ws.process.Marshal(msg)
	if err != nil {
		return err
	}
	return client.wsConn.WriteMsg(bytes)
}

func (ws *WSService) Close(clientid uint64) {
	ws.mapClientLocker.Lock()
	defer ws.mapClientLocker.Unlock()

	client,ok := ws.mapClient[clientid]
	if ok == false{
		return
	}

	if client.wsConn!=nil {
		client.wsConn.Close()
	}

	return
}

