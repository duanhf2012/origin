package network

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"os"
	"reflect"
	"runtime/debug"
	"sync"
	"time"

	"github.com/duanhf2012/origin/service"
	"github.com/duanhf2012/origin/sysmodule"
	"github.com/gotoxu/cors"

	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
)

type IWSAgentServer interface {
	SendMsg(agentid uint32, messageType int, msg []byte) bool
	CreateAgent(urlPattern string, conn *websocket.Conn) IAgent
	Disconnect(agentid uint32)
	ReleaseAgent(iagent IAgent)
}

type IAgent interface {
	initAgent(conn *websocket.Conn, agentid uint32, iagent IAgent, WSAgentServer IWSAgentServer)
	startReadMsg()
	startSendMsg()
	OnConnected()
	OnDisconnect(err error)
	OnRecvMsg(msgtype int, data []byte)
	//OnHandleHttp(w http.ResponseWriter, r *http.Request)
	GetAgentId() uint32
	getConn() *websocket.Conn
	getWriteMsgChan() chan WSAgentMessage
}

type BaseAgent struct {
	service.BaseModule
	WsServer  IWSAgentServer
	agent     IAgent
	agentid   uint32
	conn      *websocket.Conn
	bwritemsg chan WSAgentMessage
	iagent    IAgent
}

type WSAgentMessage struct {
	msgtype   int
	bwritemsg []byte
}

type WSAgentServer struct {
	service.BaseModule
	wsUri      string
	maxAgentid uint32 //记录当前最新agentid
	//mapAgent   map[uint32]IAgent
	locker sync.Mutex

	port uint16

	httpserver *http.Server
	regAgent   map[string]reflect.Type

	caList []CA
	iswss  bool
}

const (
	MAX_AGENT_MSG_COUNT = 20480
)

func (slf *WSAgentServer) Init(port uint16) {
	slf.port = port
}

func (slf *WSAgentServer) CreateAgent(urlPattern string, conn *websocket.Conn) IAgent {
	slf.locker.Lock()
	iAgent, ok := slf.regAgent[urlPattern]
	if ok == false {
		slf.locker.Unlock()
		service.GetLogger().Printf(sysmodule.LEVER_WARN, "Cannot find %s pattern!", urlPattern)
		return nil
	}

	v := reflect.New(iAgent).Elem().Addr().Interface()
	if v == nil {
		slf.locker.Unlock()
		service.GetLogger().Printf(sysmodule.LEVER_WARN, "new %s pattern agent type is error!", urlPattern)
		return nil
	}

	pModule := v.(service.IModule)
	iagent := v.(IAgent)
	slf.maxAgentid++
	agentid := slf.maxAgentid
	iagent.initAgent(conn, agentid, iagent, slf)
	slf.AddModule(pModule)

	slf.locker.Unlock()

	service.GetLogger().Printf(sysmodule.LEVER_INFO, "Agent id %d is connected.", iagent.GetAgentId())
	return iagent
}

func (slf *WSAgentServer) ReleaseAgent(iagent IAgent) {
	iagent.getConn().Close()
	slf.locker.Lock()
	slf.ReleaseModule(iagent.GetAgentId())
	//delete(slf.mapAgent, iagent.GetAgentId())
	slf.locker.Unlock()
	//关闭写管道
	close(iagent.getWriteMsgChan())
	service.GetLogger().Printf(sysmodule.LEVER_INFO, "Agent id %d is disconnected.", iagent.GetAgentId())
}

func (slf *WSAgentServer) SetupAgent(pattern string, agent IAgent, bEnableCompression bool) {
	if slf.regAgent == nil {
		slf.regAgent = make(map[string]reflect.Type)
	}

	slf.regAgent[pattern] = reflect.TypeOf(agent).Elem() //reflect.TypeOf(agent).Elem()
}

