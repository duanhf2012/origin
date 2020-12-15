package timer

import (
	"container/heap"
	"github.com/duanhf2012/origin/log"
	"sync"
	"time"
)

func SetupTimer(timer *Timer) *Timer{
	timerHeapLock.Lock() // 使用锁规避竞争条件
	heap.Push(&timerHeap,timer)
	timerHeapLock.Unlock()
	return timer
}

type _TimerHeap struct {
	timers []*Timer
}

func (h *_TimerHeap) Len() int {
	return len(h.timers)
}

func (h *_TimerHeap) Less(i, j int) bool {
	return h.timers[i].fireTime.Before(h.timers[j].fireTime)
}

func (h *_TimerHeap) Swap(i, j int) {
	h.timers[i],h.timers[j] = h.timers[j],h.timers[i]
}

func (h *_TimerHeap) Push(x interface{}) {
	h.timers = append(h.timers, x.(*Timer))
}

func (h *_TimerHeap) Pop() (ret interface{}) {
	l := len(h.timers)
	h.timers, ret = h.timers[:l-1], h.timers[l-1]
	return
}

var (
	timerHeap     _TimerHeap // 定时器heap对象
	timerHeapLock sync.Mutex // 一个全局的锁
	timeOffset    time.Duration
)

func StartTimer(minTimerInterval time.Duration,maxTimerNum int){
	timerHeap.timers = make([]*Timer,0,maxTimerNum)
	heap.Init(&timerHeap) // 初始化定时器heap

	go  tickRoutine(minTimerInterval)
}

func tickRoutine(minTimerInterval time.Duration){
	for{
		time.Sleep(minTimerInterval)
		tick()
	}
}

func tick(){
	now := Now()
	timerHeapLock.Lock()
	if timerHeap.Len() <= 0 { // 没有任何定时器，立刻返回
		timerHeapLock.Unlock()
		return
	}
	nextFireTime := timerHeap.timers[0].fireTime
	if nextFireTime.After(now) { // 没有到时间的定时器，返回
		timerHeapLock.Unlock()
		return
	}

	t := heap.Pop(&timerHeap).(*Timer)
	timerHeapLock.Unlock()
	if len(t.C)>= cap(t.C) {
		log.Error("Timer channel full!")

		return
	}

	t.C <- t
}

func Now() time.Time{
	if timeOffset == 0 {
		return time.Now()
	}

	return time.Now().Add(timeOffset)
}


