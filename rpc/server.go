package rpc

import (
	"fmt"
	"github.com/duanhf2012/origin/log"
	"github.com/duanhf2012/origin/network"
	"math"
	"net"
	"reflect"
	"strings"
)

var processor iprocessor = &JsonProcessor{}
var LittleEndian bool

type Call struct {
	Seq uint64
	ServiceMethod string
	Arg interface{}
	Reply interface{}
	Respone *RpcResponse
	Err error
	done          chan *Call  // Strobes when call is complete.
	connid int
	callback *reflect.Value
	rpcHandler IRpcHandler
}

func (slf *Call) Done() *Call{
	return <-slf.done
}

type iprocessor interface {
	Marshal(v interface{}) ([]byte, error)
	Unmarshal(data []byte, v interface{}) error
}

type Server struct {
	functions map[interface{}]interface{}
	listenAddr string //ip:port

	cmdchannel chan *Call

	rpcHandleFinder RpcHandleFinder
	rpcserver *network.TCPServer
}

type RpcHandleFinder interface {
	FindRpcHandler(serviceMethod string) IRpcHandler
}

func (slf *Server) Init(rpcHandleFinder RpcHandleFinder) {
	slf.cmdchannel = make(chan *Call,10000)
	slf.rpcHandleFinder = rpcHandleFinder
	slf.rpcserver = &network.TCPServer{}
}

func (slf *Server) Start(listenAddr string) {
	slf.listenAddr = listenAddr
	slf.rpcserver.Addr = listenAddr
	slf.rpcserver.LenMsgLen = 2 //uint16
	slf.rpcserver.MinMsgLen = 2
	slf.rpcserver.MaxMsgLen = math.MaxUint16
	slf.rpcserver.MaxConnNum = 10000
	slf.rpcserver.PendingWriteNum = 10000
	slf.rpcserver.NewAgent =slf.NewAgent
	slf.rpcserver.LittleEndian = LittleEndian
	slf.rpcserver.Start()
}


func (gate *RpcAgent) OnDestroy() {}

type RpcAgent struct {
	conn     network.Conn
	rpcserver     *Server
	userData interface{}
}


func (agent *RpcAgent) WriteRespone(serviceMethod string,seq uint64,reply interface{},err *RpcError) {
	var rpcRespone RpcResponse
	rpcRespone.Seq = seq
	rpcRespone.Err = err
	var errM error
	if reply!=nil {
		rpcRespone.Reply,errM = processor.Marshal(reply)
		if errM != nil {
			rpcRespone.Err = ConvertError(errM)
		}
	}

	bytes,errM :=  processor.Marshal(&rpcRespone)
	if errM != nil {
		log.Error("service method %s %+v Marshal error:%+v!", serviceMethod,rpcRespone,errM)
		return
	}

	errM = agent.conn.WriteMsg(bytes)
	if errM != nil {
		log.Error("Rpc %s return is error:%+v",serviceMethod,errM)
	}
}


func (agent *RpcAgent) Run() {
	for {
		data,err := agent.conn.ReadMsg()
		if err != nil {
			log.Error("read message: %v", err)
			//will close tcpconn
			break
		}

		//解析head
		var req RpcRequest
		err = processor.Unmarshal(data,&req)
		if err != nil {
			if req.Seq>0 {
				rpcError := RpcError("rpc Unmarshal request is error")
				agent.WriteRespone(req.ServiceMethod,req.Seq,nil,&rpcError)
				continue
			}else{
				log.Error("rpc Unmarshal request is error: %v", err)
				//will close tcpconn
				break
			}
		}

		//交给程序处理
		serviceMethod := strings.Split(req.ServiceMethod,".")
		if len(serviceMethod)!=2 {
			rpcError := RpcError("rpc request req.ServiceMethod is error")
			agent.WriteRespone(req.ServiceMethod,req.Seq,nil,&rpcError)
			log.Debug("rpc request req.ServiceMethod is error")
			continue
		}

		rpcHandler := agent.rpcserver.rpcHandleFinder.FindRpcHandler(serviceMethod[0])
		if rpcHandler== nil {
			rpcError := RpcError(fmt.Sprintf("service method %s not config!", req.ServiceMethod))
			agent.WriteRespone(req.ServiceMethod,req.Seq,nil,&rpcError)
			log.Error("service method %s not config!", req.ServiceMethod)
			continue
		}

		if req.NoReply == false {
			req.requestHandle = func(Returns interface{},Err *RpcError){
				agent.WriteRespone(req.ServiceMethod,req.Seq,Returns,Err)
			}
		}

		rpcHandler.PushRequest(&req)
	}
}

