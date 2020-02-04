package logicservice

import (
	"fmt"
	"github.com/duanhf2012/origin/Test/msgpb"
	"github.com/duanhf2012/origin/network"
	"github.com/duanhf2012/origin/sysservice"
	"github.com/golang/protobuf/proto"
	"time"

	"github.com/duanhf2012/origin/originnode"
	"github.com/duanhf2012/origin/service"
)

type SubNet1_Service struct {
	service.BaseService
}

func init() {
	originnode.InitService(&SubNet1_Service{})
}

//OnInit ...
func (ws *SubNet1_Service) OnInit() error {
	sysservice.GetTcpSocketPbService("ls").RegConnectEvent(ws.ConnEventHandler)
	sysservice.GetTcpSocketPbService("ls").RegDisconnectEvent(ws.DisconnEventHandler)
	sysservice.GetTcpSocketPbService("ls").RegExceptMessage(ws.ExceptMessage)
	sysservice.GetTcpSocketPbService("ls").RegMessage(110, &msgpb.Test{}, ws.MessageHandler)

	/*
	sysservice.GetTcpSocketPbService("lc").RegConnectEvent(ws.ConnEventHandler2)
	sysservice.GetTcpSocketPbService("lc").RegDisconnectEvent(ws.DisconnEventHandler2)
	sysservice.GetTcpSocketPbService("lc").RegExceptMessage(ws.ExceptMessage2)
	sysservice.GetTcpSocketPbService("lc").RegMessage(110, &msgpb.Test{}, ws.MessageHandler2)
*/

	return nil
}

//OnRun ...
func (ws *SubNet1_Service) OnRun() bool {


	time.Sleep(time.Second * 10)
	var cli network.TcpSocketClient
	cli.Connect("127.0.0.1:9004")
	test := msgpb.Test{}
	test.AssistCount = proto.Int32(343)

	cli.SendMsg(110, &test)
	cli.SendMsg(110, &test)
	return false
}

func (ws *SubNet1_Service) MessageHandler(pClient *network.SClient, msgtype uint16, msg proto.Message) {
	fmt.Print("recv:",pClient.GetId(), "：", msg,"\n")
	pClient.SendMsg(msgtype,msg)

	var a map[int]int
	a[33] = 3
	fmt.Print(a[44])
}

func (ws *SubNet1_Service) ConnEventHandler(pClient *network.SClient) {
	fmt.Print("connected..",pClient.GetId(),"\n")

}

func (ws *SubNet1_Service) DisconnEventHandler(pClient *network.SClient) {
	fmt.Print("disconnected..",pClient.GetId(),"\n")
}

func (ws *SubNet1_Service) ExceptMessage(pClient *network.SClient, pPack *network.MsgBasePack, err error) {
	fmt.Print("except..",pClient.GetId(),"，",pPack,"\n")
}



///////////////////////////

func (ws *SubNet1_Service) MessageHandler2(pClient *network.SClient, msgtype uint16, msg proto.Message) {
	fmt.Print("recv:",pClient.GetId(), "：", msg,"\n")
	pClient.SendMsg(msgtype,msg)
}

func (ws *SubNet1_Service) ConnEventHandler2(pClient *network.SClient) {
	fmt.Print("connected..",pClient.GetId(),"\n")
}

func (ws *SubNet1_Service) DisconnEventHandler2(pClient *network.SClient) {
	fmt.Print("disconnected..",pClient.GetId(),"\n")
}

func (ws *SubNet1_Service) ExceptMessage2(pClient *network.SClient, pPack *network.MsgBasePack, err error) {
	fmt.Print("except..",pClient.GetId(),"，",pPack,"\n")
}