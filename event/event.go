package event

import (
	"fmt"
	"github.com/duanhf2012/origin/log"
	"runtime"
	"sync"
)

const DefaultEventChannelLen = 10000

//事件接受器
type EventCallBack func(event IEvent)

type IEvent interface {
	GetEventType() EventType
}

type Event struct {
	Type EventType
	Data interface{}
}

func (e *Event) GetEventType() EventType{
	return e.Type
}

type IEventHandler interface {
	Init(processor IEventProcessor)
	GetEventProcessor() IEventProcessor  //获得事件
	NotifyEvent(IEvent)
	Destroy()
	//注册了事件
	addRegInfo(eventType EventType,eventProcessor IEventProcessor)
	removeRegInfo(eventType EventType,eventProcessor IEventProcessor)
}

type IEventProcessor interface {
	EventHandler(ev IEvent)
	//同一个IEventHandler，只能接受一个EventType类型回调
	RegEventReciverFunc(eventType EventType,reciver IEventHandler,callback EventCallBack)
	UnRegEventReciverFun(eventType EventType,reciver IEventHandler)
	SetEventChannel(channelNum int) bool

	castEvent(event IEvent) //广播事件
	pushEvent(event IEvent)
	addBindEvent(eventType EventType,reciver IEventHandler,callback EventCallBack)
	addListen(eventType EventType,reciver IEventHandler)
	removeBindEvent(eventType EventType,reciver IEventHandler)
	removeListen(eventType EventType,reciver IEventHandler)
	GetEventChan() chan IEvent
}

type EventHandler struct {
	//已经注册的事件类型
	eventProcessor IEventProcessor

	//已经注册的事件
	locker sync.RWMutex
	mapRegEvent map[EventType]map[IEventProcessor]interface{}  //向其他事件处理器监听的事件类型
}


type EventProcessor struct {
	eventChannel chan IEvent

	locker sync.RWMutex
	mapListenerEvent map[EventType]map[IEventProcessor]int           //监听者信息
	mapBindHandlerEvent map[EventType]map[IEventHandler]EventCallBack//收到事件处理
}

func NewEventHandler() IEventHandler{
	eh := EventHandler{}
	eh.mapRegEvent = map[EventType]map[IEventProcessor]interface{}{}

	return &eh
}

func NewEventProcessor() IEventProcessor{
	ep := EventProcessor{}
	ep.mapListenerEvent  =  map[EventType]map[IEventProcessor]int{}
	ep.mapBindHandlerEvent = map[EventType]map[IEventHandler]EventCallBack{}

	return &ep
}

func (handler *EventHandler) addRegInfo(eventType EventType,eventProcessor IEventProcessor){
	handler.locker.Lock()
	defer handler.locker.Unlock()
	if handler.mapRegEvent == nil {
		handler.mapRegEvent = map[EventType]map[IEventProcessor]interface{}{}
	}

	if _,ok := handler.mapRegEvent[eventType] ;ok == false{
		handler.mapRegEvent[eventType] = map[IEventProcessor]interface{}{}
	}
	handler.mapRegEvent[eventType][eventProcessor] = nil
}

func (handler *EventHandler) removeRegInfo(eventType EventType,eventProcessor IEventProcessor){
	if _,ok := handler.mapRegEvent[eventType];ok == true {
		delete(handler.mapRegEvent[eventType],eventProcessor)
	}
}

func (handler *EventHandler) GetEventProcessor() IEventProcessor{
	return handler.eventProcessor
}

func (handler *EventHandler) NotifyEvent(ev IEvent){
	handler.GetEventProcessor().castEvent(ev)
}

func (handler *EventHandler) Init(processor IEventProcessor){
	handler.eventProcessor = processor
	handler.mapRegEvent =map[EventType]map[IEventProcessor]interface{}{}
}

func (processor *EventProcessor) SetEventChannel(channelNum int) bool{
	processor.locker.Lock()
	defer processor.locker.Unlock()
	if processor.eventChannel!=nil {
		return false
	}

	if channelNum == 0 {
		channelNum = DefaultEventChannelLen
	}

	processor.eventChannel = make(chan IEvent,channelNum)
	return true
}

