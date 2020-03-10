package main

import (
	_ "github.com/duanhf2012/origin/Test/logicservice"
	"github.com/duanhf2012/origin/cluster"
	"github.com/duanhf2012/origin/network"
	"github.com/duanhf2012/origin/originnode"
	"github.com/duanhf2012/origin/sysservice"
	"github.com/duanhf2012/origin/sysservice/originhttp"
	"time"
)


type TcpSocketServerReciver struct {

}

func (slf *TcpSocketServerReciver) OnConnected(pClient *network.SClient){

}

func (slf *TcpSocketServerReciver) OnDisconnect(pClient *network.SClient){

}



func main() {

	node := originnode.NewOriginNode()
	if node == nil {
		return
	}

	//打开Module死循环监控
	node.EnableMonitorModule(time.Minute*5)
	originhttp.SetStaticResource(originhttp.METHOD_GET,"/img/","d:/")
	nodeCfg, _ := cluster.ReadNodeConfig("./config/nodeconfig.json", cluster.GetNodeId())
	httpserver := originhttp.NewHttpServerService(nodeCfg.HttpPort) // http服务
	for _, ca := range nodeCfg.CAFile {
		httpserver.SetHttps(ca.CertFile, ca.KeyFile)
	}

	pTcpService := sysservice.NewTcpSocketPbService(":9412")
	pTcpService.SetServiceName("ls")
/*
	pTcpService2 := sysservice.NewTcpSocketPbService(":9005")
	pTcpService2.SetServiceName("lc")
*/
	httpserver.SetPrintRequestTime(true)

	node.SetupService(httpserver,pTcpService)

	node.Init()
	node.Start()
}
