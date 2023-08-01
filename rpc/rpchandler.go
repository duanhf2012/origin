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
	"time"
)

const maxClusterNode int = 128

type FuncRpcClient func(nodeId int, serviceMethod string, client []*Client) (error, int)
type FuncRpcServer func() *Server


var nilError = reflect.Zero(reflect.TypeOf((*error)(nil)).Elem())

type RpcError string

var NilError RpcError

func (e RpcError) Error() string {
	return string(e)
}

func ConvertError(e error) RpcError {
	if e == nil {
		return NilError
	}

	rpcErr := RpcError(e.Error())
	return rpcErr
}

type RpcMethodInfo struct {
	method           reflect.Method
	inParamValue     reflect.Value
	inParam          interface{}
	outParamValue    reflect.Value
	hasResponder     bool
	rpcProcessorType RpcProcessorType
}

type  RawRpcCallBack func(rawData []byte)

type IRpcHandlerChannel interface {
	PushRpcResponse(call *Call) error
	PushRpcRequest(rpcRequest *RpcRequest) error
}

type RpcHandler struct {
	IRpcHandlerChannel

	rpcHandler      IRpcHandler
	mapFunctions    map[string]RpcMethodInfo
	mapRawFunctions map[uint32]RawRpcCallBack
	funcRpcClient   FuncRpcClient
	funcRpcServer   FuncRpcServer

	pClientList []*Client
}

type TriggerRpcConnEvent func(bConnect bool, clientSeq uint32, nodeId int)
type INodeListener interface {
	OnNodeConnected(nodeId int)
	OnNodeDisconnect(nodeId int)
}

type IDiscoveryServiceListener interface {
	OnDiscoveryService(nodeId int, serviceName []string)
	OnUnDiscoveryService(nodeId int, serviceName []string)
}

type CancelRpc func()
func emptyCancelRpc(){}

type IRpcHandler interface {
	IRpcHandlerChannel
	GetName() string
	InitRpcHandler(rpcHandler IRpcHandler, getClientFun FuncRpcClient, getServerFun FuncRpcServer, rpcHandlerChannel IRpcHandlerChannel)
	GetRpcHandler() IRpcHandler
	HandlerRpcRequest(request *RpcRequest)
	HandlerRpcResponseCB(call *Call)
	CallMethod(client *Client,ServiceMethod string, param interface{},callBack reflect.Value, reply interface{}) error

	Call(serviceMethod string, args interface{}, reply interface{}) error
	CallNode(nodeId int, serviceMethod string, args interface{}, reply interface{}) error
	AsyncCall(serviceMethod string, args interface{}, callback interface{}) error
	AsyncCallNode(nodeId int, serviceMethod string, args interface{}, callback interface{}) error

	CallWithTimeout(timeout time.Duration,serviceMethod string, args interface{}, reply interface{}) error
	CallNodeWithTimeout(timeout time.Duration,nodeId int, serviceMethod string, args interface{}, reply interface{}) error
	AsyncCallWithTimeout(timeout time.Duration,serviceMethod string, args interface{}, callback interface{})  (CancelRpc,error)
	AsyncCallNodeWithTimeout(timeout time.Duration,nodeId int, serviceMethod string, args interface{}, callback interface{})  (CancelRpc,error)

	Go(serviceMethod string, args interface{}) error
	GoNode(nodeId int, serviceMethod string, args interface{}) error
	RawGoNode(rpcProcessorType RpcProcessorType, nodeId int, rpcMethodId uint32, serviceName string, rawArgs []byte) error
	CastGo(serviceMethod string, args interface{}) error
	IsSingleCoroutine() bool
	UnmarshalInParam(rpcProcessor IRpcProcessor, serviceMethod string, rawRpcMethodId uint32, inParam []byte) (interface{}, error)
	GetRpcServer() FuncRpcServer
}

func reqHandlerNull(Returns interface{}, Err RpcError) {
}

