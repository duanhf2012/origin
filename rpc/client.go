package rpc

import (
	"errors"
	"github.com/duanhf2012/origin/network"
	"reflect"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
	"github.com/duanhf2012/origin/log"
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

type IRealClient interface {
	SetConn(conn *network.TCPConn)
	Close(waitDone bool)

	AsyncCall(timeout time.Duration,rpcHandler IRpcHandler, serviceMethod string, callback reflect.Value, args interface{}, replyParam interface{},cancelable bool)  (CancelRpc,error)
	Go(timeout time.Duration,rpcHandler IRpcHandler, noReply bool, serviceMethod string, args interface{}, reply interface{}) *Call
	RawGo(timeout time.Duration,rpcHandler IRpcHandler,processor IRpcProcessor, noReply bool, rpcMethodId uint32, serviceMethod string, rawArgs []byte, reply interface{}) *Call
	IsConnected() bool

	Run()
	OnClose()
}

type Client struct {
	clientId 		uint32
	nodeId        int
	pendingLock          sync.RWMutex
	startSeq             uint64
	pending              map[uint64]*Call
	callRpcTimeout       time.Duration
	maxCheckCallRpcCount int

	callTimerHeap CallTimerHeap
	IRealClient
}

func (client *Client) NewClientAgent(conn *network.TCPConn) network.Agent {
	client.SetConn(conn)

	return client
}

func (bc *Client) makeCallFail(call *Call) {
	if call.callback != nil && call.callback.IsValid() {
		call.rpcHandler.PushRpcResponse(call)
	} else {
		call.done <- call
	}
}

func (bc *Client) checkRpcCallTimeout() {
	for{
		time.Sleep(DefaultCheckRpcCallTimeoutInterval)
		for i := 0; i < bc.maxCheckCallRpcCount; i++ {
			bc.pendingLock.Lock()
			
			callSeq := bc.callTimerHeap.PopTimeout()
			if callSeq == 0 {
				bc.pendingLock.Unlock()
				break
			}

			pCall := bc.pending[callSeq]
			if pCall == nil {
				bc.pendingLock.Unlock()
				log.SError("callSeq ",callSeq," is not find")
				continue
			}

			delete(bc.pending,callSeq)
			strTimeout := strconv.FormatInt(int64(pCall.TimeOut.Seconds()), 10)
			pCall.Err = errors.New("RPC call takes more than " + strTimeout + " seconds,method is "+pCall.ServiceMethod)
			log.SError(pCall.Err.Error())
			bc.makeCallFail(pCall)
			bc.pendingLock.Unlock()
			continue
		}
	}
}

func (client *Client) InitPending() {
	client.pendingLock.Lock()
	client.callTimerHeap.Init()
	client.pending = make(map[uint64]*Call,4096)
	client.pendingLock.Unlock()
}

func (bc *Client) AddPending(call *Call) {
	bc.pendingLock.Lock()

	if call.Seq == 0 {
		bc.pendingLock.Unlock()
		log.SStack("call is error.")
		return
	}

	bc.pending[call.Seq] = call
	bc.callTimerHeap.AddTimer(call.Seq,call.TimeOut)

	bc.pendingLock.Unlock()
}

func (bc *Client) RemovePending(seq uint64) *Call {
	if seq == 0  {
		return nil
	}
	bc.pendingLock.Lock()
	call := bc.removePending(seq)
	bc.pendingLock.Unlock()
	return call
}

func (bc *Client) removePending(seq uint64) *Call {
	v, ok := bc.pending[seq]
	if ok == false {
		return nil
	}

	bc.callTimerHeap.Cancel(seq)
	delete(bc.pending, seq)
	return v
}

func (bc *Client) FindPending(seq uint64) (pCall *Call) {
	if seq == 0 {
		return nil
	}
	
	bc.pendingLock.Lock()
	pCall = bc.pending[seq]
	bc.pendingLock.Unlock()

	return pCall
}

func (bc *Client) cleanPending(){
	bc.pendingLock.Lock()
	for  {
		callSeq := bc.callTimerHeap.PopFirst()
		if callSeq == 0 {
			break
		}
		pCall := bc.pending[callSeq]
		if pCall == nil {
			log.SError("callSeq ",callSeq," is not find")
			continue
		}

		delete(bc.pending,callSeq)
		pCall.Err = errors.New("nodeid is disconnect ")
		bc.makeCallFail(pCall)
	}

	bc.pendingLock.Unlock()
}

func (bc *Client) generateSeq() uint64 {
	return atomic.AddUint64(&bc.startSeq, 1)
}

func (client *Client) GetNodeId() int {
	return client.nodeId
}

func (client *Client) GetClientId() uint32 {
	return client.clientId
}
