# Origin
全分布式服务器引擎
1.添加服务
Test.go

package Test

import (
	"fmt"
	"os"
	"time"

	"github.com/duanhf2012/origin/cluster"
	"github.com/duanhf2012/origin/service"
)

type CTest struct {
	service.BaseService
	tmp int
}

type TestModule struct {
	service.BaseModule
}

func (ws *TestModule) OnInit() error {
	return nil
}

func (ws *TestModule) OnRun() error {
	return nil
}

func (ws *CTest) OnInit() error {

	//添加模块
	test := &TestModule{}
	ws.AddModule(test, true)
	
	//获取模块
	pModule := ws.GetModule("TestModule")
	fmt.Print(pModule)
	return nil
}


type CTestData struct {
	Bbbb int64
	Cccc int
	Ddd  string
}

func (ws *CTest) RPC_LogTicker2(args *CTestData, quo *CTestData) error {

	*quo = *args
	return nil
}

func (ws *CTest) OnRun() error {

	ws.tmp = ws.tmp + 1
	time.Sleep(1 * time.Second)
	if ws.tmp%10 == 0 {
		var test CTestData
		test.Bbbb = 1111
		test.Cccc = 111
		test.Ddd = "1111"
		err := cluster.Go("collectTickLogService.RPC_LogTicker2", &test)
		fmt.Print(err)
	}

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
2.使用服务
main.go
package main

import (
	"Test"
	"bytes"
	"compress/flate"
	"fmt"
	"io/ioutil"

	"github.com/duanhf2012/origin/server"
	"github.com/duanhf2012/origin/sysmodule"
)


func main() {
	server := server.NewServer()
	if server == nil {
		return
	}

	test := Test.NewCTest(1000)
	server.SetupService(test)

	server.Init()
	server.Start()
}