var requestHandlerNull reflect.Value

func init() {
	requestHandlerNull = reflect.ValueOf(reqHandlerNull)
}

func (handler *RpcHandler) GetRpcHandler() IRpcHandler {
	return handler.rpcHandler
}

func (handler *RpcHandler) InitRpcHandler(rpcHandler IRpcHandler, getClientFun FuncRpcClient, getServerFun FuncRpcServer, rpcHandlerChannel IRpcHandlerChannel) {
	handler.IRpcHandlerChannel = rpcHandlerChannel
	handler.mapRawFunctions = make(map[uint32]RawRpcCallBack)
	handler.rpcHandler = rpcHandler
	handler.mapFunctions = map[string]RpcMethodInfo{}
	handler.funcRpcClient = getClientFun
	handler.funcRpcServer = getServerFun
	handler.pClientList = make([]*Client, maxClusterNode)
	handler.RegisterRpc(rpcHandler)
}

// Is this an exported - upper case - name?
func isExported(name string) bool {
	r, _ := utf8.DecodeRuneInString(name)
	return unicode.IsUpper(r)
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
	if strings.Index(method.Name, "RPC_") != 0 && strings.Index(method.Name, "RPC") != 0 {
		return nil
	}

	//取出输入参数类型
	var rpcMethodInfo RpcMethodInfo
	typ := method.Type
	if typ.NumOut() != 1 {
		return fmt.Errorf("%s The number of returned arguments must be 1", method.Name)
	}

	if typ.Out(0).String() != "error" {
		return fmt.Errorf("%s The return parameter must be of type error", method.Name)
	}

	if typ.NumIn() < 2 || typ.NumIn() > 4 {
		return fmt.Errorf("%s Unsupported parameter format", method.Name)
	}

	//1.判断第一个参数
	var parIdx = 1
	if typ.In(parIdx).String() == "rpc.RequestHandler" {
		parIdx += 1
		rpcMethodInfo.hasResponder = true
	}

	for i := parIdx; i < typ.NumIn(); i++ {
		if handler.isExportedOrBuiltinType(typ.In(i)) == false {
			return fmt.Errorf("%s Unsupported parameter types", method.Name)
		}
	}

	rpcMethodInfo.inParamValue = reflect.New(typ.In(parIdx).Elem())
	rpcMethodInfo.inParam = reflect.New(typ.In(parIdx).Elem()).Interface()
	pt, _ := GetProcessorType(rpcMethodInfo.inParamValue.Interface())
	rpcMethodInfo.rpcProcessorType = pt

	parIdx++
	if parIdx < typ.NumIn() {
		rpcMethodInfo.outParamValue = reflect.New(typ.In(parIdx).Elem())
	}

	rpcMethodInfo.method = method
	handler.mapFunctions[handler.rpcHandler.GetName()+"."+method.Name] = rpcMethodInfo
	return nil
}

func (handler *RpcHandler) RegisterRpc(rpcHandler IRpcHandler) error {
	typ := reflect.TypeOf(rpcHandler)
	for m := 0; m < typ.NumMethod(); m++ {
		method := typ.Method(m)
		err := handler.suitableMethods(method)
		if err != nil {
			panic(err)
		}
	}

	return nil
}

func (handler *RpcHandler) HandlerRpcResponseCB(call *Call) {
	defer func() {
		if r := recover(); r != nil {
			buf := make([]byte, 4096)
			l := runtime.Stack(buf, false)
			errString := fmt.Sprint(r)
			log.SError("core dump info[", errString, "]\n", string(buf[:l]))
		}
	}()

	if call.Err == nil {
		call.callback.Call([]reflect.Value{reflect.ValueOf(call.Reply), nilError})
	} else {
		call.callback.Call([]reflect.Value{reflect.ValueOf(call.Reply), reflect.ValueOf(call.Err)})
	}
	ReleaseCall(call)
}

