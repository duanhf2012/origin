package sysservice

import (
	"github.com/duanhf2012/origin/network"
	"github.com/duanhf2012/origin/service"
)

type TcpSocketPbService struct {
	service.BaseService
	listenaddr string
	tcpsocketserver network.TcpSocketServer
	reciver network.ITcpSocketServerReciver
}


func NewTcpSocketPbService(listenaddr string,reciver network.ITcpSocketServerReciver) *TcpSocketPbService {
	ts := new(TcpSocketPbService)

	ts.listenaddr = listenaddr
	ts.reciver = reciver

	ts.tcpsocketserver.Register(listenaddr,reciver)
	return ts
}

func (slf *TcpSocketPbService) OnInit() error {
	return nil
}

func (slf *TcpSocketPbService) OnRun() bool {
	slf.tcpsocketserver.Start()
	return false
}

