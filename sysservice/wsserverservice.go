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

	ws.wsserver.Init(ws.pattern, ws.port, ws.messageReciver, ws.bEnableCompression)
	return nil
}

func (ws *WSServerService) OnRun() bool {
	ws.wsserver.Start()

	return false
}

func NewWSServerService(pattern string, port uint16, messageReciver network.IMessageReceiver, bEnableCompression bool) *WSServerService {
	wss := new(WSServerService)
	wss.pattern = pattern
	wss.port = port
	wss.messageReciver = messageReciver
	wss.bEnableCompression = bEnableCompression

	wss.Init(wss, 0)
	return wss
}

func (ws *WSServerService) OnDestory() error {
	return nil
}
