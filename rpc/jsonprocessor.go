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
	AdditionParam interface{}
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


func (slf *JsonProcessor) MakeRpcRequest(seq uint64,serviceMethod string,noReply bool,inParam []byte,additionParam interface{}) IRpcRequestData{
	jsonRpcRequestData := rpcJsonRequestDataPool.Get().(*JsonRpcRequestData)
	jsonRpcRequestData.Seq = seq
	jsonRpcRequestData.ServiceMethod = serviceMethod
	jsonRpcRequestData.NoReply = noReply
	jsonRpcRequestData.InParam = inParam
	jsonRpcRequestData.AdditionParam = additionParam
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

func (slf *JsonProcessor) IsParse(param interface{}) bool {
	_,err := json.Marshal(param)
	return err==nil
}


func (slf *JsonProcessor)	GetProcessorType() RpcProcessorType{
	return RPC_PROCESSOR_JSON
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

func (slf *JsonRpcRequestData) GetParamValue() interface{}{
	return slf.AdditionParam
}

func (slf *JsonRpcRequestData) GetAdditionParams() IRawAdditionParam{
	return slf
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




