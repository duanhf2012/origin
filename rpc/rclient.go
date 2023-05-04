package rpc

import (
	"errors"
	"fmt"
	"github.com/duanhf2012/origin/log"
	"github.com/duanhf2012/origin/network"
	"math"
	"reflect"
	"runtime"
	"sync/atomic"
)

//跨结点连接的Client
type RClient struct {
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

func (rc *RClient) Go(rpcHandler IRpcHandler,noReply bool, serviceMethod string, args interface{}, reply interface{}) *Call {
	_, processor := GetProcessorType(args)
	InParam, err := processor.Marshal(args)
	if err != nil {
		log.SError(err.Error())
		call := MakeCall()
		call.DoError(err)
		return call
	}

	return rc.RawGo(rpcHandler,processor, noReply, 0, serviceMethod, InParam, reply)
}


func (rc *RClient) RawGo(rpcHandler IRpcHandler,processor IRpcProcessor, noReply bool, rpcMethodId uint32, serviceMethod string, rawArgs []byte, reply interface{}) *Call {
	call := MakeCall()
	call.ServiceMethod = serviceMethod
	call.Reply = reply
	call.Seq = rc.selfClient.generateSeq()

	request := MakeRpcRequest(processor, call.Seq, rpcMethodId, serviceMethod, noReply, rawArgs)
	bytes, err := processor.Marshal(request.RpcRequestData)
	ReleaseRpcRequest(request)

	if err != nil {
		call.Seq = 0
		log.SError(err.Error())
		call.DoError(err)
		return call
	}

	conn := rc.GetConn()
	if conn == nil || conn.IsConnected()==false {
		call.Seq = 0
		sErr := errors.New(serviceMethod + "  was called failed,rpc client is disconnect")
		log.SError(sErr.Error())
		call.DoError(sErr)
		return call
	}

	if noReply == false {
		rc.selfClient.AddPending(call)
	}

	err = conn.WriteMsg([]byte{uint8(processor.GetProcessorType())}, bytes)
	if err != nil {
		rc.selfClient.RemovePending(call.Seq)

		log.SError(err.Error())

		call.Seq = 0
		call.DoError(err)
	}

	return call
}


func (rc *RClient) AsyncCall(rpcHandler IRpcHandler, serviceMethod string, callback reflect.Value, args interface{}, replyParam interface{}) error {
	err := rc.asyncCall(rpcHandler, serviceMethod, callback, args, replyParam)
	if err != nil {
		callback.Call([]reflect.Value{reflect.ValueOf(replyParam), reflect.ValueOf(err)})
	}

	return nil
}

func (rc *RClient) asyncCall(rpcHandler IRpcHandler, serviceMethod string, callback reflect.Value, args interface{}, replyParam interface{}) error {
	processorType, processor := GetProcessorType(args)
	InParam, herr := processor.Marshal(args)
	if herr != nil {
		return herr
	}

	seq := rc.selfClient.generateSeq()
	request := MakeRpcRequest(processor, seq, 0, serviceMethod, false, InParam)
	bytes, err := processor.Marshal(request.RpcRequestData)
	ReleaseRpcRequest(request)
	if err != nil {
		return err
	}

	conn := rc.GetConn()
	if conn == nil || conn.IsConnected()==false {
		return errors.New("Rpc server is disconnect,call " + serviceMethod)
	}

	call := MakeCall()
	call.Reply = replyParam
	call.callback = &callback
	call.rpcHandler = rpcHandler
	call.ServiceMethod = serviceMethod
	call.Seq = seq
	rc.selfClient.AddPending(call)

	err = conn.WriteMsg([]byte{uint8(processorType)}, bytes)
	if err != nil {
		rc.selfClient.RemovePending(call.Seq)
		ReleaseCall(call)
		return err
	}

	return nil
}

func (rc *RClient) Run() {
	defer func() {
		if r := recover(); r != nil {
			buf := make([]byte, 4096)
			l := runtime.Stack(buf, false)
			errString := fmt.Sprint(r)
			log.SError("core dump info[", errString, "]\n", string(buf[:l]))
		}
	}()

	rc.TriggerRpcConnEvent(true, rc.selfClient.GetClientId(), rc.selfClient.GetNodeId())
	for {
		bytes, err := rc.conn.ReadMsg()
		if err != nil {
			log.SError("rpcClient ", rc.Addr, " ReadMsg error:", err.Error())
			return
		}

		processor := GetProcessor(bytes[0])
		if processor == nil {
			rc.conn.ReleaseReadMsg(bytes)
			log.SError("rpcClient ", rc.Addr, " ReadMsg head error:", err.Error())
			return
		}

		//1.解析head
		response := RpcResponse{}
		response.RpcResponseData = processor.MakeRpcResponse(0, "", nil)

		err = processor.Unmarshal(bytes[1:], response.RpcResponseData)
		rc.conn.ReleaseReadMsg(bytes)
		if err != nil {
			processor.ReleaseRpcResponse(response.RpcResponseData)
			log.SError("rpcClient Unmarshal head error:", err.Error())
			continue
		}

		v := rc.selfClient.RemovePending(response.RpcResponseData.GetSeq())
		if v == nil {
			log.SError("rpcClient cannot find seq ", response.RpcResponseData.GetSeq(), " in pending")
		} else {
			v.Err = nil
			if len(response.RpcResponseData.GetReply()) > 0 {
				err = processor.Unmarshal(response.RpcResponseData.GetReply(), v.Reply)
				if err != nil {
					log.SError("rpcClient Unmarshal body error:", err.Error())
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

func NewRClient(nodeId int, addr string, maxRpcParamLen uint32,triggerRpcConnEvent TriggerRpcConnEvent) *Client{
	client := &Client{}
	client.clientId = atomic.AddUint32(&clientSeq, 1)
	client.nodeId = nodeId
	client.maxCheckCallRpcCount = DefaultMaxCheckCallRpcCount
	client.callRpcTimeout = DefaultRpcTimeout

	c:= &RClient{}
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

	rc.selfClient.pendingLock.Lock()
	for  {
		pElem := rc.selfClient.pendingTimer.Front()
		if pElem == nil {
			break
		}

		pCall := pElem.Value.(*Call)
		pCall.Err = errors.New("nodeid is disconnect ")
		rc.selfClient.makeCallFail(pCall)
	}
	rc.selfClient.pendingLock.Unlock()
}

