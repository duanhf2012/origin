package tcpgateway

type ILoadBalance interface {
	SelectNode(serviceName string) int //选择一个结点，通过服务名称
}
