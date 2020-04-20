package event

import (
	"fmt"
	"github.com/duanhf2012/origin/log"
	"runtime"
	"sync"
)

const Default_EventChannelLen = 10000

//事件接受器
type EventCallBack func(event *Event)


type Event struct {
	Type EventType
	Data interface{}
}

type IEventHandler interface {

	GetEventProcessor() IEventProcessor  //获得事件
	NotifyEvent(*Event)

	//注册了事件
	addRegInfo(eventType EventType,eventProcessor IEventProcessor)
	removeRegInfo(eventType EventType,eventProcessor IEventProcessor)

}

type IEventProcessor interface {
	RegEventReciverFunc(eventType EventType,reciver IEventHandler,callback EventCallBack)
	UnRegEventReciverFun(eventType EventType,reciver IEventHandler)
	SetEventChannel(channelNum int) bool

	castEvent(event *Event) //广播事件
	pushEvent(event *Event)
	addBindEvent(eventType EventType,reciver IEventHandler,callback EventCallBack)
	addListen(eventType EventType,reciver IEventHandler)
	removeBindEvent(eventType EventType,reciver IEventHandler)
	removeListen(eventType EventType,reciver IEventHandler)
}

type EventHandler struct {
	//已经注册的事件类型
	eventProcessor IEventProcessor

	//已经注册的事件
	locker sync.RWMutex
	mapRegEvent map[EventType]map[IEventProcessor]interface{}  //向其他事件处理器监听的事件类型
}


type EventProcessor struct {
	eventChannel chan *Event

	locker sync.RWMutex
	mapListenerEvent map[EventType]map[IEventProcessor]int           //监听者信息
	mapBindHandlerEvent map[EventType]map[IEventHandler]EventCallBack//收到事件处理
}

func (slf *EventHandler) addRegInfo(eventType EventType,eventProcessor IEventProcessor){
	slf.locker.Lock()
	defer slf.locker.Unlock()
	if slf.mapRegEvent == nil {
		slf.mapRegEvent = map[EventType]map[IEventProcessor]interface{}{}
	}

	if _,ok := slf.mapRegEvent[eventType] ;ok == false{
		slf.mapRegEvent[eventType] = map[IEventProcessor]interface{}{}
	}
	slf.mapRegEvent[eventType][eventProcessor] = nil
}

func (slf *EventHandler) removeRegInfo(eventType EventType,eventProcessor IEventProcessor){
	if _,ok :=slf.mapRegEvent[eventType];ok == true {
		delete(slf.mapRegEvent[eventType],eventProcessor)
	}
}

func (slf *EventHandler) GetEventProcessor() IEventProcessor{
	return slf.eventProcessor
}

func (slf *EventHandler) NotifyEvent(ev *Event){
	slf.GetEventProcessor().castEvent(ev)
}

func (slf *EventHandler) Init(processor IEventProcessor){
	slf.eventProcessor = processor
}


func (slf *EventProcessor) SetEventChannel(channelNum int) bool{
	slf.locker.Lock()
	defer slf.locker.Unlock()
	if slf.eventChannel!=nil {
		return false
	}

	if channelNum == 0 {
		channelNum = Default_EventChannelLen
	}

	slf.eventChannel = make(chan *Event,channelNum)
	return true
}

func (slf *EventProcessor) addBindEvent(eventType EventType,reciver IEventHandler,callback EventCallBack){
	//mapBindHandlerEvent map[EventType]map[IEventHandler]EventCallBack//收到事件处理
	slf.locker.Lock()
	defer slf.locker.Unlock()
	if slf.mapBindHandlerEvent == nil {
		slf.mapBindHandlerEvent = map[EventType]map[IEventHandler]EventCallBack{}
	}

	if _,ok := slf.mapBindHandlerEvent[eventType]; ok == false {
		slf.mapBindHandlerEvent[eventType] = map[IEventHandler]EventCallBack{}
	}

	slf.mapBindHandlerEvent[eventType][reciver] = callback
}

func (slf *EventProcessor) addListen(eventType EventType,reciver IEventHandler){
	slf.locker.Lock()
	defer slf.locker.Unlock()

	//mapListenerEvent map[EventType]map[IEventProcessor]int
	if slf.mapListenerEvent == nil {
		slf.mapListenerEvent = map[EventType]map[IEventProcessor]int{}
	}

	if _,ok :=slf.mapListenerEvent[eventType];ok == false{
		slf.mapListenerEvent[eventType] = map[IEventProcessor]int{}
	}

	slf.mapListenerEvent[eventType][reciver.GetEventProcessor()] += 1
}

func (slf *EventProcessor) removeBindEvent(eventType EventType,reciver IEventHandler){
	slf.locker.Lock()
	defer slf.locker.Unlock()
	if _,ok := slf.mapBindHandlerEvent[eventType];ok == true{
		delete(slf.mapBindHandlerEvent[eventType],reciver)
	}
}

func (slf *EventProcessor) removeListen(eventType EventType,reciver IEventHandler){
	slf.locker.Lock()
	defer slf.locker.Unlock()
	if _,ok := slf.mapListenerEvent[eventType];ok == true{
		slf.mapListenerEvent[eventType][reciver.GetEventProcessor()]-=1
		if slf.mapListenerEvent[eventType][reciver.GetEventProcessor()] <= 0 {
			delete(slf.mapListenerEvent[eventType],reciver.GetEventProcessor())
		}
	}
}

func (slf *EventProcessor) RegEventReciverFunc(eventType EventType,reciver IEventHandler,callback EventCallBack){
	//记录reciver自己注册过的事件
	reciver.addRegInfo(eventType,slf)
	//记录当前所属IEventProcessor注册的回调
	reciver.GetEventProcessor().addBindEvent(eventType,reciver,callback)
	//将注册加入到监听中
	slf.addListen(eventType,reciver)
}

func (slf *EventProcessor) UnRegEventReciverFun(eventType EventType,reciver IEventHandler) {
	slf.removeListen(eventType,reciver)
	reciver.GetEventProcessor().removeBindEvent(eventType,reciver)
	reciver.removeRegInfo(eventType,slf)
}

func (slf *EventHandler) desctory(){
	for eventTyp,mapEventProcess := range slf.mapRegEvent {
		if mapEventProcess == nil {
			continue
		}

		//map[IEventProcessor]interface{}
		for eventProcess,_ := range mapEventProcess {
			eventProcess.UnRegEventReciverFun(eventTyp,slf)
		}
	}
}

func (slf *EventProcessor) GetEventChan() chan *Event{
	slf.locker.Lock()
	defer slf.locker.Unlock()

	if slf.eventChannel == nil {
		slf.eventChannel =make(chan *Event,Default_EventChannelLen)
	}

	return slf.eventChannel
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

	mapCallBack,ok := slf.mapBindHandlerEvent[ev.Type]
	if ok == false {
		return
	}
	for _,callback := range mapCallBack {
		callback(ev)
	}
}




func (slf *EventProcessor) pushEvent(event *Event){
	if len(slf.eventChannel)>=cap(slf.eventChannel){
		log.Error("event process channel is full.")
		return
	}

	slf.eventChannel<-event
}

func (slf *EventProcessor) castEvent(event *Event){
	if slf.mapListenerEvent == nil{
		log.Error("mapListenerEvent not init!")
		return
	}

	processor,ok :=slf.mapListenerEvent[event.Type]
	if ok == false || processor == nil{
		log.Debug("event type %d not listen.",event.Type)
		return
	}

	for proc,_ := range processor {
		proc.pushEvent(event)
	}
}
