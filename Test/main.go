package main

import (
	"github.com/duanhf2012/origin/cluster"
	"github.com/duanhf2012/origin/originnode"
	"github.com/duanhf2012/origin/sysservice"
	"github.com/duanhf2012/origin/sysservice/originhttp"
	"github.com/duanhf2012/origin/network"
)


type TcpSocketServerReciver struct {

}

func (slf *TcpSocketServerReciver) OnConnected(pClient *network.SClient){

}

func (slf *TcpSocketServerReciver) OnDisconnect(pClient *network.SClient){

}

func (slf *TcpSocketServerReciver) OnRecvMsg(pClient *network.SClient, pPack *network.MsgBasePack){

}

func main() {

	node := originnode.NewOriginNode()
	if node == nil {
		return
	}

	nodeCfg, _ := cluster.ReadNodeConfig("./config/nodeconfig.json", cluster.GetNodeId())
	httpserver := originhttp.NewHttpServerService(nodeCfg.HttpPort) // http服务
	for _, ca := range nodeCfg.CAFile {
		httpserver.SetHttps(ca.CertFile, ca.KeyFile)
	}

	pTcpService := sysservice.NewTcpSocketPbService(":9004",&TcpSocketServerReciver{})
	httpserver.SetPrintRequestTime(true)

	node.SetupService(httpserver,pTcpService)
	node.Init()
	node.Start()
}
