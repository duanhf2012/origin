package sysservice

import (
	"github.com/duanhf2012/origin/network"
	"github.com/duanhf2012/origin/service"
)

type WSServerService struct {
	service.BaseService
	wsserver network.WebsocketServer

	pattern            string
	port               uint16
	messageReciver     network.IMessageReceiver
	bEnableCompression bool
}

func (ws *WSServerService) OnInit() error {

	ws.wsserver.Init(ws.port)
	return nil
}

func (ws *WSServerService) OnRun() bool {
	ws.wsserver.Start()

	return false
}

func NewWSServerService(port uint16) *WSServerService {
	wss := new(WSServerService)

	wss.port = port

	wss.Init(wss, 0)
	return wss
}

func (ws *WSServerService) OnDestory() error {
	return nil
}
func (ws *WSServerService) SetupReciver(pattern string, messageReciver network.IMessageReceiver, bEnableCompression bool) {
	ws.wsserver.SetupReciver(pattern, messageReciver, bEnableCompression)
}
