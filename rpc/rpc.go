package rpc

import (
	"github.com/duanhf2012/origin/util/sync"
	"reflect"
	"time"
)

type RpcRequest struct {
	ref bool
	RpcRequestData IRpcRequestData

	inParam interface{}
	localReply interface{}

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

var rpcRequestPool = sync.NewPoolEx(make(chan sync.IPoolData,10240),func()sync.IPoolData{
	return &RpcRequest{}
})

var rpcCallPool =  sync.NewPoolEx(make(chan sync.IPoolData,10240),func()sync.IPoolData{
	return &Call{done:make(chan *Call,1)}
})


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
	TimeOut       time.Duration
}

type RpcCancel struct {
	Cli *Client
	CallSeq uint64
}

func (rc *RpcCancel) CancelRpc(){
	rc.Cli.RemovePending(rc.CallSeq)
}

func (slf *RpcRequest) Clear() *RpcRequest{
	slf.RpcRequestData = nil
	slf.localReply = nil
	slf.inParam = nil
	slf.requestHandle = nil
	slf.callback = nil
	slf.rpcProcessor = nil
	return slf
}

func (slf *RpcRequest) Reset() {
	slf.Clear()
}

func (slf *RpcRequest) IsRef()bool{
	return slf.ref
}

func (slf *RpcRequest) Ref(){
	slf.ref = true
}

func (slf *RpcRequest) UnRef(){
	slf.ref = false
}

func (rpcResponse *RpcResponse) Clear() *RpcResponse{
	rpcResponse.RpcResponseData = nil
	return rpcResponse
}

func (call *Call) DoError(err error){
	call.Err = err
	call.done <- call
}

func (call *Call) DoOK(){
	call.done <- call
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
	call.TimeOut = 0

	return call
}

func (call *Call) Reset() {
	call.Clear()
}

func (call *Call) IsRef()bool{
	return call.ref
}

func (call *Call) Ref(){
	call.ref = true
}

func (call *Call) UnRef(){
	call.ref = false
}

func (call *Call) Done() *Call{
	return <-call.done
}

func MakeRpcRequest(rpcProcessor IRpcProcessor,seq uint64,rpcMethodId uint32,serviceMethod string,noReply bool,inParam []byte) *RpcRequest{
	rpcRequest := rpcRequestPool.Get().(*RpcRequest)
	rpcRequest.rpcProcessor = rpcProcessor
	rpcRequest.RpcRequestData = rpcRequest.rpcProcessor.MakeRpcRequest(seq,rpcMethodId,serviceMethod,noReply,inParam)

	return rpcRequest
}

func ReleaseRpcRequest(rpcRequest *RpcRequest){
	rpcRequest.rpcProcessor.ReleaseRpcRequest(rpcRequest.RpcRequestData)
	rpcRequestPool.Put(rpcRequest)
}

func MakeCall() *Call {
	return rpcCallPool.Get().(*Call)
}

func ReleaseCall(call *Call){
	rpcCallPool.Put(call)
}

