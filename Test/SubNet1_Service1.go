package main

import (
	"fmt"

	"github.com/duanhf2012/origin/originnode"
	"github.com/duanhf2012/origin/service"
	"github.com/duanhf2012/origin/sysservice/originhttp"
)

type InputData struct {
	A1 int
	A2 int
}

type SubNet1_Service1 struct {
	service.BaseService
}

func init() {
	originnode.InitService(&SubNet1_Service1{})
}

//OnInit ...
func (ws *SubNet1_Service1) OnInit() error {
	originhttp.Post("", ws.HTTP_UserIntegralInfo)
	originhttp.Post(" /aaa/bbb", ws.Test)
	originhttp.Get("/Login/bbb", ws.HTTP_UserIntegralInfo)
	originhttp.SetStaticResource(originhttp.METHOD_GET, "/file/", "d:\\")

	return nil
}

//OnRun ...
func (ws *SubNet1_Service1) OnRun() bool {
	return false
}

//服务要对外的接口规划如下：
//RPC_MethodName(arg *DataType1, ret *DataType2) error
//如果不符合规范，在加载服务时，该函数将不会被映射，其他服务将不允能调用。
func (slf *SubNet1_Service1) RPC_Add(arg *InputData, ret *int) error {
	*ret = arg.A1 + arg.A2
	return nil
}

func (slf *SubNet1_Service1) Test(request *originhttp.HttpRequest, resp *originhttp.HttpRespone) error {
	return nil
}

func (slf *SubNet1_Service1) HTTP_UserIntegralInfo(request *originhttp.HttpRequest, resp *originhttp.HttpRespone) error {
	ret, ok := request.Query("a")
	fmt.Print(ret, ok)
	return nil
}
