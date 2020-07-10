package rpc

import (
	"fmt"
	"github.com/duanhf2012/origin/log"
	"reflect"
	"runtime"
	"strings"
	"unicode"
	"unicode/utf8"
)

type FuncRpcClient func(nodeid int,serviceMethod string) ([]*Client,error)
type FuncRpcServer func() (*Server)
var NilError = reflect.Zero(reflect.TypeOf((*error)(nil)).Elem())

type RpcError string

func (e *RpcError) Error() string {
	if e == nil {
		return ""
	}

	return string(*e)
}

func ConvertError(e error) *RpcError{
	if e == nil {
		return nil
	}

	rpcErr := RpcError(e.Error())
	return &rpcErr
}

func Errorf(format string, a ...interface{}) *RpcError {
	rpcErr := RpcError(fmt.Sprintf(format,a...))
	return &rpcErr
}


type RpcMethodInfo struct {
	method reflect.Method
	iparam reflect.Value
	oParam reflect.Value
}

type RpcHandler struct {
	callRequest chan *RpcRequest
	rpcHandler IRpcHandler
	mapfunctons map[string]RpcMethodInfo
	funcRpcClient FuncRpcClient
	funcRpcServer FuncRpcServer

	callResponeCallBack chan *Call //异步返回的回调
}

type IRpcHandler interface {
	GetName() string
	InitRpcHandler(rpcHandler IRpcHandler,getClientFun FuncRpcClient,getServerFun FuncRpcServer)
	GetRpcHandler() IRpcHandler
	PushRequest(callinfo *RpcRequest) error
	HandlerRpcRequest(request *RpcRequest)
	HandlerRpcResponeCB(call *Call)

	GetRpcRequestChan() chan *RpcRequest
	GetRpcResponeChan() chan *Call
	CallMethod(ServiceMethod string,param interface{},reply interface{}) error
	
	AsyncCall(serviceMethod string,args interface{},callback interface{}) error
	Call(serviceMethod string,args interface{},reply interface{}) error
	Go(serviceMethod string,args interface{}) error
	AsyncCallNode(nodeId int,serviceMethod string,args interface{},callback interface{}) error
	CallNode(nodeId int,serviceMethod string,args interface{},reply interface{}) error
	GoNode(nodeId int,serviceMethod string,args interface{}) error
}

func (slf *RpcHandler) GetRpcHandler() IRpcHandler{
	return slf.rpcHandler
}

func (slf *RpcHandler) InitRpcHandler(rpcHandler IRpcHandler,getClientFun FuncRpcClient,getServerFun FuncRpcServer) {
	slf.callRequest = make(chan *RpcRequest,100000)
	slf.callResponeCallBack = make(chan *Call,100000)

	slf.rpcHandler = rpcHandler
	slf.mapfunctons = map[string]RpcMethodInfo{}
	slf.funcRpcClient = getClientFun
	slf.funcRpcServer = getServerFun

	slf.RegisterRpc(rpcHandler)
}

// Is this an exported - upper case - name?
func isExported(name string) bool {
	rune, _ := utf8.DecodeRuneInString(name)
	return unicode.IsUpper(rune)
}

// Is this type exported or a builtin?
func (slf *RpcHandler) isExportedOrBuiltinType(t reflect.Type) bool {
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	// PkgPath will be non-empty even for an exported type,
	// so we need to check the type name as well.
	return isExported(t.Name()) || t.PkgPath() == ""
}


func (slf *RpcHandler) suitableMethods(method reflect.Method) error {
	//只有RPC_开头的才能被调用
	if strings.Index(method.Name,"RPC_")!=0 {
		return nil
	}

	//取出输入参数类型
	var rpcMethodInfo RpcMethodInfo
	typ := method.Type
	if typ.NumOut() != 1 {
		return fmt.Errorf("%s The number of returned arguments must be 1!",method.Name)
	}

	if typ.Out(0).String() != "error" {
		return fmt.Errorf("%s The return parameter must be of type error!",method.Name)
	}

	if typ.NumIn() != 3 {
		return fmt.Errorf("%s The number of input arguments must be 1!",method.Name)
	}

	if slf.isExportedOrBuiltinType(typ.In(1)) == false ||   slf.isExportedOrBuiltinType(typ.In(2)) == false {
		return fmt.Errorf("%s Unsupported parameter types!",method.Name)
	}

	rpcMethodInfo.iparam = reflect.New(typ.In(1).Elem()) //append(rpcMethodInfo.iparam,)
	rpcMethodInfo.oParam = reflect.New(typ.In(2).Elem())

	rpcMethodInfo.method = method
	slf.mapfunctons[slf.rpcHandler.GetName()+"."+method.Name] = rpcMethodInfo
	return nil
}