func (handler *RpcHandler) HandlerRpcRequest(request *RpcRequest) {
	if request.requestHandle == nil {
		defer ReleaseRpcRequest(request)
	}

	defer func() {
		if r := recover(); r != nil {
			buf := make([]byte, 4096)
			l := runtime.Stack(buf, false)
			errString := fmt.Sprint(r)
			log.SError("Handler Rpc ", request.RpcRequestData.GetServiceMethod(), " Core dump info[", errString, "]\n", string(buf[:l]))
			rpcErr := RpcError("call error : core dumps")
			if request.requestHandle != nil {
				request.requestHandle(nil, rpcErr)
			}
		}
	}()

	//如果是原始RPC请求
	rawRpcId := request.RpcRequestData.GetRpcMethodId()
	if rawRpcId > 0 {
		v, ok := handler.mapRawFunctions[rawRpcId]
		if ok == false {
			log.SError("RpcHandler cannot find request rpc id", rawRpcId)
			return
		}
		rawData,ok := request.inParam.([]byte)
		if ok == false {
			log.SError("RpcHandler " + handler.rpcHandler.GetName()," cannot convert in param to []byte", rawRpcId)
			return
		}

		v(rawData)
		return
	}

	//普通的rpc请求
	v, ok := handler.mapFunctions[request.RpcRequestData.GetServiceMethod()]
	if ok == false {
		err := "RpcHandler " + handler.rpcHandler.GetName() + "cannot find " + request.RpcRequestData.GetServiceMethod()
		log.SError(err)
		if request.requestHandle != nil {
			request.requestHandle(nil, RpcError(err))
		}
		return
	}

	var paramList []reflect.Value
	var err error
	//生成Call参数
	paramList = append(paramList, reflect.ValueOf(handler.GetRpcHandler())) //接受者
	if v.hasResponder == true {
		if request.requestHandle != nil {
			responder := reflect.ValueOf(request.requestHandle)
			paramList = append(paramList, responder)
		} else {
			paramList = append(paramList, requestHandlerNull)
		}
	}

	paramList = append(paramList, reflect.ValueOf(request.inParam))
	var oParam reflect.Value
	if v.outParamValue.IsValid() {
		if request.localReply != nil {
			oParam = reflect.ValueOf(request.localReply) //输出参数
		} else {
			oParam = reflect.New(v.outParamValue.Type().Elem())
		}
		paramList = append(paramList, oParam) //输出参数
	} else if request.requestHandle != nil && v.hasResponder == false { //调用方有返回值，但被调用函数没有返回参数
		rErr := "Call Rpc " + request.RpcRequestData.GetServiceMethod() + " without return parameter!"
		log.SError(rErr)
		request.requestHandle(nil, RpcError(rErr))
		return
	}

	requestHanle := request.requestHandle
	returnValues := v.method.Func.Call(paramList)
	errInter := returnValues[0].Interface()
	if errInter != nil {
		err = errInter.(error)
	}

	if v.hasResponder == false && requestHanle != nil  {
		requestHanle(oParam.Interface(), ConvertError(err))
	}
}

