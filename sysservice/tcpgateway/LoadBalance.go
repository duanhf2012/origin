package tcpgateway

type LoadBalance struct {
}

func (balance *LoadBalance) SelectNode(serviceName string,clientId uint64,eventType string,msgType uint16,msg []byte) int {
	return 1
}
