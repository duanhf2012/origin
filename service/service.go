package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/duanhf2012/origin/v2/concurrent"
	"github.com/duanhf2012/origin/v2/event"
	"github.com/duanhf2012/origin/v2/log"
	"github.com/duanhf2012/origin/v2/profiler"
	"github.com/duanhf2012/origin/v2/rpc"
	"github.com/duanhf2012/origin/v2/util/timer"
	"reflect"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
)

var timerDispatcherLen = 100000
var maxServiceEventChannelNum = 2000000

type IService interface {
	concurrent.IConcurrent
	Init(iService IService, getClientFun rpc.FuncRpcClient, getServerFun rpc.FuncRpcServer, serviceCfg interface{})
	Stop()
	Start()

	OnSetup(iService IService)
	OnInit() error
	OnStart()
	OnRetire()
	OnRelease()

	SetName(serviceName string)
	GetName() string
	GetRpcHandler() rpc.IRpcHandler
	GetServiceCfg() interface{}
	GetProfiler() *profiler.Profiler
	GetServiceEventChannelNum() int
	GetServiceTimerChannelNum() int

	SetEventChannelNum(num int)
	OpenProfiler()

	SetRetire()     //设置服务退休状态
	IsRetire() bool //服务是否退休
}

type Service struct {
	Module

	rpcHandler             rpc.RpcHandler //rpc
	name                   string         //service name
	wg                     sync.WaitGroup
	serviceCfg             interface{}
	goroutineNum           int32
	startStatus            bool
	isRelease              int32
	retire                 int32
	eventProcessor         event.IEventProcessor
	profiler               *profiler.Profiler //性能分析器
	nodeConnLister         rpc.INodeConnListener
	natsConnListener       rpc.INatsConnListener
	discoveryServiceLister rpc.IDiscoveryServiceListener
	chanEvent              chan event.IEvent
	closeSig               chan struct{}
}

// DiscoveryServiceEvent 发现服务结点
type DiscoveryServiceEvent struct {
	IsDiscovery bool
	ServiceName []string
	NodeId      string
}

type EtcdServiceRecordEvent struct {
	NetworkName string
	TTLSecond   int64
	RecordKey   string
	RecordInfo  string
}

type Empty struct {
}

func SetMaxServiceChannel(maxEventChannel int) {
	maxServiceEventChannelNum = maxEventChannel
}

func (rpcEventData *DiscoveryServiceEvent) GetEventType() event.EventType {
	return event.Sys_Event_DiscoverService
}

func (s *Service) OnSetup(iService IService) {
	if iService.GetName() == "" {
		s.name = reflect.Indirect(reflect.ValueOf(iService)).Type().Name()
	}
}

func (s *Service) OpenProfiler() {
	s.profiler = profiler.RegProfiler(s.GetName())
	if s.profiler == nil {
		log.Fatal("profiler.RegProfiler " + s.GetName() + " fail.")
	}
}

func (s *Service) IsRetire() bool {
	return atomic.LoadInt32(&s.retire) != 0
}

func (s *Service) SetRetire() {
	atomic.StoreInt32(&s.retire, 1)

	ev := event.NewEvent()
	ev.Type = event.Sys_Event_Retire

	s.pushEvent(ev)
}

func (s *Service) Init(iService IService, getClientFun rpc.FuncRpcClient, getServerFun rpc.FuncRpcServer, serviceCfg interface{}) {
	s.closeSig = make(chan struct{})
	s.dispatcher = timer.NewDispatcher(timerDispatcherLen)
	if s.chanEvent == nil {
		s.chanEvent = make(chan event.IEvent, maxServiceEventChannelNum)
	}

	s.rpcHandler.InitRpcHandler(iService.(rpc.IRpcHandler), getClientFun, getServerFun, iService.(rpc.IRpcHandlerChannel))
	s.IRpcHandler = &s.rpcHandler
	s.self = iService.(IModule)
	//初始化祖先
	s.ancestor = iService.(IModule)
	s.seedModuleId = InitModuleId
	s.descendants = map[uint32]IModule{}
	s.serviceCfg = serviceCfg
	s.goroutineNum = 1
	s.eventProcessor = event.NewEventProcessor()
	s.eventProcessor.Init(s)
	s.eventHandler = event.NewEventHandler()
	s.eventHandler.Init(s.eventProcessor)
	s.Module.IConcurrent = &concurrent.Concurrent{}
}

func (s *Service) Start() {
	s.startStatus = true
	atomic.StoreInt32(&s.isRelease, 0)
	var waitRun sync.WaitGroup
	log.Info(s.GetName() + " service is running")
	s.self.(IService).OnStart()

	for i := int32(0); i < s.goroutineNum; i++ {
		s.wg.Add(1)
		waitRun.Add(1)
		go func() {
			waitRun.Done()
			s.run()
		}()
	}

	waitRun.Wait()
}

