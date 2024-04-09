package rpc

import (
	"errors"
	"fmt"
	"github.com/duanhf2012/origin/v2/log"
	"github.com/duanhf2012/origin/v2/network"
	"math"
	"reflect"
	"runtime"
	"sync/atomic"
	"time"
)

//跨结点连接的Client
type RClient struct {
	compressBytesLen int
	selfClient *Client
	network.TCPClient
	conn *network.TCPConn
	TriggerRpcConnEvent
}

func (rc *RClient) IsConnected() bool {
	rc.Lock()
	defer rc.Unlock()

	return rc.conn != nil && rc.conn.IsConnected() == true
}

func (rc *RClient) GetConn() *network.TCPConn{
	rc.Lock()
	conn := rc.conn
	rc.Unlock()

	return conn
}

func (rc *RClient) SetConn(conn *network.TCPConn){
	rc.Lock()
	rc.conn = conn
	rc.Unlock()
}

func (rc *RClient) Go(timeout time.Duration,rpcHandler IRpcHandler,noReply bool, serviceMethod string, args interface{}, reply interface{}) *Call {
	_, processor := GetProcessorType(args)
	InParam, err := processor.Marshal(args)
	if err != nil {
		log.Error("Marshal is fail",log.ErrorAttr("error",err))
		call := MakeCall()
		call.DoError(err)
		return call
	}

	return rc.RawGo(timeout,rpcHandler,processor, noReply, 0, serviceMethod, InParam, reply)
}

func (rc *RClient) RawGo(timeout time.Duration,rpcHandler IRpcHandler,processor IRpcProcessor, noReply bool, rpcMethodId uint32, serviceMethod string, rawArgs []byte, reply interface{}) *Call {
	call := MakeCall()
	call.ServiceMethod = serviceMethod
	call.Reply = reply
	call.Seq = rc.selfClient.generateSeq()
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

	conn := rc.GetConn()
	if conn == nil || conn.IsConnected()==false {
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
		rc.selfClient.AddPending(call)
	}

	err = conn.WriteMsg([]byte{uint8(processor.GetProcessorType())|bCompress}, bytes)
	if cap(compressBuff) >0 {
		compressor.CompressBufferCollection(compressBuff)
	}
	if err != nil {
		rc.selfClient.RemovePending(call.Seq)
		log.Error("WiteMsg is fail",log.ErrorAttr("error",err))
		call.Seq = 0
		call.DoError(err)
	}

	return call
}


func (rc *RClient) AsyncCall(timeout time.Duration,rpcHandler IRpcHandler, serviceMethod string, callback reflect.Value, args interface{}, replyParam interface{},cancelable bool)  (CancelRpc,error) {
	cancelRpc,err := rc.asyncCall(timeout,rpcHandler, serviceMethod, callback, args, replyParam,cancelable)
	if err != nil {
		callback.Call([]reflect.Value{reflect.ValueOf(replyParam), reflect.ValueOf(err)})
	}

	return cancelRpc,nil
}

