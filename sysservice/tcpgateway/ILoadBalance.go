package tcpgateway

type ILoadBalance interface {
	SelectNode(serviceName string,clientId uint64,eventType string,msgType uint16,msg []byte) int //选择一个结点，通过服务名称
}
