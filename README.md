origin 游戏服务器引擎简介
==================


origin 是一个由 Go 语言（golang）编写的分布式开源游戏服务器引擎。origin适用于各类游戏服务器的开发，包括 H5（HTML5）游戏服务器。

origin 解决的问题：
* origin总体设计如go语言设计一样，总是尽可能的提供简洁和易用的模式，快速开发。
* 能够根据业务需求快速并灵活的制定服务器架构。
* 利用多核优势，将不同的service配置到不同的node，并能高效的协同工作。
* 将整个引擎抽象三大对象，node,service,module。通过统一的组合模型管理游戏中各功能模块的关系。
* 有丰富并健壮的工具库。

Hello world!
---------------
下面我们来一步步的建立origin服务器,先下载[origin引擎](https://github.com/duanhf2012/origin "origin引擎"),或者使用如下命令：
```go
go get -v -u  github.com/duanhf2012/origin
```
于是下载到GOPATH环境目录中,在src中加入main.go,内容如下：
```go
package main

import (
	"github.com/duanhf2012/origin/node"
)

func main() {
	node.Start()
}
```
一个origin进程需要创建一个node对象,Start开始运行。您也可以直接下载origin引擎示例:
```
go get -v -u github.com/duanhf2012/originserver
```
本文所有的说明都是基于该示例为主。

origin引擎三大对象关系
---------------
* Node:   可以认为每一个Node代表着一个origin进程
* Service:一个独立的服务可以认为是一个大的功能模块，他是Node的子集，创建完成并安装Node对象中。服务可以支持对外部RPC等功能。
* Module: 这是origin最小对象单元，强烈建议所有的业务模块都划分成各个小的Module组合，origin引擎将监控所有服务与Module运行状态，例如可以监控它们的慢处理和死循环函数。Module可以建立树状关系。Service本身也是Module的类型。

origin集群核心配置文件在config的cluster目录下，在cluster下有子网目录，如github.com/duanhf2012/originserver的config/cluster目录下有subnet目录，表示子网名为subnet，可以新加多个子网的目录配置。子网与子网间是隔离的，后续将支持子网间通信规则，origin集群配置以子网的模式配置，在每个子网下配置多个Node服务器,子网在应对复杂的系统时可以应用到各个子系统，方便每个子系统的隔离。在示例的subnet目录中有cluster.json与service.json配置：

cluster.json如下：
---------------
```
{
    "NodeList":[
        {
          "NodeId": 1,
          "ListenAddr":"127.0.0.1:8001",
          "NodeName": "Node_Test1",
		  "remark":"//以_打头的，表示只在本机进程，不对整个子网开发",
          "ServiceList": ["TestService1","TestService2","TestServiceCall","GateService","_TcpService","HttpService","WSService"]
        },
		 {
          "NodeId": 2,
          "ListenAddr":"127.0.0.1:8002",
          "NodeName": "Node_Test1",
		  "remark":"//以_打头的，表示只在本机进程，不对整个子网开发",
          "ServiceList": ["TestService1","TestService2","TestServiceCall","GateService","TcpService","HttpService","WSService"]
        }
    ]
```
---------------
以上配置了两个结点服务器程序:
* NodeId: 表示origin程序的结点Id标识，不允许重复。
* ListenAddr:Rpc通信服务的监听地址
* NodeName:结点名称
* remark:备注，可选项
* ServiceList:该Node将安装的服务列表
---------------

在启动程序命令program start nodeid=1中nodeid就是根据该配置装载服务。
service.json如下：
---------------
```
{
  "Service":{
	  "HttpService":{
		"ListenAddr":"0.0.0.0:9402",
		"ReadTimeout":10000,
		"WriteTimeout":10000,
		"ProcessTimeout":10000,
		"CAFile":[
		{
			"Certfile":"",
			"Keyfile":""
		}
		]
		
	  },
	  "TcpService":{
		"ListenAddr":"0.0.0.0:9030",
		"MaxConnNum":3000,
		"PendingWriteNum":10000,
		"LittleEndian":false,
		"MinMsgLen":4,
		"MaxMsgLen":65535
	  },
	  "WSService":{
		"ListenAddr":"0.0.0.0:9031",
		"MaxConnNum":3000,
		"PendingWriteNum":10000,
		"MaxMsgLen":65535
	  }  
  },
  "NodeService":[
   {
      "NodeId":1,
	  "TcpService":{
		"ListenAddr":"0.0.0.0:9830",
		"MaxConnNum":3000,
		"PendingWriteNum":10000,
		"LittleEndian":false,
		"MinMsgLen":4,
		"MaxMsgLen":65535
	  },
	  "WSService":{
		"ListenAddr":"0.0.0.0:9031",
		"MaxConnNum":3000,
		"PendingWriteNum":10000,
		"MaxMsgLen":65535
	  }  
   },
   {
      "NodeId":2,
	  "TcpService":{
		"ListenAddr":"0.0.0.0:9030",
		"MaxConnNum":3000,
		"PendingWriteNum":10000,
		"LittleEndian":false,
		"MinMsgLen":4,
		"MaxMsgLen":65535
	  },
	  "WSService":{
		"ListenAddr":"0.0.0.0:9031",
		"MaxConnNum":3000,
		"PendingWriteNum":10000,
		"MaxMsgLen":65535
	  }  
   }
  ]
 
}
```

---------------
以上配置分为两个部分：Service与NodeService，NodeService中配置的对应结点中服务的配置，如果启动程序中根据nodeid查找该域的对应的服务，如果找不到时，从Service公共部分查找。

**HttpService配置**
* ListenAddr:Http监听地址
* ReadTimeout:读网络超时毫秒
* WriteTimeout:写网络超时毫秒
* ProcessTimeout: 处理超时毫秒
* CAFile: 证书文件，如果您的服务器通过web服务器代理配置https可以忽略该配置

**TcpService配置**
* ListenAddr: 监听地址
* MaxConnNum: 允许最大连接数
* PendingWriteNum：发送网络队列最大数量
* LittleEndian:是否小端
* MinMsgLen:包最小长度
* MaxMsgLen:包最大长度

**WSService配置**
* ListenAddr: 监听地址
* MaxConnNum: 允许最大连接数
* PendingWriteNum：发送网络队列最大数量
* MaxMsgLen:包最大长度
---------------




第一章：origin基础:
---------------
查看github.com/duanhf2012/originserver中的simple_service中新建两个服务，分别是TestService1.go与CTestService2.go。

TestService1.go如下：
```
package simple_service

import (
	"github.com/duanhf2012/origin/node"
	"github.com/duanhf2012/origin/service"
)

//模块加载时自动安装TestService1服务
func init(){
	node.Setup(&TestService1{})
}

//新建自定义服务TestService1
type TestService1 struct {

	//所有的自定义服务必需加入service.Service基服务
	//那么该自定义服务将有各种功能特性
	//例如: Rpc,事件驱动,定时器等
	service.Service
}

//服务初始化函数，在安装服务时，服务将自动调用OnInit函数
func (slf *TestService1) OnInit() error {
	return nil
}


```
TestService1.go如下：
```
import (
	"github.com/duanhf2012/origin/node"
	"github.com/duanhf2012/origin/service"
)

func init(){
	node.Setup(&TestService2{})
}

type TestService2 struct {
	service.Service
}

func (slf *TestService2) OnInit() error {
	return nil
}


```

* main.go运行代码

```go
package main

import (
	"github.com/duanhf2012/origin/node"
	//导入simple_service模块
	_"orginserver/simple_service"
)

func main(){
	node.Start()
}

```

* config/cluster/subnet/cluster.json如下：
```
{
    "NodeList":[
        {
          "NodeId": 1,
          "ListenAddr":"127.0.0.1:8001",
          "NodeName": "Node_Test1",
		  "remark":"//以_打头的，表示只在本机进程，不对整个子网开发",
          "ServiceList": ["TestService1","TestService2"]
        }
    ]
}
```

编译后运行结果如下：
```
#originserver start nodeid=1
TestService1 OnInit.
TestService2 OnInit.
```

第二章：Service中常用功能:
---------------

定时器:
---------------
在开发中最常用的功能有定时任务，origin提供两种定时方式：

一种AfterFunc函数，可以间隔一定时间触发回调，如下：
```
func (slf *TestService2) OnInit() error {
	fmt.Printf("TestService2 OnInit.\n")
	slf.AfterFunc(time.Second*1,slf.OnSecondTick)
	return nil
}

func (slf *TestService2) OnSecondTick(){
	fmt.Printf("tick.\n")
	slf.AfterFunc(time.Second*1,slf.OnSecondTick)
}
```
此时日志可以看到每隔1秒钟会print一次"tick."，如果下次还需要触发，需要重新设置定时器


另一种方式是类似Linux系统的crontab命令，使用如下：
```

func (slf *TestService2) OnInit() error {
	fmt.Printf("TestService2 OnInit.\n")

	//crontab模式定时触发
	//NewCronExpr的参数分别代表:Seconds Minutes Hours DayOfMonth Month DayOfWeek
	//以下为每换分钟时触发
	cron,_:=timer.NewCronExpr("0 * * * * *")
	slf.CronFunc(cron,slf.OnCron)
	return nil
}


func (slf *TestService2) OnCron(){
	fmt.Printf(":A minute passed!\n")
}
```
以上运行结果每换分钟时打印:A minute passed!


打开多协程模式:
---------------
在origin引擎设计中，所有的服务是单协程模式，这样在编写逻辑代码时，不用考虑线程安全问题。极大的减少开发难度，但某些开发场景下不用考虑这个问题，而且需要并发执行的情况，比如，某服务只处理数据库操作控制，而数据库处理中发生阻塞等待的问题，因为一个协程，该服务接受的数据库操作只能是一个
一个的排队处理，效率过低。于是可以打开此模式指定处理协程数，代码如下：
```
func (slf *TestService1) OnInit() error {
	fmt.Printf("TestService1 OnInit.\n")
	
	//打开多线程处理模式，10个协程并发处理
	slf.SetGoRouterNum(10)
	return nil
}
```
为了


性能监控功能:
---------------
我们在开发一个大型的系统时，经常由于一些代码质量的原因，产生处理过慢或者死循环的产生，该功能可以被监测到。使用方法如下：

```
func (slf *TestService1) OnInit() error {
	fmt.Printf("TestService1 OnInit.\n")
	//打开性能分析工具
	slf.OpenProfiler()
	//监控超过1秒的慢处理
	slf.GetProfiler().SetOverTime(time.Second*1)
	//监控超过10秒的超慢处理，您可以用它来定位是否存在死循环
	//比如以下设置10秒，我的应用中是不会发生超过10秒的一次函数调用
	//所以设置为10秒。
	slf.GetProfiler().SetMaxOverTime(time.Second*10)

	slf.AfterFunc(time.Second*2,slf.Loop)
	//打开多线程处理模式，10个协程并发处理
	//slf.SetGoRouterNum(10)
	return nil
}

func (slf *TestService1) Loop(){
	for {
		time.Sleep(time.Second*1)
	}
}


func main(){
	//打开性能分析报告功能，并设置10秒汇报一次
	node.OpenProfilerReport(time.Second*10)
	node.Start()
}

```
上面通过GetProfiler().SetOverTime与slf.GetProfiler().SetMaxOverTimer设置监控时间
并在main.go中，打开了性能报告器，以每10秒汇报一次，因为上面的例子中，定时器是有死循环，所以可以得到以下报告：

2020/04/22 17:53:30 profiler.go:179: [release] Profiler report tag TestService1:
process count 0,take time 0 Milliseconds,average 0 Milliseconds/per.
too slow process:Timer_orginserver/simple_service.(*TestService1).Loop-fm is take 38003 Milliseconds
直接帮助找到TestService1服务中的Loop函数



第三章：Module使用:
---------------

Module创建与销毁:
---------------
可以认为Service就是一种Module，它有Module所有的功能。在示例代码中可以参考originserver/simple_module。
```
package simple_module

import (
	"fmt"
	"github.com/duanhf2012/origin/node"
	"github.com/duanhf2012/origin/service"
)

func init(){
	node.Setup(&TestService3{})
}

type TestService3 struct {
	service.Service
}

type Module1 struct {
	service.Module
}

type Module2 struct {
	service.Module
}

func (slf *Module1) OnInit()error{
	fmt.Printf("Module1 OnInit.\n")
	return nil
}

func (slf *Module1) OnRelease(){
	fmt.Printf("Module1 Release.\n")
}

func (slf *Module2) OnInit()error{
	fmt.Printf("Module2 OnInit.\n")
	return nil
}

func (slf *Module2) OnRelease(){
	fmt.Printf("Module2 Release.\n")
}


func (slf *TestService3) OnInit() error {
	//新建两个Module对象
	module1 := &Module1{}
	module2 := &Module2{}
	//将module1添加到服务中
	module1Id,_ := slf.AddModule(module1)
	//在module1中添加module2模块
	module1.AddModule(module2)
	fmt.Printf("module1 id is %d, module2 id is %d",module1Id,module2.GetModuleId())

	//释放模块module1
	slf.ReleaseModule(module1Id)
	fmt.Printf("xxxxxxxxxxx")
	return nil
}

```
在OnInit中创建了一条线型的模块关系TestService3->module1->module2，调用AddModule后会返回Module的Id，自动生成的Id从10e17开始,内部的id，您可以自己设置Id。当调用ReleaseModule释放时module1时，同样会将module2释放。会自动调用OnRelease函数，日志顺序如下：
```
Module1 OnInit.
Module2 OnInit.
module1 id is 100000000000000001, module2 id is 100000000000000002
Module2 Release.
Module1 Release.
```
在Module中同样可以使用定时器功能，请参照第二章节的定时器部分。


第四章：事件使用
---------------
事件是origin中一个重要的组成部分，可以在同一个node中的service与service或者与module之间进行事件通知。系统内置的几个服务，如：TcpService/HttpService等都是通过事件功能实现。他也是一个典型的观察者设计模型。在event中有两个类型的interface，一个是event.IEventProcessor它提供注册与卸载功能，另一个是event.IEventHandler提供消息广播等功能。

在目录simple_event/TestService4.go中
```
package simple_event

import (
	"github.com/duanhf2012/origin/event"
	"github.com/duanhf2012/origin/node"
	"github.com/duanhf2012/origin/service"
	"time"
)

const (
	//自定义事件类型，必需从event.Sys_Event_User_Define开始
	//event.Sys_Event_User_Define以内给系统预留
	EVENT1 event.EventType =event.Sys_Event_User_Define+1
)

func init(){
	node.Setup(&TestService4{})
}

type TestService4 struct {
	service.Service
}

func (slf *TestService4) OnInit() error {
	//10秒后触发广播事件
	slf.AfterFunc(time.Second*10,slf.TriggerEvent)
	return nil
}

func (slf *TestService4) TriggerEvent(){
	//广播事件，传入event.Event对象，类型为EVENT1,Data可以自定义任何数据
	//这样，所有监听者都可以收到该事件
	slf.GetEventHandler().NotifyEvent(&event.Event{
		Type: EVENT1,
		Data: "event data.",
	})
}


```

在目录simple_event/TestService5.go中
```
package simple_event

import (
	"fmt"
	"github.com/duanhf2012/origin/event"
	"github.com/duanhf2012/origin/node"
	"github.com/duanhf2012/origin/service"
)

func init(){
	node.Setup(&TestService5{})
}

type TestService5 struct {
	service.Service
}

type TestModule struct {
	service.Module
}

func (slf *TestModule) OnInit() error{
	//在当前node中查找TestService4
	pService := node.GetService("TestService4")

	//在TestModule中，往TestService4中注册EVENT1类型事件监听
	pService.(*TestService4).GetEventProcessor().RegEventReciverFunc(EVENT1,slf.GetEventHandler(),slf.OnModuleEvent)
	return nil
}

func (slf *TestModule) OnModuleEvent(ev *event.Event){
	fmt.Printf("OnModuleEvent type :%d data:%+v\n",ev.Type,ev.Data)
}


//服务初始化函数，在安装服务时，服务将自动调用OnInit函数
func (slf *TestService5) OnInit() error {
	//通过服务名获取服务对象
	pService := node.GetService("TestService4")

	////在TestModule中，往TestService4中注册EVENT1类型事件监听
	pService.(*TestService4).GetEventProcessor().RegEventReciverFunc(EVENT1,slf.GetEventHandler(),slf.OnServiceEvent)
	slf.AddModule(&TestModule{})
	return nil
}

func (slf *TestService5) OnServiceEvent(ev *event.Event){
	fmt.Printf("OnServiceEvent type :%d data:%+v\n",ev.Type,ev.Data)
}


```
程序运行10秒后，调用slf.TriggerEvent函数广播事件，于是在TestService5中会收到
```
OnServiceEvent type :1001 data:event data.
OnModuleEvent type :1001 data:event data.
```
在上面的TestModule中监听的事情，当这个Module被Release时监听会自动卸载。

第五章：RPC使用
---------------
RPC是service与service间通信的重要方式，它允许跨进程node互相访问，当然也可以指定nodeid进行调用。如下示例：

TestService6.go文件如下：
```
package simple_rpc

import (
	"github.com/duanhf2012/origin/node"
	"github.com/duanhf2012/origin/service"
)

func init(){
	node.Setup(&TestService6{})
}

type TestService6 struct {
	service.Service
}

func (slf *TestService6) OnInit() error {
	return nil
}

type InputData struct {
	A int
	B int
}

func (slf *TestService6) RPC_Sum(input *InputData,output *int) error{
	*output = input.A+input.B
	return nil
}

```

TestService7.go文件如下：
```
package simple_rpc

import (
	"fmt"
	"github.com/duanhf2012/origin/node"
	"github.com/duanhf2012/origin/service"
	"time"
)

func init(){
	node.Setup(&TestService7{})
}

type TestService7 struct {
	service.Service
}

func (slf *TestService7) OnInit() error {
	slf.AfterFunc(time.Second*2,slf.CallTest)
	slf.AfterFunc(time.Second*2,slf.AsyncCallTest)
	slf.AfterFunc(time.Second*2,slf.GoTest)
	return nil
}

func (slf *TestService7) CallTest(){
	var input InputData
	input.A = 300
	input.B = 600
	var output int

	//同步调用其他服务的rpc,input为传入的rpc,output为输出参数
	err := slf.Call("TestService6.RPC_Sum",&input,&output)
	if err != nil {
		fmt.Printf("Call error :%+v\n",err)
	}else{
		fmt.Printf("Call output %d\n",output)
	}
}


func (slf *TestService7) AsyncCallTest(){
	var input InputData
	input.A = 300
	input.B = 600
	/*slf.AsyncCallNode(1,"TestService6.RPC_Sum",&input,func(output *int,err error){
	})*/
	//异步调用，在数据返回时，会回调传入函数
	//注意函数的第一个参数一定是RPC_Sum函数的第二个参数，err error为RPC_Sum返回值
	slf.AsyncCall("TestService6.RPC_Sum",&input,func(output *int,err error){
		if err != nil {
			fmt.Printf("AsyncCall error :%+v\n",err)
		}else{
			fmt.Printf("AsyncCall output %d\n",*output)
		}
	})
}

func (slf *TestService7) GoTest(){
	var input InputData
	input.A = 300
	input.B = 600

	//在某些应用场景下不需要数据返回可以使用Go，它是不阻塞的,只需要填入输入参数
	err := slf.Go("TestService6.RPC_Sum",&input)
	if err != nil {
		fmt.Printf("Go error :%+v\n",err)
	}

	//以下是广播方式，如果在同一个子网中有多个同名的服务名，CastGo将会广播给所有的node
	//slf.CastGo("TestService6.RPC_Sum",&input)
}

```
您可以把TestService6配置到其他的Node中，比如NodeId为2中。只要在一个子网，origin引擎可以无差别调用。开发者只需要关注Service关系。同样它也是您服务器架构设计的核心需要思考的部分。


第六章：HttpService使用
---------------

第七章：TcpService服务使用
---------------

第八章：其他系统模块介绍
---------------

备注:
---------------
**感觉不错请star, 谢谢!**

**欢迎加入origin服务器开发交流群：168306674**

提交bug及特性: https://github.com/duanhf2012/origin/issues

[生活不易，且行且珍惜，感谢！](https://github.com/duanhf2012/other/blob/master/pay.png "Thanks!")

