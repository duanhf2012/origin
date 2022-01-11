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

type PBRawPackInfo struct {
	typ uint16
	rawMsg []byte
}

func NewPBRawProcessor() *PBRawProcessor {
	processor := &PBRawProcessor{}
	return processor
}

func (pbRawProcessor *PBRawProcessor) SetByteOrder(littleEndian bool) {
	pbRawProcessor.LittleEndian = littleEndian
}

// must goroutine safe
func (pbRawProcessor *PBRawProcessor ) MsgRoute(clientId uint64, msg interface{}) error{
	pPackInfo := msg.(*PBRawPackInfo)
	pbRawProcessor.msgHandler(clientId,pPackInfo.typ,pPackInfo.rawMsg)
	return nil
}

// must goroutine safe
func (pbRawProcessor *PBRawProcessor ) Unmarshal(clientId uint64,data []byte) (interface{}, error) {
	var msgType uint16
	if pbRawProcessor.LittleEndian == true {
		msgType = binary.LittleEndian.Uint16(data[:2])
	}else{
		msgType = binary.BigEndian.Uint16(data[:2])
	}

	return &PBRawPackInfo{typ:msgType,rawMsg:data},nil
}

// must goroutine safe
func (pbRawProcessor *PBRawProcessor ) Marshal(clientId uint64,msg interface{}) ([]byte, error){
	pMsg := msg.(*PBRawPackInfo)

	buff := make([]byte, 2, len(pMsg.rawMsg)+RawMsgTypeSize)
	if pbRawProcessor.LittleEndian == true {
		binary.LittleEndian.PutUint16(buff[:2],pMsg.typ)
	}else{
		binary.BigEndian.PutUint16(buff[:2],pMsg.typ)
	}

	buff = append(buff,pMsg.rawMsg...)
	return buff,nil
}

func (pbRawProcessor *PBRawProcessor) SetRawMsgHandler(handle RawMessageHandler)  {
	pbRawProcessor.msgHandler = handle
}

func (pbRawProcessor *PBRawProcessor) MakeRawMsg(msgType uint16,msg []byte,pbRawPackInfo *PBRawPackInfo)  {
	pbRawPackInfo.typ = msgType
	pbRawPackInfo.rawMsg = msg
	//return &PBRawPackInfo{typ:msgType,rawMsg:msg}
}

func (pbRawProcessor *PBRawProcessor) UnknownMsgRoute(clientId uint64,msg interface{}){
	if pbRawProcessor.unknownMessageHandler == nil {
		return
	}
	pbRawProcessor.unknownMessageHandler(clientId,msg.([]byte))
}

// connect event
func (pbRawProcessor *PBRawProcessor) ConnectedRoute(clientId uint64){
	pbRawProcessor.connectHandler(clientId)
}

func (pbRawProcessor *PBRawProcessor) DisConnectedRoute(clientId uint64){
	pbRawProcessor.disconnectHandler(clientId)
}

func (pbRawProcessor *PBRawProcessor) SetUnknownMsgHandler(unknownMessageHandler UnknownRawMessageHandler){
	pbRawProcessor.unknownMessageHandler = unknownMessageHandler
}

func (pbRawProcessor *PBRawProcessor) SetConnectedHandler(connectHandler RawConnectHandler){
	pbRawProcessor.connectHandler = connectHandler
}

func (pbRawProcessor *PBRawProcessor) SetDisConnectedHandler(disconnectHandler RawConnectHandler){
	pbRawProcessor.disconnectHandler = disconnectHandler
}

func (slf *PBRawPackInfo) GetPackType() uint16 {
	return slf.typ
}

func (slf *PBRawPackInfo) GetMsg() []byte {
	return slf.rawMsg
}

func (slf *PBRawPackInfo) SetPackInfo(typ uint16,rawMsg  []byte){
	slf.typ = typ
	slf.rawMsg = rawMsg
}