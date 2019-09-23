package main

import (
	"strings"

	"github.com/duanhf2012/origin/cluster"
	"github.com/duanhf2012/origin/originnode"
	"github.com/duanhf2012/origin/sysservice"
)

func main() {
	strings.ReplaceAll
	node := originnode.NewOriginNode()
	if node == nil {
		return
	}

	nodeCfg, _ := cluster.ReadNodeConfig("./config/nodeconfig.json", cluster.GetNodeId())
	httpserver := sysservice.NewHttpServerService(nodeCfg.HttpPort) // http服务
	for _, ca := range nodeCfg.CAFile {
		httpserver.SetHttps(ca.CertFile, ca.KeyFile)
	}
	httpserver.SetPrintRequestTime(true)

	node.SetupService(httpserver)
	node.Init()
	node.Start()
}
