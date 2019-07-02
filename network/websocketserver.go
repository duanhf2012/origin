package network

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"os"
	"runtime/debug"
	"sync"
	"time"

	"github.com/duanhf2012/origin/service"
	"github.com/duanhf2012/origin/sysmodule"

	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
	"github.com/gotoxu/cors"
)

type IWebsocketServer interface {
	SendMsg(clientid uint64, messageType int, msg []byte) bool
	CreateClient(conn *websocket.Conn) *WSClient
	Disconnect(clientid uint64)
	ReleaseClient(pclient *WSClient)
	Clients() []uint64
	BroadcastMsg(messageType int, msg []byte) int
}

type IMessageReceiver interface {
	initReciver(messageReciver IMessageReceiver, websocketServer IWebsocketServer)

	OnConnected(clientid uint64)
	OnDisconnect(clientid uint64, err error)
	OnRecvMsg(clientid uint64, msgtype int, data []byte)
	OnHandleHttp(w http.ResponseWriter, r *http.Request)
	IsInit() bool
}

type Reciver struct {
	messageReciver     IMessageReceiver
	bEnableCompression bool
}

type BaseMessageReciver struct {
	messageReciver IMessageReceiver
	WsServer       IWebsocketServer
}

type WSClient struct {
	clientid  uint64
	conn      *websocket.Conn
	bwritemsg chan WSMessage
}

type WSMessage struct {
	msgtype   int
	bwritemsg []byte
}

type WebsocketServer struct {
	wsUri       string
	maxClientid uint64 //记录当前最新clientid
	mapClient   map[uint64]*WSClient
	locker      sync.RWMutex

	port uint16

	httpserver *http.Server
	reciver    map[string]Reciver

	caList []CA

	iswss bool
}

const (
	MAX_MSG_COUNT = 20480
)

func (slf *WebsocketServer) Init(port uint16) {

	slf.port = port
	slf.mapClient = make(map[uint64]*WSClient)
}

func (slf *WebsocketServer) CreateClient(conn *websocket.Conn) *WSClient {
	slf.locker.Lock()
	slf.maxClientid++
	clientid := slf.maxClientid
	pclient := &WSClient{clientid, conn, make(chan WSMessage, MAX_MSG_COUNT)}
	slf.mapClient[pclient.clientid] = pclient
	slf.locker.Unlock()

	service.GetLogger().Printf(sysmodule.LEVER_INFO, "Client id %d is connected.", clientid)
	return pclient
}

func (slf *WebsocketServer) ReleaseClient(pclient *WSClient) {
	pclient.conn.Close()
	slf.locker.Lock()
	delete(slf.mapClient, pclient.clientid)
	slf.locker.Unlock()
	//关闭写管道
	close(pclient.bwritemsg)
	service.GetLogger().Printf(sysmodule.LEVER_INFO, "Client id %d is disconnected.", pclient.clientid)
}

func (slf *WebsocketServer) SetupReciver(pattern string, messageReciver IMessageReceiver, bEnableCompression bool) {
	messageReciver.initReciver(messageReciver, slf)

	if slf.reciver == nil {
		slf.reciver = make(map[string]Reciver)
	}
	slf.reciver[pattern] = Reciver{messageReciver, bEnableCompression}
}

func (slf *WebsocketServer) startListen() {
	listenPort := fmt.Sprintf(":%d", slf.port)

	var tlscatList []tls.Certificate
	var tlsConfig *tls.Config
	for _, cadata := range slf.caList {
		cer, err := tls.LoadX509KeyPair(cadata.certfile, cadata.keyfile)
		if err != nil {
			service.GetLogger().Printf(sysmodule.LEVER_FATAL, "load CA  %s-%s file is error :%s", cadata.certfile, cadata.keyfile, err.Error())
			os.Exit(1)
			return
		}
		tlscatList = append(tlscatList, cer)
	}

	if len(tlscatList) > 0 {
		tlsConfig = &tls.Config{Certificates: tlscatList}
	}

	slf.httpserver = &http.Server{
		Addr:           listenPort,
		Handler:        slf.initRouterHandler(),
		ReadTimeout:    10 * time.Second,
		WriteTimeout:   10 * time.Second,
		MaxHeaderBytes: 1 << 20,
		TLSConfig:      tlsConfig,
	}

	var err error
	if slf.iswss == true {
		err = slf.httpserver.ListenAndServeTLS("", "")
	} else {
		err = slf.httpserver.ListenAndServe()
	}

	if err != nil {
		service.GetLogger().Printf(sysmodule.LEVER_FATAL, "http.ListenAndServe(%d, nil) error:%v\n", slf.port, err)
		os.Exit(1)
	}
}

