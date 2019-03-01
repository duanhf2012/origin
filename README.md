orgin 游戏服务器引擎简介
==================


orgin 是一个由 Go 语言（golang）编写的分布式开源游戏服务器引擎。Leaf 适用于各类游戏服务器的开发，包括 H5（HTML5）游戏服务器。

orgin 解决的问题：
* orgin总体设计如go语言设计一样，总是尽可能的提供简洁和易用的模式，快速开发。
* 能够根据业务需求快速并灵活的制定服务器架构，甚至做到上线后根据负载需求动态调整。
* 利用多核优势，将不同的service配置到不同的node，并能高效的协同工作。
* 将整个引擎抽象三大对象，node,service,module。通过统一的组合模式管理游戏中各功能模块的关系。
* 有丰富并健壮的工具库。

Hello world!
---------------
下面我们来一步步的建立orgin服务器,先下载[orgin引擎](github.com/duanhf2012/origin),或者使用如下命令：
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
我们准备新建两个服务，分别是CTestService1与CTestService2。
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




