package main

import (
	"github.com/duanhf2012/origin/originnode"
	"github.com/duanhf2012/origin/service"
)

type SubNet2_Service2 struct {
	service.BaseService
}

func init() {
	originnode.InitService(&SubNet2_Service2{})
}

//OnInit ...
func (ws *SubNet2_Service2) OnInit() error {
	return nil
}

//OnRun ...
func (ws *SubNet2_Service2) OnRun() bool {
	return false
}

func (slf *SubNet2_Service2) RPC_Div(arg *InputData, ret *int) error {

	*ret = arg.A1 / arg.A2

	return nil
}