func (agent *RpcAgent) OnClose() {
}

func (agent *RpcAgent) WriteMsg(msg interface{}) {
}

func (agent *RpcAgent) LocalAddr() net.Addr {
	return agent.conn.LocalAddr()
}

func (agent *RpcAgent) RemoteAddr() net.Addr {
	return agent.conn.RemoteAddr()
}

func (agent *RpcAgent)  Close() {
	agent.conn.Close()
}

func (agent *RpcAgent) Destroy() {
	agent.conn.Destroy()
}


func (slf *Server) NewAgent(conn *network.TCPConn) network.Agent {
	agent := &RpcAgent{conn: conn, rpcserver: slf}

	return agent
}

func (slf *Server) myselfRpcHandlerGo(handlerName string,methodName string, args interface{},reply interface{}) error {
	rpcHandler := slf.rpcHandleFinder.FindRpcHandler(handlerName)
	if rpcHandler== nil {
		err := fmt.Errorf("service method %s.%s not config!", handlerName,methodName)
		log.Error("%s",err.Error())
		return err
	}

	return rpcHandler.CallMethod(fmt.Sprintf("%s.%s",handlerName,methodName),args,reply)
}


func (slf *Server) rpcHandlerGo(noReply bool,handlerName string,methodName string, args interface{},reply interface{}) *Call {
	pCall := &Call{}
	pCall.done = make( chan *Call,1)
	rpcHandler := slf.rpcHandleFinder.FindRpcHandler(handlerName)
	if rpcHandler== nil {
		pCall.Err = fmt.Errorf("service method %s.%s not config!", handlerName,methodName)
		log.Error("%s",pCall.Err.Error())
		pCall.done <- pCall
		return pCall
	}
	var req RpcRequest
	req.ServiceMethod = fmt.Sprintf("%s.%s",handlerName,methodName)
	req.localParam = args
	req.localReply = reply
	req.NoReply = noReply

	if noReply == false {
		req.requestHandle = func(Returns interface{},Err *RpcError){
			if Err!=nil {
				pCall.Err = Err
			}else{
				pCall.Err = nil
			}

			pCall.done <- pCall
		}
	}


	rpcHandler.PushRequest(&req)

	return pCall
}

func (slf *Server) rpcHandlerAsyncGo(callerRpcHandler IRpcHandler,noReply bool,handlerName string,methodName string,args interface{},reply interface{},callback reflect.Value) error {
	pCall := &Call{}
	//pCall.done = make( chan *Call,1)
	pCall.rpcHandler = callerRpcHandler
	pCall.callback = &callback
	pCall.Reply = reply
	rpcHandler := slf.rpcHandleFinder.FindRpcHandler(handlerName)
	if rpcHandler== nil {
		err := fmt.Errorf("service method %s.%s not config!", handlerName,methodName)
		log.Error("%+v",err)
		return err
	}

	var req RpcRequest
	req.ServiceMethod = fmt.Sprintf("%s.%s",handlerName,methodName)
	req.localParam = args
	req.localReply = reply
	req.NoReply = noReply

	if noReply == false {
		req.requestHandle = func(Returns interface{},Err *RpcError){
			if Err == nil {
				pCall.Err = nil
			}else{
				pCall.Err = Err
			}

			if Returns!=nil {
				pCall.Reply = Returns
			}
			pCall.rpcHandler.(*RpcHandler).callResponeCallBack<-pCall
		}
	}


	err := rpcHandler.PushRequest(&req)
	if err != nil {
		return err
	}

	return nil
}
