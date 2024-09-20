package rpc

import (
	"errors"

	"github.com/duanhf2012/origin/v2/log"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

type CallSet struct {
	pendingLock          sync.RWMutex
	startSeq             uint64
	pending              map[uint64]*Call
	callRpcTimeout       time.Duration
	maxCheckCallRpcCount int

	callTimerHeap CallTimerHeap
}

func (cs *CallSet) Init() {
	cs.pendingLock.Lock()
	cs.callTimerHeap.Init()
	cs.pending = make(map[uint64]*Call, 4096)

	cs.maxCheckCallRpcCount = DefaultMaxCheckCallRpcCount
	cs.callRpcTimeout = DefaultRpcTimeout

	go cs.checkRpcCallTimeout()
	cs.pendingLock.Unlock()
}

func (cs *CallSet) makeCallFail(call *Call) {
	if call.callback != nil && call.callback.IsValid() {
		call.rpcHandler.PushRpcResponse(call)
	} else {
		call.done <- call
	}
}

func (cs *CallSet) checkRpcCallTimeout() {
	for {
		time.Sleep(DefaultCheckRpcCallTimeoutInterval)
		for i := 0; i < cs.maxCheckCallRpcCount; i++ {
			cs.pendingLock.Lock()

			callSeq := cs.callTimerHeap.PopTimeout()
			if callSeq == 0 {
				cs.pendingLock.Unlock()
				break
			}

			pCall := cs.pending[callSeq]
			if pCall == nil {
				cs.pendingLock.Unlock()
				log.Error("call seq is not find", log.Uint64("seq", callSeq))
				continue
			}

			delete(cs.pending, callSeq)
			strTimeout := strconv.FormatInt(int64(pCall.TimeOut.Seconds()), 10)
			pCall.Err = errors.New("RPC call takes more than " + strTimeout + " seconds,method is " + pCall.ServiceMethod)
			log.Error("call timeout", log.String("error", pCall.Err.Error()))
			cs.makeCallFail(pCall)
			cs.pendingLock.Unlock()
			continue
		}
	}
}

func (cs *CallSet) AddPending(call *Call) {
	cs.pendingLock.Lock()

	if call.Seq == 0 {
		cs.pendingLock.Unlock()
		log.Stack("call is error.")
		return
	}

	cs.pending[call.Seq] = call
	cs.callTimerHeap.AddTimer(call.Seq, call.TimeOut)

	cs.pendingLock.Unlock()
}

func (cs *CallSet) RemovePending(seq uint64) *Call {
	if seq == 0 {
		return nil
	}
	cs.pendingLock.Lock()
	call := cs.removePending(seq)
	cs.pendingLock.Unlock()
	return call
}

func (cs *CallSet) removePending(seq uint64) *Call {
	v, ok := cs.pending[seq]
	if ok == false {
		return nil
	}

	cs.callTimerHeap.Cancel(seq)
	delete(cs.pending, seq)
	return v
}

func (cs *CallSet) FindPending(seq uint64) (pCall *Call) {
	if seq == 0 {
		return nil
	}

	cs.pendingLock.Lock()
	pCall = cs.pending[seq]
	cs.pendingLock.Unlock()

	return pCall
}

func (cs *CallSet) cleanPending() {
	cs.pendingLock.Lock()
	for {
		callSeq := cs.callTimerHeap.PopFirst()
		if callSeq == 0 {
			break
		}
		pCall := cs.pending[callSeq]
		if pCall == nil {
			log.Error("call Seq is not find", log.Uint64("seq", callSeq))
			continue
		}

		delete(cs.pending, callSeq)
		pCall.Err = errors.New("node is disconnect ")
		cs.makeCallFail(pCall)
	}

	cs.pendingLock.Unlock()
}

func (cs *CallSet) generateSeq() uint64 {
	return atomic.AddUint64(&cs.startSeq, 1)
}
