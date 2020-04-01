package processor

import (
	"encoding/binary"
	"fmt"
	"github.com/golang/protobuf/proto"
	"reflect"
)

type MessageInfo struct {
	msgType    reflect.Type
	msgHandler MessageHandler
}

type MessageHandler func(clientid uint64,msg proto.Message)
const MsgTypeSize = 2
type PBProcessor struct {
	mapMsg map[uint16]MessageInfo
	LittleEndian bool
}

func NewPBProcessor() *PBProcessor {
	processor := &PBProcessor{mapMsg:map[uint16]MessageInfo{}}
	return processor
}

func (p *PBProcessor) SetByteOrder(littleEndian bool) {
	p.LittleEndian = littleEndian
}


type PBPackInfo struct {
	typ uint16
	msg proto.Message
}

// must goroutine safe
func (slf *PBProcessor ) Route(msg interface{},userdata interface{}) error{
	pPackInfo := msg.(*PBPackInfo)
	v,ok := slf.mapMsg[pPackInfo.typ]
	if ok == false {
		return fmt.Errorf("cannot find msgtype %d is register!",pPackInfo.typ)
	}


	v.msgHandler(userdata.(uint64),pPackInfo.msg)
	return nil
}

// must goroutine safe
func (slf *PBProcessor ) Unmarshal(data []byte) (interface{}, error) {
	var msgType uint16
	if slf.LittleEndian == true {
		msgType = binary.LittleEndian.Uint16(data[:2])
	}else{
		msgType = binary.BigEndian.Uint16(data[:2])
	}

	info,ok := slf.mapMsg[msgType]
	if ok == false {
		return nil,fmt.Errorf("cannot find register %d msgtype!",msgType)
	}
	msg := reflect.New(info.msgType.Elem()).Interface()
	protoMsg := msg.(proto.Message)
	err := proto.Unmarshal(data[2:], protoMsg)
	if err != nil {
		return nil,err
	}

	return &PBPackInfo{typ:msgType,msg:protoMsg},nil
}

// must goroutine safe
func (slf *PBProcessor ) Marshal(msg interface{}) ([]byte, error){
	pMsg := msg.(*PBPackInfo)

	bytes,err := proto.Marshal(pMsg.msg.(proto.Message))
	if err != nil {
		return nil,err
	}

	buff := make([]byte, 2, len(bytes)+MsgTypeSize)
	if slf.LittleEndian == true {
		binary.LittleEndian.PutUint16(buff[:2],pMsg.typ)
	}else{
		binary.BigEndian.PutUint16(buff[:2],pMsg.typ)
	}

	buff = append(buff,bytes...)
	return buff,nil
}

func (slf *PBProcessor) Register(msgtype uint16,msg proto.Message,handle MessageHandler)  {
	var info MessageInfo

	info.msgType = reflect.TypeOf(msg.(proto.Message))
	info.msgHandler = handle
	slf.mapMsg[msgtype] = info
}

func (slf *PBProcessor) MakeMsg(msgType uint16,protoMsg proto.Message) *PBPackInfo {
	return &PBPackInfo{typ:msgType,msg:protoMsg}
}
