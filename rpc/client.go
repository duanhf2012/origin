package rpc

import (
	"errors"
	"github.com/duanhf2012/origin/v2/network"
	"reflect"
	"time"
	"github.com/duanhf2012/origin/v2/log"
	"fmt"
)

const(
	DefaultRpcConnNum           = 1
	DefaultRpcLenMsgLen         = 4
	DefaultRpcMinMsgLen         = 2
	DefaultMaxCheckCallRpcCount = 1000
	DefaultMaxPendingWriteNum 	 = 200000
	
	DefaultConnectInterval = 2*time.Second
	DefaultCheckRpcCallTimeoutInterval = 1*time.Second
	DefaultRpcTimeout = 15*time.Second
)

var clientSeq uint32

type IWriter interface {
	WriteMsg (nodeId string,args ...[]byte) error
	IsConnected() bool
}

type IRealClient interface {
	SetConn(conn *network.TCPConn)
	Close(waitDone bool)

	AsyncCall(NodeId string,timeout time.Duration,rpcHandler IRpcHandler, serviceMethod string, callback reflect.Value, args interface{}, replyParam interface{},cancelable bool)  (CancelRpc,error)
	Go(NodeId string,timeout time.Duration,rpcHandler IRpcHandler, noReply bool, serviceMethod string, args interface{}, reply interface{}) *Call
	RawGo(NodeId string,timeout time.Duration,rpcHandler IRpcHandler,processor IRpcProcessor, noReply bool, rpcMethodId uint32, serviceMethod string, rawArgs []byte, reply interface{}) *Call
	IsConnected() bool

	Run()
	OnClose()

	Bind(server IServer)
}



type Client struct {
	clientId             uint32
	targetNodeId               string
	compressBytesLen int

	*CallSet
	IRealClient
}

func (client *Client) NewClientAgent(conn *network.TCPConn) network.Agent {
	client.SetConn(conn)

	return client
}

func (client *Client) GetTargetNodeId() string {
	return client.targetNodeId
}

func (client *Client) GetClientId() uint32 {
	return client.clientId
}


func (client *Client) processRpcResponse(responseData []byte) error{
	bCompress := (responseData[0]>>7) > 0
	processor := GetProcessor(responseData[0]&0x7f)
	if processor == nil {
		//rc.conn.ReleaseReadMsg(responseData)
		err:= errors.New(fmt.Sprintf("cannot find process %d",responseData[0]&0x7f))
		log.Error(err.Error())
		return err
	}

	//1.解析head
	response := RpcResponse{}
	response.RpcResponseData = processor.MakeRpcResponse(0, "", nil)

	//解压缩
	byteData := responseData[1:]
	var compressBuff []byte

	if bCompress == true {
		var unCompressErr error
		compressBuff,unCompressErr = compressor.UncompressBlock(byteData)
		if unCompressErr!= nil {
			//rc.conn.ReleaseReadMsg(responseData)
			err := fmt.Errorf("uncompressBlock failed,err :%s",unCompressErr.Error())
			return err
		}

		byteData = compressBuff
	}

	err := processor.Unmarshal(byteData, response.RpcResponseData)
	if cap(compressBuff) > 0 {
		compressor.UnCompressBufferCollection(compressBuff)
	}

	//rc.conn.ReleaseReadMsg(bytes)
	if err != nil {
		processor.ReleaseRpcResponse(response.RpcResponseData)
		log.Error("rpcClient Unmarshal head error",log.ErrorAttr("error",err))
		return nil
	}

	v := client.RemovePending(response.RpcResponseData.GetSeq())
	if v == nil {
		log.Error("rpcClient cannot find seq",log.Uint64("seq",response.RpcResponseData.GetSeq()))
	} else {
		v.Err = nil
		if len(response.RpcResponseData.GetReply()) > 0 {
			err = processor.Unmarshal(response.RpcResponseData.GetReply(), v.Reply)
			if err != nil {
				log.Error("rpcClient Unmarshal body failed",log.ErrorAttr("error",err))
				v.Err = err
			}
		}

		if response.RpcResponseData.GetErr() != nil {
			v.Err = response.RpcResponseData.GetErr()
		}

		if v.callback != nil && v.callback.IsValid() {
			v.rpcHandler.PushRpcResponse(v)
		} else {
			v.done <- v
		}
	}

	processor.ReleaseRpcResponse(response.RpcResponseData)

	return nil
}


