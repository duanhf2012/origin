package rpc

import (
	"github.com/golang/protobuf/proto"
	"sync"
)

type PBProcessor struct {
}

var rpcPbResponeDataPool sync.Pool
var rpcPbRequestDataPool sync.Pool


func init(){
	rpcPbResponeDataPool.New = func()interface{}{
		return &JsonRpcResponseData{}
	}

	rpcPbRequestDataPool.New = func()interface{}{
		return &JsonRpcRequestData{}
	}
}

func (slf *PBRpcRequestData) MakeRequest(seq uint64,serviceMethod string,noReply bool,inParam []byte) *PBRpcRequestData{
	slf.Seq = proto.Uint64(seq)
	slf.ServiceMethod = proto.String(serviceMethod)
	slf.NoReply = proto.Bool(noReply)
	slf.InParam = inParam
	return slf
}

func (slf *PBRpcResponseData) MakeRespone(seq uint64,err *RpcError,reply []byte) *PBRpcResponseData{
	slf.Seq = proto.Uint64(seq)
	slf.Error = proto.String(err.Error())
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

func (slf *PBProcessor) MakeRpcResponse(seq uint64,err *RpcError,reply []byte) IRpcResponseData {
	pPBRpcResponseData := rpcPbResponeDataPool.Get().(*PBRpcResponseData)
	pPBRpcResponseData.MakeRespone(seq,err,reply)
	return pPBRpcResponseData
}

func (slf *PBProcessor) ReleaseRpcRequest(rpcRequestData IRpcRequestData){
	rpcPbRequestDataPool.Put(rpcRequestData)
}

func (slf *PBProcessor) ReleaseRpcRespose(rpcRequestData IRpcRequestData){
	rpcPbResponeDataPool.Put(rpcRequestData)
}


func (slf *PBRpcRequestData) IsReply() bool{
	return slf.GetNoReply()
}

func (slf *PBRpcResponseData)		GetErr() *RpcError {
	return Errorf(slf.GetError())
}









