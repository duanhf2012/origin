package rpc

import (
	"github.com/golang/protobuf/proto"
	"fmt"
	"sync"
)

type PBProcessor struct {
}

var rpcPbResponeDataPool sync.Pool
var rpcPbRequestDataPool sync.Pool


func init(){
	rpcPbResponeDataPool.New = func()interface{}{
		return &PBRpcResponseData{}
	}

	rpcPbRequestDataPool.New = func()interface{}{
		return &PBRpcRequestData{}
	}
}

func (m *PBRpcRequestData) GetParamValue() interface{}{
	if m.GetAddtionParam() == nil {
		return nil
	}

	switch x := m.AddtionParam.AdditionOneof.(type) {
	case *AdditionParam_SParam:
		return x.SParam
	case *AdditionParam_UParam:
		return x.UParam
	case *AdditionParam_StrParam:
		return x.StrParam
	case *AdditionParam_BParam:
		return x.BParam
	}

	return nil
}

func (m *PBRpcRequestData) GetAdditionParams() IRawAdditionParam{
	if m.GetAddtionParam() == nil {
		return nil
	}

	return m
}

func (slf *PBRpcRequestData) MakeRequest(seq uint64,serviceMethod string,noReply bool,inParam []byte,inAdditionParam interface{}) *PBRpcRequestData{
	slf.Seq = proto.Uint64(seq)
	slf.ServiceMethod = proto.String(serviceMethod)
	slf.NoReply = proto.Bool(noReply)
	slf.InParam = inParam

	if inAdditionParam == nil {
		return slf
	}

	switch inAdditionParam.(type) {
	case *int:
		slf.AddtionParam =  &AdditionParam{AdditionOneof:&AdditionParam_SParam{int64(*inAdditionParam.(*int))}}
	case *int32:
		slf.AddtionParam =  &AdditionParam{AdditionOneof:&AdditionParam_SParam{int64(*inAdditionParam.(*int32))}}
	case *int16:
		slf.AddtionParam =  &AdditionParam{AdditionOneof:&AdditionParam_SParam{int64(*inAdditionParam.(*int16))}}
	case *int64:
		slf.AddtionParam =  &AdditionParam{AdditionOneof:&AdditionParam_SParam{*inAdditionParam.(*int64)}}
	case *uint:
		slf.AddtionParam =  &AdditionParam{AdditionOneof:&AdditionParam_UParam{uint64(*inAdditionParam.(*uint))}}
	case *uint32:
		slf.AddtionParam =  &AdditionParam{AdditionOneof:&AdditionParam_UParam{uint64(*inAdditionParam.(*uint32))}}
	case *uint16:
		slf.AddtionParam =  &AdditionParam{AdditionOneof:&AdditionParam_UParam{uint64(*inAdditionParam.(*uint16))}}
	case *uint64:
		slf.AddtionParam =  &AdditionParam{AdditionOneof:&AdditionParam_UParam{*inAdditionParam.(*uint64)}}
	case *string:
		slf.AddtionParam =  &AdditionParam{AdditionOneof:&AdditionParam_StrParam{*inAdditionParam.(*string)}}
	case *[]byte:
		slf.AddtionParam = &AdditionParam{AdditionOneof: &AdditionParam_BParam{*inAdditionParam.(*[]byte)}}
	default:
		panic(fmt.Sprintf("not support type %+v",inAdditionParam))
	}

	return slf
}

func (slf *PBRpcResponseData) MakeRespone(seq uint64,err *RpcError,reply []byte) *PBRpcResponseData{
	slf.Seq = proto.Uint64(seq)
	if err != nil {
		slf.Error = proto.String(err.Error())
	}
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


func (slf *PBProcessor) MakeRpcRequest(seq uint64,serviceMethod string,noReply bool,inParam []byte,inAdditionParam interface{}) IRpcRequestData{
	pPbRpcRequestData := rpcPbRequestDataPool.Get().(*PBRpcRequestData)
	pPbRpcRequestData.MakeRequest(seq,serviceMethod,noReply,inParam,inAdditionParam)
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

func (slf *PBProcessor) ReleaseRpcRespose(rpcRequestData IRpcResponseData){
	rpcPbResponeDataPool.Put(rpcRequestData)
}

func (slf *PBProcessor) IsParse(param interface{}) bool {
	_,ok := param.(proto.Message)
	return ok
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








