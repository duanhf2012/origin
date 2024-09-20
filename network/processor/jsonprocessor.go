package processor

import (
	"encoding/json"
	"fmt"
	"github.com/duanhf2012/origin/v2/log"
	"github.com/duanhf2012/origin/v2/util/bytespool"
	"reflect"
)

type MessageJsonInfo struct {
	msgType    reflect.Type
	msgHandler MessageJsonHandler
}

type MessageJsonHandler func(clientId string, msg interface{})
type ConnectJsonHandler func(clientId string)
type UnknownMessageJsonHandler func(clientId string, msg []byte)

type JsonProcessor struct {
	mapMsg       map[uint16]MessageJsonInfo
	LittleEndian bool

	unknownMessageHandler UnknownMessageJsonHandler
	connectHandler        ConnectJsonHandler
	disconnectHandler     ConnectJsonHandler
	bytespool.IBytesMemPool
}

type JsonPackInfo struct {
	typ    uint16
	msg    interface{}
	rawMsg []byte
}

func NewJsonProcessor() *JsonProcessor {
	processor := &JsonProcessor{mapMsg: map[uint16]MessageJsonInfo{}}
	processor.IBytesMemPool = bytespool.NewMemAreaPool()

	return processor
}

func (jsonProcessor *JsonProcessor) SetByteOrder(littleEndian bool) {
	jsonProcessor.LittleEndian = littleEndian
}

// MsgRoute must goroutine safe
func (jsonProcessor *JsonProcessor) MsgRoute(clientId string, msg interface{}, recyclerReaderBytes func(data []byte)) error {
	pPackInfo := msg.(*JsonPackInfo)
	defer recyclerReaderBytes(pPackInfo.rawMsg)

	v, ok := jsonProcessor.mapMsg[pPackInfo.typ]
	if ok == false {
		return fmt.Errorf("cannot find msgtype %d is register", pPackInfo.typ)
	}

	v.msgHandler(clientId, pPackInfo.msg)
	return nil
}

func (jsonProcessor *JsonProcessor) Unmarshal(clientId string, data []byte) (interface{}, error) {
	typeStruct := struct {
		Type int `json:"typ"`
	}{}
	err := json.Unmarshal(data, &typeStruct)
	if err != nil {
		return nil, err
	}

	msgType := uint16(typeStruct.Type)
	info, ok := jsonProcessor.mapMsg[msgType]
	if ok == false {
		return nil, fmt.Errorf("cannot find register %d msgType", msgType)
	}

	msgData := reflect.New(info.msgType.Elem()).Interface()
	err = json.Unmarshal(data, msgData)
	if err != nil {
		return nil, err
	}

	return &JsonPackInfo{typ: msgType, msg: msgData, rawMsg: data}, nil
}

func (jsonProcessor *JsonProcessor) Marshal(clientId string, msg interface{}) ([]byte, error) {
	rawMsg, err := json.Marshal(msg)
	if err != nil {
		return nil, err
	}

	return rawMsg, nil
}

func (jsonProcessor *JsonProcessor) Register(msgType uint16, msg interface{}, handle MessageJsonHandler) {
	var info MessageJsonInfo

	info.msgType = reflect.TypeOf(msg)
	info.msgHandler = handle
	jsonProcessor.mapMsg[msgType] = info
}

func (jsonProcessor *JsonProcessor) MakeMsg(msgType uint16, msg interface{}) *JsonPackInfo {
	return &JsonPackInfo{typ: msgType, msg: msg}
}

func (jsonProcessor *JsonProcessor) MakeRawMsg(msgType uint16, msg []byte) *JsonPackInfo {
	return &JsonPackInfo{typ: msgType, rawMsg: msg}
}

func (jsonProcessor *JsonProcessor) UnknownMsgRoute(clientId string, msg interface{}, recyclerReaderBytes func(data []byte)) {
	defer recyclerReaderBytes(msg.([]byte))
	if jsonProcessor.unknownMessageHandler == nil {
		log.Debug("Unknown message", log.String("clientId", clientId))
		return
	}

	jsonProcessor.unknownMessageHandler(clientId, msg.([]byte))

}

func (jsonProcessor *JsonProcessor) ConnectedRoute(clientId string) {
	if jsonProcessor.connectHandler != nil {
		jsonProcessor.connectHandler(clientId)
	}
}

func (jsonProcessor *JsonProcessor) DisConnectedRoute(clientId string) {
	if jsonProcessor.disconnectHandler != nil {
		jsonProcessor.disconnectHandler(clientId)
	}
}

func (jsonProcessor *JsonProcessor) RegisterUnknownMsg(unknownMessageHandler UnknownMessageJsonHandler) {
	jsonProcessor.unknownMessageHandler = unknownMessageHandler
}

func (jsonProcessor *JsonProcessor) RegisterConnected(connectHandler ConnectJsonHandler) {
	jsonProcessor.connectHandler = connectHandler
}

func (jsonProcessor *JsonProcessor) RegisterDisConnected(disconnectHandler ConnectJsonHandler) {
	jsonProcessor.disconnectHandler = disconnectHandler
}

func (slf *JsonPackInfo) GetPackType() uint16 {
	return slf.typ
}

func (slf *JsonPackInfo) GetMsg() interface{} {
	return slf.msg
}
