package processor

import (
	"encoding/binary"
	"fmt"
	"github.com/duanhf2012/origin/network"
	"github.com/gogo/protobuf/proto"
	"reflect"
)

type MessageInfo struct {
	msgType    reflect.Type
	msgHandler MessageHandler
}

type MessageHandler func(clientId uint64, msg proto.Message)
type ConnectHandler func(clientId uint64)
type UnknownMessageHandler func(clientId uint64, msg []byte)

const MsgTypeSize = 2

type PBProcessor struct {
	mapMsg       map[uint16]MessageInfo
	LittleEndian bool

	unknownMessageHandler UnknownMessageHandler
	connectHandler        ConnectHandler
	disconnectHandler     ConnectHandler
	network.INetMempool
}

type PBPackInfo struct {
	typ    uint16
	msg    proto.Message
	rawMsg []byte
}

func NewPBProcessor() *PBProcessor {
	processor := &PBProcessor{mapMsg: map[uint16]MessageInfo{}}
	processor.INetMempool = network.NewMemAreaPool()
	return processor
}

func (pbProcessor *PBProcessor) SetByteOrder(littleEndian bool) {
	pbProcessor.LittleEndian = littleEndian
}

func (slf *PBPackInfo) GetPackType() uint16 {
	return slf.typ
}

func (slf *PBPackInfo) GetMsg() proto.Message {
	return slf.msg
}

// must goroutine safe
func (pbProcessor *PBProcessor) MsgRoute(clientId uint64, msg interface{}) error {
	pPackInfo := msg.(*PBPackInfo)
	v, ok := pbProcessor.mapMsg[pPackInfo.typ]
	if ok == false {
		return fmt.Errorf("Cannot find msgtype %d is register!", pPackInfo.typ)
	}

	v.msgHandler(clientId, pPackInfo.msg)
	return nil
}

// must goroutine safe
func (pbProcessor *PBProcessor) Unmarshal(clientId uint64, data []byte) (interface{}, error) {
	defer pbProcessor.ReleaseByteSlice(data)
	return pbProcessor.UnmarshalWithOutRelease(clientId, data)
}

// unmarshal but not release data
func (pbProcessor *PBProcessor) UnmarshalWithOutRelease(clientId uint64, data []byte) (interface{}, error) {
	var msgType uint16
	if pbProcessor.LittleEndian == true {
		msgType = binary.LittleEndian.Uint16(data[:2])
	} else {
		msgType = binary.BigEndian.Uint16(data[:2])
	}

	info, ok := pbProcessor.mapMsg[msgType]
	if ok == false {
		return nil, fmt.Errorf("cannot find register %d msgtype!", msgType)
	}
	msg := reflect.New(info.msgType.Elem()).Interface()
	protoMsg := msg.(proto.Message)
	err := proto.Unmarshal(data[2:], protoMsg)
	if err != nil {
		return nil, err
	}

	return &PBPackInfo{typ: msgType, msg: protoMsg}, nil
}

// must goroutine safe
func (pbProcessor *PBProcessor) Marshal(clientId uint64, msg interface{}) ([]byte, error) {
	pMsg := msg.(*PBPackInfo)

	var err error
	if pMsg.msg != nil {
		pMsg.rawMsg, err = proto.Marshal(pMsg.msg)
		if err != nil {
			return nil, err
		}
	}

	buff := make([]byte, 2, len(pMsg.rawMsg)+MsgTypeSize)
	if pbProcessor.LittleEndian == true {
		binary.LittleEndian.PutUint16(buff[:2], pMsg.typ)
	} else {
		binary.BigEndian.PutUint16(buff[:2], pMsg.typ)
	}

	buff = append(buff, pMsg.rawMsg...)
	return buff, nil
}

func (pbProcessor *PBProcessor) Register(msgtype uint16, msg proto.Message, handle MessageHandler) {
	var info MessageInfo

	info.msgType = reflect.TypeOf(msg.(proto.Message))
	info.msgHandler = handle
	pbProcessor.mapMsg[msgtype] = info
}

func (pbProcessor *PBProcessor) MakeMsg(msgType uint16, protoMsg proto.Message) *PBPackInfo {
	return &PBPackInfo{typ: msgType, msg: protoMsg}
}

func (pbProcessor *PBProcessor) MakeRawMsg(msgType uint16, msg []byte) *PBPackInfo {
	return &PBPackInfo{typ: msgType, rawMsg: msg}
}

func (pbProcessor *PBProcessor) UnknownMsgRoute(clientId uint64, msg interface{}) {
	pbProcessor.unknownMessageHandler(clientId, msg.([]byte))
}

// connect event
func (pbProcessor *PBProcessor) ConnectedRoute(clientId uint64) {
	pbProcessor.connectHandler(clientId)
}

func (pbProcessor *PBProcessor) DisConnectedRoute(clientId uint64) {
	pbProcessor.disconnectHandler(clientId)
}

func (pbProcessor *PBProcessor) RegisterUnknownMsg(unknownMessageHandler UnknownMessageHandler) {
	pbProcessor.unknownMessageHandler = unknownMessageHandler
}

func (pbProcessor *PBProcessor) RegisterConnected(connectHandler ConnectHandler) {
	pbProcessor.connectHandler = connectHandler
}

func (pbProcessor *PBProcessor) RegisterDisConnected(disconnectHandler ConnectHandler) {
	pbProcessor.disconnectHandler = disconnectHandler
}
