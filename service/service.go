package service

import (
	"errors"
	"fmt"
	"github.com/duanhf2012/origin/event"
	"github.com/duanhf2012/origin/log"
	"github.com/duanhf2012/origin/profiler"
	"github.com/duanhf2012/origin/rpc"
	originSync "github.com/duanhf2012/origin/util/sync"
	"github.com/duanhf2012/origin/util/timer"
	"reflect"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
)


var closeSig chan bool
var timerDispatcherLen = 100000

type IService interface {
	Init(iService IService,getClientFun rpc.FuncRpcClient,getServerFun rpc.FuncRpcServer,serviceCfg interface{})
	SetName(serviceName string)
	GetName() string
	OnSetup(iService IService)
	OnInit() error
	OnStart()
	OnRelease()
	Wait()
	Start()
	GetRpcHandler() rpc.IRpcHandler
	GetServiceCfg()interface{}
	OpenProfiler()
	GetProfiler() *profiler.Profiler
}

// eventPool的内存池,缓存Event
var maxServiceEventChannel = 2000000
var eventPool = originSync.NewPoolEx(make(chan originSync.IPoolData, maxServiceEventChannel), func() originSync.IPoolData {
	return &event.Event{}
})

type Service struct {
	Module
	rpcHandler rpc.RpcHandler           //rpc
	name           string    //service name
	wg             sync.WaitGroup
	serviceCfg     interface{}
	goroutineNum   int32
	startStatus    bool
	eventProcessor event.IEventProcessor
	profiler *profiler.Profiler //性能分析器
	rpcEventLister rpc.IRpcListener
	chanEvent chan event.IEvent
}

// RpcConnEvent Node结点连接事件
type RpcConnEvent struct{
	IsConnect bool
	NodeId int
}

func SetMaxServiceChannel(maxEventChannel int){
	maxServiceEventChannel = maxEventChannel
	eventPool = originSync.NewPoolEx(make(chan originSync.IPoolData, maxServiceEventChannel), func() originSync.IPoolData {
		return &event.Event{}
	})
}

func (rpcEventData *RpcConnEvent) GetEventType() event.EventType{
	return event.Sys_Event_Rpc_Event
}

func (s *Service) OnSetup(iService IService){
	if iService.GetName() == "" {
		s.name = reflect.Indirect(reflect.ValueOf(iService)).Type().Name()
	}
}

func (s *Service) OpenProfiler()  {
	s.profiler = profiler.RegProfiler(s.GetName())
	if s.profiler==nil {
		log.SFatal("rofiler.RegProfiler ",s.GetName()," fail.")
	}
}

func (s *Service) Init(iService IService,getClientFun rpc.FuncRpcClient,getServerFun rpc.FuncRpcServer,serviceCfg interface{}) {
	s.dispatcher =timer.NewDispatcher(timerDispatcherLen)
	s.chanEvent = make(chan event.IEvent,maxServiceEventChannel)
	s.rpcHandler.InitRpcHandler(iService.(rpc.IRpcHandler),getClientFun,getServerFun,iService.(rpc.IRpcHandlerChannel))
	s.IRpcHandler = &s.rpcHandler
	s.self = iService.(IModule)
	//初始化祖先
	s.ancestor = iService.(IModule)
	s.seedModuleId =InitModuleId
	s.descendants = map[uint32]IModule{}
	s.serviceCfg = serviceCfg
	s.goroutineNum = 1
	s.eventProcessor = event.NewEventProcessor()
	s.eventProcessor.Init(s)
	s.eventHandler =  event.NewEventHandler()
	s.eventHandler.Init(s.eventProcessor)
}


func (s *Service) Start() {
	s.startStatus = true
	for i:=int32(0);i< s.goroutineNum;i++{
		s.wg.Add(1)
		go func(){
			s.Run()
		}()
	}
}

