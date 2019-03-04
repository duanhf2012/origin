orgin 游戏服务器引擎简介
==================


orgin 是一个由 Go 语言（golang）编写的分布式开源游戏服务器引擎。orgin适用于各类游戏服务器的开发，包括 H5（HTML5）游戏服务器。

orgin 解决的问题：
* orgin总体设计如go语言设计一样，总是尽可能的提供简洁和易用的模式，快速开发。
* 能够根据业务需求快速并灵活的制定服务器架构。
* 利用多核优势，将不同的service配置到不同的node，并能高效的协同工作。
* 将整个引擎抽象三大对象，node,service,module。通过统一的组合模式管理游戏中各功能模块的关系。
* 有丰富并健壮的工具库。

Hello world!
---------------
下面我们来一步步的建立orgin服务器,先下载[orgin引擎](https://github.com/duanhf2012/origin "orgin引擎"),或者使用如下命令：
```go
go get -v -u  github.com/duanhf2012/origin
```
于是下载到GOPATH环境目录中,在src中加入main.go,内容如下：
```go
package main

import (
	"github.com/duanhf2012/origin/originnode"
)

func main() {
	node := originnode.NewOrginNode()
	if node == nil {
		return
	}

	node.Init()
	node.Start()
}
```
一个orgin服务器需要创建一个node对象，然后必需有Init和Start的流程。

orgin引擎三大对象关系
---------------
* Node:   可以认为每一个Node代表着一个orgin进程
* Service:一个独立的服务可以认为是一个大的功能模块，他是Node的子集，创建完成并安装Node对象中。服务可以支持外部RPC和HTTP接口对外功能。
* Module: 这是orgin最小对象单元，非常建议所有的业务模块都划分成各个小的Module组合。Module可以建立树状关系。Service也是Module的类型。

orgin集群核心配置文件config/cluser.json如下:
---------------
```
{
"PublicServiceList":["logiclog"],

"NodeList":[

{
	"NodeID":1,
	"NodeName":"N_Node1",
	"ServerAddr":"127.0.0.1:8080",
	
	"ServiceList":["CTestService1"],
	"ClusterNode":["N_Node2"]
},

{
	"NodeID":2,
	"NodeName":"N_Node2",
	"ServerAddr":"127.0.0.1:8081",
	"ServiceList":["CTestService2","CTestService3"],
	"ClusterNode":[]
}

]
}
```
orgin所有的结点与服务通过配置进行关联，配置文件分为两大配置结点：
* PublicServiceList：用于公共服务配置，所有的结点默认会加载该服务列表。
* NodeList：Node所有的列表
    * NodeId:     Node编号，用于标识唯一的进程id
	* NodeName:   Node名称，用于区分Node结点功能。例如，可以是GameServer
	* ServerAddr: 结点监听的地址与端口
	* ServiceList:结点中允许开启的服务列表
	* ClusterNode:将与列表中的Node产生集群关系。允许访问这些结点中所有的服务。

orgin第一个服务:
---------------
我们准备的NodeId为1的结点下新建两个服务，分别是CTestService1与CTestService2。
* config/cluster.json内容如下
```
{
"NodeList":[
{
	"NodeID":1,
	"NodeName":"N_Node1",
	"ServerAddr":"127.0.0.1:8080",
	
	"ServiceList":["CTestService1","CTestService2"],
	"ClusterNode":[]
}
]
}
```
* main.go运行代码

```go
package main

import (
	"fmt"
	"time"

	"github.com/duanhf2012/origin/originnode"
	"github.com/duanhf2012/origin/service"
)

//定义一个服务，必需继承自service.BaseService
type CTestService1 struct {
	service.BaseService
}

//服务的初始化
func (slf *CTestService1) OnInit() error {
	fmt.Println("CTestService1.OnInit")
	return nil
}

//服务运行，返回值如果为true，将会重复的进入OnRun，
//直到返回false为止，此函数只会进入一次
func (slf *CTestService1) OnRun() bool {
	fmt.Println("CTestService1.OnRun")
	return false
}

//当OnRun退出时，调用OnEndRun，可以收尾OnRun的运行处理
func (slf *CTestService1) OnEndRun() {
	fmt.Println("CTestService1.OnEndRun")
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
	
	//返回true，将重复进入
	return true
}

func (slf *CTestService2) OnEndRun() {
	fmt.Println("CTestService2.OnEndRun")
}

func main() {
	//新建一个orgin node对象
	node := originnode.NewOrginNode()
	if node == nil {
		return
	}

	//安装CTestService1与CTestService2服务
	node.SetupService(&CTestService1{}, &CTestService2{})
	node.Init()
	node.Start()
}
```
通过以下命令运行：
```
main.exe NodeId=1
```
输出结果：
```
CTestService2.OnInit
CTestService1.OnInit
CTestService2.OnRun
CTestService1.OnRun
CTestService1.OnEndRun
CTestService2.OnRun
CTestService2.OnRun
CTestService2.OnRun
CTestService2.OnRun
```
通过日志可以确认，在Node启动时分别驱动Service的OnInit,OnRun,OnEndRun，上面的日志中CTestService2.OnRun会被循环调用，
因为在OnRun的返回是true，否则只会进入一次。如果你不需要OnRun可以不定义OnRun函数。我们已经成功的调用了两个服务了。

orgin服务间通信:
---------------
orgin是通过rpc的方式互相调用，当前结点只能访问cluster.json中有配置ClusterNode的结点或本地结点中所有的服务接口,下面我们来用实际例子来说明，如下代码所示：
```
package main

import (
	"Server/service/websockservice"
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

//输入参数，注意变量名首字母大写
type InputData struct {
	A1 int
	A2 int
}

func (slf *CTestService1) OnRun() bool {
	var ret int
	input := InputData{100, 11}
	
	//调用服务名.接口名称，传入传出参数必需为地址，符合RPC_Add接口规范
	//以下可以采用其他，注意如果在服务名前加入下划线"_"表示访问本node中的服务
	//_servicename.methodname
	//servicename.methodname
	err := cluster.Call("CTestService2.RPC_Add", &input, &ret)
	fmt.Print(err, "\n", ret)

	return false
}

type CTestService2 struct {
	service.BaseService
}

//注意格式一定要RPC_开头，函数第一个参数为输入参数，第二个为输出参数，只允许指针类型
//返回值必需为error，如果不满足格式，装载服务时将被会忽略。
func (slf *CTestService2) RPC_Add(arg *InputData, ret *int) error {
	*ret = arg.A1 + arg.A2
	return nil
}

func main() {
	node := originnode.NewOrginNode()
	if node == nil {
		return
	}
	node.SetupService(&CTestService1{}, &CTestService2{})
	node.Init()
	node.Start()
}

```
输入结果为：
```
<nil>
111
```
cluster.Call只允许调用一个结点中的服务，如果服务在多个结点中，是不允许的。注意，Call方式是阻塞模式，只有当被调服务响应时才返回，或者超过最大超时时间。如果不想阻塞，可以采用Go方式调用。例如：cluster.Go(true,"CTestService2.RPC_Send", &input)
第一个参数代码是否广播，如果调用的服务接口在多个Node中存在，都将被调用。还可以向指定的NodeId调用，例如：
```
func (slf *CCluster) CallNode(nodeid int, servicemethod string, args interface{}, reply interface{}) error
func (slf *CCluster) GoNode(nodeid int, args interface{}, servicemethod string) error
```
在实际使用时，注意抽象service，只有合理的划分service，orgin是以service为最小集群单元放到不同的node中，以达到动态移动service功能到不同的node进程中。

orgin中Module使用:
---------------
module在orgin引擎中是最小的对象单元，service本质上也是一个复杂的module。它同样有着以下方法:
```
OnInit() error //Module初始化时调用
OnRun() bool   //Module运行时调用
OnEndRun()
```
在使用规则上和service是一样的，因为本质上是一样的对象。看以下简单示例：
```
package main

import (
	"fmt"
	"time"

	"github.com/duanhf2012/origin/originnode"
	"github.com/duanhf2012/origin/service"
)

type CTestService1 struct {
	service.BaseService
}

type CTestModule1 struct {
	service.BaseModule
}

func (slf *CTestModule1) OnInit() error {
	fmt.Printf("CTestModule1::OnInit\n")
	return nil
}

func (slf *CTestModule1) OnRun() bool {
	fmt.Printf("CTestModule1::OnRun\n")
	time.Sleep(time.Second * 1)
	return true
}

func (slf *CTestModule1) OnEndRun() {
	fmt.Printf("CTestModule1::OnEndRun\n")
}

func (slf *CTestModule1) Add(a int, b int) int {
	return a + b
}

func (slf *CTestService1) OnRun() bool {
	testmodule := CTestModule1{}

	//可以设置自定义id
	//testmodule.SetModuleId(PLAYERID)

	//添加module到slf对象中
	moduleId := slf.AddModule(&testmodule)

	//获取module对象
	pModule := slf.GetModuleById(moduleId)
	//转换为CTestModule1类型
	ret := pModule.(*CTestModule1).Add(3, 4)
	fmt.Printf("ret is %d\n", ret)

	time.Sleep(time.Second * 4)
	//释放module
	slf.ReleaseModule(moduleId)

	return false
}

func main() {
	node := originnode.NewOrginNode()
	if node == nil {
		return
	}
	node.SetupService(&CTestService1{})
	node.Init()
	node.Start()
}
```
执行结果如下：
```
ret is 7
CTestModule1::OnInit
CTestModule1::OnRun
CTestModule1::OnRun
CTestModule1::OnRun
CTestModule1::OnRun
CTestModule1::OnEndRun
```
以上创建新的Module加入到当前服务对象中，可以获取释放动作。同样CTestModule1模块也可以加入子模块，使用方法一样。以上日志每秒钟CTestModule1::OnRun打印一次，4秒后ReleaseModule，对象被释放，执行CTestModule1::OnEndRun。

orgin中其他重要服务:
---------------
* github.com\duanhf2012\origin\sysservice集成了系统常用的服务
	* httpserverervice:提供对外的http服务
	* wsserverservice :websocket服务
	* logservice      :日志服务
	
以上服务请参照github.com\duanhf2012\origin\Test目录使用方法
* github.com\duanhf2012\origin\sysmodule集成了系统常用的Module
	* DBModule: mysql数据库操作模块，支持异步调用
	* RedisModule: Redis操作模块，支持异步调用
	* LogModule: 日志模块，支持区分日志等级
	* HttpClientPoolModule:http客户端模块