func (handler *RpcHandler) CallMethod(client *Client,ServiceMethod string, param interface{},callBack reflect.Value, reply interface{}) error {
	var err error
	v, ok := handler.mapFunctions[ServiceMethod]
	if ok == false {
		err = errors.New("RpcHandler " + handler.rpcHandler.GetName() + " cannot find" + ServiceMethod)
		log.SError(err.Error())
		return err
	}

	var paramList []reflect.Value
	var returnValues []reflect.Value
	var pCall *Call
	var callSeq uint64
	if v.hasResponder == true {
		paramList = append(paramList, reflect.ValueOf(handler.GetRpcHandler())) //接受者
		pCall = MakeCall()
		pCall.callback = &callBack
		pCall.Seq = client.generateSeq()
		callSeq = pCall.Seq
		pCall.TimeOut = DefaultRpcTimeout
		pCall.ServiceMethod = ServiceMethod
		client.AddPending(pCall)

		//有返回值时
		if reply != nil {
			//如果是Call同步调用
			hander :=func(Returns interface{}, Err RpcError) {
				rpcCall := client.RemovePending(callSeq)
				if rpcCall == nil {
					log.SError("cannot find call seq ",callSeq)
					return
				}

				//解析数据
				if len(Err)!=0 {
					rpcCall.Err = Err
				}else if Returns != nil {
					_, processor := GetProcessorType(Returns)
					var bytes []byte
					bytes,rpcCall.Err = processor.Marshal(Returns)
					if rpcCall.Err == nil {
						rpcCall.Err = processor.Unmarshal(bytes,reply)
					}
				}

				//如果找不到，说明已经超时
				rpcCall.Reply = reply
				rpcCall.done<-rpcCall
			}
			paramList = append(paramList,  reflect.ValueOf(hander))
		}else{//无返回值时,是一个requestHandlerNull空回调
			paramList = append(paramList, callBack)
		}
		paramList = append(paramList, reflect.ValueOf(param))

		//rpc函数被调用
		returnValues = v.method.Func.Call(paramList)

		//判断返回值是否错误，有错误时则回调
		errInter := returnValues[0].Interface()
		if errInter != nil && callBack!=requestHandlerNull{
			err = errInter.(error)
			callBack.Call([]reflect.Value{reflect.ValueOf(reply), reflect.ValueOf(err)})
		}
	}else{
		paramList = append(paramList, reflect.ValueOf(handler.GetRpcHandler())) //接受者
		paramList = append(paramList, reflect.ValueOf(param))

		//被调用RPC函数有返回值时
		if v.outParamValue.IsValid() {
			//不带返回值参数的RPC函数
			if reply == nil {
				paramList = append(paramList, reflect.New(v.outParamValue.Type().Elem()))
			}else{
				//带返回值参数的RPC函数
				paramList = append(paramList, reflect.ValueOf(reply)) //输出参数
			}
		}

		returnValues = v.method.Func.Call(paramList)
		errInter := returnValues[0].Interface()

		//如果无回调
		if callBack != requestHandlerNull {
			valErr := nilError
			if errInter != nil {
				err = errInter.(error)
				valErr = reflect.ValueOf(err)
			}

			callBack.Call([]reflect.Value{reflect.ValueOf(reply),valErr })
		}
	}

	rpcCall := client.FindPending(callSeq)
	if rpcCall!=nil  {
		err =  rpcCall.Done().Err
		if rpcCall.callback!= nil {
			valErr := nilError
			if rpcCall.Err != nil {
				valErr = reflect.ValueOf(rpcCall.Err)
			}
			rpcCall.callback.Call([]reflect.Value{reflect.ValueOf(rpcCall.Reply), valErr})
		}
		client.RemovePending(rpcCall.Seq)
		ReleaseCall(rpcCall)
	}

	return err
}

func (handler *RpcHandler) goRpc(processor IRpcProcessor, bCast bool, nodeId int, serviceMethod string, args interface{}) error {
	var pClientList [maxClusterNode]*Client
	err, count := handler.funcRpcClient(nodeId, serviceMethod, pClientList[:])
	if count == 0 {
		if err != nil {
			log.SError("Call ", serviceMethod, " is error:", err.Error())
		} else {
			log.SError("Can not find ", serviceMethod)
		}
		return err
	}

	if count > 1 && bCast == false {
		log.SError("Cannot call %s more then 1 node!", serviceMethod)
		return errors.New("cannot call more then 1 node")
	}

	//2.rpcClient调用
	for i := 0; i < count; i++ {
		pCall := pClientList[i].Go(DefaultRpcTimeout,handler.rpcHandler,true, serviceMethod, args, nil)
		if pCall.Err != nil {
			err = pCall.Err
		}
		pClientList[i].RemovePending(pCall.Seq)
		ReleaseCall(pCall)
	}

	return err
}

