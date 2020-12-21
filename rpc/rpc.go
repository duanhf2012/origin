package rpc

import (
	"reflect"
	"sync"
	"time"
)

type RpcRequest struct {
	ref bool
	RpcRequestData IRpcRequestData

	bLocalRequest bool
	localReply interface{}
	localParam interface{} //本地调用的参数列表
	inputArgs IRawInputArgs

	requestHandle RequestHandler
	callback *reflect.Value
	rpcProcessor IRpcProcessor
}

type RpcResponse struct {
	RpcResponseData IRpcResponseData
}

type Responder = RequestHandler

func (r *Responder) IsInvalid() bool {
	return reflect.ValueOf(*r).Pointer() == reflect.ValueOf(reqHandlerNull).Pointer()
}

//var rpcResponsePool sync.Pool
var rpcRequestPool sync.Pool
var rpcCallPool sync.Pool


type IRpcRequestData interface {
	GetSeq() uint64
	GetServiceMethod() string
	GetInParam() []byte
	IsNoReply() bool
	GetRpcMethodId() uint32
}

type IRpcResponseData interface {
	GetSeq() uint64
	GetErr() *RpcError
	GetReply() []byte
}

type IRawInputArgs interface {
	GetRawData() []byte  //获取原始数据
	DoGc()           //处理完成,回收内存
}

type RpcHandleFinder interface {
	FindRpcHandler(serviceMethod string) IRpcHandler
}

type RequestHandler func(Returns interface{},Err RpcError)

type Call struct {
	ref           bool
	Seq           uint64
	ServiceMethod string
	Reply         interface{}
	Response      *RpcResponse
	Err           error
	done          chan *Call  // Strobes when call is complete.
	connId        int
	callback      *reflect.Value
	rpcHandler    IRpcHandler
	callTime      time.Time
}

func init(){
	rpcRequestPool.New = func() interface{} {
		return &RpcRequest{}
	}

	rpcCallPool.New = func() interface{} {
		return &Call{done:make(chan *Call,1)}
	}
}

func (slf *RpcRequest) Clear() *RpcRequest{
	slf.RpcRequestData = nil
	slf.localReply = nil
	slf.localParam = nil
	slf.requestHandle = nil
	slf.callback = nil
	slf.bLocalRequest = false
	slf.inputArgs = nil
	slf.rpcProcessor = nil
	return slf
}

func (rpcResponse *RpcResponse) Clear() *RpcResponse{
	rpcResponse.RpcResponseData = nil
	return rpcResponse
}

func (call *Call) Clear() *Call{
	call.Seq = 0
	call.ServiceMethod = ""
	call.Reply = nil
	call.Response = nil
	if len(call.done)>0 {
		call.done = make(chan *Call,1)
	}

	call.Err = nil
	call.connId = 0
	call.callback = nil
	call.rpcHandler = nil
	return call
}

func (call *Call) Done() *Call{
	return <-call.done
}

func MakeRpcRequest(rpcProcessor IRpcProcessor,seq uint64,rpcMethodId uint32,serviceMethod string,noReply bool,inParam []byte) *RpcRequest{
	rpcRequest := rpcRequestPool.Get().(*RpcRequest).Clear()
	rpcRequest.rpcProcessor = rpcProcessor
	rpcRequest.RpcRequestData = rpcRequest.rpcProcessor.MakeRpcRequest(seq,rpcMethodId,serviceMethod,noReply,inParam)
	rpcRequest.ref = true

	return rpcRequest
}

func ReleaseRpcRequest(rpcRequest *RpcRequest){
	if rpcRequest.ref == false {
		panic("Duplicate memory release!")
	}
	rpcRequest.ref = false
	rpcRequest.rpcProcessor.ReleaseRpcRequest(rpcRequest.RpcRequestData)
	rpcRequestPool.Put(rpcRequest)
}

func MakeCall() *Call {
	call := rpcCallPool.Get().(*Call).Clear()
	call.ref = true
	return call
}

func ReleaseCall(call *Call){
	if call.ref == false {
		panic("Duplicate memory release!")
	}
	call.ref = false
	rpcCallPool.Put(call)
}

