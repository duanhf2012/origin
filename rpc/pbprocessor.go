package rpc

import (
	"github.com/golang/protobuf/proto"
)

type PBProcessor struct {
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
	return (&PBRpcRequestData{}).MakeRequest(seq,serviceMethod,noReply,inParam)
}

func (slf *PBProcessor) MakeRpcResponse(seq uint64,err *RpcError,reply []byte) IRpcResponseData {
	return (&PBRpcResponseData{}).MakeRespone(seq,err,reply)
}

func (slf *PBRpcRequestData) IsReply() bool{
	return slf.GetNoReply()
}

func (slf *PBRpcResponseData)		GetErr() *RpcError {
	return Errorf(slf.GetError())
}









