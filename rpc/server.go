package rpc

import (
	"errors"
	"fmt"
	"github.com/duanhf2012/origin/log"
	"github.com/duanhf2012/origin/network"
	"math"
	"net"
	"reflect"
	"strings"
	"time"
)

type RpcProcessorType uint8

const (
	RpcProcessorJson   RpcProcessorType = 0
	RpcProcessorGoGoPB RpcProcessorType = 1
)

//var processor IRpcProcessor = &JsonProcessor{}
var arrayProcessor = []IRpcProcessor{&JsonProcessor{}, &GoGoPBProcessor{}}
var arrayProcessorLen uint8 = 2
var LittleEndian bool

type Server struct {
	functions       map[interface{}]interface{}
	rpcHandleFinder RpcHandleFinder
	rpcServer       *network.TCPServer
}

type RpcAgent struct {
	conn      network.Conn
	rpcServer *Server
	userData  interface{}
}

func AppendProcessor(rpcProcessor IRpcProcessor) {
	arrayProcessor = append(arrayProcessor, rpcProcessor)
	arrayProcessorLen++
}

func GetProcessorType(param interface{}) (RpcProcessorType, IRpcProcessor) {
	for i := uint8(1); i < arrayProcessorLen; i++ {
		if arrayProcessor[i].IsParse(param) == true {
			return RpcProcessorType(i), arrayProcessor[i]
		}
	}

	return RpcProcessorJson, arrayProcessor[RpcProcessorJson]
}

func GetProcessor(processorType uint8) IRpcProcessor {
	if processorType >= arrayProcessorLen {
		return nil
	}
	return arrayProcessor[processorType]
}

func (server *Server) Init(rpcHandleFinder RpcHandleFinder) {
	server.rpcHandleFinder = rpcHandleFinder
	server.rpcServer = &network.TCPServer{}
}

const Default_ReadWriteDeadline = 15*time.Second

func (server *Server) Start(listenAddr string, maxRpcParamLen uint32) {
	splitAddr := strings.Split(listenAddr, ":")
	if len(splitAddr) != 2 {
		log.SFatal("listen addr is error :", listenAddr)
	}

	server.rpcServer.Addr = ":" + splitAddr[1]
	server.rpcServer.LenMsgLen = 4 //uint16
	server.rpcServer.MinMsgLen = 2
	if maxRpcParamLen > 0 {
		server.rpcServer.MaxMsgLen = maxRpcParamLen
	} else {
		server.rpcServer.MaxMsgLen = math.MaxUint32
	}

	server.rpcServer.MaxConnNum = 100000
	server.rpcServer.PendingWriteNum = 2000000
	server.rpcServer.NewAgent = server.NewAgent
	server.rpcServer.LittleEndian = LittleEndian
	server.rpcServer.WriteDeadline = Default_ReadWriteDeadline
	server.rpcServer.ReadDeadline = Default_ReadWriteDeadline
	server.rpcServer.Start()
}

func (agent *RpcAgent) OnDestroy() {}

func (agent *RpcAgent) WriteResponse(processor IRpcProcessor, serviceMethod string, seq uint64, reply interface{}, rpcError RpcError) {
	var mReply []byte
	var errM error

	if reply != nil {
		mReply, errM = processor.Marshal(reply)
		if errM != nil {
			rpcError = ConvertError(errM)
		}
	}

	var rpcResponse RpcResponse
	rpcResponse.RpcResponseData = processor.MakeRpcResponse(seq, rpcError, mReply)
	bytes, errM := processor.Marshal(rpcResponse.RpcResponseData)
	defer processor.ReleaseRpcResponse(rpcResponse.RpcResponseData)

	if errM != nil {
		log.SError("service method ", serviceMethod, " Marshal error:", errM.Error())
		return
	}

	errM = agent.conn.WriteMsg([]byte{uint8(processor.GetProcessorType())}, bytes)
	if errM != nil {
		log.SError("Rpc ", serviceMethod, " return is error:", errM.Error())
	}
}

