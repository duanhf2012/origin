package rpc

import (
	"errors"
	"github.com/duanhf2012/origin/v2/log"
	"github.com/duanhf2012/origin/v2/network"
	"reflect"
	"strings"
	"sync/atomic"
	"time"
)

// LClient 本结点的Client
type LClient struct {
	selfClient *Client
}

func (lc *LClient) Lock() {
}

func (lc *LClient) Unlock() {
}

func (lc *LClient) Run() {
}

func (lc *LClient) OnClose() {
}

func (lc *LClient) IsConnected() bool {
	return true
}

func (lc *LClient) SetConn(conn *network.TCPConn) {
}

func (lc *LClient) Close(waitDone bool) {
}

func (lc *LClient) Go(nodeId string, timeout time.Duration, rpcHandler IRpcHandler, noReply bool, serviceMethod string, args interface{}, reply interface{}) *Call {
	pLocalRpcServer := rpcHandler.GetRpcServer()()
	//判断是否是同一服务
	findIndex := strings.Index(serviceMethod, ".")
	if findIndex == -1 {
		sErr := errors.New("Call serviceMethod " + serviceMethod + " is error!")
		log.Error("call rpc fail", log.String("error", sErr.Error()))
		call := MakeCall()
		call.DoError(sErr)

		return call
	}

	serviceName := serviceMethod[:findIndex]
	if serviceName == rpcHandler.GetName() { //自己服务调用
		//调用自己rpcHandler处理器
		err := pLocalRpcServer.myselfRpcHandlerGo(lc.selfClient, serviceName, serviceMethod, args, requestHandlerNull, reply)
		call := MakeCall()

		if err != nil {
			call.DoError(err)
			return call
		}

		call.DoOK()
		return call
	}

	//其他的rpcHandler的处理器
	return pLocalRpcServer.selfNodeRpcHandlerGo(timeout, nil, lc.selfClient, noReply, serviceName, 0, serviceMethod, args, reply, nil)
}

func (lc *LClient) RawGo(nodeId string, timeout time.Duration, rpcHandler IRpcHandler, processor IRpcProcessor, noReply bool, rpcMethodId uint32, serviceName string, rawArgs []byte, reply interface{}) *Call {
	pLocalRpcServer := rpcHandler.GetRpcServer()()

	//服务自我调用
	if serviceName == rpcHandler.GetName() {
		call := MakeCall()
		call.ServiceMethod = serviceName
		call.Reply = reply
		call.TimeOut = timeout

		err := pLocalRpcServer.myselfRpcHandlerGo(lc.selfClient, serviceName, serviceName, rawArgs, requestHandlerNull, nil)
		call.Err = err
		call.done <- call

		return call
	}

	//其他的rpcHandler的处理器
	return pLocalRpcServer.selfNodeRpcHandlerGo(timeout, processor, lc.selfClient, true, serviceName, rpcMethodId, serviceName, nil, nil, rawArgs)
}

func (lc *LClient) AsyncCall(nodeId string, timeout time.Duration, rpcHandler IRpcHandler, serviceMethod string, callback reflect.Value, args interface{}, reply interface{}, cancelable bool) (CancelRpc, error) {
	pLocalRpcServer := rpcHandler.GetRpcServer()()

	//判断是否是同一服务
	findIndex := strings.Index(serviceMethod, ".")
	if findIndex == -1 {
		err := errors.New("Call serviceMethod " + serviceMethod + " is error!")
		callback.Call([]reflect.Value{reflect.ValueOf(reply), reflect.ValueOf(err)})
		log.Error("serviceMethod format is error", log.String("error", err.Error()))
		return emptyCancelRpc, nil
	}

	serviceName := serviceMethod[:findIndex]
	//调用自己rpcHandler处理器
	if serviceName == rpcHandler.GetName() { //自己服务调用
		return emptyCancelRpc, pLocalRpcServer.myselfRpcHandlerGo(lc.selfClient, serviceName, serviceMethod, args, callback, reply)
	}

	//其他的rpcHandler的处理器
	cancelRpc, err := pLocalRpcServer.selfNodeRpcHandlerAsyncGo(timeout, lc.selfClient, rpcHandler, false, serviceName, serviceMethod, args, reply, callback, cancelable)
	if err != nil {
		callback.Call([]reflect.Value{reflect.ValueOf(reply), reflect.ValueOf(err)})
	}

	return cancelRpc, nil
}

func NewLClient(localNodeId string, callSet *CallSet) *Client {
	client := &Client{}
	client.clientId = atomic.AddUint32(&clientSeq, 1)
	client.targetNodeId = localNodeId
	//client.maxCheckCallRpcCount = DefaultMaxCheckCallRpcCount
	//client.callRpcTimeout = DefaultRpcTimeout

	lClient := &LClient{}
	lClient.selfClient = client
	client.IRealClient = lClient
	client.CallSet = callSet
	return client
}

func (lc *LClient) Bind(_ IServer) {
}
