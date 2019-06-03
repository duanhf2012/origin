package sysservice

import (
	"github.com/duanhf2012/origin/network"
	"github.com/duanhf2012/origin/service"
)

type WSAgentService struct {
	service.BaseService
	agentserver        network.WSAgentServer
	pattern            string
	port               uint16
	bEnableCompression bool
}

func (ws *WSAgentService) OnInit() error {
	ws.AddModule(&ws.agentserver)
	ws.agentserver.Init(ws.port)
	return nil
}

func (ws *WSAgentService) OnRun() bool {
	ws.agentserver.Start()

	return false
}

func NewWSAgentService(port uint16) *WSAgentService {
	wss := new(WSAgentService)

	wss.port = port
	return wss
}

func (ws *WSAgentService) OnDestory() error {
	return nil
}

func (ws *WSAgentService) SetupAgent(pattern string, agent network.IAgent, bEnableCompression bool) {
	ws.agentserver.SetupAgent(pattern, agent, bEnableCompression)
}
