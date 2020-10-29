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
type RpcProcessorType uint8

const (
	RPC_PROCESSOR_JSON RpcProcessorType = 0
	RPC_PROCESSOR_PB RpcProcessorType = 1
)

//var processor IRpcProcessor = &JsonProcessor{}
var arrayProcessor  = []IRpcProcessor{&JsonProcessor{},&PBProcessor{}}
var arrayProcessorLen uint8 = 2
var LittleEndian bool

type Server struct {
	functions map[interface{}]interface{}
	cmdchannel chan *Call
	rpcHandleFinder RpcHandleFinder
	rpcserver *network.TCPServer
}

func AppendProcessor(rpcProcessor IRpcProcessor) {
	arrayProcessor = append(arrayProcessor,rpcProcessor)
	arrayProcessorLen++
}

func GetProcessorType(param interface{}) (RpcProcessorType,IRpcProcessor){
	for i:=uint8(1);i<arrayProcessorLen;i++{
		if arrayProcessor[i].IsParse(param) == true {
			return RpcProcessorType(i),arrayProcessor[i]
		}
	}

	return RPC_PROCESSOR_JSON,arrayProcessor[RPC_PROCESSOR_JSON]
}

func GetProcessor(processorType uint8) IRpcProcessor{
	if processorType>=arrayProcessorLen{
		return nil
	}
	return arrayProcessor[processorType]
}

func (slf *Server) Init(rpcHandleFinder RpcHandleFinder) {
	slf.cmdchannel = make(chan *Call,100000)
	slf.rpcHandleFinder = rpcHandleFinder
	slf.rpcserver = &network.TCPServer{}
}

