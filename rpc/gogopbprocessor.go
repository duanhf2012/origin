package rpc

import (
	"github.com/duanhf2012/origin/util/sync"
	"github.com/gogo/protobuf/proto"
)

type GoGoPBProcessor struct {
}

var rpcGoGoPbResponseDataPool =sync.NewPool(make(chan interface{},10240), func()interface{}{
	return &GoGoPBRpcResponseData{}
})

var rpcGoGoPbRequestDataPool =sync.NewPool(make(chan interface{},10240), func()interface{}{
	return &GoGoPBRpcRequestData{}
})

func (slf *GoGoPBRpcRequestData) MakeRequest(seq uint64,rpcMethodId uint32,serviceMethod string,noReply bool,inParam []byte) *GoGoPBRpcRequestData{
	slf.Seq = seq
	slf.RpcMethodId = rpcMethodId
	slf.ServiceMethod = serviceMethod
	slf.NoReply = noReply
	slf.InParam = inParam

	return slf
}


func (slf *GoGoPBRpcResponseData) MakeRespone(seq uint64,err RpcError,reply []byte) *GoGoPBRpcResponseData{
	slf.Seq = seq
	slf.Error = err.Error()
	slf.Reply = reply

	return slf
}

func (slf *GoGoPBProcessor) Marshal(v interface{}) ([]byte, error){
	return proto.Marshal(v.(proto.Message))
}

func (slf *GoGoPBProcessor) Unmarshal(data []byte, msg interface{}) error{
	protoMsg := msg.(proto.Message)
	return proto.Unmarshal(data, protoMsg)
}

func (slf *GoGoPBProcessor) MakeRpcRequest(seq uint64,rpcMethodId uint32,serviceMethod string,noReply bool,inParam []byte) IRpcRequestData{
	pGogoPbRpcRequestData := rpcGoGoPbRequestDataPool.Get().(*GoGoPBRpcRequestData)
	pGogoPbRpcRequestData.MakeRequest(seq,rpcMethodId,serviceMethod,noReply,inParam)
	return pGogoPbRpcRequestData
}

func (slf *GoGoPBProcessor) MakeRpcResponse(seq uint64,err RpcError,reply []byte) IRpcResponseData {
	pGoGoPBRpcResponseData := rpcGoGoPbResponseDataPool.Get().(*GoGoPBRpcResponseData)
	pGoGoPBRpcResponseData.MakeRespone(seq,err,reply)
	return pGoGoPBRpcResponseData
}

func (slf *GoGoPBProcessor) ReleaseRpcRequest(rpcRequestData IRpcRequestData){
	rpcGoGoPbRequestDataPool.Put(rpcRequestData)
}

func (slf *GoGoPBProcessor) ReleaseRpcResponse(rpcResponseData IRpcResponseData){
	rpcGoGoPbResponseDataPool.Put(rpcResponseData)
}

func (slf *GoGoPBProcessor) IsParse(param interface{}) bool {
	_,ok := param.(proto.Message)
	return ok
}

func (slf *GoGoPBProcessor)	GetProcessorType() RpcProcessorType{
	return RpcProcessorGoGoPB
}

func (slf *GoGoPBRpcRequestData) IsNoReply() bool{
	return slf.GetNoReply()
}

func (slf *GoGoPBRpcResponseData)		GetErr() *RpcError {
	if slf.GetError() == "" {
		return nil
	}
	return Errorf(slf.GetError())
}








