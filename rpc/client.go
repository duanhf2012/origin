package rpc

import (
	"container/list"
	"errors"
	"github.com/duanhf2012/origin/network"
	"reflect"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

const(
	DefaultRpcConnNum           = 1
	DefaultRpcLenMsgLen         = 4
	DefaultRpcMinMsgLen         = 2
	DefaultMaxCheckCallRpcCount = 1000
	DefaultMaxPendingWriteNum 	 = 200000


	DefaultConnectInterval = 2*time.Second
	DefaultCheckRpcCallTimeoutInterval = 5*time.Second
	DefaultRpcTimeout = 15*time.Second
)

var clientSeq uint32

type IRealClient interface {
	SetConn(conn *network.TCPConn)
	Close(waitDone bool)

	AsyncCall(rpcHandler IRpcHandler, serviceMethod string, callback reflect.Value, args interface{}, replyParam interface{}) error
	Go(rpcHandler IRpcHandler, noReply bool, serviceMethod string, args interface{}, reply interface{}) *Call
	RawGo(rpcHandler IRpcHandler,processor IRpcProcessor, noReply bool, rpcMethodId uint32, serviceMethod string, rawArgs []byte, reply interface{}) *Call
	IsConnected() bool

	Run()
	OnClose()
}

type Client struct {
	clientId 		uint32
	nodeId        int
	pendingLock          sync.RWMutex
	startSeq             uint64
	pending              map[uint64]*list.Element
	pendingTimer         *list.List
	callRpcTimeout       time.Duration
	maxCheckCallRpcCount int

	IRealClient
}

func (client *Client) NewClientAgent(conn *network.TCPConn) network.Agent {
	client.SetConn(conn)

	return client
}

func (bc *Client) makeCallFail(call *Call) {
	bc.removePending(call.Seq)
	if call.callback != nil && call.callback.IsValid() {
		call.rpcHandler.PushRpcResponse(call)
	} else {
		call.done <- call
	}
}

func (bc *Client) checkRpcCallTimeout() {
	for{
		time.Sleep(DefaultCheckRpcCallTimeoutInterval)
		now := time.Now()

		for i := 0; i < bc.maxCheckCallRpcCount; i++ {
			bc.pendingLock.Lock()
			if bc.pendingTimer == nil {
				bc.pendingLock.Unlock()
				break
			}

			pElem := bc.pendingTimer.Front()
			if pElem == nil {
				bc.pendingLock.Unlock()
				break
			}
			pCall := pElem.Value.(*Call)
			if now.Sub(pCall.callTime) > bc.callRpcTimeout {
				strTimeout := strconv.FormatInt(int64(bc.callRpcTimeout/time.Second), 10)
				pCall.Err = errors.New("RPC call takes more than " + strTimeout + " seconds")
				bc.makeCallFail(pCall)
				bc.pendingLock.Unlock()
				continue
			}
			bc.pendingLock.Unlock()
			break
		}
	}
}

func (client *Client) InitPending() {
	client.pendingLock.Lock()
	if client.pending != nil {
		for _, v := range client.pending {
			v.Value.(*Call).Err = errors.New("node is disconnect")
			v.Value.(*Call).done <- v.Value.(*Call)
		}
	}

	client.pending = make(map[uint64]*list.Element, 4096)
	client.pendingTimer = list.New()
	client.pendingLock.Unlock()
}


func (bc *Client) AddPending(call *Call) {
	bc.pendingLock.Lock()
	call.callTime = time.Now()
	elemTimer := bc.pendingTimer.PushBack(call)
	bc.pending[call.Seq] = elemTimer //如果下面发送失败，将会一一直存在这里
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
	call := v.Value.(*Call)
	bc.pendingTimer.Remove(v)
	delete(bc.pending, seq)
	return call
}

func (bc *Client) FindPending(seq uint64) *Call {
	if seq == 0 {
		return nil
	}
	
	bc.pendingLock.Lock()
	v, ok := bc.pending[seq]
	if ok == false {
		bc.pendingLock.Unlock()
		return nil
	}

	pCall := v.Value.(*Call)
	bc.pendingLock.Unlock()

	return pCall
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
