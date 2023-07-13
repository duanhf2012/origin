package rpc

import (
	"container/heap"
	"time"
)

type CallTimer struct {
	SeqId    uint64
	FireTime int64
}

type CallTimerHeap struct {
	callTimer   []CallTimer
	mapSeqIndex map[uint64]int
}

func (h *CallTimerHeap) Init() {
	h.mapSeqIndex = make(map[uint64]int, 4096)
	h.callTimer = make([]CallTimer, 0, 4096)
}

func (h *CallTimerHeap) Len() int {
	return len(h.callTimer)
}

func (h *CallTimerHeap) Less(i, j int) bool {
	return h.callTimer[i].FireTime < h.callTimer[j].FireTime
}

func (h *CallTimerHeap) Swap(i, j int) {
	h.callTimer[i], h.callTimer[j] = h.callTimer[j], h.callTimer[i]
	h.mapSeqIndex[h.callTimer[i].SeqId] = i
	h.mapSeqIndex[h.callTimer[j].SeqId] = j
}

func (h *CallTimerHeap) Push(t any) {
	callTimer := t.(CallTimer)
	h.mapSeqIndex[callTimer.SeqId] = len(h.callTimer)
	h.callTimer = append(h.callTimer, callTimer)
}

func (h *CallTimerHeap) Pop() any {
	l := len(h.callTimer)
	seqId := h.callTimer[l-1].SeqId

	h.callTimer = h.callTimer[:l-1]
	delete(h.mapSeqIndex, seqId)
	return seqId
}

func (h *CallTimerHeap) Cancel(seq uint64) bool {
	index, ok := h.mapSeqIndex[seq]
	if ok == false {
		return false
	}

	heap.Remove(h, index)
	return true
}

func (h *CallTimerHeap) AddTimer(seqId uint64,d time.Duration){
	heap.Push(h, CallTimer{
		SeqId:    seqId,
		FireTime: time.Now().Add(d).UnixNano(),
	})
}

func (h *CallTimerHeap) PopTimeout() uint64 {
	if h.Len() == 0 {
		return 0
	}

	nextFireTime := h.callTimer[0].FireTime
	if nextFireTime > time.Now().UnixNano() {
		return 0
	}

	return heap.Pop(h).(uint64)
}

func (h *CallTimerHeap) PopFirst() uint64 {
	if h.Len() == 0 {
		return 0
	}
	
	return heap.Pop(h).(uint64)
}

