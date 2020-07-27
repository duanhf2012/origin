package processor

import (
	"encoding/json"
	"fmt"
	"reflect"
)

type MessageJsonInfo struct {
	msgType    reflect.Type
	msgHandler MessageJsonHandler
}

type MessageJsonHandler func(clientid uint64,msg interface{})
type ConnectJsonHandler func(clientid uint64)
type UnknownMessageJsonHandler func(clientid uint64,msg []byte)

type JsonProcessor struct {
	//SetByteOrder(littleEndian bool)
	//SetMsgLen(lenMsgLen int, minMsgLen uint32, maxMsgLen uint32)
	mapMsg map[uint16]MessageJsonInfo
	LittleEndian bool

	unknownMessageHandler UnknownMessageJsonHandler
	connectHandler ConnectJsonHandler
	disconnectHandler ConnectJsonHandler
}

func NewJsonProcessor() *JsonProcessor {
	processor := &JsonProcessor{mapMsg:map[uint16]MessageJsonInfo{}}
	return processor
}

func (p *JsonProcessor) SetByteOrder(littleEndian bool) {
	p.LittleEndian = littleEndian
}

type JsonPackInfo struct {
	typ uint16
	msg interface{}
	rawMsg []byte
}

func (slf *JsonPackInfo) GetPackType() uint16 {
	return slf.typ
}

func (slf *JsonPackInfo) GetMsg() interface{} {
	return slf.msg
}

// must goroutine safe
func (slf *JsonProcessor ) MsgRoute(msg interface{},userdata interface{}) error{
	pPackInfo := msg.(*JsonPackInfo)
	v,ok := slf.mapMsg[pPackInfo.typ]
	if ok == false {
		return fmt.Errorf("cannot find msgtype %d is register!",pPackInfo.typ)
	}

	v.msgHandler(userdata.(uint64),pPackInfo.msg)
	return nil
}

func (slf *JsonProcessor) Unmarshal(data []byte) (interface{}, error) {
	typeStrcut := struct {Type int `json:"typ"`}{}
	err := json.Unmarshal(data, &typeStrcut)
	if err != nil {
		return nil, err
	}
	msgType := uint16(typeStrcut.Type)

	info,ok := slf.mapMsg[msgType]
	if ok == false {
		return nil,fmt.Errorf("cannot find register %d msgtype!",msgType)
	}
	msg := reflect.New(info.msgType.Elem()).Interface()
	err = json.Unmarshal(data, msg)
	if err != nil {
		return nil,err
	}

	return &JsonPackInfo{typ:msgType,msg:msg},nil
}


func (slf *JsonProcessor) Marshal(msg interface{}) ([]byte, error) {
	rawMsg,err := json.Marshal(msg)
	if err != nil {
		return nil,err
	}

	return rawMsg,nil
}

func (slf *JsonProcessor) Register(msgtype uint16,msg interface{},handle MessageJsonHandler)  {
	var info MessageJsonInfo

	info.msgType = reflect.TypeOf(msg)
	info.msgHandler = handle
	slf.mapMsg[msgtype] = info
}

func (slf *JsonProcessor) MakeMsg(msgType uint16,msg interface{}) *JsonPackInfo {
	return &JsonPackInfo{typ:msgType,msg:msg}
}

func (slf *JsonProcessor) MakeRawMsg(msgType uint16,msg []byte) *JsonPackInfo {
	return &JsonPackInfo{typ:msgType,rawMsg:msg}
}

func (slf *JsonProcessor) UnknownMsgRoute(msg interface{}, userData interface{}){
	slf.unknownMessageHandler(userData.(uint64),msg.([]byte))
}

// connect event
func (slf *JsonProcessor) ConnectedRoute(userData interface{}){
	slf.connectHandler(userData.(uint64))
}

func (slf *JsonProcessor) DisConnectedRoute(userData interface{}){
	slf.disconnectHandler(userData.(uint64))
}

func (slf *JsonProcessor) RegisterUnknownMsg(unknownMessageHandler UnknownMessageJsonHandler){
	slf.unknownMessageHandler = unknownMessageHandler
}

func (slf *JsonProcessor) RegisterConnected(connectHandler ConnectJsonHandler){
	slf.connectHandler = connectHandler
}

func (slf *JsonProcessor) RegisterDisConnected(disconnectHandler ConnectJsonHandler){
	slf.disconnectHandler = disconnectHandler
}