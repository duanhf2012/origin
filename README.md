origin 游戏服务器引擎简介
=========================

origin 是一个由 Go 语言（golang）编写的分布式开源游戏服务器引擎。origin适用于各类游戏服务器的开发，包括 H5（HTML5）游戏服务器。

origin 解决的问题：

* origin总体设计如go语言设计一样，总是尽可能的提供简洁和易用的模式，快速开发。
* 能够根据业务需求快速并灵活的制定服务器架构。
* 利用多核优势，将不同的service配置到不同的node，并能高效的协同工作。
* 将整个引擎抽象三大对象，node,service,module。通过统一的组合模型管理游戏中各功能模块的关系。
* 有丰富并健壮的工具库。

Hello world!
------------

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

以上只是基础代码，具体运行参数和配置请参照第一章节。

一个origin进程需要创建一个node对象,Start开始运行。您也可以直接下载origin引擎示例:

```
go get -v -u github.com/duanhf2012/originserver
```

本文所有的说明都是基于该示例为主。

origin引擎三大对象关系
----------------------

* Node:   可以认为每一个Node代表着一个origin进程
* Service:一个独立的服务可以认为是一个大的功能模块，他是Node的子集，创建完成并安装Node对象中。服务可以支持对外部RPC等功能。
* Module: 这是origin最小对象单元，强烈建议所有的业务模块都划分成各个小的Module组合，origin引擎将监控所有服务与Module运行状态，例如可以监控它们的慢处理和死循环函数。Module可以建立树状关系。Service本身也是Module的类型。

origin集群核心配置文件在config的cluster目录下，如github.com/duanhf2012/originserver的config/cluster目录下有cluster.json与service.json配置：

cluster.json如下：
------------------

```
{
    "NodeList":[
        {
          "NodeId": 1,
          "Private": false,
          "ListenAddr":"127.0.0.1:8001",
          "MaxRpcParamLen": 409600,
          "CompressBytesLen": 20480,
          "NodeName": "Node_Test1",
          "remark":"//以_打头的，表示只在本机进程，不对整个子网公开",
          "ServiceList": ["TestService1","TestService2","TestServiceCall","GateService","_TcpService","HttpService","WSService"]
        },
        {
          "NodeId": 2,
          "Private": false,
          "ListenAddr":"127.0.0.1:8002",
          "MaxRpcParamLen": 409600,
          "CompressBytesLen": 20480,
          "NodeName": "Node_Test1",
          "remark":"//以_打头的，表示只在本机进程，不对整个子网公开",
          "ServiceList": ["TestService1","TestService2","TestServiceCall","GateService","TcpService","HttpService","WSService"]
        }
    ]
```

---

以上配置了两个结点服务器程序:

* NodeId: 表示origin程序的结点Id标识，不允许重复。
* Private: 是否私有结点，如果为true，表示其他结点不会发现它，但可以自我运行。
* ListenAddr:Rpc通信服务的监听地址
* MaxRpcParamLen:Rpc参数数据包最大长度，该参数可以缺省，默认一次Rpc调用支持最大4294967295byte长度数据。
* CompressBytesLen:Rpc网络数据压缩，当数据>=20480byte时将被压缩。该参数可以缺省或者填0时不进行压缩。
* NodeName:结点名称
* remark:备注，可选项
* ServiceList:该Node拥有的服务列表，注意：origin按配置的顺序进行安装初始化。但停止服务的顺序是相反。

---

在启动程序命令originserver -start nodeid=1中nodeid就是根据该配置装载服务。
更多参数使用，请使用originserver -help查看。
service.json如下：
------------------

