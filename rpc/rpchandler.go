package rpc

import (
	"errors"
	"fmt"
	"github.com/duanhf2012/origin/log"
	"reflect"
	"runtime"
	"strings"
	"unicode"
	"unicode/utf8"
)

const maxClusterNode int = 128
type FuncRpcClient func(nodeId int,serviceMethod string,client []*Client) (error,int)
type FuncRpcServer func() (*Server)
var nilError = reflect.Zero(reflect.TypeOf((*error)(nil)).Elem())

type RpcError string
var NilError RpcError
func (e RpcError) Error() string {
	return string(e)
}

func ConvertError(e error) RpcError{
	if e == nil {
		return NilError
	}

	rpcErr := RpcError(e.Error())
	return rpcErr
}

func Errorf(format string, a ...interface{}) *RpcError {
	rpcErr := RpcError(fmt.Sprintf(format,a...))

	return &rpcErr
}

type RpcMethodInfo struct {
	method reflect.Method
	inParamValue reflect.Value
	inParam interface{}
	outParamValue reflect.Value
	hasResponder bool
	rpcProcessorType RpcProcessorType
}

type RawRpcCallBack interface {
	Unmarshal(data []byte) (interface{},error)
	CB(data interface{})
}

//type RawRpcCallBack func(rawData []byte)
type RpcHandler struct {
	callRequest   chan *RpcRequest
	rpcHandler    IRpcHandler
	mapFunctions  map[string]RpcMethodInfo
	mapRawFunctions map[uint32] RawRpcCallBack
	funcRpcClient FuncRpcClient
	funcRpcServer FuncRpcServer

	callResponseCallBack chan *Call //异步返回的回调
}

type TriggerRpcEvent func(bConnect bool,nodeId int)
type IRpcListener interface {
	OnRpcConnected(nodeId int)
	OnRpcDisconnect(nodeId int)
}

type IRpcHandler interface {
	GetName() string
	InitRpcHandler(rpcHandler IRpcHandler,getClientFun FuncRpcClient,getServerFun FuncRpcServer)
	GetRpcHandler() IRpcHandler
	PushRequest(callInfo *RpcRequest) error
	HandlerRpcRequest(request *RpcRequest)
	HandlerRpcResponseCB(call *Call)

	GetRpcRequestChan() chan *RpcRequest
	GetRpcResponseChan() chan *Call
	CallMethod(ServiceMethod string,param interface{},reply interface{}) error
	
	AsyncCall(serviceMethod string,args interface{},callback interface{}) error
	Call(serviceMethod string,args interface{},reply interface{}) error
	Go(serviceMethod string,args interface{}) error
	AsyncCallNode(nodeId int,serviceMethod string,args interface{},callback interface{}) error
	CallNode(nodeId int,serviceMethod string,args interface{},reply interface{}) error
	GoNode(nodeId int,serviceMethod string,args interface{}) error
	RawGoNode(rpcProcessorType RpcProcessorType,nodeId int,rpcMethodId uint32,serviceName string,rawArgs IRawInputArgs) error
	CastGo(serviceMethod string,args interface{})
	IsSingleCoroutine() bool
	UnmarshalInParam(rpcProcessor IRpcProcessor,serviceMethod string,rawRpcMethodId uint32,inParam []byte) (interface{},error)
}


func reqHandlerNull(Returns interface{},Err RpcError) {
}

var requestHandlerNull reflect.Value
func init(){
	requestHandlerNull = reflect.ValueOf(reqHandlerNull)
}

func (handler *RpcHandler) GetRpcHandler() IRpcHandler{
	return handler.rpcHandler
}

func (handler *RpcHandler) InitRpcHandler(rpcHandler IRpcHandler,getClientFun FuncRpcClient,getServerFun FuncRpcServer) {
	handler.callRequest = make(chan *RpcRequest,1000000)
	handler.callResponseCallBack = make(chan *Call,1000000)
	handler.mapRawFunctions = make(map[uint32] RawRpcCallBack)
	handler.rpcHandler = rpcHandler
	handler.mapFunctions = map[string]RpcMethodInfo{}
	handler.funcRpcClient = getClientFun
	handler.funcRpcServer = getServerFun

	handler.RegisterRpc(rpcHandler)
}

// Is this an exported - upper case - name?
func isExported(name string) bool {
	rune, _ := utf8.DecodeRuneInString(name)
	return unicode.IsUpper(rune)
}

