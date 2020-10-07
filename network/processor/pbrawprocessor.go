package processor

import (
	"encoding/binary"
	"reflect"
)

type RawMessageInfo struct {
	msgType    reflect.Type
	msgHandler RawMessageHandler
}

type RawMessageHandler func(clientId uint64,packType uint16,msg []byte)
type RawConnectHandler func(clientId uint64)
type UnknownRawMessageHandler func(clientId uint64,msg []byte)

const RawMsgTypeSize = 2
type PBRawProcessor struct {
	msgHandler RawMessageHandler
	LittleEndian bool

	unknownMessageHandler UnknownRawMessageHandler
	connectHandler RawConnectHandler
	disconnectHandler RawConnectHandler
}

func NewPBRawProcessor() *PBRawProcessor {
	processor := &PBRawProcessor{}
	return processor
}

func (p *PBRawProcessor) SetByteOrder(littleEndian bool) {
	p.LittleEndian = littleEndian
}

type PBRawPackInfo struct {
	typ uint16
	rawMsg []byte
}

func (slf *PBRawPackInfo) GetPackType() uint16 {
	return slf.typ
}

func (slf *PBRawPackInfo) GetMsg() []byte {
	return slf.rawMsg
}

// must goroutine safe
func (slf *PBRawProcessor ) MsgRoute(msg interface{},userdata interface{}) error{
	pPackInfo := msg.(*PBRawPackInfo)
	slf.msgHandler(userdata.(uint64),pPackInfo.typ,pPackInfo.rawMsg)
	return nil
}

// must goroutine safe
func (slf *PBRawProcessor ) Unmarshal(data []byte) (interface{}, error) {
	var msgType uint16
	if slf.LittleEndian == true {
		msgType = binary.LittleEndian.Uint16(data[:2])
	}else{
		msgType = binary.BigEndian.Uint16(data[:2])
	}

	return &PBRawPackInfo{typ:msgType,rawMsg:data[2:]},nil
}

// must goroutine safe
func (slf *PBRawProcessor ) Marshal(msg interface{}) ([]byte, error){
	pMsg := msg.(*PBRawPackInfo)

	buff := make([]byte, 2, len(pMsg.rawMsg)+RawMsgTypeSize)
	if slf.LittleEndian == true {
		binary.LittleEndian.PutUint16(buff[:2],pMsg.typ)
	}else{
		binary.BigEndian.PutUint16(buff[:2],pMsg.typ)
	}

	buff = append(buff,pMsg.rawMsg...)
	return buff,nil
}

func (slf *PBRawProcessor) SetRawMsgHandler(handle RawMessageHandler)  {
	slf.msgHandler = handle
}


func (slf *PBRawProcessor) MakeRawMsg(msgType uint16,msg []byte) *PBRawPackInfo {
	return &PBRawPackInfo{typ:msgType,rawMsg:msg}
}

func (slf *PBRawProcessor) UnknownMsgRoute(msg interface{}, userData interface{}){
	if slf.unknownMessageHandler == nil {
		return
	}
	slf.unknownMessageHandler(userData.(uint64),msg.([]byte))
}

// connect event
func (slf *PBRawProcessor) ConnectedRoute(userData interface{}){
	slf.connectHandler(userData.(uint64))
}

func (slf *PBRawProcessor) DisConnectedRoute(userData interface{}){
	slf.disconnectHandler(userData.(uint64))
}

func (slf *PBRawProcessor) SetUnknownMsgHandler(unknownMessageHandler UnknownRawMessageHandler){
	slf.unknownMessageHandler = unknownMessageHandler
}

func (slf *PBRawProcessor) SetConnectedHandler(connectHandler RawConnectHandler){
	slf.connectHandler = connectHandler
}

func (slf *PBRawProcessor) SetDisConnectedHandler(disconnectHandler RawConnectHandler){
	slf.disconnectHandler = disconnectHandler
}

