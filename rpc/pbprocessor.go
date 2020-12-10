package rpc

import (
	"github.com/golang/protobuf/proto"
	"sync"
)

type PBProcessor struct {
}

var rpcPbResponseDataPool sync.Pool
var rpcPbRequestDataPool sync.Pool


func init(){
	rpcPbResponseDataPool.New = func()interface{}{
		return &PBRpcResponseData{}
	}

	rpcPbRequestDataPool.New = func()interface{}{
		return &PBRpcRequestData{}
	}
}

func (slf *PBRpcRequestData) MakeRequest(seq uint64,serviceMethod string,noReply bool,inParam []byte) *PBRpcRequestData{
	slf.Seq = seq
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
	protoMsg := msg.(proto.Message)
	return proto.Unmarshal(data, protoMsg)
}

func (slf *PBProcessor) MakeRpcRequest(seq uint64,serviceMethod string,noReply bool,inParam []byte) IRpcRequestData{
	pPbRpcRequestData := rpcPbRequestDataPool.Get().(*PBRpcRequestData)
	pPbRpcRequestData.MakeRequest(seq,serviceMethod,noReply,inParam)
	return pPbRpcRequestData
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
	return RpcProcessorPb
}

func (slf *PBRpcRequestData) IsNoReply() bool{
	return slf.GetNoReply()
}

func (slf *PBRpcResponseData)		GetErr() *RpcError {
	if slf.GetError() == "" {
		return nil
	}
	return Errorf(slf.GetError())
}