```
{
"Global": {
		"AreaId": 1
	},
  "Service":{
	  "HttpService":{
		"ListenAddr":"0.0.0.0:9402",
		"ReadTimeout":10000,
		"WriteTimeout":10000,
		"ProcessTimeout":10000,
		"ManualStart": false,
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

---

以上配置分为两个部分：Global,Service与NodeService。Global是全局配置，在任何服务中都可以通过cluster.GetCluster().GetGlobalCfg()获取，NodeService中配置的对应结点中服务的配置，如果启动程序中根据nodeid查找该域的对应的服务，如果找不到时，从Service公共部分查找。

**HttpService配置**

* ListenAddr:Http监听地址
* ReadTimeout:读网络超时毫秒
* WriteTimeout:写网络超时毫秒
* ProcessTimeout: 处理超时毫秒
* ManualStart: 是否手动控制开始监听，如果true，需要手动调用StartListen()函数
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

---

第一章：origin基础:
-------------------

查看github.com/duanhf2012/originserver中的simple_service中新建两个服务，分别是TestService1.go与CTestService2.go。

simple_service/TestService1.go如下：

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

simple_service/TestService2.go如下：

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

* config/cluster/cluster.json如下：

```
{
    "NodeList":[
        {
          "NodeId": 1,
          "Private": false,
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
#originserver -start nodeid=1
TestService1 OnInit.
TestService2 OnInit.
```

第二章：Service中常用功能:
--------------------------

定时器:
-------

在开发中最常用的功能有定时任务，origin提供两种定时方式：

一种AfterFunc函数，可以间隔一定时间触发回调，参照simple_service/TestService2.go,实现如下：

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


func (slf *TestService2) OnCron(cron *timer.Cron){
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
	slf.SetGoRoutineNum(10)
	return nil
}
```


性能监控功能:
-------------

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
	//slf.SetGoRoutineNum(10)
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

结点连接和断开事件监听:
-----------------------

在有些业务中需要关注某结点是否断开连接，可以注册回调如下：

```
func (ts *TestService) OnInit() error{
	ts.RegRpcListener(ts)

	return nil
}

func (ts *TestService) OnNodeConnected(nodeId int){
}

func (ts *TestService) OnNodeDisconnect(nodeId int){
}
```

第三章：Module使用:
-------------------

Module创建与销毁:
-----------------

可以认为Service就是一种Module，它有Module所有的功能。在示例代码中可以参考originserver/simple_module/TestService3.go。

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
----------------

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

func (slf *TestModule) OnModuleEvent(ev event.IEvent){
	event := ev.(*event.Event)
	fmt.Printf("OnModuleEvent type :%d data:%+v\n",event.GetEventType(),event.Data)
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

func (slf *TestService5) OnServiceEvent(ev event.IEvent){
	event := ev.(*event.Event)
	fmt.Printf("OnServiceEvent type :%d data:%+v\n",event.Type,event.Data)
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

simple_rpc/TestService6.go文件如下：

```go
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

// 注意RPC函数名的格式必需为RPC_FunctionName或者是RPCFunctionName，如下的RPC_Sum也可以写成RPCSum
func (slf *TestService6) RPC_Sum(input *InputData,output *int) error{
	*output = input.A+input.B
	return nil
}

```

simple_rpc/TestService7.go文件如下：

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
	
	
	//自定义超时,默认rpc超时时间为15s
	err = slf.CallWithTimeout(time.Second*1, "TestService6.RPC_Sum", &input, &output)
	if err != nil {
		fmt.Printf("Call error :%+v\n", err)
	} else {
		fmt.Printf("Call output %d\n", output)
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
	err := slf.AsyncCall("TestService6.RPC_Sum", &input, func(output *int, err error) {
		if err != nil {
			fmt.Printf("AsyncCall error :%+v\n", err)
		} else {
			fmt.Printf("AsyncCall output %d\n", *output)
		}
	})
	fmt.Println(err)

	//自定义超时,返回一个cancel函数，可以在业务需要时取消rpc调用
	rpcCancel, err := slf.AsyncCallWithTimeout(time.Second*1, "TestService6.RPC_Sum", &input, func(output *int, err error) {
		//如果下面注释的rpcCancel()函数被调用，这里可能将不再返回
		if err != nil {
			fmt.Printf("AsyncCall error :%+v\n", err)
		} else {
			fmt.Printf("AsyncCall output %d\n", *output)
		}
	})
	//rpcCancel()
	fmt.Println(err, rpcCancel)
	
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


第六章：并发函数调用
---------------
在开发中经常会有将某些任务放到其他协程中并发执行，执行完成后，将服务的工作线程去回调。使用方式很简单，先打开该功能如下代码：
```
	//以下通过cpu数量来定开启协程并发数量，建议:(1)cpu密集型计算使用1.0  (2)i/o密集型使用2.0或者更高
	slf.OpenConcurrentByNumCPU(1.0)
	
	//以下通过函数打开并发协程数，以下协程数最小5，最大10，任务管道的cap数量1000000
	//origin会根据任务的数量在最小与最大协程数间动态伸缩
	//slf.OpenConcurrent(5, 10, 1000000)
```

使用示例如下:
```

func (slf *TestService13) testAsyncDo() {
	var context struct {
		data int64
	}

	//1.示例普通使用
	//参数一的函数在其他协程池中执行完成，将执行完成事件放入服务工作协程，
	//参数二的函数在服务协程中执行，是协程安全的。
	slf.AsyncDo(func() bool {
		//该函数回调在协程池中执行
		context.data = 100
		return true
	}, func(err error) {
		//函数将在服务协程中执行
		fmt.Print(context.data) //显示100
	})

	//2.示例按队列顺序
	//参数一传入队列Id,同一个队列Id将在协程池中被排队执行
	//以下进行两次调用，因为两次都传入参数queueId都为1，所以它们会都进入queueId为1的排队执行
	queueId := int64(1)
	for i := 0; i < 2; i++ {
		slf.AsyncDoByQueue(queueId, func() bool {
			//该函数会被2次调用，但是会排队执行
			return true
		}, func(err error) {
			//函数将在服务协程中执行
		})
	}

	//3.函数参数可以某中一个为空
	//参数二函数将被延迟执行
	slf.AsyncDo(nil, func(err error) {
		//将在下
	})

	//参数一函数在协程池中执行，但没有在服务协程中回调
	slf.AsyncDo(func() bool {
		return true
	}, nil)

	//4.函数返回值控制不进行回调
	slf.AsyncDo(func() bool {
		//返回false时，参数二函数将不会被执行; 为true时，则会被执行
		return false
	}, func(err error) {
		//该函数将不会被执行
	})
}
```


第七章：配置服务发现
--------------------

origin引擎默认使用读取所有结点配置的进行确认结点有哪些Service。引擎也支持动态服务发现的方式，使用了内置的DiscoveryMaster服务用于中心Service，DiscoveryClient用于向DiscoveryMaster获取整个origin网络中所有的结点以及服务信息。具体实现细节请查看这两部分的服务实现。具体使用方式，在以下cluster配置中加入以下内容：

```
{
	"MasterDiscoveryNode": [{
		"NodeId": 2,
		"ListenAddr": "127.0.0.1:10001",
		"MaxRpcParamLen": 409600,
		"NeighborService":["HttpGateService"]
	},
	{
		"NodeId": 1,
		"ListenAddr": "127.0.0.1:8801",
		"MaxRpcParamLen": 409600
	}],


	"NodeList": [{
		"NodeId": 1,
		"ListenAddr": "127.0.0.1:8801",
		"MaxRpcParamLen": 409600,
		"NodeName": "Node_Test1",
		"Private": false,
		"remark": "//以_打头的，表示只在本机进程，不对整个子网开发",
		"ServiceList": ["_TestService1", "TestService9", "TestService10"],
		"DiscoveryService": ["TestService8"]
	}]
}
```

新上有两新不同的字段分别为MasterDiscoveryNode与DiscoveryService。其中:

MasterDiscoveryNode中配置了结点Id为1的服务发现Master，他的监听地址ListenAddr为127.0.0.1:8801，结点为2的也是一个服务发现Master，不同在于多了"NeighborService":["HttpGateService"]配置。如果"NeighborService"有配置具体的服务时，则表示该结点是一个邻居Master结点。当前运行的Node结点会从该Master结点上筛选HttpGateService的服务，并且当前运行的Node结点不会向上同步本地所有公开的服务，和邻居结点关系是单向的。

NeighborService可以用在当有多个以Master中心结点的网络，发现跨网络的服务场景。
DiscoveryService表示将筛选origin网络中的TestService8服务，注意如果DiscoveryService不配置，则筛选功能不生效。

第八章：HttpService使用
-----------------------

HttpService是origin引擎中系统实现的http服务，http接口中常用的GET,POST以及url路由处理。

simple_http/TestHttpService.go文件如下：

```
package simple_http

import (
	"fmt"
	"github.com/duanhf2012/origin/node"
	"github.com/duanhf2012/origin/service"
	"github.com/duanhf2012/origin/sysservice"
	"net/http"
)

func init(){
	node.Setup(&sysservice.HttpService{})
	node.Setup(&TestHttpService{})
}

//新建自定义服务TestService1
type TestHttpService struct {
	service.Service
}

func (slf *TestHttpService) OnInit() error {
	//获取系统httpservice服务
	httpservice := node.GetService("HttpService").(*sysservice.HttpService)

	//新建并设置路由对象
	httpRouter := sysservice.NewHttpHttpRouter()
	httpservice.SetHttpRouter(httpRouter,slf.GetEventHandler())

	//GET方法，请求url:http://127.0.0.1:9402/get/query?nickname=boyce
	//并header中新增key为uid,value为1000的头,则用postman测试返回结果为：
	//head uid:1000, nickname:boyce
	httpRouter.GET("/get/query", slf.HttpGet)

	//POST方法 请求url:http://127.0.0.1:9402/post/query
	//返回结果为：{"msg":"hello world"}
	httpRouter.POST("/post/query", slf.HttpPost)

	//GET方式获取目录下的资源，http://127.0.0.1:port/img/head/a.jpg
	httpRouter.SetServeFile(sysservice.METHOD_GET,"/img/head/","d:/img")

	//如果配置"ManualStart": true配置为true，则使用以下方法进行开启http监听
	//httpservice.StartListen()
	return nil
}

func (slf *TestHttpService) HttpGet(session *sysservice.HttpSession){
	//从头中获取key为uid对应的值
	uid := session.GetHeader("uid")
	//从url参数中获取key为nickname对应的值
	nickname,_ := session.Query("nickname")
	//向body部分写入数据
	session.Write([]byte(fmt.Sprintf("head uid:%s, nickname:%s",uid,nickname)))
	//写入http状态
	session.WriteStatusCode(http.StatusOK)
	//完成返回
	session.Done()
}

type HttpRespone struct {
	Msg string `json:"msg"`
}

func (slf *TestHttpService) HttpPost(session *sysservice.HttpSession){
	//也可以采用直接返回数据对象方式，如下：
	session.WriteJsonDone(http.StatusOK,&HttpRespone{Msg: "hello world"})
}

```

注意，要在main.go中加入import _ "orginserver/simple_service"，并且在config/cluster/cluster.json中的ServiceList加入服务。

第九章：TcpService服务使用
--------------------------

TcpService是origin引擎中系统实现的Tcp服务，可以支持自定义消息格式处理器。只要重新实现network.Processor接口。目前内置已经实现最常用的protobuf处理器。

simple_tcp/TestTcpService.go文件如下：

```
package simple_tcp

import (
	"fmt"
	"github.com/duanhf2012/origin/network/processor"
	"github.com/duanhf2012/origin/node"
	"github.com/duanhf2012/origin/service"
	"github.com/duanhf2012/origin/sysservice"
	"github.com/golang/protobuf/proto"
	"orginserver/simple_tcp/msgpb"
)

func init(){
	node.Setup(&sysservice.TcpService{})
	node.Setup(&TestTcpService{})
}

//新建自定义服务TestService1
type TestTcpService struct {
	service.Service
	processor *processor.PBProcessor
	tcpService *sysservice.TcpService
}

func (slf *TestTcpService) OnInit() error {
	//获取安装好了的TcpService对象
	slf.tcpService =  node.GetService("TcpService").(*sysservice.TcpService)

	//新建内置的protobuf处理器，您也可以自定义路由器，比如json，后续会补充
	slf.processor = processor.NewPBProcessor()

	//注册监听客户连接断开事件
	slf.processor.RegisterDisConnected(slf.OnDisconnected)
	//注册监听客户连接事件
	slf.processor.RegisterConnected(slf.OnConnected)
	//注册监听消息类型MsgType_MsgReq，并注册回调
	slf.processor.Register(uint16(msgpb.MsgType_MsgReq),&msgpb.Req{},slf.OnRequest)
	//将protobuf消息处理器设置到TcpService服务中
	slf.tcpService.SetProcessor(slf.processor,slf.GetEventHandler())

	return nil
}


func (slf *TestTcpService) OnConnected(clientid uint64){
	fmt.Printf("client id %d connected\n",clientid)
}


func (slf *TestTcpService) OnDisconnected(clientid uint64){
	fmt.Printf("client id %d disconnected\n",clientid)
}

func (slf *TestTcpService) OnRequest (clientid uint64,msg proto.Message){
	//解析客户端发过来的数据
	pReq := msg.(*msgpb.Req)
	//发送数据给客户端
	err := slf.tcpService.SendMsg(clientid,&msgpb.Req{
		Msg: proto.String(pReq.GetMsg()),
	})
	if err != nil {
		fmt.Printf("send msg is fail %+v!",err)
	}
}
```

第十章：其他系统模块介绍
------------------------

* sysservice/wsservice.go:支持了WebSocket协议，使用方法与TcpService类似
* sysmodule/DBModule.go:对mysql数据库操作
* sysmodule/RedisModule.go:对Redis数据进行操作
* sysmodule/HttpClientPoolModule.go:Http客户端请求封装
* log/log.go:日志的封装，可以使用它构建对象记录业务文件日志
* util:在该目录下，有常用的uuid,hash,md5,协程封装等工具库
* https://github.com/duanhf2012/originservice: 其他扩展支持的服务可以在该工程上看到，目前支持firebase推送的封装。

备注:
-----

**感觉不错请star, 谢谢!**

**欢迎加入origin服务器开发QQ交流群:168306674，有任何疑问我都会及时解答**

提交bug及特性: https://github.com/duanhf2012/origin/issues

[因服务器是由个人维护，如果这个项目对您有帮助，您可以点我进行捐赠，感谢！](http://www.cppblog.com/images/cppblog_com/API/21416/r_pay.jpg "Thanks!")

特别感谢以下赞助网友：

```
咕咕兽
_
死磕代码
bp-li
阿正
```