func (handler *RpcHandler) callRpc(timeout time.Duration,nodeId int, serviceMethod string, args interface{}, reply interface{}) error {
	var pClientList [maxClusterNode]*Client
	err, count := handler.funcRpcClient(nodeId, serviceMethod, pClientList[:])
	if err != nil {
		log.SError("Call serviceMethod is error:", err.Error())
		return err
	} else if count <= 0 {
		err = errors.New("Call serviceMethod is error:cannot find " + serviceMethod)
		log.SError(err.Error())
		return err
	} else if count > 1 {
		log.SError("Cannot call more then 1 node!")
		return errors.New("cannot call more then 1 node")
	}

	pClient := pClientList[0]
	pCall := pClient.Go(timeout,handler.rpcHandler,false, serviceMethod, args, reply)

	err = pCall.Done().Err
	pClient.RemovePending(pCall.Seq)
	ReleaseCall(pCall)
	return err
}

func (handler *RpcHandler) asyncCallRpc(timeout time.Duration,nodeId int, serviceMethod string, args interface{}, callback interface{}) (CancelRpc,error) {
	fVal := reflect.ValueOf(callback)
	if fVal.Kind() != reflect.Func {
		err := errors.New("call " + serviceMethod + " input callback param is error!")
		log.SError(err.Error())
		return emptyCancelRpc,err
	}

	if fVal.Type().NumIn() != 2 {
		err := errors.New("call " + serviceMethod + " callback param function is error!")
		log.SError(err.Error())
		return emptyCancelRpc,err
	}

	if fVal.Type().In(0).Kind() != reflect.Ptr || fVal.Type().In(1).String() != "error" {
		err := errors.New("call " + serviceMethod + " callback param function is error!")
		log.SError(err.Error())
		return emptyCancelRpc,err
	}

	reply := reflect.New(fVal.Type().In(0).Elem()).Interface()
	var pClientList [2]*Client
	err, count := handler.funcRpcClient(nodeId, serviceMethod, pClientList[:])
	if count == 0 || err != nil {
		if err == nil {
			if nodeId  > 0 {
				err = fmt.Errorf("cannot find %s from nodeId %d",serviceMethod,nodeId)
			}else {
				err = fmt.Errorf("No %s service found in the origin network",serviceMethod)
			}
		}
		fVal.Call([]reflect.Value{reflect.ValueOf(reply), reflect.ValueOf(err)})
		log.SError("Call serviceMethod is error:", err.Error())
		return emptyCancelRpc,nil
	}

	if count > 1 {
		err := errors.New("cannot call more then 1 node")
		fVal.Call([]reflect.Value{reflect.ValueOf(reply), reflect.ValueOf(err)})
		log.SError(err.Error())
		return emptyCancelRpc,nil
	}

	//2.rpcClient调用
	//如果调用本结点服务
	return pClientList[0].AsyncCall(timeout,handler.rpcHandler, serviceMethod, fVal, args, reply,false)
}

func (handler *RpcHandler) GetName() string {
	return handler.rpcHandler.GetName()
}

func (handler *RpcHandler) IsSingleCoroutine() bool {
	return handler.rpcHandler.IsSingleCoroutine()
}

func (handler *RpcHandler) CallWithTimeout(timeout time.Duration,serviceMethod string, args interface{}, reply interface{}) error {
	return handler.callRpc(timeout,0, serviceMethod, args, reply)
}

func (handler *RpcHandler) CallNodeWithTimeout(timeout time.Duration,nodeId int, serviceMethod string, args interface{}, reply interface{})  error{
	return handler.callRpc(timeout,nodeId, serviceMethod, args, reply)
}

