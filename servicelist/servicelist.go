package servicelist

import (
	"github.com/duanhf2012/origin/originnode"
	"github.com/duanhf2012/origin/service"
)

var node = func() *originnode.COriginNode {
	//1.新建OrginNode结点
	node := originnode.NewOriginNode()
	if node == nil {
		println("originnode.NewOriginNode fail")
		return nil
	}
	return node
}()

var serviceList []service.IService

// 增加服务列表 在init中调用
// 因为是init的时候调用 所以不用锁
func PushService(s service.IService) {
	serviceList = append(serviceList, s)
}

//在main中调用该函数即可加载所有service
//debugCheckUrl "localhost:6060"
func Start(debugCheckUrl string) {
	node.OpenDebugCheck(debugCheckUrl)
	node.SetupService(serviceList...)

	//5.初始化结点
	node.Init()

	//6.开始结点
	node.Start()
}