// Is this type exported or a builtin?
func (handler *RpcHandler) isExportedOrBuiltinType(t reflect.Type) bool {
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	// PkgPath will be non-empty even for an exported type,
	// so we need to check the type name as well.
	return isExported(t.Name()) || t.PkgPath() == ""
}

func (handler *RpcHandler) suitableMethods(method reflect.Method) error {
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

	if typ.NumIn() <2  || typ.NumIn() > 4 {
		return fmt.Errorf("%s Unsupported parameter format!",method.Name)
	}

	//1.判断第一个参数
	var parIdx int = 1
	if typ.In(parIdx).String() == "rpc.RequestHandler" {
		parIdx += 1
		rpcMethodInfo.hasResponder = true
	}

	for i:= parIdx ;i<typ.NumIn();i++{
		if handler.isExportedOrBuiltinType(typ.In(i)) == false {
			return fmt.Errorf("%s Unsupported parameter types!",method.Name)
		}
	}

	rpcMethodInfo.inParamValue = reflect.New(typ.In(parIdx).Elem()) //append(rpcMethodInfo.iparam,)
	rpcMethodInfo.inParam  = reflect.New(typ.In(parIdx).Elem()).Interface()
	pt,_ := GetProcessorType(rpcMethodInfo.inParamValue.Interface())
	rpcMethodInfo.rpcProcessorType = pt

	parIdx++
	if parIdx< typ.NumIn() {
		rpcMethodInfo.outParamValue = reflect.New(typ.In(parIdx).Elem())
	}

	rpcMethodInfo.method = method
	handler.mapFunctions[handler.rpcHandler.GetName()+"."+method.Name] = rpcMethodInfo
	return nil
}

func  (handler *RpcHandler) RegisterRpc(rpcHandler IRpcHandler) error {
	typ := reflect.TypeOf(rpcHandler)
	for m:=0;m<typ.NumMethod();m++{
		method := typ.Method(m)
		err := handler.suitableMethods(method)
		if err != nil {
			panic(err)
		}
	}

	return nil
}

func (handler *RpcHandler) PushRequest(req *RpcRequest) error{
	if len(handler.callRequest) >= cap(handler.callRequest){
		return fmt.Errorf("RpcHandler %s Rpc Channel is full.", handler.GetName())
	}

	handler.callRequest <- req
	return nil
}

func (handler *RpcHandler) GetRpcRequestChan() (chan *RpcRequest) {
	return handler.callRequest
}

func (handler *RpcHandler) GetRpcResponseChan() chan *Call{
	return handler.callResponseCallBack
}

func (handler *RpcHandler) HandlerRpcResponseCB(call *Call){
	defer func() {
		if r := recover(); r != nil {
			buf := make([]byte, 4096)
			l := runtime.Stack(buf, false)
			err := fmt.Errorf("%v: %s\n", r, buf[:l])
			log.Error("core dump info:%+v",err)
		}
	}()

	if call.Err == nil {
		call.callback.Call([]reflect.Value{reflect.ValueOf(call.Reply),nilError})
	}else{
		call.callback.Call([]reflect.Value{reflect.ValueOf(call.Reply),reflect.ValueOf(call.Err)})
	}
	ReleaseCall(call)
}