func (handler *RpcHandler) AsyncCallWithTimeout(timeout time.Duration,serviceMethod string, args interface{}, callback interface{})  (CancelRpc,error){
	return handler.asyncCallRpc(timeout,0, serviceMethod, args, callback)
}

func (handler *RpcHandler) AsyncCallNodeWithTimeout(timeout time.Duration,nodeId int, serviceMethod string, args interface{}, callback interface{})  (CancelRpc,error){
	return handler.asyncCallRpc(timeout,nodeId, serviceMethod, args, callback)
}

func (handler *RpcHandler) AsyncCall(serviceMethod string, args interface{}, callback interface{}) error {
	_,err := handler.asyncCallRpc(DefaultRpcTimeout,0, serviceMethod, args, callback)
	return err
}

func (handler *RpcHandler) Call(serviceMethod string, args interface{}, reply interface{}) error {
	return handler.callRpc(DefaultRpcTimeout,0, serviceMethod, args, reply)
}

func (handler *RpcHandler) Go(serviceMethod string, args interface{}) error {
	return handler.goRpc(nil, false, 0, serviceMethod, args)
}

func (handler *RpcHandler) AsyncCallNode(nodeId int, serviceMethod string, args interface{}, callback interface{}) error {
	_,err:= handler.asyncCallRpc(DefaultRpcTimeout,nodeId, serviceMethod, args, callback)

	return err
}

func (handler *RpcHandler) CallNode(nodeId int, serviceMethod string, args interface{}, reply interface{}) error {
	return handler.callRpc(DefaultRpcTimeout,nodeId, serviceMethod, args, reply)
}

func (handler *RpcHandler) GoNode(nodeId int, serviceMethod string, args interface{}) error {
	return handler.goRpc(nil, false, nodeId, serviceMethod, args)
}

func (handler *RpcHandler) CastGo(serviceMethod string, args interface{}) error {
	return handler.goRpc(nil, true, 0, serviceMethod, args)
}

func (handler *RpcHandler) RawGoNode(rpcProcessorType RpcProcessorType, nodeId int, rpcMethodId uint32, serviceName string, rawArgs []byte) error {
	processor := GetProcessor(uint8(rpcProcessorType))
	err, count := handler.funcRpcClient(nodeId, serviceName, handler.pClientList)
	if count == 0 || err != nil {
		log.SError("Call serviceMethod is error:", err.Error())
		return err
	}
	if count > 1 {
		err := errors.New("cannot call more then 1 node")
		log.SError(err.Error())
		return err
	}

	//2.rpcClient调用
	//如果调用本结点服务
	for i := 0; i < count; i++ {
		//跨node调用
		pCall := handler.pClientList[i].RawGo(DefaultRpcTimeout,handler.rpcHandler,processor, true, rpcMethodId, serviceName, rawArgs, nil)
		if pCall.Err != nil {
			err = pCall.Err
		}

		handler.pClientList[i].RemovePending(pCall.Seq)
		ReleaseCall(pCall)
	}

	return err
}

func (handler *RpcHandler) RegRawRpc(rpcMethodId uint32, rawRpcCB RawRpcCallBack) {
	handler.mapRawFunctions[rpcMethodId] = rawRpcCB
}

func (handler *RpcHandler) UnmarshalInParam(rpcProcessor IRpcProcessor, serviceMethod string, rawRpcMethodId uint32, inParam []byte) (interface{}, error) {
	if rawRpcMethodId > 0 {
		return inParam,nil
	}

	v, ok := handler.mapFunctions[serviceMethod]
	if ok == false {
		return nil, errors.New("RpcHandler " + handler.rpcHandler.GetName() + " cannot find " + serviceMethod)
	}

	var err error
	param := reflect.New(v.inParamValue.Type().Elem()).Interface()
	err = rpcProcessor.Unmarshal(inParam, param)
	return param, err
}


func (handler *RpcHandler) GetRpcServer() FuncRpcServer{
	return handler.funcRpcServer
}