func (s *Service) run() {
	defer s.wg.Done()
	var bStop = false

	cr := s.IConcurrent.(*concurrent.Concurrent)
	concurrentCBChannel := cr.GetCallBackChannel()

	for {
		var analyzer *profiler.Analyzer
		select {
		case <-s.closeSig:
			bStop = true
			s.Release()
			cr.Close()
		case cb := <-concurrentCBChannel:
			cr.DoCallback(cb)
		case ev := <-s.chanEvent:
			switch ev.GetEventType() {
			case event.Sys_Event_Retire:
				log.Info("service OnRetire", log.String("serviceName", s.GetName()))
				s.self.(IService).OnRetire()
			case event.ServiceRpcRequestEvent:
				cEvent, ok := ev.(*event.Event)
				if ok == false {
					log.Error("Type event conversion error")
					break
				}
				rpcRequest, ok := cEvent.Data.(*rpc.RpcRequest)
				if ok == false {
					log.Error("Type *rpc.RpcRequest conversion error")
					break
				}
				if s.profiler != nil {
					analyzer = s.profiler.Push("[Req]" + rpcRequest.RpcRequestData.GetServiceMethod())
				}

				s.GetRpcHandler().HandlerRpcRequest(rpcRequest)
				if analyzer != nil {
					analyzer.Pop()
					analyzer = nil
				}
				event.DeleteEvent(cEvent)
			case event.ServiceRpcResponseEvent:
				cEvent, ok := ev.(*event.Event)
				if ok == false {
					log.Error("Type event conversion error")
					break
				}
				rpcResponseCB, ok := cEvent.Data.(*rpc.Call)
				if ok == false {
					log.Error("Type *rpc.Call conversion error")
					break
				}
				if s.profiler != nil {
					analyzer = s.profiler.Push("[Res]" + rpcResponseCB.ServiceMethod)
				}
				s.GetRpcHandler().HandlerRpcResponseCB(rpcResponseCB)
				if analyzer != nil {
					analyzer.Pop()
					analyzer = nil
				}
				event.DeleteEvent(cEvent)
			default:
				if s.profiler != nil {
					analyzer = s.profiler.Push("[SEvent]" + strconv.Itoa(int(ev.GetEventType())))
				}
				s.eventProcessor.EventHandler(ev)
				if analyzer != nil {
					analyzer.Pop()
					analyzer = nil
				}
			}

		case t := <-s.dispatcher.ChanTimer:
			if s.profiler != nil {
				analyzer = s.profiler.Push("[timer]" + t.GetName())
			}
			t.Do()
			if analyzer != nil {
				analyzer.Pop()
				analyzer = nil
			}
		}

		if bStop == true {
			break
		}
	}
}

func (s *Service) GetName() string {
	return s.name
}

func (s *Service) SetName(serviceName string) {
	s.name = serviceName
}

func (s *Service) Release() {
	defer func() {
		if r := recover(); r != nil {
			buf := make([]byte, 4096)
			l := runtime.Stack(buf, false)
			errString := fmt.Sprint(r)
			log.Dump(string(buf[:l]), log.String("error", errString))
		}
	}()

	if atomic.AddInt32(&s.isRelease, -1) == -1 {
		s.self.OnRelease()
	}

}

func (s *Service) OnRelease() {
}

func (s *Service) OnInit() error {
	return nil
}

func (s *Service) Stop() {
	log.Info("stop " + s.GetName() + " service ")
	close(s.closeSig)
	s.wg.Wait()
	log.Info(s.GetName() + " service has been stopped")
}

func (s *Service) GetServiceCfg() interface{} {
	return s.serviceCfg
}

func (s *Service) ParseServiceCfg(cfg interface{}) error {
	if s.serviceCfg == nil {
		return errors.New("no service configuration found")
	}

	rv := reflect.ValueOf(s.serviceCfg)
	if rv.Kind() == reflect.Ptr && rv.IsNil() {
		return errors.New("no service configuration found")
	}

	bytes, err := json.Marshal(s.serviceCfg)
	if err != nil {
		return err
	}

	return json.Unmarshal(bytes, cfg)
}

func (s *Service) GetProfiler() *profiler.Profiler {
	return s.profiler
}

func (s *Service) RegEventReceiverFunc(eventType event.EventType, receiver event.IEventHandler, callback event.EventCallBack) {
	s.eventProcessor.RegEventReceiverFunc(eventType, receiver, callback)
}

func (s *Service) UnRegEventReceiverFunc(eventType event.EventType, receiver event.IEventHandler) {
	s.eventProcessor.UnRegEventReceiverFun(eventType, receiver)
}

