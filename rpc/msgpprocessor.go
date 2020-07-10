package rpc

import "sync"

type IMsgp interface {
	UnmarshalMsg(bts []byte) (o []byte, err error)
	MarshalMsg(b []byte) (o []byte, err error)
}

var rpcResponeDataPool sync.Pool
var rpcRequestDataPool sync.Pool

type MsgpProcessor struct {

}

func init(){
	rpcResponeDataPool.New = func()interface{}{
		return &MsgpRpcResponseData{}
	}

	rpcRequestDataPool.New = func()interface{}{
		return &MsgpRpcRequestData{}
	}
}

//go:generate msgp
type MsgpRpcRequestData struct {
	//packhead
	Seq uint64             // sequence number chosen by client
	ServiceMethod string   // format: "Service.Method"
	NoReply bool           //是否需要返回
	//packbody
	InParam []byte
}

//go:generate msgp
type MsgpRpcResponseData struct {
	//head
	Seq           uint64   // sequence number chosen by client
	Err string

	//returns
	Reply []byte
}


func (slf *MsgpProcessor) Marshal(v interface{}) ([]byte, error){
	msgp := v.(IMsgp)

	return msgp.MarshalMsg(nil)
}

func (slf *MsgpProcessor) Unmarshal(data []byte, v interface{}) error{
	msgp := v.(IMsgp)
	_,err := msgp.UnmarshalMsg(data)
	return err
}

func (slf *MsgpProcessor) MakeRpcRequest(seq uint64,serviceMethod string,noReply bool,inParam []byte) IRpcRequestData{
	rpcRequestData := rpcRequestDataPool.Get().(*MsgpRpcRequestData)
	rpcRequestData.Seq = seq
	rpcRequestData.ServiceMethod = serviceMethod
	rpcRequestData.NoReply = noReply
	rpcRequestData.InParam = inParam

	return rpcRequestData//&MsgpRpcRequestData{Seq:seq,ServiceMethod:serviceMethod,NoReply:noReply,InParam:inParam}
}

func (slf *MsgpProcessor) MakeRpcResponse(seq uint64,err *RpcError,reply []byte) IRpcResponseData {
	rpcRequestData := rpcResponeDataPool.Get().(*MsgpRpcResponseData)
	rpcRequestData.Seq = seq
	rpcRequestData.Err = err.Error()
	rpcRequestData.Reply = reply

	return rpcRequestData
}

func (slf *MsgpProcessor) ReleaseRpcRequest(rpcRequestData IRpcRequestData){
	rpcRequestDataPool.Put(rpcRequestData)
}

func (slf *MsgpProcessor) ReleaseRpcRespose(rpcRequestData IRpcResponseData){
	rpcResponeDataPool.Put(rpcRequestData)
}

func (slf *MsgpRpcRequestData) IsNoReply() bool{
	return slf.NoReply
}

func (slf *MsgpRpcRequestData) GetSeq() uint64{
	return slf.Seq
}

func (slf *MsgpRpcRequestData) GetServiceMethod() string{
	return slf.ServiceMethod
}

func (slf *MsgpRpcRequestData) GetInParam() []byte{
	return slf.InParam
}

func (slf *MsgpRpcResponseData)	GetSeq() uint64 {
	return slf.Seq
}

func (slf *MsgpRpcResponseData)		GetErr() *RpcError {
	if slf.Err == ""{
		return nil
	}

	return Errorf(slf.Err)
}


func (slf *MsgpRpcResponseData)		GetReply() []byte{
	return slf.Reply
}