//func (rc *Client) Go(timeout time.Duration,rpcHandler IRpcHandler,noReply bool, serviceMethod string, args interface{}, reply interface{}) *Call {
//	_, processor := GetProcessorType(args)
//	InParam, err := processor.Marshal(args)
//	if err != nil {
//		log.Error("Marshal is fail",log.ErrorAttr("error",err))
//		call := MakeCall()
//		call.DoError(err)
//		return call
//	}
//
//	return rc.RawGo(timeout,rpcHandler,processor, noReply, 0, serviceMethod, InParam, reply)
//}

func (rc *Client) rawGo(nodeId string,w IWriter,timeout time.Duration,rpcHandler IRpcHandler,processor IRpcProcessor, noReply bool, rpcMethodId uint32, serviceMethod string, rawArgs []byte, reply interface{}) *Call {
	call := MakeCall()
	call.ServiceMethod = serviceMethod
	call.Reply = reply
	call.Seq = rc.generateSeq()
	call.TimeOut = timeout

	request := MakeRpcRequest(processor, call.Seq, rpcMethodId, serviceMethod, noReply, rawArgs)
	bytes, err := processor.Marshal(request.RpcRequestData)
	ReleaseRpcRequest(request)

	if err != nil {
		call.Seq = 0
		log.Error("marshal is fail",log.String("error",err.Error()))
		call.DoError(err)
		return call
	}

	if w == nil || w.IsConnected()==false {
		call.Seq = 0
		sErr := errors.New(serviceMethod + "  was called failed,rpc client is disconnect")
		log.Error("conn is disconnect",log.String("error",sErr.Error()))
		call.DoError(sErr)
		return call
	}

	var compressBuff[]byte
	bCompress := uint8(0)
	if rc.compressBytesLen > 0 && len(bytes) >= rc.compressBytesLen {
		var cErr error
		compressBuff,cErr = compressor.CompressBlock(bytes)
		if cErr != nil {
			call.Seq = 0
			log.Error("compress fail",log.String("error",cErr.Error()))
			call.DoError(cErr)
			return call
		}
		if len(compressBuff) < len(bytes) {
			bytes = compressBuff
			bCompress = 1<<7
		}
	}

	if noReply == false {
		rc.AddPending(call)
	}

	err = w.WriteMsg(nodeId,[]byte{uint8(processor.GetProcessorType())|bCompress}, bytes)
	if cap(compressBuff) >0 {
		compressor.CompressBufferCollection(compressBuff)
	}
	if err != nil {
		rc.RemovePending(call.Seq)
		log.Error("WiteMsg is fail",log.ErrorAttr("error",err))
		call.Seq = 0
		call.DoError(err)
	}

	return call
}

func (rc *Client) asyncCall(nodeId string,w IWriter,timeout time.Duration,rpcHandler IRpcHandler, serviceMethod string, callback reflect.Value, args interface{}, replyParam interface{},cancelable bool) (CancelRpc,error) {
	processorType, processor := GetProcessorType(args)
	InParam, herr := processor.Marshal(args)
	if herr != nil {
		return emptyCancelRpc,herr
	}

	seq := rc.generateSeq()
	request := MakeRpcRequest(processor, seq, 0, serviceMethod, false, InParam)
	bytes, err := processor.Marshal(request.RpcRequestData)
	ReleaseRpcRequest(request)
	if err != nil {
		return emptyCancelRpc,err
	}

	if w == nil || w.IsConnected()==false {
		return emptyCancelRpc,errors.New("Rpc server is disconnect,call " + serviceMethod)
	}

	var compressBuff[]byte
	bCompress := uint8(0)
	if rc.compressBytesLen>0 &&len(bytes) >= rc.compressBytesLen {
		var cErr error
		compressBuff,cErr = compressor.CompressBlock(bytes)
		if cErr != nil {
			return emptyCancelRpc,cErr
		}

		if len(compressBuff) < len(bytes) {
			bytes = compressBuff
			bCompress = 1<<7
		}
	}

	call := MakeCall()
	call.Reply = replyParam
	call.callback = &callback
	call.rpcHandler = rpcHandler
	call.ServiceMethod = serviceMethod
	call.Seq = seq
	call.TimeOut = timeout
	rc.AddPending(call)

	err = w.WriteMsg(nodeId,[]byte{uint8(processorType)|bCompress}, bytes)
	if cap(compressBuff) >0 {
		compressor.CompressBufferCollection(compressBuff)
	}
	if err != nil {
		rc.RemovePending(call.Seq)
		ReleaseCall(call)
		return emptyCancelRpc,err
	}

	if cancelable {
		rpcCancel := RpcCancel{CallSeq:seq,Cli: rc}
		return rpcCancel.CancelRpc,nil
	}

	return emptyCancelRpc,nil
}