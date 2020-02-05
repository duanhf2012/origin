package originnode

import (
	"fmt"
	"log"
	"strconv"
	"strings"

	"github.com/duanhf2012/origin/util"

	"net/http"
	_ "net/http/pprof"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/duanhf2012/origin/cluster"
	"github.com/duanhf2012/origin/service"
	"github.com/duanhf2012/origin/sysmodule"
	"github.com/duanhf2012/origin/sysservice"
)

type CExitCtl struct {
	exitChan  chan bool
	waitGroup *sync.WaitGroup
}

type COriginNode struct {
	CExitCtl
	serviceManager     service.IServiceManager
	sigs               chan os.Signal
	debugListenAddress string
}

var initservicelist []service.IService

func InitService(iservice service.IService) {
	initservicelist = append(initservicelist, iservice)
}

func (s *COriginNode) Init() {

	s.SetupService(initservicelist...)

	//初始化全局模块
	logger := service.InstanceServiceMgr().FindService("syslog").(service.ILogger)
	ret := service.InstanceServiceMgr().Init(logger, s.exitChan, s.waitGroup)
	if ret == false {
		os.Exit(-1)
	}

	util.Log = logger.Printf
	s.sigs = make(chan os.Signal, 1)
	signal.Notify(s.sigs, syscall.SIGINT, syscall.SIGTERM)
}

// OpenDebugCheck ("localhost:6060")...http://localhost:6060/
func (s *COriginNode) OpenDebugCheck(listenAddress string) {
	s.debugListenAddress = listenAddress
}

func (s *COriginNode) SetupService(services ...service.IService) {
	ppService := &sysservice.PProfService{}
	services = append(services, ppService)
	cluster.InstanceClusterMgr().AddLocalService(ppService)
	for i := 0; i < len(services); i++ {
		services[i].Init(services[i])

		if cluster.InstanceClusterMgr().HasLocalService(services[i].GetServiceName()) == true {
			service.InstanceServiceMgr().Setup(services[i])
		}
	}
}

func (s *COriginNode) Start() {
	if s.debugListenAddress != "" {
		go func() {
			log.Println(http.ListenAndServe(s.debugListenAddress, nil))
		}()
	}

	//开始运行集群
	cluster.InstanceClusterMgr().Start()

	//开启所有服务
	service.InstanceServiceMgr().Start()

	//监听退出信号
	select {
	case <-s.sigs:
		service.GetLogger().Printf(sysmodule.LEVER_WARN, "Recv stop sig")
		fmt.Printf("Recv stop sig")
	}

	//停止运行程序
	s.Stop()
	service.GetLogger().Printf(sysmodule.LEVER_INFO, "Node stop run...")
}

func (s *COriginNode) Stop() {
	close(s.exitChan)
	s.waitGroup.Wait()
}

func GetCmdParamNodeId() int {
	if len(os.Args) < 2 {
		return 0
		//return fmt.Errorf("Param error not find NodeId=number")
	}

	parts := strings.Split(os.Args[1], "=")
	if len(parts) < 2 {
		return 0
		//return fmt.Errorf("Param error not find NodeId=number")
	}

	if parts[0] != "NodeId" {
		return 0
		//return fmt.Errorf("Param error not find NodeId=number")
	}

	//读取配置
	currentNodeid, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0
	}

	return currentNodeid
}

func NewOriginNode() *COriginNode {
	CurrentNodeId := GetCmdParamNodeId()
	if CurrentNodeId == 0 {
		fmt.Print("Param error not find NodeId=number")
		os.Exit(-1)
	}

	//创建模块
	node := new(COriginNode)
	node.exitChan = make(chan bool)
	node.waitGroup = &sync.WaitGroup{}

	//安装系统服务
	syslogservice := &sysservice.LogService{}
	syslogservice.InitLog("syslog", fmt.Sprintf("syslog_%d", CurrentNodeId), sysmodule.LEVER_DEBUG)
	service.InstanceServiceMgr().Setup(syslogservice)

	//初始化集群对象
	err := cluster.InstanceClusterMgr().Init(CurrentNodeId)
	if err != nil {
		fmt.Print(err)
		os.Exit(-1)
		return nil
	}

	return node
}

func (s *COriginNode) GetSysLog() *sysservice.LogService {
	logService := service.InstanceServiceMgr().FindService("syslog")
	if logService == nil {
		fmt.Printf("Cannot find syslog service!")
		os.Exit(-1)
		return nil
	}

	return logService.(*sysservice.LogService)
}
