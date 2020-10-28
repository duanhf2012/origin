package service

import (
	"fmt"
	"github.com/duanhf2012/origin/event"
	"github.com/duanhf2012/origin/log"
	"github.com/duanhf2012/origin/profiler"
	"github.com/duanhf2012/origin/rpc"
	"github.com/duanhf2012/origin/util/timer"
	"github.com/duanhf2012/origin/util/timewheel"
	"reflect"
	"runtime"
	"sync"
	"sync/atomic"
)


var closeSig chan bool
var timerDispatcherLen = 10000

type IService interface {
	Init(iservice IService,getClientFun rpc.FuncRpcClient,getServerFun rpc.FuncRpcServer,serviceCfg interface{})
	SetName(serviceName string)
	GetName() string
	OnSetup(iservice IService)
	OnInit() error
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
	rpc.RpcHandler   //rpc
	name string    //service name
	wg      sync.WaitGroup
	serviceCfg interface{}
	gorouterNum int32
	startStatus bool
	eventProcessor event.IEventProcessor

	//eventProcessor event.EventProcessor //事件接收者
	profiler *profiler.Profiler //性能分析器
}

func (slf *Service) OnSetup(iservice IService){
	if iservice.GetName() == "" {
		slf.name = reflect.Indirect(reflect.ValueOf(iservice)).Type().Name()
	}
}

func (slf *Service) OpenProfiler()  {
	slf.profiler = profiler.RegProfiler(slf.GetName())
	if slf.profiler==nil {
		log.Fatal("rofiler.RegProfiler %s fail.",slf.GetName())
	}
}

func (slf *Service) Init(iservice IService,getClientFun rpc.FuncRpcClient,getServerFun rpc.FuncRpcServer,serviceCfg interface{}) {
	slf.dispatcher =timer.NewDispatcher(timerDispatcherLen)

	slf.InitRpcHandler(iservice.(rpc.IRpcHandler),getClientFun,getServerFun)
	slf.self = iservice.(IModule)
	//初始化祖先
	slf.ancestor = iservice.(IModule)
	slf.seedModuleId =InitModuleId
	slf.descendants = map[int64]IModule{}
	slf.serviceCfg = serviceCfg
	slf.gorouterNum = 1
	slf.eventProcessor = event.NewEventProcessor()
	slf.eventHandler =  event.NewEventHandler()
	slf.eventHandler.Init(slf.eventProcessor)
}

func (slf *Service) SetGoRouterNum(gorouterNum int32) bool {
	//已经开始状态不允许修改协程数量,打开性能分析器不允许开多线程
	if slf.startStatus == true || slf.profiler!=nil {
		log.Error("open profiler mode is not allowed to set Multi-coroutine.")
		return false
	}

	slf.gorouterNum = gorouterNum
	return true
}

func (slf *Service) Start() {
	slf.startStatus = true
	for i:=int32(0);i<slf.gorouterNum;i++{
		slf.wg.Add(1)
		go func(){
			slf.Run()
		}()
	}
}

func (slf *Service) Run() {
	log.Debug("Start running Service %s.",slf.GetName())
	defer slf.wg.Done()
	var bStop = false
	for{
		rpcRequestChan := slf.GetRpcRequestChan()
		rpcResponeCallBack := slf.GetRpcResponeChan()
		eventChan := slf.eventProcessor.GetEventChan()
		var analyzer *profiler.Analyzer
		select {
		case <- closeSig:
			bStop = true
		case rpcRequest :=<- rpcRequestChan:
			if slf.profiler!=nil {
				analyzer = slf.profiler.Push("Req_"+rpcRequest.RpcRequestData.GetServiceMethod())
			}

			slf.GetRpcHandler().HandlerRpcRequest(rpcRequest)
			if analyzer!=nil {
				analyzer.Pop()
				analyzer = nil
			}
		case rpcResponeCB := <- rpcResponeCallBack:
			if slf.profiler!=nil {
				analyzer = slf.profiler.Push("Res_" + rpcResponeCB.ServiceMethod)
			}
			slf.GetRpcHandler().HandlerRpcResponeCB(rpcResponeCB)
			if analyzer!=nil {
				analyzer.Pop()
				analyzer = nil
			}
		case ev := <- eventChan:
			if slf.profiler!=nil {
				analyzer = slf.profiler.Push(fmt.Sprintf("Event_%d", int(ev.Type)))
			}
			slf.eventProcessor.EventHandler(ev)
			if analyzer!=nil {
				analyzer.Pop()
				analyzer = nil
			}
		case t := <- slf.dispatcher.ChanTimer:
			if t.IsClose() == false {
				if slf.profiler != nil {
					analyzer = slf.profiler.Push(fmt.Sprintf("Timer_%s", t.AdditionData.(*timer.Timer).GetFunctionName()))
				}
				t.AdditionData.(*timer.Timer).Cb()
				if analyzer != nil {
					analyzer.Pop()
					analyzer = nil
				}
				timewheel.ReleaseTimer(t)
			}
		}

		if bStop == true {
			if atomic.AddInt32(&slf.gorouterNum,-1)<=0 {
				slf.startStatus = false
				slf.Release()
				slf.OnRelease()
			}
			break
		}
	}
}

func (slf *Service) GetName() string{
	return slf.name
}

func (slf *Service) SetName(serviceName string) {
	slf.name = serviceName
}


func (slf *Service) Release(){
	defer func() {
		if r := recover(); r != nil {
			buf := make([]byte, 4096)
			l := runtime.Stack(buf, false)
			err := fmt.Errorf("%v: %s", r, buf[:l])
			log.Error("core dump info:%+v\n",err)
		}
	}()
	slf.self.OnRelease()
	log.Debug("Release Service %s.",slf.GetName())
}

func (slf *Service) OnRelease(){
}

func (slf *Service) OnInit() error {
	return nil
}

func (slf *Service) Wait(){
	slf.wg.Wait()
}

func (slf *Service) GetServiceCfg()interface{}{
	return slf.serviceCfg
}

func (slf *Service) GetProfiler() *profiler.Profiler{
	return slf.profiler
}

func (slf *Service) RegEventReciverFunc(eventType event.EventType,reciver event.IEventHandler,callback event.EventCallBack){
	slf.eventProcessor.RegEventReciverFunc(eventType,reciver,callback)
}

func (slf *Service) UnRegEventReciverFun(eventType event.EventType,reciver event.IEventHandler){
	slf.eventProcessor.UnRegEventReciverFun(eventType,reciver)
}
func (slf *Service) IsSingleCoroutine() bool {
	return slf.gorouterNum == 1
}