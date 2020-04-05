package main

import (
	"fmt"
	"github.com/duanhf2012/origin/example/GateService"
	"github.com/duanhf2012/origin/log"
	"github.com/duanhf2012/origin/node"
	"github.com/duanhf2012/origin/service"
	"github.com/duanhf2012/origin/sysmodule"
	"github.com/duanhf2012/origin/sysservice"
	"time"
)

type TestService1 struct {
	service.Service
}

type TestService2 struct {
	service.Service
}

type TestServiceCall struct {
	service.Service
	dbModule sysmodule.DBModule
	param *Param
}

func init(){
	node.Setup(&TestService1{},&TestService2{},&TestServiceCall{param:&Param{}})
}

type Module1 struct{
	service.Module
}

type Module2 struct{
	service.Module
}

type Module3 struct{
	service.Module
}

type Module4 struct{
	service.Module
}
var moduleid1 int64
var moduleid2 int64
var moduleid3 int64
var moduleid4 int64

func (slf *Module1) OnInit() error {
	fmt.Printf("I'm Module1:%d\n",slf.GetModuleId())
	return nil
}

func (slf *Module2) OnInit() error {
	fmt.Printf("I'm Module2:%d\n",slf.GetModuleId())
	moduleid3,_ = slf.AddModule(&Module3{})
	return nil
}
func (slf *Module3) OnInit() error {
	fmt.Printf("I'm Module3:%d\n",slf.GetModuleId())
	moduleid4,_ = slf.AddModule(&Module4{})

	return nil
}

func (slf *Module4) OnInit() error {
	fmt.Printf("I'm Module4:%d\n",slf.GetModuleId())
	//pService := slf.GetService().(*TestServiceCall)
	//pService.RPC_Test(nil,nil)
	slf.AfterFunc(time.Second*10,slf.TimerTest)
	return nil
}

func (slf *Module4) TimerTest(){
	fmt.Printf("Module4 tigger timer\n")
}

func (slf *Module1) OnRelease() {
	fmt.Printf("Release Module1:%d\n",slf.GetModuleId())
}
func (slf *Module2) OnRelease() {
	fmt.Printf("Release Module2:%d\n",slf.GetModuleId())
}
func (slf *Module3) OnRelease() {
	fmt.Printf("Release Module3:%d\n",slf.GetModuleId())
}
func (slf *Module4) OnRelease() {
	fmt.Printf("Release Module4:%d\n",slf.GetModuleId())
}

func (slf *TestServiceCall) OnInit() error {
	slf.OpenProfiler()

	//slf.AfterFunc(time.Second*1,slf.Run)
	slf.AfterFunc(time.Second*1,slf.Test)
	moduleid1,_ = slf.AddModule(&Module1{})
	moduleid2,_ = slf.AddModule(&Module2{})
	fmt.Print(moduleid1,moduleid2)

	slf.dbModule = sysmodule.DBModule{}
	slf.dbModule.Init(10,3, "192.168.0.5:3306", "root", "Root!!2018", "Matrix")
	slf.dbModule.SetQuerySlowTime(time.Second * 3)
	slf.AddModule(&slf.dbModule)

	slf.AfterFunc(time.Second*5,slf.Release)
	slf.AfterFunc(time.Second, slf.TestDB)
	return nil
}

func  (slf *TestServiceCall) Release(){
	slf.ReleaseModule(moduleid1)
	slf.ReleaseModule(moduleid2)
}


type Param struct {
	Index int
	A int
	B string
	Pa []string
}

var index int
func (slf *TestServiceCall) Test(){
	//any := slf.GetProfiler().Push("xxxxxx")
	//defer any.Pop()
	for{
		time.Sleep(time.Second*1)
	}

	index += 1
	//var param *Param
	param:=&Param{}
	param.A = 2342342341
	param.B = "xxxxxxxxxxxxxxxxxxxxxxx"
	param.Pa = []string{"ccccc","asfsdfsdaf","bbadfsdf","ewrwefasdf","safsadfka;fksd"}
	param.Index = index
/*
	slf.AsyncCall("TestService1.RPC_Test1",&param, func(reply *Param, err error) {
		fmt.Print(reply,"\n")
	})

 */
	slf.Go("TestService1.RPC_Test",&slf.param)
	//slf.AfterFunc(time.Second*1,slf.Test)
}
func  (slf *TestServiceCall) OnRelease(){
	fmt.Print("OnRelease")
}

func  (slf *TestServiceCall) Run(){
	//var ret int
	var input int = 10000
	bT := time.Now()            // 开始时间

	//err := slf.Call("TestServiceCall.RPC_Test",&ret,&input)
	for i:=input;i>=0;i--{
		var param Param
		param.A = 2342342341
		param.B = "xxxxxxxxxxxxxxxxxxxxxxx"
		//param.Pa = []string{"ccccc","asfsdfsdaf","bbadfsdf","ewrwefasdf","safsadfka;fksd"}
		param.Index = i
		if param.Index == 0 {
			fmt.Print(".......................\n")
		}
		err := slf.AsyncCall("TestService1.RPC_Test",&param, func(reply *Param, err error) {
			log.Debug(" index %d ,err %+v",reply.Index,err)
			if reply.Index == 0 {
				eT := time.Since(bT)      // 从开始到当前所消耗的时间
				fmt.Print(err,eT.Milliseconds())
				fmt.Print("xxxx..................",eT,err,"\n")
			}
		})
		if err != nil {
			fmt.Printf("x333333333333:%+v",err)
		}
	}

	fmt.Print("finsh....")
}

func (slf *TestService1) RPC_Test(a *Param,b *Param) error {
	//*a = *b
	//a = nil
	*b = *a

	return nil
}

func (slf *TestService1) OnInit() error {
	slf.OpenProfiler()
	return nil
}
/*
func (slf *TestServiceCall) RPC_Test(a *int,b *int) error {
	fmt.Printf("TestService2\n")
	*a = *b
	return nil
}
*/
func (slf *TestServiceCall) TestDB() {
	assetsInfo := &struct {
		Cash  int64 `json:"cash"`  //美金余额 100
		Gold  int64 `json:"gold"`  //金币余额
		Heart int64 `json:"heart"` //心数
	}{}
	sql := `call sp_select_userAssets(?)`
	userID := 100000802
	err := slf.dbModule.AsyncQuery(func(dataList *sysmodule.DataSetList, err error) {
		if err != nil {
			return
		}
		err = dataList.UnMarshal(assetsInfo)
		if err != nil {
			return
		}
	},-1, sql, &userID)

	fmt.Println(err)
}

func (slf *TestService2) OnInit() error {
	slf.OpenProfiler()
	return nil
}


func main(){

	//data := P{3, 4, 5, "CloudGeek"}
	//buf := encode(data)

	tcpService := &sysservice.TcpService{}
	gateService := &GateService.GateService{}
	tcpService.SetEventReciver(gateService)
	node.Setup(tcpService,gateService)
	node.OpenProfilerReport(time.Second*10)
	node.Start()
}


