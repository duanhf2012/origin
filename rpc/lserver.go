package rpc
import (
	"errors"
	"github.com/duanhf2012/origin/v2/log"
	"reflect"
	"time"
	"strings"
	"fmt"
)


type BaseServer struct {
	localNodeId string
	compressBytesLen int

	rpcHandleFinder RpcHandleFinder
	iServer IServer
}

func (ls *BaseServer) initBaseServer(compressBytesLen int,rpcHandleFinder RpcHandleFinder){
	ls.compressBytesLen = compressBytesLen
	ls.rpcHandleFinder = rpcHandleFinder
}

func (ls *BaseServer) myselfRpcHandlerGo(client *Client,handlerName string, serviceMethod string, args interface{},callBack reflect.Value, reply interface{}) error {
	rpcHandler := ls.rpcHandleFinder.FindRpcHandler(handlerName)
	if rpcHandler == nil {
		err := errors.New("service method " + serviceMethod + " not config!")
		log.Error("service method not config",log.String("serviceMethod",serviceMethod))
		return err
	}

	return rpcHandler.CallMethod(client,serviceMethod, args,callBack, reply)
}

func (ls *BaseServer) selfNodeRpcHandlerGo(timeout time.Duration,processor IRpcProcessor, client *Client, noReply bool, handlerName string, rpcMethodId uint32, serviceMethod string, args interface{}, reply interface{}, rawArgs []byte) *Call {
	pCall := MakeCall()
	pCall.Seq = client.generateSeq()
	pCall.TimeOut = timeout
	pCall.ServiceMethod = serviceMethod

	rpcHandler := ls.rpcHandleFinder.FindRpcHandler(handlerName)
	if rpcHandler == nil {
		err := errors.New("service method " + serviceMethod + " not config!")
		log.Error("service method not config",log.String("serviceMethod",serviceMethod),log.ErrorAttr("error",err))
		pCall.Seq = 0
		pCall.DoError(err)

		return pCall
	}

	var iParam interface{}
	if processor == nil {
		_, processor = GetProcessorType(args)
	}

	if args != nil {
		var err error
		iParam,err = processor.Clone(args)
		if err != nil {
			sErr := errors.New("RpcHandler " + handlerName + "."+serviceMethod+" deep copy inParam is error:" + err.Error())
			log.Error("deep copy inParam is failed",log.String("handlerName",handlerName),log.String("serviceMethod",serviceMethod))
			pCall.Seq = 0
			pCall.DoError(sErr)

			return pCall
		}
	}

	req := MakeRpcRequest(processor, 0, rpcMethodId, serviceMethod, noReply, nil)
	req.inParam = iParam
	req.localReply = reply
	if rawArgs != nil {
		var err error
		req.inParam, err = rpcHandler.UnmarshalInParam(processor, serviceMethod, rpcMethodId, rawArgs)
		if err != nil {
			log.Error("unmarshalInParam is failed",log.String("serviceMethod",serviceMethod),log.Uint32("rpcMethodId",rpcMethodId),log.ErrorAttr("error",err))
			pCall.Seq = 0
			pCall.DoError(err)
			ReleaseRpcRequest(req)
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
					Err = ConvertError(err)
					log.Error("returns data cannot be marshal",log.Uint64("seq",callSeq),log.ErrorAttr("error",err))
				}else{
					err = req.rpcProcessor.Unmarshal(byteReturns, reply)
					if err != nil {
						Err = ConvertError(err)
						log.Error("returns data cannot be Unmarshal",log.Uint64("seq",callSeq),log.ErrorAttr("error",err))
					}
				}
			}

			ReleaseRpcRequest(req)
			v := client.RemovePending(callSeq)
			if v == nil {
				log.Error("rpcClient cannot find seq",log.Uint64("seq",callSeq))
				return
			}

			if len(Err) == 0 {
				v.Err = nil
				v.DoOK()
			} else {
				log.Error(Err.Error())
				v.DoError(Err)
			}
		}
	}

	err := rpcHandler.PushRpcRequest(req)
	if err != nil {
		log.Error(err.Error())
		pCall.DoError(err)
		ReleaseRpcRequest(req)
	}

	return pCall
}

