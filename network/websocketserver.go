package network

import (
	"fmt"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
	"github.com/gotoxu/cors"
)

type IWebsocketServer interface {
	SendMsg(clientid uint64, messageType int, msg []byte) bool
}

type IMessageReceiver interface {
	OnListen(webServer IWebsocketServer)
	OnConnected(clientid uint64)
	OnDisconnect(clientid uint64, err error)
	OnRecvMsg(clientid uint64, msgtype int, data []byte)
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

	httpserver *http.Server
}

func (slf *WebsocketServer) wsHandler(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Upgrade(w, r, w.Header(), 1024, 1024)
	if err != nil {
		http.Error(w, "Could not open websocket connection", http.StatusBadRequest)
	}

	slf.maxClientid++
	pclient := &WSClient{slf.maxClientid, conn, make(chan WSMessage, 1024)}
	slf.mapClient[pclient.clientid] = pclient

	slf.messageReciver.OnConnected(pclient.clientid)
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
			slf.messageReciver.OnDisconnect(pclient.clientid, err)
			return
		}

		slf.messageReciver.OnRecvMsg(pclient.clientid, msgtype, message)
	}
}

func (slf *WebsocketServer) Init(pattern string, port uint16, messageReciver IMessageReceiver, bEnableCompression bool) {
	slf.pattern = pattern
	slf.port = port
	slf.bEnableCompression = bEnableCompression
	slf.mapClient = make(map[uint64]*WSClient)
	slf.messageReciver = messageReciver

	//http.HandleFunc(slf.pattern, slf.wsHandler)
}

func (slf *WebsocketServer) startListen() {

	listenPort := fmt.Sprintf(":%d", slf.port)

	slf.httpserver = &http.Server{
		Addr:           listenPort,
		Handler:        slf.initRouterHandler(),
		ReadTimeout:    10 * time.Second,
		WriteTimeout:   10 * time.Second,
		MaxHeaderBytes: 1 << 20,
	}

	slf.messageReciver.OnListen(slf)
	err := slf.httpserver.ListenAndServe()
	if err != nil {
		fmt.Printf("http.ListenAndServe(%d, nil) error\n", slf.port)
		os.Exit(1)
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

func (slf *WebsocketServer) initRouterHandler() http.Handler {
	r := mux.NewRouter()
	/*r.HandleFunc("/{server:[a-zA-Z0-9]+}/{method:[a-zA-Z0-9]+}", func(w http.ResponseWriter, r *http.Request) {
		slf.wsHandler(w, r)
	})
	*/
	r.HandleFunc(slf.pattern, func(w http.ResponseWriter, r *http.Request) {
		slf.wsHandler(w, r)
	})

	cors := cors.AllowAll()
	//return cors.Handler(gziphandler.GzipHandler(r))
	return cors.Handler(r)
}
