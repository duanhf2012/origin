package wsservice

import (
	"fmt"
	"github.com/duanhf2012/origin/v2/event"
	"github.com/duanhf2012/origin/v2/log"
	"github.com/duanhf2012/origin/v2/network"
	"github.com/duanhf2012/origin/v2/network/processor"
	"github.com/duanhf2012/origin/v2/service"
	"github.com/google/uuid"
	"strings"
	"sync"
)

type WSService struct {
	service.Service
	wsServer network.WSServer

	mapClientLocker sync.RWMutex
	mapClient       map[string]*WSClient
	process         processor.IProcessor
}

type WSPackType int8

const (
	WPT_Connected    WSPackType = 0
	WPT_DisConnected WSPackType = 1
	WPT_Pack         WSPackType = 2
	WPT_UnknownPack  WSPackType = 3
)

const Default_WS_MaxConnNum = 3000
const Default_WS_PendingWriteNum = 10000
const Default_WS_MaxMsgLen = 65535

type WSClient struct {
	id        string
	wsConn    *network.WSConn
	wsService *WSService
}

type WSPack struct {
	Type         WSPackType //0表示连接 1表示断开 2表示数据
	MsgProcessor processor.IProcessor
	ClientId     string
	Data         interface{}
}

func (ws *WSService) OnInit() error {

	iConfig := ws.GetServiceCfg()
	if iConfig == nil {
		return fmt.Errorf("%s service config is error", ws.GetName())
	}
	wsCfg := iConfig.(map[string]interface{})
	addr, ok := wsCfg["ListenAddr"]
	if ok == false {
		return fmt.Errorf("%s service config is error", ws.GetName())
	}

	ws.wsServer.Addr = addr.(string)
	ws.wsServer.MaxConnNum = Default_WS_MaxConnNum
	ws.wsServer.PendingWriteNum = Default_WS_PendingWriteNum
	ws.wsServer.MaxMsgLen = Default_WS_MaxMsgLen
	MaxConnNum, ok := wsCfg["MaxConnNum"]
	if ok == true {
		ws.wsServer.MaxConnNum = int(MaxConnNum.(float64))
	}

	PendingWriteNum, ok := wsCfg["PendingWriteNum"]
	if ok == true {
		ws.wsServer.PendingWriteNum = int(PendingWriteNum.(float64))
	}

	MaxMsgLen, ok := wsCfg["MaxMsgLen"]
	if ok == true {
		ws.wsServer.MaxMsgLen = uint32(MaxMsgLen.(float64))
	}

	ws.mapClient = make(map[string]*WSClient, ws.wsServer.MaxConnNum)
	ws.wsServer.NewAgent = ws.NewWSClient
	return ws.wsServer.Start()
}

func (ws *WSService) SetMessageType(messageType int) {
	ws.wsServer.SetMessageType(messageType)
}

func (ws *WSService) WSEventHandler(ev event.IEvent) {
	pack := ev.(*event.Event).Data.(*WSPack)
	switch pack.Type {
	case WPT_Connected:
		pack.MsgProcessor.ConnectedRoute(pack.ClientId)
	case WPT_DisConnected:
		pack.MsgProcessor.DisConnectedRoute(pack.ClientId)
	case WPT_UnknownPack:
		pack.MsgProcessor.UnknownMsgRoute(pack.ClientId, pack.Data, ws.recyclerReaderBytes)
	case WPT_Pack:
		pack.MsgProcessor.MsgRoute(pack.ClientId, pack.Data, ws.recyclerReaderBytes)
	}
}

func (ws *WSService) SetProcessor(process processor.IProcessor, handler event.IEventHandler) {
	ws.process = process
	ws.RegEventReceiverFunc(event.Sys_Event_WebSocket, handler, ws.WSEventHandler)
}

func (ws *WSService) NewWSClient(conn *network.WSConn) network.Agent {
	ws.mapClientLocker.Lock()
	defer ws.mapClientLocker.Unlock()

	uuId, _ := uuid.NewUUID()
	clientId := strings.ReplaceAll(uuId.String(), "-", "")
	pClient := &WSClient{wsConn: conn, id: clientId}
	pClient.wsService = ws
	ws.mapClient[clientId] = pClient
	return pClient

	return nil
}

func (slf *WSClient) GetId() string {
	return slf.id
}

func (slf *WSClient) Run() {
	slf.wsService.NotifyEvent(&event.Event{Type: event.Sys_Event_WebSocket, Data: &WSPack{ClientId: slf.id, Type: WPT_Connected, MsgProcessor: slf.wsService.process}})
	for {
		bytes, err := slf.wsConn.ReadMsg()
		if err != nil {
			log.Debug("read client id %s is error:%+v", slf.id, err)
			break
		}
		data, err := slf.wsService.process.Unmarshal(slf.id, bytes)
		if err != nil {
			slf.wsService.NotifyEvent(&event.Event{Type: event.Sys_Event_WebSocket, Data: &WSPack{ClientId: slf.id, Type: WPT_UnknownPack, Data: bytes, MsgProcessor: slf.wsService.process}})
			continue
		}
		slf.wsService.NotifyEvent(&event.Event{Type: event.Sys_Event_WebSocket, Data: &WSPack{ClientId: slf.id, Type: WPT_Pack, Data: data, MsgProcessor: slf.wsService.process}})
	}
}

func (slf *WSClient) OnClose() {
	slf.wsService.NotifyEvent(&event.Event{Type: event.Sys_Event_WebSocket, Data: &WSPack{ClientId: slf.id, Type: WPT_DisConnected, MsgProcessor: slf.wsService.process}})
	slf.wsService.mapClientLocker.Lock()
	defer slf.wsService.mapClientLocker.Unlock()
	delete(slf.wsService.mapClient, slf.GetId())
}

func (ws *WSService) SendMsg(clientId string, msg interface{}) error {
	ws.mapClientLocker.Lock()
	client, ok := ws.mapClient[clientId]
	if ok == false {
		ws.mapClientLocker.Unlock()
		return fmt.Errorf("client %s is disconnect", clientId)
	}

	ws.mapClientLocker.Unlock()
	bytes, err := ws.process.Marshal(clientId, msg)
	if err != nil {
		return err
	}
	return client.wsConn.WriteMsg(bytes)
}

func (ws *WSService) Close(clientId string) {
	ws.mapClientLocker.Lock()
	defer ws.mapClientLocker.Unlock()

	client, ok := ws.mapClient[clientId]
	if ok == false {
		return
	}

	if client.wsConn != nil {
		client.wsConn.Close()
	}

	return
}

func (ws *WSService) recyclerReaderBytes(data []byte) {
}