func  (slf *RpcHandler) RegisterRpc(rpcHandler IRpcHandler) error {
	typ := reflect.TypeOf(rpcHandler)
	for m:=0;m<typ.NumMethod();m++{
		method := typ.Method(m)
		err := slf.suitableMethods(method)
		if err != nil {
			panic(err)
		}
	}

	return nil
}

func (slf *RpcHandler) PushRequest(req *RpcRequest) error{
	if len(slf.callRequest) >= cap(slf.callRequest){
		return fmt.Errorf("RpcHandler %s Rpc Channel is full.",slf.GetName())
	}

	slf.callRequest <- req
	return nil
}

func (slf *RpcHandler) GetRpcRequestChan() (chan *RpcRequest) {
	return slf.callRequest
}

func (slf *RpcHandler) GetRpcResponeChan() chan *Call{
	return slf.callResponeCallBack
}

func (slf *RpcHandler) HandlerRpcResponeCB(call *Call){
	defer func() {
		if r := recover(); r != nil {
			buf := make([]byte, 4096)
			l := runtime.Stack(buf, false)
			err := fmt.Errorf("%v: %s\n", r, buf[:l])
			log.Error("core dump info:%+v",err)
		}
	}()

	if call.Err == nil {
		call.callback.Call([]reflect.Value{reflect.ValueOf(call.Reply),NilError})
	}else{
		call.callback.Call([]reflect.Value{reflect.ValueOf(call.Reply),reflect.ValueOf(call.Err)})
	}
	ReleaseCall(call)
}


func (slf *RpcHandler) HandlerRpcRequest(request *RpcRequest) {
	defer func() {
		if r := recover(); r != nil {
				buf := make([]byte, 4096)
				l := runtime.Stack(buf, false)
				err := fmt.Errorf("%v: %s", r, buf[:l])
				log.Error("Handler Rpc %s Core dump info:%+v\n",request.RpcRequestData.GetServiceMethod(),err)
				rpcErr := RpcError("call error : core dumps")
				if request.requestHandle!=nil {
					request.requestHandle(nil,&rpcErr)
				}
		}
	}()
	defer ReleaseRpcRequest(request)
	defer processor.ReleaseRpcRequest(request.RpcRequestData)

	v,ok := slf.mapfunctons[request.RpcRequestData.GetServiceMethod()]
	if ok == false {
		err := Errorf("RpcHandler %s cannot find %s",slf.rpcHandler.GetName(),request.RpcRequestData.GetServiceMethod())
		log.Error("%s",err.Error())
		if request.requestHandle!=nil {
			request.requestHandle(nil,err)
		}

		return
	}


	var paramList []reflect.Value
	var err error
	iparam := reflect.New(v.iparam.Type().Elem()).Interface()

	if request.localParam==nil{
		err = processor.Unmarshal(request.RpcRequestData.GetInParam(),iparam)
		if err!=nil {
			rerr := Errorf("Call Rpc %s Param error %+v",request.RpcRequestData.GetServiceMethod(),err)
			log.Error("%s",rerr.Error())
			if request.requestHandle!=nil {
				request.requestHandle(nil, rerr)
			}
		}
	}else {
		iparam = request.localParam
	}

	var oParam reflect.Value
	paramList = append(paramList,reflect.ValueOf(slf.GetRpcHandler())) //接受者
	oParam = reflect.New(v.oParam.Type().Elem())


	paramList = append(paramList,reflect.ValueOf(iparam))
	paramList = append(paramList,oParam) //输出参数

	returnValues := v.method.Func.Call(paramList)
	errInter := returnValues[0].Interface()
	if errInter != nil {
		err = errInter.(error)
	}

	if request.requestHandle!=nil {
		request.requestHandle(oParam.Interface(), ConvertError(err))
	}
}

