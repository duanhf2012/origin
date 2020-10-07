package tcpgateway

type IRouter interface {
	RouterMessage(clientId uint64,msgType uint16,msg []byte) //消息转发
	RouterEvent(clientId uint64,eventType string) bool//消息转发
	Load() //加载路由规则

	OnDisconnected(clientId uint64)
	OnConnected(clientId uint64)
	//ReplyMessage(clientId uint64,msg []byte)
}
