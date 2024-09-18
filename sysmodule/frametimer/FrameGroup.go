package frametimer

import (
	"container/heap"
	"context"
	"errors"
	"github.com/duanhf2012/origin/v2/event"
	"github.com/duanhf2012/origin/v2/log"
	"time"
)

type TimerCB func(context.Context, FrameTimerID)

type _timerHeap struct {
	timers []*timerData
}

func (h *_timerHeap) Len() int {
	return len(h.timers)
}

func (h *_timerHeap) Less(i, j int) bool {
	return h.timers[i].frameNum < h.timers[j].frameNum
}

func (h *_timerHeap) Swap(i, j int) {
	h.timers[i], h.timers[j] = h.timers[j], h.timers[i]
	h.timers[i].idx = i
	h.timers[j].idx = j
}

func (h *_timerHeap) Push(x interface{}) {
	td := x.(*timerData)
	h.timers = append(h.timers, td)
	td.idx = len(h.timers) - 1
}

func (h *_timerHeap) Pop() (ret interface{}) {
	l := len(h.timers)
	h.timers, ret = h.timers[:l-1], h.timers[l-1]
	return
}

type FrameGroup struct {
	groupID   FrameGroupID
	timerHeap _timerHeap
	ft        *FrameTimer

	preTickGlobalFrameNum FrameNumber // 上次tick全局帧
	preGlobalFrameNum     FrameNumber // 上次设置的全局帧,用于更新FrameTimer.mapFrameGroup关系
	frameNum              FrameNumber // 当前帧

	pause    bool  // 暂停状态
	multiple uint8 // 位数，默认1倍，只允许1-5倍数
}

func (fg *FrameGroup) init() {
	fg.timerHeap.timers = make([]*timerData, 0, 512)
	fg.groupID = fg.ft.genGroupID()
	fg.multiple = 1
	heap.Init(&fg.timerHeap)
}

func (fg *FrameGroup) convertGlobalFrameNum(frameNum FrameNumber) FrameNumber {
	return fg.ft.getGlobalFrameNumber() + (frameNum-fg.frameNum)/FrameNumber(fg.multiple)
}

func (fg *FrameGroup) refreshMinFrame() {
	if fg.timerHeap.Len() == 0 || fg.pause {
		return
	}

	globalFrameNum := fg.convertGlobalFrameNum(fg.timerHeap.timers[0].frameNum)
	fg.ft.refreshGroupMinFrame(fg.groupID, fg.preGlobalFrameNum, globalFrameNum)
	fg.preGlobalFrameNum = globalFrameNum
}

func (fg *FrameGroup) tick(globalFrame FrameNumber) {
	fg.frameNum = fg.frameNum + (globalFrame-fg.preTickGlobalFrameNum)*FrameNumber(fg.multiple)
	fg.preTickGlobalFrameNum = globalFrame

	fg.onceTick()

	fg.refreshMinFrame()
}

func (fg *FrameGroup) onceTick() {
	for fg.timerHeap.Len() > 0 {
		if fg.timerHeap.timers[0].frameNum > fg.frameNum {
			break
		}

		t := heap.Pop(&fg.timerHeap).(*timerData)

		ev := event.NewEvent()
		ev.Type = event.Sys_Event_FrameTick
		ev.Data = t
		fg.ft.NotifyEvent(ev)
		fg.ft.removeTimerData(t.timerID)

		if t.tickerFrameNum != 0 {
			fg.addTicker(t.timerID, t.tickerFrameNum, t.ctx, t.cb)
		}
	}
}

func (fg *FrameGroup) addTimer(timerID FrameTimerID, frame FrameNumber, ctx context.Context, cb TimerCB) {
	nextFrame := fg.frameNum + frame

	td := fg.ft.addTimerData(timerID, nextFrame, 0, ctx, cb)
	heap.Push(&fg.timerHeap, td)
}

func (fg *FrameGroup) addTicker(timerID FrameTimerID, frame FrameNumber, ctx context.Context, cb TimerCB) {
	nextFrame := fg.frameNum + frame

	td := fg.ft.addTimerData(timerID, nextFrame, frame, ctx, cb)
	heap.Push(&fg.timerHeap, td)
}

// SetMultiple 设置倍数，允许倍数范围1-5
func (fg *FrameGroup) SetMultiple(multiple uint8) error {
	if fg.multiple == multiple {
		return nil
	}

	if multiple < 0 || multiple > maxMultiple {
		return errors.New("invalid multiplier")
	}

	fg.multiple = multiple

	fg.refreshMinFrame()
	return nil
}

// FrameAfterFunc 创建After定时器
func (fg *FrameGroup) FrameAfterFunc(timerID *FrameTimerID, d time.Duration, ctx context.Context, cb TimerCB) {
	fg.ft.locker.Lock()
	defer fg.ft.locker.Unlock()

	frame := FrameNumber(d / fg.ft.oneFrameTime)
	newTimerID := fg.ft.genTimerID()

	fg.addTimer(newTimerID, frame, ctx, cb)
	*timerID = newTimerID
	fg.refreshMinFrame()
}

// FrameNewTicker 创建Ticker定时器
func (fg *FrameGroup) FrameNewTicker(timerID *FrameTimerID, d time.Duration, ctx context.Context, cb TimerCB) {
	fg.ft.locker.Lock()
	defer fg.ft.locker.Unlock()

	frame := FrameNumber(d / fg.ft.oneFrameTime)
	newTimerID := fg.ft.genTimerID()

	fg.addTicker(newTimerID, frame, ctx, cb)
	*timerID = newTimerID
	fg.refreshMinFrame()
}

// Pause 暂停定时器组
func (fg *FrameGroup) Pause() {
	fg.ft.locker.Lock()
	defer fg.ft.locker.Unlock()

	fg.pause = true
	fg.ft.removeGroup(fg.groupID, fg.preGlobalFrameNum)
	fg.preGlobalFrameNum = 0
}

// Resume 唤醒定时器组
func (fg *FrameGroup) Resume() {
	fg.ft.locker.Lock()
	defer fg.ft.locker.Unlock()

	fg.pause = false
	fg.refreshMinFrame()
	fg.preTickGlobalFrameNum = fg.ft.globalFrameNum
}

// CancelTimer 关闭定时器
func (fg *FrameGroup) CancelTimer(timerID FrameTimerID) {
	fg.ft.locker.Lock()
	defer fg.ft.locker.Unlock()

	td := fg.ft.getTimerData(timerID)
	if td == nil {
		log.Error("cannot find timer", log.Uint64("timerID", uint64(timerID)))
		return
	}

	heap.Remove(&fg.timerHeap, td.idx)
	fg.ft.removeGroup(fg.groupID, fg.preGlobalFrameNum)
	fg.preGlobalFrameNum = 0
	fg.refreshMinFrame()
}
