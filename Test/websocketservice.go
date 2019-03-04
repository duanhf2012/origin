package main

import (
	"fmt"

	"github.com/duanhf2012/origin/network"
	"github.com/duanhf2012/origin/service"
	"github.com/gorilla/websocket"
)

//CRHDataService ...
type CWebSockService struct {
	service.BaseService
	network.BaseMessageReciver

	//websockServer network.IWebsocketServer
}

//NewCRHService ...
func NewWebSockService() *CWebSockService {
	wss := new(CWebSockService)
	return wss
}

//OnInit ...
func (ws *CWebSockService) OnInit() error {
	return nil
}

//OnRun ...
func (ws *CWebSockService) OnRun() bool {
	return false
}

func (ws *CWebSockService) OnConnected(clientid uint64) {
	date := []byte("CWebSockService OnConnected!..")
	ws.WsServer.SendMsg(clientid, websocket.TextMessage, date)
}

func (ws *CWebSockService) OnDisconnect(clientid uint64, err error) {
	fmt.Print("CWebSockService OnDisconnect")
}

func (ws *CWebSockService) OnRecvMsg(clientid uint64, msgtype int, data []byte) {
	date := []byte("OnRecvMsg!..CWebSockService")
	ws.WsServer.SendMsg(clientid, websocket.TextMessage, date)
}