func (handler *RpcHandler) HandlerRpcRequest(request *RpcRequest) {
	defer func() {
		if r := recover(); r != nil {
				buf := make([]byte, 4096)
				l := runtime.Stack(buf, false)
				err := fmt.Errorf("%v: %s", r, buf[:l])
				log.Error("Handler Rpc %s Core dump info:%+v\n",request.RpcRequestData.GetServiceMethod(),err)
				rpcErr := RpcError("call error : core dumps")
				if request.requestHandle!=nil {
					request.requestHandle(nil,rpcErr)
				}
		}
	}()

	if request.requestHandle == nil {
		defer ReleaseRpcRequest(request)
	}

	//如果是原始RPC请求
	rawRpcId := request.RpcRequestData.GetRpcMethodId()
	if rawRpcId>0 {
		v,ok := handler.mapRawFunctions[rawRpcId]
		if ok == false {
			err := fmt.Sprintf("RpcHandler cannot find request rpc id %d!",rawRpcId)
			log.Error(err)
			return
		}

		v.CB(request.inParam)
		return
	}

	//普通的rpc请求
	v,ok := handler.mapFunctions[request.RpcRequestData.GetServiceMethod()]
	if ok == false {
		err := "RpcHandler "+handler.rpcHandler.GetName()+"cannot find "+request.RpcRequestData.GetServiceMethod()
		log.Error(err)
		if request.requestHandle!=nil {
			request.requestHandle(nil,RpcError(err))
		}
		return
	}

	var paramList []reflect.Value
	var err error
	//生成Call参数
	paramList = append(paramList,reflect.ValueOf(handler.GetRpcHandler())) //接受者
	if v.hasResponder ==  true {
		if request.requestHandle!=nil {
			responder := reflect.ValueOf(request.requestHandle)
			paramList = append(paramList,responder)
		}else{
			paramList = append(paramList,requestHandlerNull)
		}
	}

	paramList = append(paramList,reflect.ValueOf(request.inParam))
	var oParam reflect.Value
	if v.outParamValue.IsValid() {
		if request.localReply!=nil {
			oParam = reflect.ValueOf(request.localReply) //输出参数
		}else{
			oParam = reflect.New(v.outParamValue.Type().Elem())
		}
		paramList = append(paramList,oParam) //输出参数
	}else if request.requestHandle != nil && v.hasResponder==false{ //调用方有返回值，但被调用函数没有返回参数
		rErr := "Call Rpc "+request.RpcRequestData.GetServiceMethod()+" without return parameter!"
		log.Error(rErr)
		request.requestHandle(nil, RpcError(rErr))
		return
	}
	returnValues := v.method.Func.Call(paramList)
	errInter := returnValues[0].Interface()
	if errInter != nil {
		err = errInter.(error)
	}

	if request.requestHandle!=nil && v.hasResponder==false {
		request.requestHandle(oParam.Interface(), ConvertError(err))
	}
}

func (handler *RpcHandler) CallMethod(ServiceMethod string,param interface{},reply interface{}) error{
	var err error
	v,ok := handler.mapFunctions[ServiceMethod]
	if ok == false {
		err = fmt.Errorf("RpcHandler %s cannot find %s", handler.rpcHandler.GetName(),ServiceMethod)
		log.Error("%s",err.Error())

		return err
	}

	var paramList []reflect.Value
	paramList = append(paramList,reflect.ValueOf(handler.GetRpcHandler())) //接受者
	paramList = append(paramList,reflect.ValueOf(param))
	paramList = append(paramList,reflect.ValueOf(reply)) //输出参数

	returnValues := v.method.Func.Call(paramList)
	errInter := returnValues[0].Interface()
	if errInter != nil {
		err = errInter.(error)
	}

	return err
}

func (handler *RpcHandler) goRpc(processor IRpcProcessor,bCast bool,nodeId int,serviceMethod string,args interface{}) error {
	var pClientList [maxClusterNode]*Client
	err,count := handler.funcRpcClient(nodeId,serviceMethod,pClientList[:])
	if count==0||err != nil {
		log.Error("Call serviceMethod is error:%+v!",err)
		return err
	}
	if count > 1 && bCast == false{
		log.Error("Cannot call more then 1 node!")
		return fmt.Errorf("Cannot call more then 1 node!")
	}

	//2.rpcclient调用
	//如果调用本结点服务
	for i:=0;i<count;i++{
		if pClientList[i].bSelfNode == true {
			pLocalRpcServer:= handler.funcRpcServer()
			//判断是否是同一服务
			findIndex := strings.Index(serviceMethod,".")
			if findIndex==-1 {
				sErr := fmt.Errorf("Call serviceMethod %s is error!",serviceMethod)
				log.Error("%+v", sErr)
				if sErr != nil {
					err = sErr
				}
				continue
			}
			serviceName := serviceMethod[:findIndex]
			if serviceName == handler.rpcHandler.GetName() { //自己服务调用
				//调用自己rpcHandler处理器
				return pLocalRpcServer.myselfRpcHandlerGo(serviceName,serviceMethod,args,nil)
			}
			//其他的rpcHandler的处理器
			pCall := pLocalRpcServer.selfNodeRpcHandlerGo(processor,pClientList[i],true,serviceName,0,serviceMethod,args,nil,nil)
			if pCall.Err!=nil {
				err = pCall.Err
			}
			pClientList[i].RemovePending(pCall.Seq)
			ReleaseCall(pCall)
			continue
		}

		//跨node调用
		pCall := pClientList[i].Go(true,serviceMethod,args,nil)
		if pCall.Err!=nil {
			err = pCall.Err
		}
		pClientList[i].RemovePending(pCall.Seq)
		ReleaseCall(pCall)
	}

	return err
}

