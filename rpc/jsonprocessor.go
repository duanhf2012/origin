package rpc

import (
	jsoniter "github.com/json-iterator/go"
	"sync"
)

var json = jsoniter.ConfigCompatibleWithStandardLibrary

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

var rpcJsonResponseDataPool sync.Pool
var rpcJsonRequestDataPool sync.Pool

func init(){
	rpcJsonResponseDataPool.New = func()interface{}{
		return &JsonRpcResponseData{}
	}

	rpcJsonRequestDataPool.New = func()interface{}{
		return &JsonRpcRequestData{}
	}
}

func (jsonProcessor *JsonProcessor) Marshal(v interface{}) ([]byte, error){
	return json.Marshal(v)
}

func (jsonProcessor *JsonProcessor) Unmarshal(data []byte, v interface{}) error{
	return json.Unmarshal(data,v)
}

func (jsonProcessor *JsonProcessor) MakeRpcRequest(seq uint64,serviceMethod string,noReply bool,inParam []byte) IRpcRequestData{
	jsonRpcRequestData := rpcJsonRequestDataPool.Get().(*JsonRpcRequestData)
	jsonRpcRequestData.Seq = seq
	jsonRpcRequestData.ServiceMethod = serviceMethod
	jsonRpcRequestData.NoReply = noReply
	jsonRpcRequestData.InParam = inParam
	return jsonRpcRequestData
}

func (jsonProcessor *JsonProcessor) MakeRpcResponse(seq uint64,err RpcError,reply []byte) IRpcResponseData {
	jsonRpcResponseData := rpcJsonResponseDataPool.Get().(*JsonRpcResponseData)
	jsonRpcResponseData.Seq = seq
	jsonRpcResponseData.Err = err.Error()
	jsonRpcResponseData.Reply = reply

	return jsonRpcResponseData
}

func (jsonProcessor *JsonProcessor) ReleaseRpcRequest(rpcRequestData IRpcRequestData){
	rpcJsonRequestDataPool.Put(rpcRequestData)
}

func (jsonProcessor *JsonProcessor) ReleaseRpcResponse(rpcResponseData IRpcResponseData){
	rpcJsonResponseDataPool.Put(rpcResponseData)
}

func (jsonProcessor *JsonProcessor) IsParse(param interface{}) bool {
	_,err := json.Marshal(param)
	return err==nil
}

func (jsonProcessor *JsonProcessor)	GetProcessorType() RpcProcessorType{
	return RpcProcessorJson
}

func (jsonRpcRequestData *JsonRpcRequestData) IsNoReply() bool{
	return jsonRpcRequestData.NoReply
}

func (jsonRpcRequestData *JsonRpcRequestData) GetSeq() uint64{
	return jsonRpcRequestData.Seq
}

func (jsonRpcRequestData *JsonRpcRequestData) GetServiceMethod() string{
	return jsonRpcRequestData.ServiceMethod
}

func (jsonRpcRequestData *JsonRpcRequestData) GetInParam() []byte{
	return jsonRpcRequestData.InParam
}

func (jsonRpcResponseData *JsonRpcResponseData)	GetSeq() uint64 {
	return jsonRpcResponseData.Seq
}

func (jsonRpcResponseData *JsonRpcResponseData)		GetErr() *RpcError {
	if jsonRpcResponseData.Err == ""{
		return nil
	}

	return Errorf(jsonRpcResponseData.Err)
}

func (jsonRpcResponseData *JsonRpcResponseData)		GetReply() []byte{
	return jsonRpcResponseData.Reply
}





