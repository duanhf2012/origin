package rpc

import (
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

func (rc *RClient) WriteMsg (nodeId string,args ...[]byte) error{
	return rc.conn.WriteMsg(args...)
}

func (rc *RClient) Go(nodeId string,timeout time.Duration,rpcHandler IRpcHandler,noReply bool, serviceMethod string, args interface{}, reply interface{}) *Call {
	_, processor := GetProcessorType(args)
	InParam, err := processor.Marshal(args)
	if err != nil {
		log.Error("Marshal is fail",log.ErrorAttr("error",err))
		call := MakeCall()
		call.DoError(err)
		return call
	}

	return rc.selfClient.rawGo(nodeId,rc,timeout,rpcHandler,processor, noReply, 0, serviceMethod, InParam, reply)
}

func (rc *RClient) RawGo(nodeId string,timeout time.Duration,rpcHandler IRpcHandler,processor IRpcProcessor, noReply bool, rpcMethodId uint32, serviceMethod string, rawArgs []byte, reply interface{}) *Call {
	return rc.selfClient.rawGo(nodeId,rc,timeout,rpcHandler,processor, noReply, rpcMethodId, serviceMethod, rawArgs, reply)
}

func (rc *RClient) AsyncCall(nodeId string,timeout time.Duration,rpcHandler IRpcHandler, serviceMethod string, callback reflect.Value, args interface{}, replyParam interface{},cancelable bool)  (CancelRpc,error) {
	cancelRpc,err := rc.selfClient.asyncCall(nodeId,rc,timeout,rpcHandler, serviceMethod, callback, args, replyParam,cancelable)
	if err != nil {
		callback.Call([]reflect.Value{reflect.ValueOf(replyParam), reflect.ValueOf(err)})
	}

	return cancelRpc,nil
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

	rc.TriggerRpcConnEvent(true, rc.selfClient.GetClientId(), rc.selfClient.GetTargetNodeId())
	for {
		bytes, err := rc.conn.ReadMsg()
		if err != nil {
			log.Error("rclient read msg is failed",log.ErrorAttr("error",err))
			return
		}

		err = rc.selfClient.processRpcResponse(bytes)
		rc.conn.ReleaseReadMsg(bytes)
		if err != nil {
			return
		}
	}
}

func (rc *RClient) OnClose() {
	rc.TriggerRpcConnEvent(false, rc.selfClient.GetClientId(), rc.selfClient.GetTargetNodeId())
}

func NewRClient(targetNodeId string, addr string, maxRpcParamLen uint32,compressBytesLen int,triggerRpcConnEvent TriggerRpcConnEvent,callSet *CallSet) *Client{
	client := &Client{}
	client.clientId = atomic.AddUint32(&clientSeq, 1)
	client.targetNodeId = targetNodeId
	//client.maxCheckCallRpcCount = DefaultMaxCheckCallRpcCount
	//client.callRpcTimeout = DefaultRpcTimeout
	client.compressBytesLen = compressBytesLen

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
	client.CallSet = callSet
	c.Start()
	return client
}


func (rc *RClient) Close(waitDone bool) {
	rc.TCPClient.Close(waitDone)
	rc.selfClient.cleanPending()
}


func (rc *RClient) Bind(server IServer){

}