func (slf *WSAgentServer) startListen() {
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

func (slf *BaseAgent) startSendMsg() {
	for {
		msgbuf, ok := <-slf.bwritemsg
		if ok == false {
			break
		}
		slf.conn.SetWriteDeadline(time.Now().Add(15 * time.Second))
		err := slf.conn.WriteMessage(msgbuf.msgtype, msgbuf.bwritemsg)
		if err != nil {
			service.GetLogger().Printf(sysmodule.LEVER_INFO, "write agent id %d is error :%v\n", slf.GetAgentId(), err)
			break
		}
	}
}

func (slf *WSAgentServer) Start() {
	go slf.startListen()
}

func (slf *WSAgentServer) GetAgentById(agentid uint32) IAgent {
	pModule := slf.GetModuleById(agentid)
	if pModule == nil {
		service.GetLogger().Printf(sysmodule.LEVER_ERROR, "GetAgentById :%d is fail.\n", agentid)
		return nil
	}

	return pModule.(IAgent)
}

func (slf *WSAgentServer) SendMsg(agentid uint32, messageType int, msg []byte) bool {
	slf.locker.Lock()
	defer slf.locker.Unlock()

	iagent := slf.GetAgentById(agentid)
	if iagent == nil {
		return false
	}

	if len(iagent.getWriteMsgChan()) >= MAX_AGENT_MSG_COUNT {
		service.GetLogger().Printf(sysmodule.LEVER_ERROR, "message chan is full :%d\n", len(iagent.getWriteMsgChan()))
		return false
	}

	iagent.getWriteMsgChan() <- WSAgentMessage{messageType, msg}

	return true
}

func (slf *WSAgentServer) Disconnect(agentid uint32) {
	slf.locker.Lock()
	defer slf.locker.Unlock()
	iagent := slf.GetAgentById(agentid)
	if iagent == nil {
		return
	}

	iagent.getConn().Close()
}

func (slf *WSAgentServer) Stop() {
}

func (slf *BaseAgent) startReadMsg() {
	defer func() {
		if r := recover(); r != nil {
			var coreInfo string
			coreInfo = string(debug.Stack())
			coreInfo += "\n" + fmt.Sprintf("Core information is %v\n", r)
			service.GetLogger().Printf(service.LEVER_FATAL, coreInfo)
			slf.agent.OnDisconnect(errors.New("Core dump"))
			slf.WsServer.ReleaseAgent(slf.agent)
		}
	}()

	slf.agent.OnConnected()
	for {
		slf.conn.SetReadDeadline(time.Now().Add(15 * time.Second))
		msgtype, message, err := slf.conn.ReadMessage()
		if err != nil {
			slf.agent.OnDisconnect(err)
			slf.WsServer.ReleaseAgent(slf.agent)
			return
		}

		slf.agent.OnRecvMsg(msgtype, message)
	}
}

func (slf *WSAgentServer) initRouterHandler() http.Handler {
	r := mux.NewRouter()

	for pattern, _ := range slf.regAgent {
		r.HandleFunc(pattern, slf.OnHandleHttp)
	}

	cors := cors.AllowAll()
	return cors.Handler(r)
}

func (slf *WSAgentServer) SetWSS(certfile string, keyfile string) bool {
	if certfile == "" || keyfile == "" {
		return false
	}
	slf.caList = append(slf.caList, CA{certfile, keyfile})
	slf.iswss = true
	return true
}

func (slf *BaseAgent) GetAgentId() uint32 {
	return slf.agentid
}

func (slf *BaseAgent) initAgent(conn *websocket.Conn, agentid uint32, iagent IAgent, WSAgentServer IWSAgentServer) {
	slf.agent = iagent
	slf.WsServer = WSAgentServer
	slf.bwritemsg = make(chan WSAgentMessage, MAX_AGENT_MSG_COUNT)
	slf.agentid = agentid
	slf.conn = conn
}

func (slf *BaseAgent) OnConnected() {
}

func (slf *BaseAgent) OnDisconnect(err error) {
}

func (slf *BaseAgent) OnRecvMsg(msgtype int, data []byte) {
}

func (slf *BaseAgent) getConn() *websocket.Conn {
	return slf.conn
}

func (slf *BaseAgent) getWriteMsgChan() chan WSAgentMessage {
	return slf.bwritemsg
}

func (slf *BaseAgent) SendMsg(agentid uint32, messageType int, msg []byte) bool {
	return slf.WsServer.SendMsg(agentid, messageType, msg)
}

func (slf *WSAgentServer) OnHandleHttp(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Upgrade(w, r, w.Header(), 1024, 1024)
	if err != nil {
		http.Error(w, "Could not open websocket connection!", http.StatusBadRequest)
		return
	}

	agent := slf.CreateAgent(r.URL.Path, conn)
	fmt.Print(agent.GetAgentId())
	slf.AddModule(agent.(service.IModule))
	go agent.startSendMsg()
	go agent.startReadMsg()
}
