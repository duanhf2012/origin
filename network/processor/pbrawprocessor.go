package processor

import (
	"encoding/binary"
	"reflect"
)

type RawMessageInfo struct {
	msgType    reflect.Type
	msgHandler RawMessageHandler
}

type RawMessageHandler func(clientId string,packType uint16,msg []byte)
type RawConnectHandler func(clientId string)
type UnknownRawMessageHandler func(clientId string,msg []byte)

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
func (pbRawProcessor *PBRawProcessor ) MsgRoute(clientId string, msg interface{},recyclerReaderBytes func(data []byte)) error{
	pPackInfo := msg.(*PBRawPackInfo)
	pbRawProcessor.msgHandler(clientId,pPackInfo.typ,pPackInfo.rawMsg)
	recyclerReaderBytes(pPackInfo.rawMsg)
	
	return nil
}

// must goroutine safe
func (pbRawProcessor *PBRawProcessor ) Unmarshal(clientId string,data []byte) (interface{}, error) {
	var msgType uint16
	if pbRawProcessor.LittleEndian == true {
		msgType = binary.LittleEndian.Uint16(data[:2])
	}else{
		msgType = binary.BigEndian.Uint16(data[:2])
	}

	return &PBRawPackInfo{typ:msgType,rawMsg:data},nil
}

// must goroutine safe
func (pbRawProcessor *PBRawProcessor ) Marshal(clientId string,msg interface{}) ([]byte, error){
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
}

func (pbRawProcessor *PBRawProcessor) UnknownMsgRoute(clientId string,msg interface{},recyclerReaderBytes func(data []byte)){
	defer recyclerReaderBytes(msg.([]byte))
	if pbRawProcessor.unknownMessageHandler == nil {
		return
	}
	pbRawProcessor.unknownMessageHandler(clientId,msg.([]byte))
}

// connect event
func (pbRawProcessor *PBRawProcessor) ConnectedRoute(clientId string){
	pbRawProcessor.connectHandler(clientId)
}

func (pbRawProcessor *PBRawProcessor) DisConnectedRoute(clientId string){
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