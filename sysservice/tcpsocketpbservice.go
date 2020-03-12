package sysservice

import (
	"errors"
	"github.com/duanhf2012/origin/network"
	"github.com/duanhf2012/origin/service"
	"github.com/golang/protobuf/proto"
	"reflect"
)

type TcpSocketPbService struct {
	service.BaseService
	listenaddr string
	tcpsocketserver network.TcpSocketServer
	MsgProcessor
}


type MessageHandler func(clientid uint64,msgtype uint16,msg proto.Message)
type MessageRecvHandler func(pClient *network.SClient,pPack *network.MsgBasePack)
type EventHandler func(clientid uint64)
type ExceptMsgHandler func(clientid uint64,pPack *network.MsgBasePack,err error)

type MsgProcessor struct {
	mapMsg map[uint16]MessageInfo
	connEvent EventHandler
	disconnEvent EventHandler
	exceptMsgHandler ExceptMsgHandler
	messageRecvHandler MessageRecvHandler
}




func (slf *MsgProcessor) RegMessage(msgtype uint16,msg proto.Message,handle MessageHandler){
	var info MessageInfo

	info.msgType = reflect.TypeOf(msg.(proto.Message))
	info.msgHandler = handle
	slf.mapMsg[msgtype] = info
}

func (slf *MsgProcessor) RegConnectEvent(eventHandler EventHandler){
	slf.connEvent = eventHandler
}

func (slf *MsgProcessor) RegDisconnectEvent(eventHandler EventHandler){
	slf.disconnEvent = eventHandler
}

func (slf *MsgProcessor) RegExceptMessage(exceptMsgHandler ExceptMsgHandler){
	slf.exceptMsgHandler = exceptMsgHandler
}

func (slf *MsgProcessor) RegRecvMessage(msgHandler MessageRecvHandler){
	slf.messageRecvHandler = msgHandler
}

func (slf *MsgProcessor) OnExceptMsg (pClient *network.SClient,pPack *network.MsgBasePack,err error){
	if slf.exceptMsgHandler!=nil {
		slf.exceptMsgHandler(pClient.GetId(),pPack,err)
	}else{
		pClient.Close()
		//记录日志
		service.GetLogger().Printf(service.LEVER_WARN, "OnExceptMsg packtype %d,error %+v",pPack.PackType,err)
	}
}

func NewTcpSocketPbService(listenaddr string) *TcpSocketPbService {
	ts := new(TcpSocketPbService)

	ts.listenaddr = listenaddr
	ts.mapMsg = make(map[uint16]MessageInfo,1)
	ts.tcpsocketserver.Register(listenaddr,ts)
	return ts
}

func (slf *TcpSocketPbService) OnInit() error {
	return nil
}

func (slf *TcpSocketPbService) OnRun() bool {
	slf.tcpsocketserver.Start()

	return false
}


type MessageInfo struct {
	msgType    reflect.Type
	msgHandler MessageHandler
}


func (slf *TcpSocketPbService) RegMessage(msgtype uint16,msg proto.Message,handle MessageHandler){
	var info MessageInfo

	info.msgType = reflect.TypeOf(msg.(proto.Message))
	info.msgHandler = handle
	slf.mapMsg[msgtype] = info
}


func (slf *TcpSocketPbService) OnConnected(pClient *network.SClient){
	if slf.connEvent!=nil {
		slf.connEvent(pClient.GetId())
	}
}

func (slf *TcpSocketPbService) OnDisconnect(pClient *network.SClient){
	if slf.disconnEvent!=nil {
		slf.disconnEvent(pClient.GetId())
	}
}
func (slf *TcpSocketPbService) VerifyPackType(packtype uint16) bool{
	_,ok := slf.mapMsg[packtype]
	return ok
}

func (slf *MsgProcessor) Handle(pClient *network.SClient,pPack *network.MsgBasePack){
	if info, ok := slf.mapMsg[pPack.PackType]; ok {
		msg := reflect.New(info.msgType.Elem()).Interface()
		tmp := msg.(proto.Message)
		err := proto.Unmarshal(pPack.Body, tmp)
		if err != nil {
			slf.OnExceptMsg(pClient,pPack,err)
			return
		}

		info.msgHandler(pClient.GetId(),pPack.PackType, msg.(proto.Message))
		return
	}else if slf.messageRecvHandler!=nil {
		slf.messageRecvHandler(pClient,pPack)
		return
	}

	slf.OnExceptMsg(pClient,pPack,errors.New("not found PackType"))
}


func (slf *TcpSocketPbService) OnRecvMsg(pClient *network.SClient, pPack *network.MsgBasePack){
	slf.Handle(pClient,pPack)
}

func DefaultTSPbService() *TcpSocketPbService{
	iservice := service.InstanceServiceMgr().FindService("TcpSocketPbService")
	if iservice == nil {
		return  nil
	}

	return iservice.(*TcpSocketPbService)
}

func GetTcpSocketPbService(serviceName string) *TcpSocketPbService{
	iservice :=  service.InstanceServiceMgr().FindService(serviceName)
	if iservice == nil {
		return  nil
	}

	return iservice.(*TcpSocketPbService)
}

func (slf *TcpSocketPbService)  SendMsg(clientid uint64,packtype uint16,message proto.Message) error{
	return slf.tcpsocketserver.SendMsg(clientid,packtype,message)
}

func (slf *TcpSocketPbService)  Close(clientid uint64) error{
	return slf.tcpsocketserver.Close(clientid)
}

func (slf *TcpSocketPbService) Send(clientid uint64,pack *network.MsgBasePack) error {
	return slf.tcpsocketserver.Send(clientid,pack)
}
