package wsmodule

import (
	"fmt"
	"sync"
	"time"

	"github.com/duanhf2012/origin/v2/event"
	"github.com/duanhf2012/origin/v2/log"
	"github.com/duanhf2012/origin/v2/network"
	"github.com/duanhf2012/origin/v2/network/processor"
	"github.com/duanhf2012/origin/v2/service"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

type WSModule struct {
	service.Module

	WSServer network.WSServer

	mapClientLocker sync.RWMutex
	mapClient       map[string]*WSClient
	process         processor.IRawProcessor
	wsCfg           *WSCfg
}

type WSClient struct {
	id       string
	wsConn   *network.WSConn
	wsModule *WSModule
}

type WSCfg struct {
	ListenAddr      string
	MaxConnNum      int
	PendingWriteNum int
	MaxReadMsgLen       uint32
	MaxWriteMsgLen      uint32
	LittleEndian    bool //是否小端序
	KeyFile 		string
	CertFile 		string

	HandshakeTimeoutSecond time.Duration
	ReadTimeoutSecond time.Duration
	WriteTimeoutSecond time.Duration
}

type WSPackType int8

const (
	WPTConnected    WSPackType = 0
	WPTDisConnected WSPackType = 1
	WPTPack         WSPackType = 2
	WPTUnknownPack  WSPackType = 3
)

type WSPack struct {
	Type         WSPackType //0表示连接 1表示断开 2表示数据
	MsgProcessor processor.IRawProcessor
	ClientId     string
	Data         any
}

func (ws *WSModule) OnInit() error {
	if ws.wsCfg == nil || ws.process == nil {
		return fmt.Errorf("please call the Init function correctly")
	}

	ws.WSServer.MaxConnNum = ws.wsCfg.MaxConnNum
	ws.WSServer.PendingWriteNum = ws.wsCfg.PendingWriteNum
	ws.WSServer.MaxReadMsgLen = ws.wsCfg.MaxReadMsgLen
	ws.WSServer.MaxWriteMsgLen = ws.wsCfg.MaxWriteMsgLen
	ws.WSServer.Addr = ws.wsCfg.ListenAddr
	ws.WSServer.HandshakeTimeout = ws.wsCfg.HandshakeTimeoutSecond*time.Second
	ws.WSServer.ReadTimeout = ws.wsCfg.ReadTimeoutSecond*time.Second
	ws.WSServer.WriteTimeout = ws.wsCfg.WriteTimeoutSecond*time.Second

	if ws.wsCfg.KeyFile != "" && ws.wsCfg.CertFile != "" {
		ws.WSServer.KeyFile = ws.wsCfg.KeyFile
		ws.WSServer.CertFile = ws.wsCfg.CertFile
	}

	// 设置解析处理器
	ws.process.SetByteOrder(ws.wsCfg.LittleEndian)

	ws.mapClient = make(map[string]*WSClient, ws.WSServer.MaxConnNum)
	ws.WSServer.NewAgent = ws.NewWSClient

	// 设置网络事件处理
	ws.GetEventProcessor().RegEventReceiverFunc(event.Sys_Event_WebSocket, ws.GetEventHandler(), ws.wsEventHandler)

	return nil
}

func (ws *WSModule) Init(wsCfg *WSCfg, process processor.IRawProcessor) {
	ws.wsCfg = wsCfg
	ws.process = process
}

func (ws *WSModule) Start() error {
	return ws.WSServer.Start()
}

func (ws *WSModule) wsEventHandler(ev event.IEvent) {
	pack := ev.(*event.Event).Data.(*WSPack)
	switch pack.Type {
	case WPTConnected:
		ws.process.ConnectedRoute(pack.ClientId)
	case WPTDisConnected:
		ws.process.DisConnectedRoute(pack.ClientId)
	case WPTUnknownPack:
		ws.process.UnknownMsgRoute(pack.ClientId, pack.Data, ws.recyclerReaderBytes)
	case WPTPack:
		ws.process.MsgRoute(pack.ClientId, pack.Data, ws.recyclerReaderBytes)
	}
}

func (ws *WSModule) recyclerReaderBytes([]byte) {
}

func (ws *WSModule) NewWSClient(conn *network.WSConn) network.Agent {
	ws.mapClientLocker.Lock()
	defer ws.mapClientLocker.Unlock()
	pClient := &WSClient{wsConn: conn, id: primitive.NewObjectID().Hex()}
	pClient.wsModule = ws
	ws.mapClient[pClient.id] = pClient

	return pClient
}

func (wc *WSClient) GetId() string {
	return wc.id
}

func (wc *WSClient) Run() {
	wc.wsModule.NotifyEvent(&event.Event{Type: event.Sys_Event_WebSocket, Data: &WSPack{ClientId: wc.id, Type: WPTConnected}})
	for {
		bytes, err := wc.wsConn.ReadMsg()
		if err != nil {
			log.Debug("read client is error", log.String("clientId", wc.id), log.ErrorField("err", err))
			break
		}
		data, err := wc.wsModule.process.Unmarshal(wc.id, bytes)
		if err != nil {
			wc.wsModule.NotifyEvent(&event.Event{Type: event.Sys_Event_WebSocket, Data: &WSPack{ClientId: wc.id, Type: WPTUnknownPack, Data: bytes}})
			continue
		}
		wc.wsModule.NotifyEvent(&event.Event{Type: event.Sys_Event_WebSocket, Data: &WSPack{ClientId: wc.id, Type: WPTPack, Data: data}})
	}
}

func (wc *WSClient) OnClose() {
	wc.wsModule.NotifyEvent(&event.Event{Type: event.Sys_Event_WebSocket, Data: &WSPack{ClientId: wc.id, Type: WPTDisConnected}})
	wc.wsModule.mapClientLocker.Lock()
	defer wc.wsModule.mapClientLocker.Unlock()
	delete(wc.wsModule.mapClient, wc.GetId())
}

func (ws *WSModule) GetProcessor() processor.IRawProcessor {
	return ws.process
}

func (ws *WSModule) GetClientHeader(clientId string,key string) string {
	ws.mapClientLocker.Lock()
	defer ws.mapClientLocker.Unlock()

	pClient, ok := ws.mapClient[clientId]
	if ok == false || pClient.wsConn == nil {
		return ""
	}
	
	if pClient.wsConn.GetHeader() == nil {
		log.Warn("clientId header is nil", log.String("clientId", clientId))
	}

	return pClient.wsConn.GetHeader().Get(key)
}

func (ws *WSModule) GetClientIp(clientId string) string {
	ws.mapClientLocker.Lock()
	defer ws.mapClientLocker.Unlock()

	pClient, ok := ws.mapClient[clientId]
	if ok == false {
		return ""
	}

	return pClient.wsConn.RemoteAddr().String()
}

func (ws *WSModule) Close(clientId string) {
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

func (ws *WSModule) SendMsg(clientId string, msg interface{}) error {
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

func (ws *WSModule) SendRawMsg(clientId string, msg []byte) error {
	ws.mapClientLocker.Lock()
	client, ok := ws.mapClient[clientId]
	if ok == false {
		ws.mapClientLocker.Unlock()
		return fmt.Errorf("client %s is disconnect", clientId)
	}
	ws.mapClientLocker.Unlock()
	return client.wsConn.WriteMsg(msg)
}

func (ws *WSModule) SetMessageType(messageType int) {
	ws.WSServer.SetMessageType(messageType)
}
