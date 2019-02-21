package originnode

import (
	"fmt"
	"log"

	"net/http"
	_ "net/http/pprof"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/duanhf2012/origin/cluster"
	"github.com/duanhf2012/origin/service"
	"github.com/duanhf2012/origin/sysmodule"
)

type CExitCtl struct {
	exit      chan bool
	waitGroup *sync.WaitGroup
}

type COriginNode struct {
	CExitCtl
	serviceManager     service.IServiceManager
	sigs               chan os.Signal
	debugListenAddress string
}

func (s *COriginNode) Init() {
	//初始化全局模块
	service.InitLog()
	imodule := g_module.GetModuleById(sysmodule.SYS_LOG)
	service.InstanceServiceMgr().Init(imodule.(service.ILogger), s.exit, s.waitGroup)

	s.sigs = make(chan os.Signal, 1)
	signal.Notify(s.sigs, syscall.SIGINT, syscall.SIGTERM)
}

// OpenDebugCheck ("localhost:6060")...http://localhost:6060/
func (s *COriginNode) OpenDebugCheck(listenAddress string) {
	s.debugListenAddress = listenAddress
}

func (s *COriginNode) SetupService(services ...service.IService) {
	for i := 0; i < len(services); i++ {
		if cluster.InstanceClusterMgr().HasLocalService(services[i].GetServiceName()) == true {
			service.InstanceServiceMgr().Setup(services[i])
		}

	}

	//将其他服务通知已经安装
	for i := 0; i < len(services); i++ {
		for j := 0; j < len(services); j++ {
			if cluster.InstanceClusterMgr().HasLocalService(services[i].GetServiceName()) == false {
				continue
			}

			if services[i].GetServiceName() == services[j].GetServiceName() {
				continue
			}

			services[i].OnSetupService(services[j])

		}
		services[i].(service.IModule).SetOwnerService(services[i])
		services[i].(service.IModule).SetOwner(services[i].(service.IModule))
	}

}

func (s *COriginNode) Start() {
	if s.debugListenAddress != "" {
		go func() {

			log.Println(http.ListenAndServe(s.debugListenAddress, nil))
		}()
	}

	cluster.InstanceClusterMgr().Start()
	RunGlobalModule()
	service.InstanceServiceMgr().Start()

	select {
	case <-s.sigs:
		fmt.Println("收到信号推出程序")
	}

	s.Stop()
}

func (s *COriginNode) Stop() {
	close(s.exit)
	s.waitGroup.Wait()
}

func NewOrginNode() *COriginNode {
	node := new(COriginNode)
	node.exit = make(chan bool)
	node.waitGroup = &sync.WaitGroup{}
	InitGlobalModule(node.exit, node.waitGroup)
	var syslogmodule sysmodule.LogModule
	syslogmodule.Init("system", sysmodule.LEVER_INFO)
	syslogmodule.SetModuleId(sysmodule.SYS_LOG)
	AddModule(&syslogmodule)

	err := cluster.InstanceClusterMgr().Init()
	if err != nil {
		fmt.Print(err)
		return nil
	}

	return node
}

func HasCmdParam(param string) bool {
	for i := 0; i < len(os.Args); i++ {
		if os.Args[i] == param {
			return true
		}
	}

	return false
}
