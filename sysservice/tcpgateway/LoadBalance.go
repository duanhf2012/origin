package tcpgateway

type LoadBalance struct {
}

func (balance *LoadBalance) SelectNode(serviceName string) int {
	return 1
}
