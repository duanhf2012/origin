package GateService

import (
	"encoding/json"
	"fmt"
	"github.com/duanhf2012/origin/network/processor"
	"github.com/duanhf2012/origin/node"
	"github.com/duanhf2012/origin/service"
	"github.com/duanhf2012/origin/sysmodule"
	"github.com/duanhf2012/origin/sysservice"
	"net/http"
	"time"
)

type GateService struct {
	service.Service
	processor *processor.PBProcessor
	processor2 *processor.PBProcessor
	httpRouter sysservice.IHttpRouter

	redisModule *sysmodule.RedisModule
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

	wsService.SetProcessor(slf.processor2,slf.GetEventHandler())


	httpervice := node.GetService("HttpService").(*sysservice.HttpService)
	slf.httpRouter = sysservice.NewHttpHttpRouter()
	httpervice.SetHttpRouter(slf.httpRouter,slf.GetEventHandler())

	slf.httpRouter.GET("/get/query", slf.HttpTest)
	slf.httpRouter.POST("/post/query", slf.HttpTestPost)
	slf.httpRouter.SetServeFile(sysservice.METHOD_GET,"/img/head/","d:/img")

	//pCronExpr,_ := timer.NewCronExpr("0 * * * * *")
	//slf.CronFunc(pCronExpr,slf.Test)

	redisCfg := sysmodule.ConfigRedis{
		IP            :"192.168.0.5",
		Port          :"6379",
		Password      :"",
		DbIndex       :0,
		MaxIdle       :50, //最大的空闲连接数，表示即使没有redis连接时依然可以保持N个空闲的连接，而不被清除，随时处于待命状态。
		MaxActive     :50, //最大的激活连接数，表示同时最多有N个连接
		IdleTimeout   :30, //最大的空闲连接等待时间，超过此时间后，空闲连接将被关闭
		SyncRouterNum :10, //异步执行Router数量
	}
	slf.redisModule = &sysmodule.RedisModule{}
	slf.redisModule.Init(&redisCfg)
	slf.redisModule.OnInit()
	slf.AddModule(slf.redisModule)

	slf.AfterFunc(time.Second, slf.TestRedis)

	return nil
}

func (slf *GateService) Test(){
	fmt.Print("xxxxx\n")
}

func (slf *GateService) TestRedis() {
	slf.redisModule.GetHashValueByHashKeyList("BITGET_2160_LastSetLevelInfo", "SBTC_USD_1", "BTC_SUSDT_1", "SBTC_USD_3")
	//slf.redisModule.GetHashValueByKey("BITGET_2160_LastSetLevelInfo", "SBTC_USD_1")
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
