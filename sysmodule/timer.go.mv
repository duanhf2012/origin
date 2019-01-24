package wsservice

import (
	"origin/service"
)

//声明控制器函数Map类型变量

type cWSService struct {
	service.BaseService
	port int
}

func (ws *cWSService) OnInit() error {
	return nil
}

func (ws *cWSService) OnRun() error {

	return nil
}

func (ws *cWSService) OnDestory() error {
	return nil
}

func NewWSService(servicetype int) *cWSService {
	wss := new(cWSService)
	wss.Init(wss, servicetype)
	return wss
}

func (ws *cWSService) RPC_TestMethod(a string, b int) error {
	return nil
}