func (slf *RpcHandler) CallMethod(ServiceMethod string,param interface{},reply interface{}) error{
	var err error
	v,ok := slf.mapfunctons[ServiceMethod]
	if ok == false {
		err = fmt.Errorf("RpcHandler %s cannot find %s",slf.rpcHandler.GetName(),ServiceMethod)
		log.Error("%s",err.Error())

		return err
	}

	var paramList []reflect.Value
	paramList = append(paramList,reflect.ValueOf(slf.GetRpcHandler())) //接受者
	paramList = append(paramList,reflect.ValueOf(param))
	paramList = append(paramList,reflect.ValueOf(reply)) //输出参数

	returnValues := v.method.Func.Call(paramList)
	errInter := returnValues[0].Interface()
	if errInter != nil {
		err = errInter.(error)
	}

	return err
}

func (slf *RpcHandler) goRpc(bCast bool,nodeId int,serviceMethod string,args interface{}) error {
	pClientList,err := slf.funcRpcClient(nodeId,serviceMethod)
	if err != nil {
		log.Error("Call serviceMethod is error:%+v!",err)
		return err
	}
	if len(pClientList) > 1 && bCast == false{
		log.Error("Cannot call more then 1 node!")
		return fmt.Errorf("Cannot call more then 1 node!")
	}

	//2.rpcclient调用
	//如果调用本结点服务
	for _,pClient := range pClientList {
		if pClient.blocalhost == true {
			pLocalRpcServer:=slf.funcRpcServer()
			//判断是否是同一服务
			sMethod := strings.Split(serviceMethod,".")
			if len(sMethod)!=2 {
				serr := fmt.Errorf("Call serviceMethod %s is error!",serviceMethod)
				log.Error("%+v",serr)
				if serr!= nil {
					err = serr
				}
				continue
			}
			//调用自己rpcHandler处理器
			if sMethod[0] == slf.rpcHandler.GetName() { //自己服务调用
				//
				return pLocalRpcServer.myselfRpcHandlerGo(sMethod[0],sMethod[1],args,nil)
			}
			//其他的rpcHandler的处理器
			pCall := pLocalRpcServer.rpcHandlerGo(true,sMethod[0],sMethod[1],args,nil)
			defer ReleaseCall(pCall)
			if pCall.Err!=nil {
				err = pCall.Err
			}
			continue
		}

		//跨node调用
		pCall := pClient.Go(false,serviceMethod,args,nil)
		if pCall.Err!=nil {
			err = pCall.Err
		}
		ReleaseCall(pCall)
	}

	return err
}

func (slf *RpcHandler) callRpc(nodeId int,serviceMethod string,args interface{},reply interface{}) error {
	pClientList,err := slf.funcRpcClient(nodeId,serviceMethod)
	if err != nil {
		log.Error("Call serviceMethod is error:%+v!",err)
		return err
	}
	if len(pClientList) > 1 {
		log.Error("Cannot call more then 1 node!")
		return fmt.Errorf("Cannot call more then 1 node!")
	}

	//2.rpcclient调用
	//如果调用本结点服务
	pClient := pClientList[0]
	if pClient.blocalhost == true {
		pLocalRpcServer:=slf.funcRpcServer()
		//判断是否是同一服务
		sMethod := strings.Split(serviceMethod,".")
		if len(sMethod)!=2 {
			err := fmt.Errorf("Call serviceMethod %s is error!",serviceMethod)
			log.Error("%+v",err)
			return err
		}
		//调用自己rpcHandler处理器
		if sMethod[0] == slf.rpcHandler.GetName() { //自己服务调用
			//
			return pLocalRpcServer.myselfRpcHandlerGo(sMethod[0],sMethod[1],args,reply)
		}
		//其他的rpcHandler的处理器
		pCall := pLocalRpcServer.rpcHandlerGo(false,sMethod[0],sMethod[1],args,reply)
		err = pCall.Done().Err
		ReleaseCall(pCall)
		return err
	}

	//跨node调用
	pCall := pClient.Go(false,serviceMethod,args,reply)
	if pCall.Err != nil {
		return pCall.Err
	}
	err = pCall.Done().Err
	ReleaseCall(pCall)
	return err
}

