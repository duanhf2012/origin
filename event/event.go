package event

import (
	"github.com/duanhf2012/origin/log"
	"sync"
)

const Default_EventChannelLen = 10000

//事件接受器
type EventReciver func(event *Event) error

type Event struct {
	Type EventType
	Data interface{}
}

type IEventProcessor interface {
	NotifyEvent(*Event)
	OnEventHandler(event *Event) error
	SetEventReciver(eventProcessor IEventProcessor)
	GetEventReciver() IEventProcessor
	SetEventChanNum(num int32) bool
}

type EventProcessor struct {
	//事件管道
	EventChan chan *Event
	eventReciver IEventProcessor

	eventChanNumLocker sync.RWMutex
	eventChanNum int32
}

func (slf *EventProcessor) NotifyEvent(pEvent *Event) {
	if len(slf.EventChan) >= int(slf.eventChanNum) {
		log.Error("event queue is full!")
	}
	slf.EventChan <-pEvent
}

func (slf *EventProcessor) OnEventHandler(event *Event) error{
	return nil
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
