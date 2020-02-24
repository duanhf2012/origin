package logicservice

import (
	"fmt"
	"github.com/duanhf2012/origin/Test/msgpb"
	"github.com/duanhf2012/origin/cluster"
	"github.com/duanhf2012/origin/network"
	"github.com/duanhf2012/origin/sysservice"
	"github.com/golang/protobuf/proto"
	"time"

	"github.com/duanhf2012/origin/originnode"
	"github.com/duanhf2012/origin/service"
	"github.com/duanhf2012/origin/sysservice/originhttp"
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


	//originhttp.Post(" / aaa/bb/ :user/:pass/", ws.HTTP_UserIntegralInfo)

	originhttp.Get(" /aaa/bbb", ws.HTTP_UserIntegralInfo)
	originhttp.SetStaticResource(originhttp.METHOD_GET, "/file/", "d:\\")

	return nil
}

type InputData struct {
	A1 int
	A2 int
}

//OnRun ...
func (ws *SubNet1_Service) OnRun() bool {


	time.Sleep(time.Second * 10)
	var cli network.TcpSocketClient
	cli.Connect("127.0.0.1:9402")
	test := msgpb.Test{}
	test.AssistCount = proto.Int32(343)

	cli.SendMsg(110, &test)
	cli.SendMsg(110, &test)

	var a InputData
	var rs int
	a.A1 = 3
	a.A2 = 4
	cluster.Call("SubNet1_Service2.RPC_Sub",&a,&rs)
	fmt.Print(rs)
	return false
}

func (ws *SubNet1_Service) MessageHandler(clientid uint64, msgtype uint16, msg proto.Message) {
	fmt.Print("recv:",clientid, "：", msg,"\n")
	sysservice.GetTcpSocketPbService("ls").SendMsg(clientid,msgtype,msg)
/*test core dump
	var a map[int]int
	a[33] = 3
	fmt.Print(a[44])
 */
}

func (ws *SubNet1_Service) ConnEventHandler(clientid uint64) {
	fmt.Print("connected..",clientid,"\n")
}

func (ws *SubNet1_Service) DisconnEventHandler(clientid uint64) {
	fmt.Print("disconnected..",clientid,"\n")
}

func (ws *SubNet1_Service) ExceptMessage(clientid uint64, pPack *network.MsgBasePack, err error) {
	fmt.Print("except..",clientid,"，",pPack,"\n")
}



///////////////////////////

func (ws *SubNet1_Service) MessageHandler2(clientid uint64, msgtype uint16, msg proto.Message) {
	fmt.Print("recv:",clientid, "：", msg,"\n")
	//pClient.SendMsg(msgtype,msg)
	sysservice.GetTcpSocketPbService("ls").SendMsg(clientid,msgtype,msg)
}

func (ws *SubNet1_Service) ConnEventHandler2(clientid uint64) {
	fmt.Print("connected..",clientid,"\n")
}

func (ws *SubNet1_Service) DisconnEventHandler2(clientid uint64) {
	fmt.Print("disconnected..",clientid,"\n")
}

func (ws *SubNet1_Service) ExceptMessage2(clientid uint64, pPack *network.MsgBasePack, err error) {
	fmt.Print("except..",clientid,"，",pPack,"\n")
}


func (slf *SubNet1_Service) HTTP_UserIntegralInfo(request *originhttp.HttpRequest, resp *originhttp.HttpRespone) error {
	ret, ok := request.Query("a")
	fmt.Print(ret, ok)
	return nil
}