package rpc

import "github.com/duanhf2012/origin/v2/event"

type NotifyEventFun func (event event.IEvent)

// RpcConnEvent Node结点连接事件
type RpcConnEvent struct{
	IsConnect bool
	NodeId string
}

func (rc *RpcConnEvent) GetEventType() event.EventType{
	return event.Sys_Event_Node_Conn_Event
}

type NatsConnEvent struct {
	IsConnect bool
}

func (nc *NatsConnEvent)  GetEventType() event.EventType{
	return event.Sys_Event_Nats_Conn_Event
}
