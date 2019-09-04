package main

import (
	"github.com/duanhf2012/origin/originnode"
	"github.com/duanhf2012/origin/service"
)

type SubNet2_Service1 struct {
	service.BaseService
}

func init() {
	originnode.InitService(&SubNet2_Service1{})
}

//OnInit ...
func (ws *SubNet2_Service1) OnInit() error {
	return nil
}

//OnRun ...
func (ws *SubNet2_Service1) OnRun() bool {
	return false
}

func (slf *SubNet2_Service1) RPC_Multi(arg *InputData, ret *int) error {

	*ret = arg.A1 * arg.A2

	return nil
}
