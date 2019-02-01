package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/duanhf2012/origin/cluster"
	"github.com/duanhf2012/origin/network"
	"github.com/duanhf2012/origin/originnode"
	"github.com/duanhf2012/origin/service"
	"github.com/duanhf2012/origin/sysservice"

	"github.com/gorilla/websocket"
)

type CMessageReceiver struct {
}

func (slf *CMessageReceiver) OnConnected(webServer network.IWebsocketServer, clientid uint64) {
	fmt.Printf("%d\n", clientid)
}

func (slf *CMessageReceiver) OnDisconnect(webServer network.IWebsocketServer, clientid uint64, err error) {
	fmt.Printf("%d\n", clientid)
	fmt.Print(err)
}

func (slf *CMessageReceiver) OnRecvMsg(webServer network.IWebsocketServer, clientid uint64, msgtype int, data []byte) {
	fmt.Printf("%d,%d\n", clientid, msgtype)
	fmt.Print(string(data))

	webServer.SendMsg(clientid, websocket.TextMessage, data)
}

func Test(res http.ResponseWriter, req *http.Request) {
	io.WriteString(res, "test..........!\n")
}

type CTest struct {
	service.BaseService
	tmp int
}

func (ws *CTest) OnInit() error {

	return nil
}

type CTestData struct {
	Bbbb int64
	Cccc int
	Ddd  string
}

type CTestModule struct {
	service.BaseModule
}

func (ws *CTestModule) DoSomething() {
	fmt.Printf("CTestModule do some thing!")
}

func (ws *CTest) RPC_LogTicker2(args *CTestData, quo *CTestData) error {

	*quo = *args
	return nil
}

func (ws *CTest) HTTP_LogTicker2(request *sysservice.HttpRequest, resp *sysservice.HttpRespone) error {

	data := CTestData{111, 333, "34444"}
	resp.Respone, _ = json.Marshal(&data)
	return nil
}

func (ws *CTest) OnRun() error {

	ws.tmp = ws.tmp + 1
	time.Sleep(1 * time.Second)
	//if ws.tmp%10 == 0 {
	var test CTestData
	test.Bbbb = 1111
	test.Cccc = 111
	test.Ddd = "1111"
	var test2 CTestData
	err := cluster.Call("_CTest.RPC_LogTicker2", &test, &test2)
	fmt.Print(err, test2)
	//}

	//模块的示例
	testModule := CTestModule{}
	testModule.SetModuleType(1)
	ws.AddModule(&testModule)

	pTmpModule := ws.GetModuleByType(1)
	pTmpModuleTest := pTmpModule.(*CTestModule)
	pTmpModuleTest.DoSomething()
	pservice := testModule.GetOwnerService()
	fmt.Printf("%T", pservice)
	return nil
}

func NewCTest(servicetype int) *CTest {
	wss := new(CTest)
	wss.Init(wss, servicetype)
	return wss
}

func checkFileIsExist(filename string) bool {
	var exist = true
	if _, err := os.Stat(filename); os.IsNotExist(err) {
		exist = false
	}
	return exist
}

func (ws *CTest) OnDestory() error {
	return nil
}

func main() {
	node := originnode.NewOrginNode()
	if node == nil {
		return
	}

	var module CTestModule
	module.SetModuleType(1)
	originnode.AddModule(&module)
	ptest := originnode.GetModuleByType(1)
	fmt.Print(ptest)

	var receiver CMessageReceiver
	wsservice := sysservice.NewWSServerService("/ws", 1314, &receiver, false)
	test := NewCTest(0)
	httpserver := sysservice.NewHttpServerService(9120)
	node.SetupService(test, httpserver, wsservice)

	node.Init()
	node.Start()
}
