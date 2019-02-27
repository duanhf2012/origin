package main

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/duanhf2012/origin/sysmodule"

	"github.com/duanhf2012/origin/originnode"
	"github.com/duanhf2012/origin/service"
	"github.com/duanhf2012/origin/sysservice"
)

//子模块
type CTestModule struct {
	service.BaseModule
}

func (ws *CTestModule) DoSomething() {
	fmt.Printf("CTestModule do some thing!\n")
}

func (ws *CTestModule) OnInit() error {
	fmt.Printf("CTestModule.OnInit\n")
	return nil
}

func (ws *CTestModule) OnRun() bool {
	time.Sleep(2 * time.Second)
	fmt.Printf("CTestModule.OnRun\n")
	return true
}

func (ws *CTestModule) OnEndRun() {
	fmt.Printf("CTestModule.OnEndRun\n")
}

//CTest服务定义
type CTest struct {
	service.BaseService
	tmp int
}

func (ws *CTest) OnInit() error {
	fmt.Printf("CTest.OnInit\n")

	testModule := CTestModule{}
	moduleId := ws.AddModule(&testModule)
	pTmpModule := ws.GetModuleById(moduleId)
	pTmpModuleTest := pTmpModule.(*CTestModule)
	pTmpModuleTest.DoSomething()

	return nil
}

func (ws *CTest) OnRun() bool {

	ws.tmp = ws.tmp + 1
	time.Sleep(1 * time.Second)
	logic := service.InstanceServiceMgr().FindService("logiclog")
	logic.(sysmodule.ILogger).Printf(sysmodule.LEVER_DEBUG, "CTest.OnRun\n")
	fmt.Printf("CTest.OnRun\n")
	/*
		//if ws.tmp%10 == 0 {
		var test CTestData
		test.Bbbb = 1111
		test.Cccc = 111
		test.Ddd = "1111"
		var test2 CTestData
		err := cluster.Call("_CTest.RPC_LogTicker2\n", &test, &test2)
		fmt.Print(err, test2)
		//}

		//模块的示例
		testModule := CTestModule{}
		moduleId := ws.AddModule(&testModule)
		pTmpModule := ws.GetModuleById(moduleId)
		pTmpModuleTest := pTmpModule.(*CTestModule)
		pTmpModuleTest.DoSomething()
		pservice := testModule.GetOwnerService()
		fmt.Printf("%T", pservice)
	*/
	return true
}

func (ws *CTest) OnEndRun() {
	fmt.Printf("CTest.OnEndRun\n")
}

func (ws *CTest) RPC_LogTicker2(args *CTestData, quo *CTestData) error {

	*quo = *args
	return nil
}

type CTestData struct {
	Bbbb int64
	Cccc int
	Ddd  string
}

func (ws *CTest) HTTP_LogTicker2(request *sysservice.HttpRequest, resp *sysservice.HttpRespone) error {

	data := CTestData{111, 333, "34444"}
	resp.Respone, _ = json.Marshal(&data)
	return nil
}

func main() {
	node := originnode.NewOrginNode()
	if node == nil {
		return
	}

	test := CTest{}
	logiclogservice := &sysservice.LogService{}
	logiclogservice.InitLog("logiclog", sysmodule.LEVER_DEBUG)

	node.SetupService(logiclogservice, &test)

	node.Init()
	node.Start()
}
