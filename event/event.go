package event

import (
	"fmt"
	"github.com/duanhf2012/origin/log"
	"runtime"
	"sync"
)

const Default_EventChannelLen = 10000

//事件接受器
type EventReciverFunc func(event *Event)

type Event struct {
	Type EventType
	Data interface{}
}

type IEventProcessor interface {
	NotifyEvent(*Event)

	SetEventReciver(eventProcessor IEventProcessor)
	GetEventReciver() IEventProcessor
	SetEventChanNum(num int32) bool
	RegEventReciverFunc(eventType EventType,reciverFunc EventReciverFunc)
	UnRegEventReciverFun(eventType EventType)
}

type EventProcessor struct {
	//事件管道
	EventChan chan *Event
	eventReciver IEventProcessor

	eventChanNumLocker sync.RWMutex
	eventChanNum int32
	mapEventReciverFunc map[EventType]EventReciverFunc
}

func (slf *EventProcessor) RegEventReciverFunc(eventType EventType,reciverFunc EventReciverFunc){
	if slf.mapEventReciverFunc == nil {
		slf.mapEventReciverFunc = map[EventType]EventReciverFunc{}
	}
	slf.mapEventReciverFunc[eventType] = reciverFunc
}

func (slf *EventProcessor) UnRegEventReciverFun(eventType EventType){
	delete(slf.mapEventReciverFunc,eventType)
}

func (slf *EventProcessor) NotifyEvent(pEvent *Event) {
	if len(slf.EventChan) >= int(slf.eventChanNum) {
		log.Error("event queue is full!")
	}
	slf.EventChan <-pEvent
}



func (slf *EventProcessor) GetEventChan() chan *Event{
	slf.eventChanNumLocker.Lock()
	defer  slf.eventChanNumLocker.Unlock()

	if slf.eventChanNum == 0 {
		slf.eventChanNum = Default_EventChannelLen
	}

	if slf.EventChan == nil {
		slf.EventChan = make(chan *Event,slf.eventChanNum)
	}

	return slf.EventChan
}

//不允许重复设置
func (slf *EventProcessor) SetEventChanNum(num int32) bool {
	slf.eventChanNumLocker.Lock()
	defer  slf.eventChanNumLocker.Unlock()
	if slf.eventChanNum>0 {
		return false
	}

	slf.eventChanNum = num
	return true
}

func (slf *EventProcessor) SetEventReciver(eventProcessor IEventProcessor){
	slf.eventReciver = eventProcessor
}


func (slf *EventProcessor) GetEventReciver() IEventProcessor{
	return slf.eventReciver
}

type IHttpEventData interface {
	Handle()
}

func (slf *EventProcessor) EventHandler(ev *Event) {
	defer func() {
		if r := recover(); r != nil {
			buf := make([]byte, 4096)
			l := runtime.Stack(buf, false)
			err := fmt.Errorf("%v: %s", r, buf[:l])
			log.Error("core dump info:%+v\n",err)
		}
	}()

	if slf.innerEventHandler(ev) == true {
		return
	}

	if fun,ok := slf.mapEventReciverFunc[ev.Type];ok == false{
		return
	}else{
		fun(ev)
	}
}

func (slf *EventProcessor) innerEventHandler(ev *Event) bool {
	switch ev.Type {
	case Sys_Event_Http_Event:
		ev.Data.(IHttpEventData).Handle()
		return true
	}

	return false
}


