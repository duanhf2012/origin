package frametimer

import (
	"context"
	"github.com/duanhf2012/origin/v2/event"
	"github.com/duanhf2012/origin/v2/log"
	"github.com/duanhf2012/origin/v2/service"
	"sync"
	"time"
)

const defaultFps = 50
const defaultSleepInterval = time.Millisecond * 3
const maxFps = 1000
const maxMultiple = 5

type FrameGroupID uint64
type FrameTimerID uint64
type FrameNumber uint64

type timerData struct {
	frameNum       FrameNumber
	timerID        FrameTimerID
	idx            int
	cb             TimerCB
	tickerFrameNum FrameNumber
	ctx            context.Context
}

type FrameTimer struct {
	service.Module
	fps          uint32
	oneFrameTime time.Duration
	ticker       *time.Ticker

	mapFrameGroup  map[FrameNumber]map[FrameGroupID]struct{}
	mapGroup       map[FrameGroupID]*FrameGroup
	globalFrameNum FrameNumber // 当前帧

	maxTimerID FrameTimerID
	maxGroupID FrameGroupID

	mapTimer      map[FrameTimerID]*timerData
	timerDataPool *sync.Pool

	locker        sync.Mutex
	sleepInterval time.Duration
}

func (ft *FrameTimer) getTimerData(timerID FrameTimerID) *timerData {
	return ft.mapTimer[timerID]
}

func (ft *FrameTimer) addTimerData(timerID FrameTimerID, frameNum FrameNumber, tickerFrameNum FrameNumber, ctx context.Context, cb TimerCB) *timerData {
	td := ft.timerDataPool.Get().(*timerData)
	td.timerID = timerID
	td.frameNum = frameNum
	td.cb = cb
	td.idx = -1
	td.tickerFrameNum = tickerFrameNum

	ft.mapTimer[timerID] = td
	return td
}

func (ft *FrameTimer) removeTimerData(timerID FrameTimerID) {
	td := ft.mapTimer[timerID]
	if td == nil {
		return
	}

	ft.timerDataPool.Put(td)
}

func (ft *FrameTimer) genGroupID() FrameGroupID {
	ft.maxGroupID++
	return ft.maxGroupID
}

func (ft *FrameTimer) genTimerID() FrameTimerID {
	ft.maxTimerID++
	return ft.maxTimerID
}

func (ft *FrameTimer) getGlobalFrameNumber() FrameNumber {
	return ft.globalFrameNum
}

func (ft *FrameTimer) frameTick() {
	preFrameNum := ft.globalFrameNum
	ft.globalFrameNum++

	ft.locker.Lock()
	defer ft.locker.Unlock()
	for i := preFrameNum; i <= ft.globalFrameNum; i++ {
		mapGroup := ft.mapFrameGroup[i]
		delete(ft.mapFrameGroup, i)
		for groupID := range mapGroup {
			group := ft.mapGroup[groupID]
			if group == nil {
				continue
			}

			group.tick(ft.globalFrameNum)
		}
	}
}

func (ft *FrameTimer) removeGroup(groupID FrameGroupID, frameNum FrameNumber) {
	delete(ft.mapFrameGroup[frameNum], groupID)
}

func (ft *FrameTimer) refreshGroupMinFrame(groupID FrameGroupID, preFrameNum FrameNumber, newFrameNum FrameNumber) {
	ft.removeGroup(groupID, preFrameNum)

	mapGroup := ft.mapFrameGroup[newFrameNum]
	if mapGroup == nil {
		mapGroup = make(map[FrameGroupID]struct{}, 6)
		ft.mapFrameGroup[newFrameNum] = mapGroup
	}

	mapGroup[groupID] = struct{}{}
}

func (ft *FrameTimer) OnInit() error {
	ft.mapFrameGroup = make(map[FrameNumber]map[FrameGroupID]struct{}, 1024)
	ft.mapGroup = make(map[FrameGroupID]*FrameGroup, 1024)
	ft.mapTimer = make(map[FrameTimerID]*timerData, 2048)
	ft.timerDataPool = &sync.Pool{
		New: func() any {
			return &timerData{}
		},
	}

	if ft.fps == 0 {
		ft.fps = defaultFps
	}

	ft.GetEventProcessor().RegEventReceiverFunc(event.Sys_Event_FrameTick, ft.GetEventHandler(), func(e event.IEvent) {
		ev := e.(*event.Event)
		td, ok := ev.Data.(*timerData)
		if !ok {
			log.Error("convert *timerData error")
			return
		}
		td.cb(td.ctx, td.timerID)
		event.DeleteEvent(e)
	})

	ft.oneFrameTime = time.Second / time.Duration(ft.fps)
	ft.ticker = time.NewTicker(ft.oneFrameTime)

	if ft.sleepInterval == 0 {
		ft.sleepInterval = defaultSleepInterval
	}

	go func() {
		preTime := time.Now()
		var preFrame FrameNumber

		for {
			time.Sleep(ft.sleepInterval)
			frameMax := FrameNumber(time.Now().Sub(preTime) / ft.oneFrameTime)
			for i := preFrame + 1; i <= frameMax; i++ {
				ft.frameTick()
			}

			preFrame = frameMax
		}
	}()

	return nil
}

// SetFps 设置帧率，越大误差越低。如果有倍数加速需求，可以适当加大fps，以减少误差。默认50fps
func (ft *FrameTimer) SetFps(fps uint32) {
	if fps > maxFps {
		fps = maxFps
	}

	ft.fps = fps
}

// SetAccuracyInterval 设置时间间隔精度，在循环中sleep该时间进行判断。实际上因为sleep有误差，所以暂时不使用fps得出。默认为3ms
func (ft *FrameTimer) SetAccuracyInterval(interval time.Duration) {
	ft.sleepInterval = interval
}

// NewGroup 创建定时器组
func (ft *FrameTimer) NewGroup() *FrameGroup {
	var group FrameGroup
	group.ft = ft
	group.init()

	ft.locker.Lock()
	defer ft.locker.Unlock()
	ft.mapGroup[group.groupID] = &group
	return &group
}