func (agent *RpcAgent) Run() {
	for {
		data, err := agent.conn.ReadMsg()
		if err != nil {
			log.SError("remoteAddress:", agent.conn.RemoteAddr().String(), ",read message: ", err.Error())
			//will close tcpconn
			break
		}

		processor := GetProcessor(data[0])
		if processor == nil {
			agent.conn.ReleaseReadMsg(data)
			log.SError("remote rpc  ", agent.conn.RemoteAddr(), " cannot find processor:", data[0])
			return
		}

		//解析head
		req := MakeRpcRequest(processor, 0, 0, "", false, nil)
		err = processor.Unmarshal(data[1:], req.RpcRequestData)
		agent.conn.ReleaseReadMsg(data)
		if err != nil {
			log.SError("rpc Unmarshal request is error:", err.Error())
			if req.RpcRequestData.GetSeq() > 0 {
				rpcError := RpcError(err.Error())
				if req.RpcRequestData.IsNoReply() == false {
					agent.WriteResponse(processor, req.RpcRequestData.GetServiceMethod(), req.RpcRequestData.GetSeq(), nil, rpcError)
				}
				ReleaseRpcRequest(req)
				continue
			} else {
				//will close tcpconn
				ReleaseRpcRequest(req)
				break
			}
		}

		//交给程序处理
		serviceMethod := strings.Split(req.RpcRequestData.GetServiceMethod(), ".")
		if len(serviceMethod) < 1 {
			rpcError := RpcError("rpc request req.ServiceMethod is error")
			if req.RpcRequestData.IsNoReply() == false {
				agent.WriteResponse(processor, req.RpcRequestData.GetServiceMethod(), req.RpcRequestData.GetSeq(), nil, rpcError)
			}
			ReleaseRpcRequest(req)
			log.SError("rpc request req.ServiceMethod is error")
			continue
		}

		rpcHandler := agent.rpcServer.rpcHandleFinder.FindRpcHandler(serviceMethod[0])
		if rpcHandler == nil {
			rpcError := RpcError(fmt.Sprintf("service method %s not config!", req.RpcRequestData.GetServiceMethod()))
			if req.RpcRequestData.IsNoReply() == false {
				agent.WriteResponse(processor, req.RpcRequestData.GetServiceMethod(), req.RpcRequestData.GetSeq(), nil, rpcError)
			}

			log.SError("service method ", req.RpcRequestData.GetServiceMethod(), " not config!")
			ReleaseRpcRequest(req)
			continue
		}

		if req.RpcRequestData.IsNoReply() == false {
			req.requestHandle = func(Returns interface{}, Err RpcError) {
				agent.WriteResponse(processor, req.RpcRequestData.GetServiceMethod(), req.RpcRequestData.GetSeq(), Returns, Err)
				ReleaseRpcRequest(req)
			}
		}

		req.inParam, err = rpcHandler.UnmarshalInParam(req.rpcProcessor, req.RpcRequestData.GetServiceMethod(), req.RpcRequestData.GetRpcMethodId(), req.RpcRequestData.GetInParam())
		if err != nil {
			rErr := "Call Rpc " + req.RpcRequestData.GetServiceMethod() + " Param error " + err.Error()
			if req.requestHandle != nil {
				req.requestHandle(nil, RpcError(rErr))
			} else {
				ReleaseRpcRequest(req)
			}
			log.SError(rErr)
			continue
		}

		err = rpcHandler.PushRpcRequest(req)
		if err != nil {
			rpcError := RpcError(err.Error())

			if req.RpcRequestData.IsNoReply() {
				agent.WriteResponse(processor, req.RpcRequestData.GetServiceMethod(), req.RpcRequestData.GetSeq(), nil, rpcError)
			}

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

func (agent *RpcAgent) Close() {
	agent.conn.Close()
}

func (agent *RpcAgent) Destroy() {
	agent.conn.Destroy()
}

func (server *Server) NewAgent(c *network.TCPConn) network.Agent {
	agent := &RpcAgent{conn: c, rpcServer: server}

	return agent
}

func (server *Server) myselfRpcHandlerGo(client *Client,handlerName string, serviceMethod string, args interface{},callBack reflect.Value, reply interface{}) error {
	rpcHandler := server.rpcHandleFinder.FindRpcHandler(handlerName)
	if rpcHandler == nil {
		err := errors.New("service method " + serviceMethod + " not config!")
		log.SError(err.Error())
		return err
	}



	return rpcHandler.CallMethod(client,serviceMethod, args,callBack, reply)
}

func (server *Server) selfNodeRpcHandlerGo(processor IRpcProcessor, client *Client, noReply bool, handlerName string, rpcMethodId uint32, serviceMethod string, args interface{}, reply interface{}, rawArgs []byte) *Call {
	pCall := MakeCall()
	pCall.Seq = client.generateSeq()

	rpcHandler := server.rpcHandleFinder.FindRpcHandler(handlerName)
	if rpcHandler == nil {
		pCall.Seq = 0
		pCall.Err = errors.New("service method " + serviceMethod + " not config!")
		pCall.done <- pCall
		log.SError(pCall.Err.Error())

		return pCall
	}

	if processor == nil {
		_, processor = GetProcessorType(args)
	}
	req := MakeRpcRequest(processor, 0, rpcMethodId, serviceMethod, noReply, nil)
	req.inParam = args
	req.localReply = reply
	if rawArgs != nil {
		var err error
		req.inParam, err = rpcHandler.UnmarshalInParam(processor, serviceMethod, rpcMethodId, rawArgs)
		if err != nil {
			ReleaseRpcRequest(req)
			pCall.Err = err
			pCall.done <- pCall
			return pCall
		}
	}

	if noReply == false {
		client.AddPending(pCall)
		callSeq := pCall.Seq
		req.requestHandle = func(Returns interface{}, Err RpcError) {
			if reply != nil && Returns != reply && Returns != nil {
				byteReturns, err := req.rpcProcessor.Marshal(Returns)
				if err != nil {
					log.SError("returns data cannot be marshal ", callSeq)
					ReleaseRpcRequest(req)
				}

				err = req.rpcProcessor.Unmarshal(byteReturns, reply)
				if err != nil {
					log.SError("returns data cannot be Unmarshal ", callSeq)
					ReleaseRpcRequest(req)
				}
			}

			v := client.RemovePending(callSeq)
			if v == nil {
				log.SError("rpcClient cannot find seq ",callSeq, " in pending")
				ReleaseRpcRequest(req)
				return
			}
			if len(Err) == 0 {
				v.Err = nil
			} else {
				v.Err = Err
			}
			v.done <- v
			ReleaseRpcRequest(req)
		}
	}

	err := rpcHandler.PushRpcRequest(req)
	if err != nil {
		ReleaseRpcRequest(req)
		pCall.Err = err
		pCall.done <- pCall
	}

	return pCall
}

func (server *Server) selfNodeRpcHandlerAsyncGo(client *Client, callerRpcHandler IRpcHandler, noReply bool, handlerName string, serviceMethod string, args interface{}, reply interface{}, callback reflect.Value) error {
	rpcHandler := server.rpcHandleFinder.FindRpcHandler(handlerName)
	if rpcHandler == nil {
		err := errors.New("service method " + serviceMethod + " not config!")
		log.SError(err.Error())
		return err
	}

	_, processor := GetProcessorType(args)
	req := MakeRpcRequest(processor, 0, 0, serviceMethod, noReply, nil)
	req.inParam = args
	req.localReply = reply

	if noReply == false {
		callSeq := client.generateSeq()
		pCall := MakeCall()
		pCall.Seq = callSeq
		pCall.rpcHandler = callerRpcHandler
		pCall.callback = &callback
		pCall.Reply = reply
		pCall.ServiceMethod = serviceMethod
		client.AddPending(pCall)
		req.requestHandle = func(Returns interface{}, Err RpcError) {
			v := client.RemovePending(callSeq)
			if v == nil {
				log.SError("rpcClient cannot find seq ", pCall.Seq, " in pending")
				//ReleaseCall(pCall)
				ReleaseRpcRequest(req)
				return
			}
			if len(Err) == 0 {
				pCall.Err = nil
			} else {
				pCall.Err = Err
			}

			if Returns != nil {
				pCall.Reply = Returns
			}
			pCall.rpcHandler.PushRpcResponse(pCall)
			ReleaseRpcRequest(req)
		}
	}

	err := rpcHandler.PushRpcRequest(req)
	if err != nil {
		ReleaseRpcRequest(req)
		return err
	}

	return nil
}
