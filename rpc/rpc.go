package rpc

import (
	"reflect"
	"sync"
	"time"
)

type RpcRequest struct {
	RpcRequestData IRpcRequestData

	bLocalRequest bool
	localReply interface{}
	localParam interface{} //本地调用的参数列表
	localRawParam []byte
	requestHandle RequestHandler
	callback *reflect.Value
}

type RpcResponse struct {
	RpcResponeData IRpcResponseData
}

func (slf *RpcRequest) Clear() *RpcRequest{
	slf.RpcRequestData = nil
	slf.localReply = nil
	slf.localParam = nil
	slf.requestHandle = nil
	slf.callback = nil
	return slf
}

func (slf *RpcResponse) Clear() *RpcResponse{
	slf.RpcResponeData = nil
	return slf
}

type IRawAdditionParam interface {
	GetParamValue() interface{}
}

type IRpcRequestData interface {
	GetSeq() uint64
	GetServiceMethod() string
	GetInParam() []byte
	IsNoReply() bool
	GetAdditionParams() IRawAdditionParam
}

type IRpcResponseData interface {
	GetSeq() uint64
	GetErr() *RpcError
	GetReply() []byte
}

type RequestHandler func(Returns interface{},Err *RpcError)

type RawAdditionParamNull struct {
}

func (slf *RawAdditionParamNull) GetParamValue() interface{}{
	return nil
}


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
	calltime time.Time
}

func (slf *Call) Clear() *Call{
	slf.Seq = 0
	slf.ServiceMethod = ""
	slf.Arg = nil
	slf.Reply = nil
	slf.Respone = nil
	slf.Err = nil
	slf.connid = 0
	slf.callback = nil
	slf.rpcHandler = nil
	return slf
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
	return rpcResponePool.Get().(*RpcResponse).Clear()
}

func MakeRpcRequest() *RpcRequest{
	return rpcRequestPool.Get().(*RpcRequest).Clear()
}

func MakeCall() *Call {
	return rpcCallPool.Get().(*Call).Clear()
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