func (processor *EventProcessor) addBindEvent(eventType EventType,reciver IEventHandler,callback EventCallBack){
	processor.locker.Lock()
	defer processor.locker.Unlock()

	if _,ok := processor.mapBindHandlerEvent[eventType]; ok == false {
		processor.mapBindHandlerEvent[eventType] = map[IEventHandler]EventCallBack{}
	}

	processor.mapBindHandlerEvent[eventType][reciver] = callback
}

func (processor *EventProcessor) addListen(eventType EventType,reciver IEventHandler){
	processor.locker.Lock()
	defer processor.locker.Unlock()

	if _,ok := processor.mapListenerEvent[eventType];ok == false{
		processor.mapListenerEvent[eventType] = map[IEventProcessor]int{}
	}

	processor.mapListenerEvent[eventType][reciver.GetEventProcessor()] += 1
}

func (processor *EventProcessor) removeBindEvent(eventType EventType,reciver IEventHandler){
	processor.locker.Lock()
	defer processor.locker.Unlock()
	if _,ok := processor.mapBindHandlerEvent[eventType];ok == true{
		delete(processor.mapBindHandlerEvent[eventType],reciver)
	}
}

func (processor *EventProcessor) removeListen(eventType EventType,reciver IEventHandler){
	processor.locker.Lock()
	defer processor.locker.Unlock()
	if _,ok := processor.mapListenerEvent[eventType];ok == true{
		processor.mapListenerEvent[eventType][reciver.GetEventProcessor()]-=1
		if processor.mapListenerEvent[eventType][reciver.GetEventProcessor()] <= 0 {
			delete(processor.mapListenerEvent[eventType],reciver.GetEventProcessor())
		}
	}
}

func (processor *EventProcessor) RegEventReciverFunc(eventType EventType,reciver IEventHandler,callback EventCallBack){
	//记录reciver自己注册过的事件
	reciver.addRegInfo(eventType, processor)
	//记录当前所属IEventProcessor注册的回调
	reciver.GetEventProcessor().addBindEvent(eventType,reciver,callback)
	//将注册加入到监听中
	processor.addListen(eventType,reciver)
}

func (processor *EventProcessor) UnRegEventReciverFun(eventType EventType,reciver IEventHandler) {
	processor.removeListen(eventType,reciver)
	reciver.GetEventProcessor().removeBindEvent(eventType,reciver)
	reciver.removeRegInfo(eventType, processor)
}

func (handler *EventHandler) Destroy(){
	handler.locker.Lock()
	defer handler.locker.Unlock()
	for eventTyp,mapEventProcess := range handler.mapRegEvent {
		if mapEventProcess == nil {
			continue
		}

		for eventProcess,_ := range mapEventProcess {
			eventProcess.UnRegEventReciverFun(eventTyp, handler)
		}
	}
}

func (processor *EventProcessor) GetEventChan() chan IEvent{
	processor.locker.Lock()
	defer processor.locker.Unlock()

	if processor.eventChannel == nil {
		processor.eventChannel =make(chan IEvent,DefaultEventChannelLen)
	}

	return processor.eventChannel
}

func (processor *EventProcessor) EventHandler(ev IEvent) {
	defer func() {
		if r := recover(); r != nil {
			buf := make([]byte, 4096)
			l := runtime.Stack(buf, false)
			err := fmt.Errorf("%v: %s", r, buf[:l])
			log.Error("core dump info:%+v\n",err)
		}
	}()

	mapCallBack,ok := processor.mapBindHandlerEvent[ev.GetEventType()]
	if ok == false {
		return
	}
	for _,callback := range mapCallBack {
		callback(ev)
	}
}

func (processor *EventProcessor) pushEvent(event IEvent){
	if len(processor.eventChannel)>=cap(processor.eventChannel){
		log.Error("event process channel is full.")
		return
	}

	processor.eventChannel<-event
}

func (processor *EventProcessor) castEvent(event IEvent){
	if processor.mapListenerEvent == nil {
		log.Error("mapListenerEvent not init!")
		return
	}

	eventProcessor,ok := processor.mapListenerEvent[event.GetEventType()]
	if ok == false || processor == nil{
		log.Debug("event type %d not listen.",event.GetEventType())
		return
	}

	for proc,_ := range eventProcessor {
		proc.pushEvent(event)
	}
}
