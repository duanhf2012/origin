package event

type EventType int

//大于Sys_Event_User_Define给用户定义
const (
	Sys_Event_Tcp         EventType = 1
	Sys_Event_Http_Event  EventType = 2
	Sys_Event_WebSocket   EventType = 3
	Sys_Event_User_Define EventType = 1000
)