func (slf *Server) Start(listenAddr string) {
	 splitAddr := strings.Split(listenAddr,":")
	 if len(splitAddr)!=2{
	 	log.Fatal("listen addr is error :%s",listenAddr)
	 }
	slf.rpcserver.Addr = ":"+splitAddr[1]
	slf.rpcserver.LenMsgLen = 2 //uint16
	slf.rpcserver.MinMsgLen = 2
	slf.rpcserver.MaxMsgLen = math.MaxUint16
	slf.rpcserver.MaxConnNum = 10000
	slf.rpcserver.PendingWriteNum = 2000000
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


func (agent *RpcAgent) WriteRespone(processor IRpcProcessor,serviceMethod string,seq uint64,reply interface{},err *RpcError) {
	var mReply []byte
	var rpcError *RpcError
	var errM error

	if err != nil {
		rpcError = err
	} else {
		if reply!=nil {
			mReply,errM = processor.Marshal(reply)
			if errM != nil {
				rpcError = ConvertError(errM)
			}
		}
	}

	var rpcResponse RpcResponse
	rpcResponse.RpcResponeData = processor.MakeRpcResponse(seq,rpcError,mReply)
	bytes,errM :=  processor.Marshal(rpcResponse.RpcResponeData)
	defer processor.ReleaseRpcRespose(rpcResponse.RpcResponeData)

	if errM != nil {
		log.Error("service method %s %+v Marshal error:%+v!", serviceMethod,rpcResponse,errM)
		return
	}

	errM = agent.conn.WriteMsg([]byte{uint8(processor.GetProcessorType())},bytes)
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
		processor := GetProcessor(uint8(data[0]))
		if processor==nil {
			agent.conn.ReleaseReadMsg(data)
			log.Error("remote rpc  %s data head error:%+v",agent.conn.RemoteAddr(),err)
			return
		}

		//解析head
		req := MakeRpcRequest()
		req.rpcProcessor = processor
		req.RpcRequestData = processor.MakeRpcRequest(0,"",false,nil,nil)
		err = processor.Unmarshal(data[1:],req.RpcRequestData)
		agent.conn.ReleaseReadMsg(data)
		if err != nil {
			log.Error("rpc Unmarshal request is error: %v", err)
			if req.RpcRequestData.GetSeq()>0 {
				rpcError := RpcError(err.Error())
				agent.WriteRespone(processor,req.RpcRequestData.GetServiceMethod(),req.RpcRequestData.GetSeq(),nil,&rpcError)
				processor.ReleaseRpcRequest(req.RpcRequestData)
				ReleaseRpcRequest(req)
				continue
			}else{
				//will close tcpconn
				processor.ReleaseRpcRequest(req.RpcRequestData)
				ReleaseRpcRequest(req)
				break
			}
		}

		//交给程序处理
		serviceMethod := strings.Split(req.RpcRequestData.GetServiceMethod(),".")
		if len(serviceMethod)!=2 {
			rpcError := RpcError("rpc request req.ServiceMethod is error")
			agent.WriteRespone(processor,req.RpcRequestData.GetServiceMethod(),req.RpcRequestData.GetSeq(),nil,&rpcError)
			processor.ReleaseRpcRequest(req.RpcRequestData)
			ReleaseRpcRequest(req)
			log.Debug("rpc request req.ServiceMethod is error")
			continue
		}

		rpcHandler := agent.rpcserver.rpcHandleFinder.FindRpcHandler(serviceMethod[0])
		if rpcHandler== nil {
			rpcError := RpcError(fmt.Sprintf("service method %s not config!", req.RpcRequestData.GetServiceMethod()))
			agent.WriteRespone(processor,req.RpcRequestData.GetServiceMethod(),req.RpcRequestData.GetSeq(),nil,&rpcError)
			processor.ReleaseRpcRequest(req.RpcRequestData)
			ReleaseRpcRequest(req)
			log.Error("service method %s not config!", req.RpcRequestData.GetServiceMethod())
			continue
		}

		if req.RpcRequestData.IsNoReply()==false {
			req.requestHandle = func(Returns interface{},Err *RpcError){
				agent.WriteRespone(processor,req.RpcRequestData.GetServiceMethod(),req.RpcRequestData.GetSeq(),Returns,Err)
			}
		}

		err = rpcHandler.PushRequest(req)
		if err != nil {
			rpcError := RpcError(err.Error())

			if req.RpcRequestData.IsNoReply() {
				agent.WriteRespone(processor,req.RpcRequestData.GetServiceMethod(),req.RpcRequestData.GetSeq(),nil,&rpcError)
			}

			processor.ReleaseRpcRequest(req.RpcRequestData)
			ReleaseRpcRequest(req)
		}
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


func (slf *Server) selfNodeRpcHandlerGo(processor IRpcProcessor,client *Client,noReply bool,handlerName string,methodName string, args interface{},rawArgs []byte,reply interface{},additionParam interface{}) *Call {
	pCall := MakeCall()
	pCall.Seq = client.generateSeq()

	rpcHandler := slf.rpcHandleFinder.FindRpcHandler(handlerName)
	if rpcHandler== nil {
		pCall.Err = fmt.Errorf("service method %s.%s not config!", handlerName,methodName)
		log.Error("%s",pCall.Err.Error())
		pCall.done <- pCall
		return pCall
	}
	req :=  MakeRpcRequest()

	req.bLocalRequest = true
	req.localParam = args
	req.localReply = reply
	req.localRawParam = rawArgs
	if processor == nil {
		_,processor = GetProcessorType(args)
	}

	req.RpcRequestData = processor.MakeRpcRequest(0,fmt.Sprintf("%s.%s",handlerName,methodName),noReply,nil,additionParam)
	req.rpcProcessor = processor
	if noReply == false {
		client.AddPending(pCall)
		req.requestHandle = func(Returns interface{},Err *RpcError){
			v := client.RemovePending(pCall.Seq)
			if v == nil {
				log.Error("rpcClient cannot find seq %d in pending",pCall.Seq)
				ReleaseCall(pCall)
				return
			}

			if Err!=nil {
				pCall.Err = Err
			}else{
				pCall.Err = nil
			}

			pCall.done <- pCall
		}
	}

	err := rpcHandler.PushRequest(req)
	if err != nil {
		processor.ReleaseRpcRequest(req.RpcRequestData)
		ReleaseRpcRequest(req)
		pCall.Err = err
		pCall.done <- pCall
	}

	return pCall
}

func (slf *Server) selfNodeRpcHandlerAsyncGo(client *Client,callerRpcHandler IRpcHandler,noReply bool,handlerName string,methodName string,args interface{},reply interface{},callback reflect.Value) error {
	pCall := MakeCall()
	pCall.Seq = client.generateSeq()
	pCall.rpcHandler = callerRpcHandler
	pCall.callback = &callback
	pCall.Reply = reply
	rpcHandler := slf.rpcHandleFinder.FindRpcHandler(handlerName)
	if rpcHandler== nil {
		err := fmt.Errorf("service method %s.%s not config!", handlerName,methodName)
		log.Error("%+v",err)
		ReleaseCall(pCall)
		return err
	}

	req := MakeRpcRequest()
	req.localParam = args
	req.localReply = reply
	req.bLocalRequest = true
	_,processor := GetProcessorType(args)
	req.rpcProcessor =processor
	req.RpcRequestData = processor.MakeRpcRequest(0,fmt.Sprintf("%s.%s",handlerName,methodName),noReply,nil,nil)
	if noReply == false {
		client.AddPending(pCall)
		req.requestHandle = func(Returns interface{},Err *RpcError){
			v := client.RemovePending(pCall.Seq)
			if v == nil {
				log.Error("rpcClient cannot find seq %d in pending",pCall.Seq)
				ReleaseCall(pCall)
				processor.ReleaseRpcRequest(req.RpcRequestData)
				ReleaseRpcRequest(req)
				return
			}

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


	err := rpcHandler.PushRequest(req)
	if err != nil {
		processor.ReleaseRpcRequest(req.RpcRequestData)
		ReleaseRpcRequest(req)

		return err
	}

	return nil
}