func (s *Service) RegRawRpc(rpcMethodId uint32, rawRpcCB rpc.RawRpcCallBack) {
	s.rpcHandler.RegRawRpc(rpcMethodId, rawRpcCB)
}

func (s *Service) OnStart() {
}

func (s *Service) OnNodeConnEvent(ev event.IEvent) {
	re := ev.(*rpc.RpcConnEvent)
	if re.IsConnect {
		s.nodeConnLister.OnNodeConnected(re.NodeId)
	} else {
		s.nodeConnLister.OnNodeDisconnect(re.NodeId)
	}
}

func (s *Service) OnNatsConnEvent(ev event.IEvent) {
	event := ev.(*rpc.NatsConnEvent)
	if event.IsConnect {
		s.natsConnListener.OnNatsConnected()
	} else {
		s.natsConnListener.OnNatsDisconnect()
	}
}

func (s *Service) OnDiscoverServiceEvent(ev event.IEvent) {
	de := ev.(*DiscoveryServiceEvent)
	if de.IsDiscovery {
		s.discoveryServiceLister.OnDiscoveryService(de.NodeId, de.ServiceName)
	} else {
		s.discoveryServiceLister.OnUnDiscoveryService(de.NodeId, de.ServiceName)
	}
}

func (s *Service) RegNodeConnListener(nodeConnListener rpc.INodeConnListener) {
	s.nodeConnLister = nodeConnListener
	s.RegEventReceiverFunc(event.Sys_Event_Node_Conn_Event, s.GetEventHandler(), s.OnNodeConnEvent)
	RegRpcEventFun(s.GetName())
}

func (s *Service) UnRegNodeConnListener() {
	s.UnRegEventReceiverFunc(event.Sys_Event_Node_Conn_Event, s.GetEventHandler())
	UnRegRpcEventFun(s.GetName())
}

func (s *Service) RegNatsConnListener(natsConnListener rpc.INatsConnListener) {
	s.natsConnListener = natsConnListener
	s.RegEventReceiverFunc(event.Sys_Event_Nats_Conn_Event, s.GetEventHandler(), s.OnNatsConnEvent)
	RegRpcEventFun(s.GetName())
}

func (s *Service) UnRegNatsConnListener() {
	s.UnRegEventReceiverFunc(event.Sys_Event_Nats_Conn_Event, s.GetEventHandler())
	UnRegRpcEventFun(s.GetName())
}

func (s *Service) RegDiscoverListener(discoveryServiceListener rpc.IDiscoveryServiceListener) {
	s.discoveryServiceLister = discoveryServiceListener
	s.RegEventReceiverFunc(event.Sys_Event_DiscoverService, s.GetEventHandler(), s.OnDiscoverServiceEvent)
	RegRpcEventFun(s.GetName())
}

func (s *Service) UnRegDiscoverListener() {
	s.UnRegEventReceiverFunc(event.Sys_Event_DiscoverService, s.GetEventHandler())
	UnRegRpcEventFun(s.GetName())
}

func (s *Service) PushRpcRequest(rpcRequest *rpc.RpcRequest) error {
	ev := event.NewEvent()
	ev.Type = event.ServiceRpcRequestEvent
	ev.Data = rpcRequest

	return s.pushEvent(ev)
}

func (s *Service) PushRpcResponse(call *rpc.Call) error {
	ev := event.NewEvent()
	ev.Type = event.ServiceRpcResponseEvent
	ev.Data = call

	return s.pushEvent(ev)
}

func (s *Service) PushEvent(ev event.IEvent) error {
	return s.pushEvent(ev)
}

func (s *Service) pushEvent(ev event.IEvent) error {
	if len(s.chanEvent) >= maxServiceEventChannelNum {
		err := errors.New("the event channel in the service is full")
		log.Error(err.Error())
		return err
	}

	s.chanEvent <- ev
	return nil
}

func (s *Service) GetServiceEventChannelNum() int {
	return len(s.chanEvent)
}

func (s *Service) GetServiceTimerChannelNum() int {
	return len(s.dispatcher.ChanTimer)
}

func (s *Service) SetEventChannelNum(num int) {
	if s.chanEvent == nil {
		s.chanEvent = make(chan event.IEvent, num)
	} else {
		panic("this stage cannot be set")
	}
}

func (s *Service) SetGoRoutineNum(goroutineNum int32) bool {
	//已经开始状态不允许修改协程数量,打开性能分析器不允许开多线程
	if s.startStatus == true || s.profiler != nil {
		log.Error("open profiler mode is not allowed to set Multi-coroutine.")
		return false
	}

	s.goroutineNum = goroutineNum
	return true
}

func (s *Service) OnRetire() {
}
