package rpc

import (
	"errors"

	"strconv"
	"sync"
	"sync/atomic"
	"time"
	"github.com/duanhf2012/origin/v2/log"
)

type CallSet struct {
	pendingLock          sync.RWMutex
	startSeq             uint64
	pending              map[uint64]*Call
	callRpcTimeout       time.Duration
	maxCheckCallRpcCount int

	callTimerHeap CallTimerHeap
}



func (cs *CallSet) Init(){
	cs.pendingLock.Lock()
	cs.callTimerHeap.Init()
	cs.pending = make(map[uint64]*Call,4096)

	cs.maxCheckCallRpcCount = DefaultMaxCheckCallRpcCount
	cs.callRpcTimeout = DefaultRpcTimeout

	go cs.checkRpcCallTimeout()
	cs.pendingLock.Unlock()
}

func (bc *CallSet) makeCallFail(call *Call) {
	if call.callback != nil && call.callback.IsValid() {
		call.rpcHandler.PushRpcResponse(call)
	} else {
		call.done <- call
	}
}

func (bc *CallSet) checkRpcCallTimeout() {
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
				log.Error("call seq is not find",log.Uint64("seq", callSeq))
				continue
			}

			delete(bc.pending,callSeq)
			strTimeout := strconv.FormatInt(int64(pCall.TimeOut.Seconds()), 10)
			pCall.Err = errors.New("RPC call takes more than " + strTimeout + " seconds,method is "+pCall.ServiceMethod)
			log.Error("call timeout",log.String("error",pCall.Err.Error()))
			bc.makeCallFail(pCall)
			bc.pendingLock.Unlock()
			continue
		}
	}
}

func (bc *CallSet) AddPending(call *Call) {
	bc.pendingLock.Lock()

	if call.Seq == 0 {
		bc.pendingLock.Unlock()
		log.Stack("call is error.")
		return
	}

	bc.pending[call.Seq] = call
	bc.callTimerHeap.AddTimer(call.Seq,call.TimeOut)

	bc.pendingLock.Unlock()
}

func (bc *CallSet) RemovePending(seq uint64) *Call {
	if seq == 0  {
		return nil
	}
	bc.pendingLock.Lock()
	call := bc.removePending(seq)
	bc.pendingLock.Unlock()
	return call
}

func (bc *CallSet) removePending(seq uint64) *Call {
	v, ok := bc.pending[seq]
	if ok == false {
		return nil
	}

	bc.callTimerHeap.Cancel(seq)
	delete(bc.pending, seq)
	return v
}

func (bc *CallSet) FindPending(seq uint64) (pCall *Call) {
	if seq == 0 {
		return nil
	}

	bc.pendingLock.Lock()
	pCall = bc.pending[seq]
	bc.pendingLock.Unlock()

	return pCall
}

func (bc *CallSet) cleanPending(){
	bc.pendingLock.Lock()
	for  {
		callSeq := bc.callTimerHeap.PopFirst()
		if callSeq == 0 {
			break
		}
		pCall := bc.pending[callSeq]
		if pCall == nil {
			log.Error("call Seq is not find",log.Uint64("seq",callSeq))
			continue
		}

		delete(bc.pending,callSeq)
		pCall.Err = errors.New("nodeid is disconnect ")
		bc.makeCallFail(pCall)
	}

	bc.pendingLock.Unlock()
}

func (bc *CallSet) generateSeq() uint64 {
	return atomic.AddUint64(&bc.startSeq, 1)
}