func (server *BaseServer) selfNodeRpcHandlerAsyncGo(timeout time.Duration,client *Client, callerRpcHandler IRpcHandler, noReply bool, handlerName string, serviceMethod string, args interface{}, reply interface{}, callback reflect.Value,cancelable bool) (CancelRpc,error)  {
	rpcHandler := server.rpcHandleFinder.FindRpcHandler(handlerName)
	if rpcHandler == nil {
		err := errors.New("service method " + serviceMethod + " not config!")
		log.Error(err.Error())
		return emptyCancelRpc,err
	}

	_, processor := GetProcessorType(args)
	iParam,err := processor.Clone(args)
	if err != nil {
		errM := errors.New("RpcHandler " + handlerName + "."+serviceMethod+" deep copy inParam is error:" + err.Error())
		log.Error(errM.Error())
		return emptyCancelRpc,errM
	}

	req := MakeRpcRequest(processor, 0, 0, serviceMethod, noReply, nil)
	req.inParam = iParam
	req.localReply = reply

	cancelRpc := emptyCancelRpc
	var callSeq uint64
	if noReply == false {
		callSeq = client.generateSeq()
		pCall := MakeCall()
		pCall.Seq = callSeq
		pCall.rpcHandler = callerRpcHandler
		pCall.callback = &callback
		pCall.Reply = reply
		pCall.ServiceMethod = serviceMethod
		pCall.TimeOut = timeout
		client.AddPending(pCall)
		rpcCancel :=  RpcCancel{CallSeq: callSeq,Cli: client}
		cancelRpc = rpcCancel.CancelRpc

		req.requestHandle = func(Returns interface{}, Err RpcError) {
			v := client.RemovePending(callSeq)
			if v == nil {
				ReleaseRpcRequest(req)
				return
			}
			if len(Err) == 0 {
				v.Err = nil
			} else {
				v.Err = Err
			}

			if Returns != nil {
				v.Reply = Returns
			}
			v.rpcHandler.PushRpcResponse(v)
			ReleaseRpcRequest(req)
		}
	}

	err = rpcHandler.PushRpcRequest(req)
	if err != nil {
		ReleaseRpcRequest(req)
		if callSeq > 0 {
			client.RemovePending(callSeq)
		}
		return emptyCancelRpc,err
	}

	return cancelRpc,nil
}

func (bs *BaseServer) processRpcRequest(data []byte,connTag string,wrResponse writeResponse) error{
	bCompress := (data[0]>>7) > 0
	processor := GetProcessor(data[0]&0x7f)
	if processor == nil {
		return errors.New("cannot find processor")
	}

	//解析head
	var compressBuff []byte
	byteData := data[1:]
	if bCompress == true {
		var unCompressErr error

		compressBuff,unCompressErr = compressor.UncompressBlock(byteData)
		if unCompressErr!= nil {
			return errors.New("UncompressBlock failed")
		}

		byteData = compressBuff
	}

	req := MakeRpcRequest(processor, 0, 0, "", false, nil)
	err := processor.Unmarshal(byteData, req.RpcRequestData)
	if cap(compressBuff) > 0 {
		compressor.UnCompressBufferCollection(compressBuff)
	}

	if err != nil {
		if req.RpcRequestData.GetSeq() > 0 {
			rpcError := RpcError(err.Error())
			if req.RpcRequestData.IsNoReply() == false {
				wrResponse(processor,connTag, req.RpcRequestData.GetServiceMethod(), req.RpcRequestData.GetSeq(), nil, rpcError)
			}
		}

		ReleaseRpcRequest(req)
		return err
	}

	//交给程序处理
	serviceMethod := strings.Split(req.RpcRequestData.GetServiceMethod(), ".")
	if len(serviceMethod) < 1 {
		rpcError := RpcError("rpc request req.ServiceMethod is error")
		if req.RpcRequestData.IsNoReply() == false {
			wrResponse(processor,connTag, req.RpcRequestData.GetServiceMethod(), req.RpcRequestData.GetSeq(), nil, rpcError)
		}
		ReleaseRpcRequest(req)
		log.Error("rpc request req.ServiceMethod is error")
		return nil
	}

	rpcHandler := bs.rpcHandleFinder.FindRpcHandler(serviceMethod[0])
	if rpcHandler == nil {
		rpcError := RpcError(fmt.Sprintf("service method %s not config!", req.RpcRequestData.GetServiceMethod()))
		if req.RpcRequestData.IsNoReply() == false {
			wrResponse(processor,connTag, req.RpcRequestData.GetServiceMethod(), req.RpcRequestData.GetSeq(), nil, rpcError)
		}
		log.Error("serviceMethod not config",log.String("serviceMethod",req.RpcRequestData.GetServiceMethod()))
		ReleaseRpcRequest(req)
		return nil
	}

	if req.RpcRequestData.IsNoReply() == false {
		req.requestHandle = func(Returns interface{}, Err RpcError) {
			wrResponse(processor,connTag, req.RpcRequestData.GetServiceMethod(), req.RpcRequestData.GetSeq(), Returns, Err)
			ReleaseRpcRequest(req)
		}
	}

	req.inParam, err = rpcHandler.UnmarshalInParam(req.rpcProcessor, req.RpcRequestData.GetServiceMethod(), req.RpcRequestData.GetRpcMethodId(), req.RpcRequestData.GetInParam())
	if err != nil {
		rErr := "Call Rpc " + req.RpcRequestData.GetServiceMethod() + " Param error " + err.Error()
		log.Error("call rpc param error",log.String("serviceMethod",req.RpcRequestData.GetServiceMethod()),log.ErrorAttr("error",err))
		if req.requestHandle != nil {
			req.requestHandle(nil, RpcError(rErr))
		} else {
			ReleaseRpcRequest(req)
		}

		return nil
	}

	err = rpcHandler.PushRpcRequest(req)
	if err != nil {
		rpcError := RpcError(err.Error())

		if req.RpcRequestData.IsNoReply() {
			wrResponse(processor, connTag,req.RpcRequestData.GetServiceMethod(), req.RpcRequestData.GetSeq(), nil, rpcError)
		}

		ReleaseRpcRequest(req)
	}

	return nil
}