func (slf *WSClient) startSendMsg() {
	for {
		msgbuf, ok := <-slf.bwritemsg
		if ok == false {
			break
		}
		slf.conn.SetWriteDeadline(time.Now().Add(15 * time.Second))
		err := slf.conn.WriteMessage(msgbuf.msgtype, msgbuf.bwritemsg)
		if err != nil {
			service.GetLogger().Printf(sysmodule.LEVER_INFO, "write client id %d is error :%v\n", slf.clientid, err)
			break
		}
	}
}

func (slf *WebsocketServer) Start() {

	go slf.startListen()
}

func (slf *WebsocketServer) Clients() []uint64 {
	slf.locker.RLock()
	defer slf.locker.RUnlock()
	r := make([]uint64, 0, len(slf.mapClient))
	for i, _ := range slf.mapClient {
		r = append(r, i)
	}
	return r
}

func (slf *WebsocketServer) BroadcastMsg(messageType int, msg []byte) int {
	slf.locker.RLock()
	defer slf.locker.RUnlock()
	err := 0
	wsMsg := WSMessage{messageType, msg}
	for _, value := range slf.mapClient {
		if len(value.bwritemsg) >= MAX_MSG_COUNT {
			service.GetLogger().Printf(sysmodule.LEVER_ERROR, "message chan is full :%d\n", len(value.bwritemsg))
			err++
		}
		value.bwritemsg <- wsMsg
	}
	return err
}

func (slf *WebsocketServer) SendMsg(clientid uint64, messageType int, msg []byte) bool {
	slf.locker.RLock()
	defer slf.locker.RUnlock()
	value, ok := slf.mapClient[clientid]
	if ok == false {
		return false
	}

	if len(value.bwritemsg) >= MAX_MSG_COUNT {
		service.GetLogger().Printf(sysmodule.LEVER_ERROR, "message chan is full :%d\n", len(value.bwritemsg))
		return false
	}

	value.bwritemsg <- WSMessage{messageType, msg}

	return true
}

func (slf *WebsocketServer) Disconnect(clientid uint64) {
	slf.locker.Lock()
	defer slf.locker.Unlock()
	value, ok := slf.mapClient[clientid]
	if ok == false {
		return
	}

	value.conn.Close()
}

func (slf *WebsocketServer) Stop() {
}

func (slf *BaseMessageReciver) startReadMsg(pclient *WSClient) {
	defer func() {
		if r := recover(); r != nil {
			var coreInfo string
			coreInfo = string(debug.Stack())
			coreInfo += "\n" + fmt.Sprintf("Core information is %v\n", r)
			service.GetLogger().Printf(service.LEVER_FATAL, coreInfo)
			slf.messageReciver.OnDisconnect(pclient.clientid, errors.New("Core dump"))
			slf.WsServer.ReleaseClient(pclient)
		}
	}()

	for {
		pclient.conn.SetReadDeadline(time.Now().Add(15 * time.Second))
		msgtype, message, err := pclient.conn.ReadMessage()
		if err != nil {
			slf.messageReciver.OnDisconnect(pclient.clientid, err)
			slf.WsServer.ReleaseClient(pclient)

			return
		}

		slf.messageReciver.OnRecvMsg(pclient.clientid, msgtype, message)
	}
}

func (slf *BaseMessageReciver) initReciver(messageReciver IMessageReceiver, websocketServer IWebsocketServer) {
	slf.messageReciver = messageReciver
	slf.WsServer = websocketServer
}

func (slf *BaseMessageReciver) OnConnected(clientid uint64) {
}

func (slf *BaseMessageReciver) OnDisconnect(clientid uint64, err error) {
}

func (slf *BaseMessageReciver) OnRecvMsg(clientid uint64, msgtype int, data []byte) {
}

func (slf *BaseMessageReciver) OnHandleHttp(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Upgrade(w, r, w.Header(), 1024, 1024)
	if err != nil {
		http.Error(w, "Could not open websocket connection", http.StatusBadRequest)
		return
	}

	pclient := slf.WsServer.CreateClient(conn)
	slf.messageReciver.OnConnected(pclient.clientid)
	go pclient.startSendMsg()
	go slf.startReadMsg(pclient)
}

func (slf *WebsocketServer) initRouterHandler() http.Handler {
	r := mux.NewRouter()

	for pattern, reciver := range slf.reciver {
		if reciver.messageReciver.IsInit() == true {
			r.HandleFunc(pattern, reciver.messageReciver.OnHandleHttp)
		}
	}

	cors := cors.AllowAll()
	return cors.Handler(r)
}

func (slf *WebsocketServer) SetWSS(certfile string, keyfile string) bool {
	if certfile == "" || keyfile == "" {
		return false
	}
	slf.caList = append(slf.caList, CA{certfile, keyfile})
	slf.iswss = true
	return true
}
