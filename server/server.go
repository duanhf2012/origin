package server

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
)

type CExitCtl struct {
	exit      chan bool
	waitGroup *sync.WaitGroup
}

type cserver struct {
	CExitCtl
	serviceManager service.IServiceManager
	sigs           chan os.Signal
}

func (s *cserver) Init() {
	service.InitLog()
	service.InstanceServiceMgr().Init()

	s.exit = make(chan bool)
	s.waitGroup = &sync.WaitGroup{}
	s.sigs = make(chan os.Signal, 1)
	signal.Notify(s.sigs, syscall.SIGINT, syscall.SIGTERM)
}

func (s *cserver) SetupService(services ...service.IService) {
	for i := 0; i < len(services); i++ {
		if cluster.InstanceClusterMgr().HasLocalService(services[i].GetServiceName()) == true {
			service.InstanceServiceMgr().Setup(services[i])
		}

	}

}

func (s *cserver) Start() {
	go func() {

		log.Println(http.ListenAndServe("localhost:6060", nil))
	}()

	service.InstanceServiceMgr().Start(s.exit, s.waitGroup)
	cluster.InstanceClusterMgr().Start()

	select {
	case <-s.sigs:
		fmt.Println("收到信号推出程序")
	}

	s.Stop()
}

func (s *cserver) Stop() {
	close(s.exit)
	s.waitGroup.Wait()
}

func NewServer() *cserver {
	err := cluster.InstanceClusterMgr().Init()
	if err != nil {
		fmt.Print(err)
		return nil
	}

	return new(cserver)
}

func HasCmdParam(param string) bool {
	for i := 0; i < len(os.Args); i++ {
		if os.Args[i] == param {
			return true
		}
	}

	return false
}
