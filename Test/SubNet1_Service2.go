package main

import (
	"github.com/duanhf2012/origin/originnode"
	"github.com/duanhf2012/origin/service"
)

type SubNet1_Service2 struct {
	service.BaseService
}

func init() {
	originnode.InitService(&SubNet1_Service2{})
}

//OnInit ...
func (ws *SubNet1_Service2) OnInit() error {
	return nil
}

//OnRun ...
func (ws *SubNet1_Service2) OnRun() bool {
	return false
}

func (slf *SubNet1_Service2) RPC_Sub(arg *InputData, ret *int) error {

	*ret = arg.A1 - arg.A2

	return nil
}
