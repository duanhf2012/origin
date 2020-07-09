package rpc

import (
	"reflect"
	"sync"
)

type RpcRequest struct {
	RpcRequestData IRpcRequestData

	localReply interface{}
	localParam interface{} //本地调用的参数列表
	requestHandle RequestHandler
	callback *reflect.Value
}

type RpcResponse struct {
	RpcResponeData IRpcResponseData
}

type IRpcRequestData interface {
	GetSeq() uint64
	GetServiceMethod() string
	GetInParam() []byte
	IsReply() bool
}

type IRpcResponseData interface {
	GetSeq() uint64
	GetErr() *RpcError
	GetReply() []byte
}

type RequestHandler func(Returns interface{},Err *RpcError)


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

func (slf *Call) Clear(){
	slf.Seq = 0
	slf.ServiceMethod = ""
	slf.Arg = nil
	slf.Reply = nil
	slf.Respone = nil
	slf.Err = nil
	slf.connid = 0
	slf.callback = nil
	slf.rpcHandler = nil
}

func (slf *Call) Done() *Call{
	return <-slf.done
}

type RpcHandleFinder interface {
	FindRpcHandler(serviceMethod string) IRpcHandler
}


var rpcResponePool sync.Pool
var rpcRequestPool sync.Pool
var rpcCallPool sync.Pool

func init(){
	rpcResponePool.New = func()interface{}{
		return &RpcResponse{}
	}

	rpcRequestPool.New = func() interface{} {
		return &RpcRequest{}
	}

	rpcCallPool.New = func() interface{} {
		return &Call{done:make(chan *Call,1)}
	}
}

func MakeRpcResponse() *RpcResponse{
	return rpcResponePool.Get().(*RpcResponse)
}

func MakeRpcRequest() *RpcRequest{
	return rpcRequestPool.Get().(*RpcRequest)
}

func MakeCall() *Call {
	call := rpcCallPool.Get().(*Call)

	return call
}

func ReleaseRpcResponse(rpcRespone *RpcResponse){
	rpcResponePool.Put(rpcRespone)
}

func ReleaseRpcRequest(rpcRequest *RpcRequest){
	rpcRequestPool.Put(rpcRequest)
}

func ReleaseCall(call *Call){
	rpcCallPool.Put(call)
}