func (s *Service) Run() {
	log.SDebug("Start running Service ", s.GetName())
	defer s.wg.Done()
	var bStop = false
	s.self.(IService).OnStart()
	for{
		var analyzer *profiler.Analyzer
		select {
		case <- closeSig:
			bStop = true
		case ev := <- s.chanEvent:
			switch ev.GetEventType() {
			case event.ServiceRpcRequestEvent:
				cEvent,ok := ev.(*event.Event)
				if ok == false {
					log.SError("Type event conversion error")
					break
				}
				rpcRequest,ok := cEvent.Data.(*rpc.RpcRequest)
				if ok == false {
					log.SError("Type *rpc.RpcRequest conversion error")
					break
				}
				if s.profiler!=nil {
					analyzer = s.profiler.Push("[Req]"+rpcRequest.RpcRequestData.GetServiceMethod())
				}

				s.GetRpcHandler().HandlerRpcRequest(rpcRequest)
				if analyzer!=nil {
					analyzer.Pop()
					analyzer = nil
				}
				eventPool.Put(cEvent)
			case event.ServiceRpcResponseEvent:
				cEvent,ok := ev.(*event.Event)
				if ok == false {
					log.SError("Type event conversion error")
					break
				}
				rpcResponseCB,ok := cEvent.Data.(*rpc.Call)
				if ok == false {
					log.SError("Type *rpc.Call conversion error")
					break
				}
				if s.profiler!=nil {
					analyzer = s.profiler.Push("[Res]" + rpcResponseCB.ServiceMethod)
				}
				s.GetRpcHandler().HandlerRpcResponseCB(rpcResponseCB)
				if analyzer!=nil {
					analyzer.Pop()
					analyzer = nil
				}
				eventPool.Put(cEvent)
			default:
				if s.profiler!=nil {
					analyzer = s.profiler.Push("[SEvent]"+strconv.Itoa(int(ev.GetEventType())))
				}
				s.eventProcessor.EventHandler(ev)
				if analyzer!=nil {
					analyzer.Pop()
					analyzer = nil
				}
			}

		case t := <- s.dispatcher.ChanTimer:
			if s.profiler != nil {
				analyzer = s.profiler.Push("[timer]"+t.GetName())
			}
			t.Do()
			if analyzer != nil {
				analyzer.Pop()
				analyzer = nil
			}
		}

		if bStop == true {
			if atomic.AddInt32(&s.goroutineNum,-1)<=0 {
				s.startStatus = false
				s.Release()
				s.OnRelease()
			}
			break
		}
	}
}

func (s *Service) GetName() string{
	return s.name
}

func (s *Service) SetName(serviceName string) {
	s.name = serviceName
}

func (s *Service) Release(){
	defer func() {
		if r := recover(); r != nil {
			buf := make([]byte, 4096)
			l := runtime.Stack(buf, false)
			errString := fmt.Sprint(r)
			log.SError("core dump info[",errString,"]\n",string(buf[:l]))
		}
	}()
	s.self.OnRelease()
	log.SDebug("Release Service ", s.GetName())
}

func (s *Service) OnRelease(){
}

func (s *Service) OnInit() error {
	return nil
}

func (s *Service) Wait(){
	s.wg.Wait()
}

func (s *Service) GetServiceCfg()interface{}{
	return s.serviceCfg
}

func (s *Service) GetProfiler() *profiler.Profiler{
	return s.profiler
}

func (s *Service) RegEventReceiverFunc(eventType event.EventType, receiver event.IEventHandler,callback event.EventCallBack){
	s.eventProcessor.RegEventReceiverFunc(eventType, receiver,callback)
}

func (s *Service) UnRegEventReceiverFunc(eventType event.EventType, receiver event.IEventHandler){
	s.eventProcessor.UnRegEventReceiverFun(eventType, receiver)
}

func (s *Service) IsSingleCoroutine() bool {
	return s.goroutineNum == 1
}

func (s *Service) RegRawRpc(rpcMethodId uint32,rawRpcCB rpc.RawRpcCallBack){
	s.rpcHandler.RegRawRpc(rpcMethodId,rawRpcCB)
}

func (s *Service) OnStart(){
}

func (s *Service) OnRpcEvent(ev event.IEvent){
	event := ev.(*RpcConnEvent)
	if event.IsConnect {
		s.rpcEventLister.OnNodeConnected(event.NodeId)
	}else{
		s.rpcEventLister.OnNodeDisconnect(event.NodeId)
	}
}

func (s *Service) RegRpcListener(rpcEventLister rpc.IRpcListener) {
	s.rpcEventLister = rpcEventLister
	s.RegEventReceiverFunc(event.Sys_Event_Rpc_Event,s.GetEventHandler(),s.OnRpcEvent)
	RegRpcEventFun(s.GetName())
}

func (s *Service) UnRegRpcListener(rpcLister rpc.IRpcListener) {
	s.UnRegEventReceiverFunc(event.Sys_Event_Rpc_Event,s.GetEventHandler())
	RegRpcEventFun(s.GetName())
}


func (s *Service) PushRpcRequest(rpcRequest *rpc.RpcRequest) error{
	ev := eventPool.Get().(*event.Event)
	ev.Type = event.ServiceRpcRequestEvent
	ev.Data = rpcRequest

	return s.pushEvent(ev)
}

func (s *Service) PushRpcResponse(call *rpc.Call) error{
	ev := eventPool.Get().(*event.Event)
	ev.Type = event.ServiceRpcResponseEvent
	ev.Data = call

	return s.pushEvent(ev)
}

func (s *Service) PushEvent(ev event.IEvent) error{
	return s.pushEvent(ev)
}

func (s *Service) pushEvent(ev event.IEvent) error{
	if len(s.chanEvent) >= maxServiceEventChannel {
		err := errors.New("The event channel in the service is full")
		log.SError(err.Error())
		return err
	}

	s.chanEvent <- ev
	return nil
}


func (s *Service) SetGoRoutineNum(goroutineNum int32) bool {
	//已经开始状态不允许修改协程数量,打开性能分析器不允许开多线程
	if s.startStatus == true || s.profiler!=nil {
		log.SError("open profiler mode is not allowed to set Multi-coroutine.")
		return false
	}

	s.goroutineNum = goroutineNum
	return true
}
