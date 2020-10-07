package tcpgateway

type LoadBalance struct {
}

func (slf *LoadBalance) SelectNode(serviceName string) int {
	return 1
}
