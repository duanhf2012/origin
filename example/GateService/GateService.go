package GateService

import (
	"github.com/duanhf2012/origin/event"
	"github.com/duanhf2012/origin/node"
	"github.com/duanhf2012/origin/service"
	"github.com/duanhf2012/origin/sysservice"
)

type GateService struct {
	processor *PBProcessor
	service.Service
}

func (slf *GateService) OnInit() error{
	tcpervice := node.GetService("TcpService").(*sysservice.TcpService)
	tcpervice.SetProcessor(&PBProcessor{})
	return nil
}


func (slf *GateService) OnEventHandler(ev *event.Event) error{
	if ev.Type == event.Sys_Event_Tcp_RecvPack {
		pPack := ev.Data.(*sysservice.TcpPack)
		slf.processor.Route(pPack.Data,pPack.ClientId)
	}else if ev.Type == event.Sys_Event_Tcp_Connected {
		pPack := ev.Data.(*sysservice.TcpPack)
		slf.OnConnected(pPack.ClientId)
	}else if ev.Type == event.Sys_Event_Tcp_DisConnected {
		pPack := ev.Data.(*sysservice.TcpPack)
		slf.OnDisconnected(pPack.ClientId)
	}
	return nil
}

func (slf *GateService) OnConnected(clientid uint64){

}


func (slf *GateService) OnDisconnected(clientid uint64){

}
