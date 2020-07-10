package rpc

import (
	"encoding/json"
	"sync"
)

type JsonProcessor struct {
}

type JsonRpcRequestData struct {
	//packhead
	Seq uint64             // sequence number chosen by client
	ServiceMethod string   // format: "Service.Method"
	NoReply bool           //是否需要返回
	//packbody
	InParam []byte
}

type JsonRpcResponseData struct {
	//head
	Seq           uint64   // sequence number chosen by client
	Err string

	//returns
	Reply []byte
}



var rpcJsonResponeDataPool sync.Pool
var rpcJsonRequestDataPool sync.Pool



func init(){
	rpcJsonResponeDataPool.New = func()interface{}{
		return &JsonRpcResponseData{}
	}

	rpcJsonRequestDataPool.New = func()interface{}{
		return &JsonRpcRequestData{}
	}
}

func (slf *JsonProcessor) Marshal(v interface{}) ([]byte, error){
	return json.Marshal(v)
}

func (slf *JsonProcessor) Unmarshal(data []byte, v interface{}) error{
	return json.Unmarshal(data,v)
}

func (slf *JsonProcessor) MakeRpcRequest(seq uint64,serviceMethod string,noReply bool,inParam []byte) IRpcRequestData{
	jsonRpcRequestData := rpcJsonRequestDataPool.Get().(*JsonRpcRequestData)
	jsonRpcRequestData.Seq = seq
	jsonRpcRequestData.ServiceMethod = serviceMethod
	jsonRpcRequestData.NoReply = noReply
	jsonRpcRequestData.InParam = inParam

	return jsonRpcRequestData
}

func (slf *JsonProcessor) MakeRpcResponse(seq uint64,err *RpcError,reply []byte) IRpcResponseData {
	jsonRpcResponseData := rpcJsonResponeDataPool.Get().(*JsonRpcResponseData)
	jsonRpcResponseData.Seq = seq
	jsonRpcResponseData.Err = err.Error()
	jsonRpcResponseData.Reply = reply
	return jsonRpcResponseData
}

func (slf *JsonProcessor) ReleaseRpcRequest(rpcRequestData IRpcRequestData){
	rpcJsonRequestDataPool.Put(rpcRequestData)
}

func (slf *JsonProcessor) ReleaseRpcRespose(rpcRequestData IRpcResponseData){
	rpcJsonResponeDataPool.Put(rpcRequestData)
}

func (slf *JsonRpcRequestData) IsNoReply() bool{
	return slf.NoReply
}

func (slf *JsonRpcRequestData) GetSeq() uint64{
	return slf.Seq
}

func (slf *JsonRpcRequestData) GetServiceMethod() string{
	return slf.ServiceMethod
}

func (slf *JsonRpcRequestData) GetInParam() []byte{
	return slf.InParam
}

func (slf *JsonRpcResponseData)	GetSeq() uint64 {
	return slf.Seq
}

func (slf *JsonRpcResponseData)		GetErr() *RpcError {
	if slf.Err == ""{
		return nil
	}

	return Errorf(slf.Err)
}


func (slf *JsonRpcResponseData)		GetReply() []byte{
	return slf.Reply
}






