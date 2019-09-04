package main

import (
	"fmt"

	"github.com/duanhf2012/origin/cluster"
	"github.com/duanhf2012/origin/originnode"
	"github.com/duanhf2012/origin/service"
)

type SubNet1_Service struct {
	service.BaseService
}

func init() {
	originnode.InitService(&SubNet1_Service{})
}

//OnInit ...
func (ws *SubNet1_Service) OnInit() error {

	return nil
}

//OnRun ...
func (ws *SubNet1_Service) OnRun() bool {
	var in InputData
	var ret int
	in.A1 = 10
	in.A2 = 20
	err := cluster.Call("SubNet2_Service1.RPC_Multi", &in, &ret)
	fmt.Printf("%+v", err)
	return false
}
