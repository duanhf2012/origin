package network

import (
	"fmt"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"
)

type IWebsocketServer interface {
	SendMsg(clientid uint64, messageType int, msg []byte) bool
}

type IMessageReceiver interface {
	OnConnected(webServer IWebsocketServer, clientid uint64)
	OnDisconnect(webServer IWebsocketServer, clientid uint64, err error)
	OnRecvMsg(webServer IWebsocketServer, clientid uint64, msgtype int, data []byte)
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

	pattern            string
	port               uint16
	bEnableCompression bool
	locker             sync.Mutex
	messageReciver     IMessageReceiver
}

func (slf *WebsocketServer) wsHandler(w http.ResponseWriter, r *http.Request) {
	/*if r.Header.Get("Origin") != "http://"+r.Host {
		http.Error(w, "Origin not allowed", 403)
		return
	}
	*/
	conn, err := websocket.Upgrade(w, r, w.Header(), 1024, 1024)
	if err != nil {
		http.Error(w, "Could not open websocket connection", http.StatusBadRequest)
	}

	slf.maxClientid++
	pclient := &WSClient{slf.maxClientid, conn, make(chan WSMessage, 1024)}
	slf.mapClient[pclient.clientid] = pclient
	slf.messageReciver.OnConnected(slf, pclient.clientid)
	go pclient.startSendMsg()
	go slf.OnConnected(pclient)
}

func (slf *WebsocketServer) OnConnected(pclient *WSClient) {
	for {
		msgtype, message, err := pclient.conn.ReadMessage()
		if err != nil {
			pclient.conn.Close()
			slf.locker.Lock()
			defer slf.locker.Unlock()
			delete(slf.mapClient, pclient.clientid)
			slf.messageReciver.OnDisconnect(slf, pclient.clientid, err)
			return
		}

		slf.messageReciver.OnRecvMsg(slf, pclient.clientid, msgtype, message)
	}
}

func (slf *WebsocketServer) Init(pattern string, port uint16, messageReciver IMessageReceiver, bEnableCompression bool) {
	slf.pattern = pattern
	slf.port = port
	slf.bEnableCompression = bEnableCompression
	slf.mapClient = make(map[uint64]*WSClient)
	slf.messageReciver = messageReciver

	http.HandleFunc("/ws", slf.wsHandler)
}

func (slf *WebsocketServer) startListen() {
	address := fmt.Sprintf(":%d", slf.port)
	err := http.ListenAndServe(address, nil)
	if err != nil {
		fmt.Printf("%v", err)
	}
}

func (slf *WSClient) startSendMsg() {
	for {
		msgbuf := <-slf.bwritemsg
		slf.conn.WriteMessage(msgbuf.msgtype, msgbuf.bwritemsg)
	}
}

func (slf *WebsocketServer) Start() {

	go slf.startListen()
}

func (slf *WebsocketServer) SendMsg(clientid uint64, messageType int, msg []byte) bool {
	slf.locker.Lock()
	defer slf.locker.Unlock()
	value, ok := slf.mapClient[clientid]
	if ok == false {
		return false
	}

	value.bwritemsg <- WSMessage{messageType, msg}
	return true
}

func (slf *WebsocketServer) Stop() {

}