func (handler *RpcHandler) callRpc(nodeId int,serviceMethod string,args interface{},reply interface{}) error {
	var pClientList [maxClusterNode]*Client
	err,count := handler.funcRpcClient(nodeId,serviceMethod,pClientList[:])
	if count==0||err != nil {
		log.Error("Call serviceMethod is error:%+v!",err)
		return err
	}
	if count > 1 {
		log.Error("Cannot call more then 1 node!")
		return fmt.Errorf("Cannot call more then 1 node!")
	}

	//2.rpcclient调用
	//如果调用本结点服务
	pClient := pClientList[0]
	if pClient.bSelfNode == true {
		pLocalRpcServer:= handler.funcRpcServer()
		//判断是否是同一服务
		findIndex := strings.Index(serviceMethod,".")
		if findIndex==-1 {
			err := fmt.Errorf("Call serviceMethod %s is error!",serviceMethod)
			log.Error("%+v",err)
			return err
		}
		serviceName := serviceMethod[:findIndex]
		if serviceName == handler.rpcHandler.GetName() { //自己服务调用
			//调用自己rpcHandler处理器
			return pLocalRpcServer.myselfRpcHandlerGo(serviceName,serviceMethod,args,reply)
		}
		//其他的rpcHandler的处理器
		pCall := pLocalRpcServer.selfNodeRpcHandlerGo(nil,pClient,false,serviceName,0,serviceMethod,args,reply,nil)
		err = pCall.Done().Err
		pClient.RemovePending(pCall.Seq)
		ReleaseCall(pCall)
		return err
	}

	//跨node调用
	pCall := pClient.Go(false,serviceMethod,args,reply)
	if pCall.Err != nil {
		err = pCall.Err
		ReleaseCall(pCall)
		return err
	}
	err = pCall.Done().Err
	pClient.RemovePending(pCall.Seq)
	ReleaseCall(pCall)
	return err
}

func (handler *RpcHandler) asyncCallRpc(nodeid int,serviceMethod string,args interface{},callback interface{}) error {
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
	var pClientList [maxClusterNode]*Client
	err,count := handler.funcRpcClient(nodeid,serviceMethod,pClientList[:])
	if count==0||err != nil {
		fVal.Call([]reflect.Value{reflect.ValueOf(reply),reflect.ValueOf(err)})
		log.Error("Call serviceMethod is error:%+v!",err)
		return nil
	}

	if count > 1 {
		err := fmt.Errorf("Cannot call more then 1 node!")
		fVal.Call([]reflect.Value{reflect.ValueOf(reply),reflect.ValueOf(err)})
		log.Error("Cannot call more then 1 node!")
		return nil
	}

	//2.rpcclient调用
	//如果调用本结点服务
	pClient := pClientList[0]
	if pClient.bSelfNode == true {
		pLocalRpcServer:= handler.funcRpcServer()
		//判断是否是同一服务
		findIndex := strings.Index(serviceMethod,".")
		if findIndex==-1 {
			err := fmt.Errorf("Call serviceMethod %s is error!",serviceMethod)
			fVal.Call([]reflect.Value{reflect.ValueOf(reply),reflect.ValueOf(err)})
			log.Error("%+v",err)
			return nil
		}
		serviceName := serviceMethod[:findIndex]
		//调用自己rpcHandler处理器
		if serviceName == handler.rpcHandler.GetName() { //自己服务调用
			err := pLocalRpcServer.myselfRpcHandlerGo(serviceName,serviceMethod,args,reply)
			if err == nil {
				fVal.Call([]reflect.Value{reflect.ValueOf(reply),nilError})
			}else{
				fVal.Call([]reflect.Value{reflect.ValueOf(reply),reflect.ValueOf(err)})
			}
		}

		//其他的rpcHandler的处理器
		err =  pLocalRpcServer.selfNodeRpcHandlerAsyncGo(pClient, handler,false,serviceName,serviceMethod,args,reply,fVal)
		if err != nil {
			fVal.Call([]reflect.Value{reflect.ValueOf(reply),reflect.ValueOf(err)})
		}
		return nil
	}

	//跨node调用
	err =  pClient.AsyncCall(handler,serviceMethod,fVal,args,reply)
	if err != nil {
		fVal.Call([]reflect.Value{reflect.ValueOf(reply),reflect.ValueOf(err)})
	}
	return nil
}

func (handler *RpcHandler) GetName() string{
	return handler.rpcHandler.GetName()
}

func (handler *RpcHandler) IsSingleCoroutine() bool{
	return handler.rpcHandler.IsSingleCoroutine()
}

