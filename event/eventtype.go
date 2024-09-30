package event

type EventType int

// 大于Sys_Event_User_Define给用户定义
const (
	ServiceRpcRequestEvent  EventType = -1
	ServiceRpcResponseEvent EventType = -2

	Sys_Event_Tcp             EventType = -3
	Sys_Event_Http_Event      EventType = -4
	Sys_Event_WebSocket       EventType = -5
	Sys_Event_Kcp         	  EventType = -6
	Sys_Event_Node_Conn_Event EventType = -7
	Sys_Event_Nats_Conn_Event EventType = -8
	Sys_Event_DiscoverService EventType = -9
	Sys_Event_Retire          EventType = -10
	Sys_Event_EtcdDiscovery   EventType = -11
	Sys_Event_Gin_Event       EventType = -12
	Sys_Event_FrameTick       EventType = -13

	Sys_Event_User_Define EventType = 1
)
