package rpc

import (
	"github.com/duanhf2012/origin/util/sync"
	"google.golang.org/protobuf/proto"
	"fmt"
)

type PBProcessor struct {
}

var rpcPbResponseDataPool =sync.NewPool(make(chan interface{},10240), func()interface{}{
	return &PBRpcResponseData{}
})

var rpcPbRequestDataPool =sync.NewPool(make(chan interface{},10240), func()interface{}{
	return &PBRpcRequestData{}
})

func (slf *PBRpcRequestData) MakeRequest(seq uint64,rpcMethodId uint32,serviceMethod string,noReply bool,inParam []byte) *PBRpcRequestData{
	slf.Seq = seq
	slf.RpcMethodId = rpcMethodId
	slf.ServiceMethod = serviceMethod
	slf.NoReply = noReply
	slf.InParam = inParam

	return slf
}


func (slf *PBRpcResponseData) MakeRespone(seq uint64,err RpcError,reply []byte) *PBRpcResponseData{
	slf.Seq = seq
	slf.Error = err.Error()
	slf.Reply = reply

	return slf
}

func (slf *PBProcessor) Marshal(v interface{}) ([]byte, error){
	return proto.Marshal(v.(proto.Message))
}

func (slf *PBProcessor) Unmarshal(data []byte, msg interface{}) error{
	protoMsg,ok := msg.(proto.Message)
	if ok == false {
		return fmt.Errorf("%+v is not of proto.Message type",msg)
	}
	return proto.Unmarshal(data, protoMsg)
}

func (slf *PBProcessor) MakeRpcRequest(seq uint64,rpcMethodId uint32,serviceMethod string,noReply bool,inParam []byte) IRpcRequestData{
	pGogoPbRpcRequestData := rpcPbRequestDataPool.Get().(*PBRpcRequestData)
	pGogoPbRpcRequestData.MakeRequest(seq,rpcMethodId,serviceMethod,noReply,inParam)
	return pGogoPbRpcRequestData
}

func (slf *PBProcessor) MakeRpcResponse(seq uint64,err RpcError,reply []byte) IRpcResponseData {
	pPBRpcResponseData := rpcPbResponseDataPool.Get().(*PBRpcResponseData)
	pPBRpcResponseData.MakeRespone(seq,err,reply)
	return pPBRpcResponseData
}

func (slf *PBProcessor) ReleaseRpcRequest(rpcRequestData IRpcRequestData){
	rpcPbRequestDataPool.Put(rpcRequestData)
}

func (slf *PBProcessor) ReleaseRpcResponse(rpcResponseData IRpcResponseData){
	rpcPbResponseDataPool.Put(rpcResponseData)
}

func (slf *PBProcessor) IsParse(param interface{}) bool {
	_,ok := param.(proto.Message)
	return ok
}

func (slf *PBProcessor)	GetProcessorType() RpcProcessorType{
	return RpcProcessorPB
}

func (slf *PBProcessor) Clone(src interface{}) (interface{},error){
	srcMsg,ok := src.(proto.Message)
	if ok == false {
		return nil,fmt.Errorf("param is not of proto.message type")
	}

	return proto.Clone(srcMsg),nil
}

func (slf *PBRpcRequestData) IsNoReply() bool{
	return slf.GetNoReply()
}

func (slf *PBRpcResponseData)		GetErr() *RpcError {
	if slf.GetError() == "" {
		return nil
	}

	err := RpcError(slf.GetError())
	return &err
}