func (handler *RpcHandler) AsyncCall(serviceMethod string,args interface{},callback interface{}) error {
	return handler.asyncCallRpc(0,serviceMethod,args,callback)
}

func (handler *RpcHandler) Call(serviceMethod string,args interface{},reply interface{}) error {
	return handler.callRpc(0,serviceMethod,args,reply)
}

func (handler *RpcHandler) Go(serviceMethod string,args interface{}) error {
	return handler.goRpc(nil,false,0,serviceMethod,args)
}

func (handler *RpcHandler) AsyncCallNode(nodeId int,serviceMethod string,args interface{},callback interface{}) error {
	return handler.asyncCallRpc(nodeId,serviceMethod,args,callback)
}

func (handler *RpcHandler) CallNode(nodeId int,serviceMethod string,args interface{},reply interface{}) error {
	return handler.callRpc(nodeId,serviceMethod,args,reply)
}

func (handler *RpcHandler) GoNode(nodeId int,serviceMethod string,args interface{}) error {
	return handler.goRpc(nil,false,nodeId,serviceMethod,args)
}

func (handler *RpcHandler) CastGo(serviceMethod string,args interface{})  {
	handler.goRpc(nil,true,0,serviceMethod,args)
}

func (handler *RpcHandler) RawGoNode(rpcProcessorType RpcProcessorType,nodeId int,rpcMethodId uint32,serviceName string,rawArgs IRawInputArgs) error {
	processor := GetProcessor(uint8(rpcProcessorType))
	var pClientList [maxClusterNode]*Client
	err,count := handler.funcRpcClient(nodeId,serviceName,pClientList[:])
	if count==0||err != nil {
		//args.DoGc()
		log.Error("Call serviceMethod is error:%+v!",err)
		return err
	}
	if count > 1 {
		//args.DoGc()
		log.Error("Cannot call more then 1 node!")
		return fmt.Errorf("Cannot call more then 1 node!")
	}

	//2.rpcclient调用
	//如果调用本结点服务
	for i:=0;i<count;i++{
		if pClientList[i].bSelfNode == true {
			pLocalRpcServer:= handler.funcRpcServer()
			//调用自己rpcHandler处理器
			if serviceName == handler.rpcHandler.GetName() { //自己服务调用
				err:= pLocalRpcServer.myselfRpcHandlerGo(serviceName,serviceName,rawArgs.GetRawData(),nil)
				//args.DoGc()
				return err
			}

			//其他的rpcHandler的处理器
			pCall := pLocalRpcServer.selfNodeRpcHandlerGo(processor,pClientList[i],true,serviceName,rpcMethodId,serviceName,nil,nil,rawArgs.GetRawData())
			rawArgs.DoEscape()
			if pCall.Err!=nil {
				err = pCall.Err
			}
			pClientList[i].RemovePending(pCall.Seq)
			ReleaseCall(pCall)
			continue
		}

		//跨node调用
		pCall := pClientList[i].RawGo(processor,true,rpcMethodId,serviceName,rawArgs.GetRawData(),nil)
		rawArgs.DoFree()
		if pCall.Err!=nil {
			err = pCall.Err
		}
		pClientList[i].RemovePending(pCall.Seq)
		ReleaseCall(pCall)
	}

	return err
}

func (handler *RpcHandler) RegRawRpc(rpcMethodId uint32,rawRpcCB RawRpcCallBack){
	handler.mapRawFunctions[rpcMethodId] = rawRpcCB
}

func (handler *RpcHandler) UnmarshalInParam(rpcProcessor IRpcProcessor,serviceMethod string,rawRpcMethodId uint32,inParam []byte) (interface{},error){
	if rawRpcMethodId>0 {
		v,ok := handler.mapRawFunctions[rawRpcMethodId]
		if ok == false {
			err := fmt.Errorf("RpcHandler cannot find request rpc id %d!",rawRpcMethodId)
			log.Error(err.Error())
			return nil,err
		}

		msg,err := v.Unmarshal(inParam)
		if err != nil {
			err := fmt.Errorf("RpcHandler cannot Unmarshal rpc id %d!",rawRpcMethodId)
			log.Error(err.Error())
			return nil,err
		}

		return msg,err
	}

	v,ok := handler.mapFunctions[serviceMethod]
	if ok == false {
		return nil,errors.New( "RpcHandler "+handler.rpcHandler.GetName()+"cannot find "+serviceMethod)
	}

	var err error
	param := reflect.New(v.inParamValue.Type().Elem()).Interface()
	err = rpcProcessor.Unmarshal(inParam,param)
	return param,err
}