func (slf *RpcHandler) asyncCallRpc(nodeid int,serviceMethod string,args interface{},callback interface{}) error {
	fVal := reflect.ValueOf(callback)
	if fVal.Kind()!=reflect.Func{
		err := fmt.Errorf("call %s input callback param is error!",serviceMethod)
		log.Error("+v",err)
		return err
	}

    if fVal.Type().NumIn()!= 2 {
    	err := fmt.Errorf("call %s callback param function is error!",serviceMethod)
		log.Error("%+v",err)
		return err
	}

	if  fVal.Type().In(0).Kind() != reflect.Ptr || fVal.Type().In(1).String() != "error"{
		err :=  fmt.Errorf("call %s callback  function param is error!",serviceMethod)
		log.Error("%+v",err)
		return err
	}

	reply := reflect.New(fVal.Type().In(0).Elem()).Interface()
	pClientList,err := slf.funcRpcClient(nodeid,serviceMethod)
	if err != nil {
		fVal.Call([]reflect.Value{reflect.ValueOf(reply),reflect.ValueOf(err)})
		log.Error("Call serviceMethod is error:%+v!",err)
		return nil
	}

	if len(pClientList) > 1 {
		err := fmt.Errorf("Cannot call more then 1 node!")
		fVal.Call([]reflect.Value{reflect.ValueOf(reply),reflect.ValueOf(err)})
		log.Error("Cannot call more then 1 node!")
		return nil
	}

	//2.rpcclient调用
	//如果调用本结点服务
	pClient := pClientList[0]
	if pClient.blocalhost == true {
		pLocalRpcServer:=slf.funcRpcServer()
		//判断是否是同一服务
		sMethod := strings.Split(serviceMethod,".")
		if len(sMethod)!=2 {
			err := fmt.Errorf("Call serviceMethod %s is error!",serviceMethod)
			fVal.Call([]reflect.Value{reflect.ValueOf(reply),reflect.ValueOf(err)})
			log.Error("%+v",err)
			return nil
		}
		//调用自己rpcHandler处理器
		if sMethod[0] == slf.rpcHandler.GetName() { //自己服务调用
			err := pLocalRpcServer.myselfRpcHandlerGo(sMethod[0],sMethod[1],args,reply)
			if err == nil {
				fVal.Call([]reflect.Value{reflect.ValueOf(reply),NilError})
			}else{
				fVal.Call([]reflect.Value{reflect.ValueOf(reply),reflect.ValueOf(err)})
			}

		}

		//其他的rpcHandler的处理器
		if callback!=nil {
			err =  pLocalRpcServer.rpcHandlerAsyncGo(slf,false,sMethod[0],sMethod[1],args,reply,fVal)
			if err != nil {
				fVal.Call([]reflect.Value{reflect.ValueOf(reply),reflect.ValueOf(err)})
			}
			return nil
		}
		pCall := pLocalRpcServer.rpcHandlerGo(false,sMethod[0],sMethod[1],args,reply)
		defer ReleaseCall(pCall)

		pResult := pCall.Done()
		return pResult.Err
	}

	//跨node调用
	err =  pClient.AsycGo(slf,serviceMethod,fVal,args,reply)
	if err != nil {
		fVal.Call([]reflect.Value{reflect.ValueOf(reply),reflect.ValueOf(err)})
	}
	return nil
}

func (slf *RpcHandler) GetName() string{
	return slf.rpcHandler.GetName()
}


//func (slf *RpcHandler) asyncCallRpc(serviceMethod string,mutiCoroutine bool,callback interface{},args ...interface{}) error {
//func (slf *RpcHandler) callRpc(serviceMethod string,reply interface{},mutiCoroutine bool,args ...interface{}) error {
//func (slf *RpcHandler) goRpc(serviceMethod string,mutiCoroutine bool,args ...interface{}) error {
//(reply *int,err error) {}
func (slf *RpcHandler) AsyncCall(serviceMethod string,args interface{},callback interface{}) error {
	return slf.asyncCallRpc(0,serviceMethod,args,callback)
}

func (slf *RpcHandler) Call(serviceMethod string,args interface{},reply interface{}) error {
	return slf.callRpc(0,serviceMethod,args,reply)
}


func (slf *RpcHandler) Go(serviceMethod string,args interface{}) error {
	return slf.goRpc(false,0,serviceMethod,args)
}

func (slf *RpcHandler) AsyncCallNode(nodeId int,serviceMethod string,args interface{},callback interface{}) error {
	return slf.asyncCallRpc(nodeId,serviceMethod,args,callback)
}

func (slf *RpcHandler) CallNode(nodeId int,serviceMethod string,args interface{},reply interface{}) error {
	return slf.callRpc(nodeId,serviceMethod,args,reply)
}

func (slf *RpcHandler) GoNode(nodeId int,serviceMethod string,args interface{}) error {
	return slf.goRpc(false,nodeId,serviceMethod,args)
}

func (slf *RpcHandler) CastGo(serviceMethod string,args interface{})  {
	slf.goRpc(true,0,serviceMethod,args)
}
