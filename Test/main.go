package main

import (
	"fmt"
	"time"

	"github.com/duanhf2012/origin/cluster"

	"github.com/duanhf2012/origin/sysservice"

	"github.com/duanhf2012/origin/originnode"
	"github.com/duanhf2012/origin/service"
)

type CTestService1 struct {
	service.BaseService
}

func (slf *CTestService1) OnInit() error {
	fmt.Println("CTestService1.OnInit")
	return nil
}

//RPC回调函数
type InputData struct {
	A1 int
	A2 int
}

func (slf *CTestService1) OnRun() bool {
	fmt.Println("CTestService1.OnRun")
	var ret int
	input := InputData{100, 11}
	err := cluster.Call("CTestService2.RPC_Add", &input, &ret)
	fmt.Print(err, "\n", ret, "\n")

	return false
}

func (slf *CTestService1) OnEndRun() {
	fmt.Println("CTestService1.OnEndRun")
}

//所有的服务，如果想对外提供HTTP服务，接口规范必需为以下格式，
//HTTP_MethodName(request *sysservice.HttpRequest, resp *sysservice.HttpRespone) error
//访问方式：http://127.0.0.1:9120/ServiceName/MethodName
//则以下接口访问方式：https://proxy.atbc.com:9120/CTestService1/GetInfo
func (slf *CTestService1) HTTP_GetInfo(request *sysservice.HttpRequest, resp *sysservice.HttpRespone) error {
	strRet := "{a:\"hello,world!\"}"
	resp.Respone = []byte(strRet)
	return nil
}

type CTestService2 struct {
	service.BaseService
}

func (slf *CTestService2) OnInit() error {
	fmt.Println("CTestService2.OnInit")
	return nil
}

func (slf *CTestService2) OnRun() bool {
	fmt.Println("CTestService2.OnRun")
	time.Sleep(time.Second * 5)
	return true
}

func (slf *CTestService2) OnEndRun() {
	fmt.Println("CTestService2.OnEndRun")
}

//服务要对外的接口规划如下：
//RPC_MethodName(arg *DataType1, ret *DataType2) error
//如果不符合规范，在加载服务时，该函数将不会被映射，其他服务将不允能调用。
func (slf *CTestService2) RPC_Add(arg *InputData, ret *int) error {

	*ret = arg.A1 + arg.A2

	return nil
}

func main() {
	node := originnode.NewOrginNode()
	if node == nil {
		return
	}

	//1.新增http服务
	//该服务比较特殊，安装加载完成后，会将所有的service符合规范的HTTP_接口映射
	//将能被外部调用
	httpserver := sysservice.NewHttpServerService(9120)

	//2.新建websocket服务

	//新建websocket消息回调服务，网络i/o发生事件时回调OnConnected,OnDisconnect,OnRecvMsg接口。
	wss := NewWebSockService()

	//新建websocket监听服务
	pWS := sysservice.NewWSServerService(9121)
	//设置以下证书文件，支持https
	//pWS.SetWSS("/root/1884337_proxy.atbc.com.pem", "/root/1884337_proxy.atbc.com.key")

	//将回调服务安装到websocket监听服务中
	pWS.SetupReciver("/wss", wss, false)

	//设置以下证书，支持wss
	//httpserver.SetHttps("/root/1884337_proxy.atbc.com.pem", "/root/1884337_proxy.atbc.com.key")
	node.SetupService(&CTestService1{}, &CTestService2{}, httpserver, wss, pWS)
	node.Init()
	node.Start()
}
