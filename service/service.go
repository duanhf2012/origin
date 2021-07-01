package service

import (
	"fmt"
	"github.com/duanhf2012/origin/event"
	"github.com/duanhf2012/origin/log"
	"github.com/duanhf2012/origin/profiler"
	"github.com/duanhf2012/origin/rpc"
	"github.com/duanhf2012/origin/util/timer"
	"reflect"
	"runtime"
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
}

type RpcEventData struct{
	IsConnect bool
	NodeId int
}

func (rpcEventData *RpcEventData) GetEventType() event.EventType{
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

	s.rpcHandler.InitRpcHandler(iService.(rpc.IRpcHandler),getClientFun,getServerFun)
	s.IRpcHandler = &s.rpcHandler
	s.self = iService.(IModule)
	//初始化祖先
	s.ancestor = iService.(IModule)
	s.seedModuleId =InitModuleId
	s.descendants = map[int64]IModule{}
	s.serviceCfg = serviceCfg
	s.goroutineNum = 1
	s.eventProcessor = event.NewEventProcessor()
	s.eventHandler =  event.NewEventHandler()
	s.eventHandler.Init(s.eventProcessor)
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

func (s *Service) Start() {
	s.startStatus = true
	s.eventProcessor.SetEventChannel(0)
	
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
		rpcRequestChan := s.GetRpcRequestChan()
		rpcResponseCallBack := s.GetRpcResponseChan()
		eventChan := s.eventProcessor.GetEventChan()
		var analyzer *profiler.Analyzer
		select {
		case <- closeSig:
			bStop = true
		case rpcRequest :=<- rpcRequestChan:
			if s.profiler!=nil {
				analyzer = s.profiler.Push("[Req]"+rpcRequest.RpcRequestData.GetServiceMethod())
			}

			s.GetRpcHandler().HandlerRpcRequest(rpcRequest)
			if analyzer!=nil {
				analyzer.Pop()
				analyzer = nil
			}
		case rpcResponseCB := <-rpcResponseCallBack:
			if s.profiler!=nil {
				analyzer = s.profiler.Push("[Res]" + rpcResponseCB.ServiceMethod)
			}
			s.GetRpcHandler().HandlerRpcResponseCB(rpcResponseCB)
			if analyzer!=nil {
				analyzer.Pop()
				analyzer = nil
			}
		case ev := <- eventChan:
			if s.profiler!=nil {
				analyzer = s.profiler.Push(fmt.Sprintf("[Event]%d", int(ev.GetEventType())))
			}
			s.eventProcessor.EventHandler(ev)
			if analyzer!=nil {
				analyzer.Pop()
				analyzer = nil
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
			log.SError("core dump info:",r," : ",string(buf[:l]))
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
	s.eventProcessor.RegEventReciverFunc(eventType, receiver,callback)
}

func (s *Service) UnRegEventReceiverFunc(eventType event.EventType, receiver event.IEventHandler){
	s.eventProcessor.UnRegEventReciverFun(eventType, receiver)
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
	event := ev.(*RpcEventData)
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