func (rc *RClient) asyncCall(timeout time.Duration,rpcHandler IRpcHandler, serviceMethod string, callback reflect.Value, args interface{}, replyParam interface{},cancelable bool) (CancelRpc,error) {
	processorType, processor := GetProcessorType(args)
	InParam, herr := processor.Marshal(args)
	if herr != nil {
		return emptyCancelRpc,herr
	}

	seq := rc.selfClient.generateSeq()
	request := MakeRpcRequest(processor, seq, 0, serviceMethod, false, InParam)
	bytes, err := processor.Marshal(request.RpcRequestData)
	ReleaseRpcRequest(request)
	if err != nil {
		return emptyCancelRpc,err
	}

	conn := rc.GetConn()
	if conn == nil || conn.IsConnected()==false {
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
	rc.selfClient.AddPending(call)

	err = conn.WriteMsg([]byte{uint8(processorType)|bCompress}, bytes)
	if cap(compressBuff) >0 {
		compressor.CompressBufferCollection(compressBuff)
	}
	if err != nil {
		rc.selfClient.RemovePending(call.Seq)
		ReleaseCall(call)
		return emptyCancelRpc,err
	}

	if cancelable {
		rpcCancel := RpcCancel{CallSeq:seq,Cli: rc.selfClient}
		return rpcCancel.CancelRpc,nil
	}

	return emptyCancelRpc,nil
}

func (rc *RClient) Run() {
	defer func() {
		if r := recover(); r != nil {
			buf := make([]byte, 4096)
			l := runtime.Stack(buf, false)
			errString := fmt.Sprint(r)
			log.Dump(string(buf[:l]),log.String("error",errString))
		}
	}()

	rc.TriggerRpcConnEvent(true, rc.selfClient.GetClientId(), rc.selfClient.GetNodeId())
	for {
		bytes, err := rc.conn.ReadMsg()
		if err != nil {
			log.Error("rclient read msg is failed",log.ErrorAttr("error",err))
			return
		}

		bCompress := (bytes[0]>>7) > 0
		processor := GetProcessor(bytes[0]&0x7f)
		if processor == nil {
			rc.conn.ReleaseReadMsg(bytes)
			log.Error("cannot find process",log.Uint8("process type",bytes[0]&0x7f))
			return
		}

		//1.解析head
		response := RpcResponse{}
		response.RpcResponseData = processor.MakeRpcResponse(0, "", nil)

		//解压缩
		byteData := bytes[1:]
		var compressBuff []byte

		if bCompress == true {
			var unCompressErr error
			compressBuff,unCompressErr = compressor.UncompressBlock(byteData)
			if unCompressErr!= nil {
				rc.conn.ReleaseReadMsg(bytes)
				log.Error("uncompressBlock failed",log.ErrorAttr("error",unCompressErr))
				return
			}
			byteData = compressBuff
		}

		err = processor.Unmarshal(byteData, response.RpcResponseData)
		if cap(compressBuff) > 0 {
			compressor.UnCompressBufferCollection(compressBuff)
		}

		rc.conn.ReleaseReadMsg(bytes)
		if err != nil {
			processor.ReleaseRpcResponse(response.RpcResponseData)
			log.Error("rpcClient Unmarshal head error",log.ErrorAttr("error",err))
			continue
		}
		
		v := rc.selfClient.RemovePending(response.RpcResponseData.GetSeq())
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
	}
}

func (rc *RClient) OnClose() {
	rc.TriggerRpcConnEvent(false, rc.selfClient.GetClientId(), rc.selfClient.GetNodeId())
}

func NewRClient(nodeId int, addr string, maxRpcParamLen uint32,compressBytesLen int,triggerRpcConnEvent TriggerRpcConnEvent) *Client{
	client := &Client{}
	client.clientId = atomic.AddUint32(&clientSeq, 1)
	client.nodeId = nodeId
	client.maxCheckCallRpcCount = DefaultMaxCheckCallRpcCount
	client.callRpcTimeout = DefaultRpcTimeout
	c:= &RClient{}
	c.compressBytesLen = compressBytesLen
	c.selfClient = client
	c.Addr = addr
	c.ConnectInterval = DefaultConnectInterval
	c.PendingWriteNum = DefaultMaxPendingWriteNum
	c.AutoReconnect = true
	c.TriggerRpcConnEvent = triggerRpcConnEvent
	c.ConnNum = DefaultRpcConnNum
	c.LenMsgLen = DefaultRpcLenMsgLen
	c.MinMsgLen = DefaultRpcMinMsgLen
	c.ReadDeadline = Default_ReadWriteDeadline
	c.WriteDeadline = Default_ReadWriteDeadline
	c.LittleEndian = LittleEndian
	c.NewAgent = client.NewClientAgent

	if maxRpcParamLen > 0 {
		c.MaxMsgLen = maxRpcParamLen
	} else {
		c.MaxMsgLen = math.MaxUint32
	}
	client.IRealClient = c
	client.InitPending()
	go client.checkRpcCallTimeout()
	c.Start()
	return client
}


func (rc *RClient) Close(waitDone bool) {
	rc.TCPClient.Close(waitDone)
	rc.selfClient.cleanPending()
}

