package GateService

import (
	"encoding/json"
	"fmt"
	"github.com/duanhf2012/origin/network/processor"
	"github.com/duanhf2012/origin/node"
	"github.com/duanhf2012/origin/service"
	"github.com/duanhf2012/origin/sysservice"
	"github.com/duanhf2012/origin/util/timer"
	"net/http"
)

type GateService struct {
	service.Service
	processor *processor.PBProcessor
	processor2 *processor.PBProcessor
	httpRouter sysservice.IHttpRouter
}

func (slf *GateService) OnInit() error{
	tcpervice := node.GetService("TcpService").(*sysservice.TcpService)
	slf.processor = &processor.PBProcessor{}
	slf.processor.RegisterDisConnected(slf.OnDisconnected)
	slf.processor.RegisterConnected(slf.OnConnected)
	tcpervice.SetProcessor(slf.processor,slf.GetEventHandler())


	wsService := node.GetService("WSService").(*sysservice.WSService)
	slf.processor2 = &processor.PBProcessor{}
	slf.processor2.RegisterDisConnected(slf.OnWSDisconnected)
	slf.processor2.RegisterConnected(slf.OnWSConnected)
	slf.processor2.Register()
	wsService.SetProcessor(slf.processor2,slf.GetEventHandler())


	httpervice := node.GetService("HttpService").(*sysservice.HttpService)
	slf.httpRouter = sysservice.NewHttpHttpRouter()
	httpervice.SetHttpRouter(slf.httpRouter,slf.GetEventHandler())

	slf.httpRouter.GET("/get/query", slf.HttpTest)
	slf.httpRouter.POST("/post/query", slf.HttpTestPost)
	slf.httpRouter.SetServeFile(sysservice.METHOD_GET,"/img/head/","d:/img")

	pCronExpr,_ := timer.NewCronExpr("0 * * * * *")
	slf.CronFunc(pCronExpr,slf.Test)

	return nil
}

func (slf *GateService) Test(){
	fmt.Print("xxxxx\n")
}

func (slf *GateService) HttpTest(session *sysservice.HttpSession) {
	session.SetHeader("a","b")
	session.Write([]byte("this is a test"))
	v,_:=session.Query("a")
	v2,_:=session.Query("b")
	fmt.Print(string(session.GetBody()),"\n",v,"\n",v2)
}

func (slf *GateService) HttpTestPost(session *sysservice.HttpSession) {
	session.SetHeader("a","b")
	v,_:=session.Query("a")
	v2,_:=session.Query("b")

	byteBody := session.GetBody()
	fmt.Print(string(session.GetBody()),"\n",v,"\n",v2)

	testa := struct {
		AA int `json:"aa"`
		BB string `json:"bb"`
	}{}
	json.Unmarshal(byteBody, &testa)
	fmt.Println(testa)

	testa.AA = 100
	testa.BB = "this is a test"
	session.WriteJsonDone(http.StatusOK,"asdasda")
}

func (slf *GateService) OnConnected(clientid uint64){
	fmt.Printf("client id %d connected",clientid)
}


func (slf *GateService) OnDisconnected(clientid uint64){
	fmt.Printf("client id %d disconnected",clientid)
}

func (slf *GateService) OnWSConnected(clientid uint64){
	fmt.Printf("client id %d connected",clientid)
}


func (slf *GateService) OnWSDisconnected(clientid uint64){
	fmt.Printf("client id %d disconnected",clientid)
}
