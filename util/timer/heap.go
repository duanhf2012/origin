package timer

import (
	"container/heap"
	"sync"
	"time"
)

func SetupTimer(timer ITimer) ITimer{
	if timer.IsOpen() == true {
		return nil
	}

	timer.Open(true)
	timerHeapLock.Lock() // 使用锁规避竞争条件
	heap.Push(&timerHeap,timer)
	timerHeapLock.Unlock()
	return timer
}

func NewTimer(d time.Duration) *Timer{
	c := make(chan ITimer,1)
	timer := newTimer(d,c,nil,"")
	SetupTimer(timer)

	return timer
}

func ReleaseTimer(timer *Timer) {
	releaseTimer(timer)
}

type _TimerHeap struct {
	timers []ITimer
}

func (h *_TimerHeap) Len() int {
	return len(h.timers)
}

func (h *_TimerHeap) Less(i, j int) bool {
	return h.timers[i].GetFireTime().Before(h.timers[j].GetFireTime())
}

func (h *_TimerHeap) Swap(i, j int) {
	h.timers[i],h.timers[j] = h.timers[j],h.timers[i]
}

func (h *_TimerHeap) Push(x interface{}) {
	h.timers = append(h.timers, x.(ITimer))
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
	timerHeap.timers = make([]ITimer,0,maxTimerNum)
	heap.Init(&timerHeap) // 初始化定时器heap

	go  tickRoutine(minTimerInterval)
}

func tickRoutine(minTimerInterval time.Duration){
	var bContinue bool
	for{
		bContinue = tick()
		if bContinue == false {
			time.Sleep(minTimerInterval)
		}
	}
}

func tick() bool{
	now := Now()
	timerHeapLock.Lock()
	if timerHeap.Len() <= 0 { // 没有任何定时器，立刻返回
		timerHeapLock.Unlock()
		return false
	}
	nextFireTime := timerHeap.timers[0].GetFireTime()
	if nextFireTime.After(now) { // 没有到时间的定时器，返回
		timerHeapLock.Unlock()
		return false
	}

	t := heap.Pop(&timerHeap).(ITimer)
	timerHeapLock.Unlock()
	t.Open(false)
	t.AppendChannel(t)

	return true
}

func Now() time.Time{
	if timeOffset == 0 {
		return time.Now()
	}

	return time.Now().Add(timeOffset)
}


