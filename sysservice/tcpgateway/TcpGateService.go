package tcpgateway

import (
	"github.com/duanhf2012/origin/log"
	"github.com/duanhf2012/origin/network/processor"
	"github.com/duanhf2012/origin/node"
	"github.com/duanhf2012/origin/service"
	"github.com/duanhf2012/origin/sysservice/tcpservice"
)

func init(){
	node.Setup(&tcpservice.TcpService{})
	node.Setup(&TcpGateService{})
}

type MsgTypeRouterInfo struct {
	router      IRouter
	serviceName string
}

type TcpGateService struct {
	service.Service

	processor processor.IRawProcessor
	tcpService *tcpservice.TcpService

	loadBalance ILoadBalance
	router IRouter
}


func (slf *TcpGateService) OnInit() error {
	slf.OnLoad()

	//注册监听客户连接断开事件
	slf.processor.SetDisConnectedHandler(slf.router.OnDisconnected)
	//注册监听客户连接事件
	slf.processor.SetConnectedHandler(slf.router.OnConnected)

	//注册监听消息类型MsgType_MsgReq，并注册回调
	slf.processor.SetRawMsgHandler(slf.router.RouterMessage)
	//将protobuf消息处理器设置到TcpService服务中
	slf.tcpService.SetProcessor(slf.processor,slf.GetEventHandler())
	
	return nil
}

func (slf *TcpGateService) OnLoad() {
	//设置默认LoadBalance
	if slf.loadBalance == nil {
		slf.loadBalance = &LoadBalance{}
	}

	//设置默认Router
	if slf.router == nil {
		slf.router = NewRouter(slf.loadBalance,slf,slf.GetServiceCfg())
	}

	//新建内置的protobuf处理器，您也可以自定义路由器，比如json
	if slf.processor == nil {
		slf.processor = processor.NewPBRawProcessor()
	}

	//加载路由
	slf.router.Load()

	//设置默认的TcpService服务
	if slf.tcpService == nil {
		slf.tcpService =  node.GetService("TcpService").(*tcpservice.TcpService)
	}

	if slf.tcpService == nil {
		panic("TcpService is not installed!")
	}
}

func (slf *TcpGateService) SetLoadBalance(loadBalance ILoadBalance){
	slf.loadBalance = loadBalance
}

func (slf *TcpGateService) SetRouter(router IRouter){
	slf.router = router
}

func (slf *TcpGateService) SetRawProcessor(processor processor.IRawProcessor){
	slf.processor = processor
}

func (slf *TcpGateService) SetTcpGateService(tcpService *tcpservice.TcpService){
	slf.tcpService = tcpService
}

func (slf *TcpGateService) RPC_Dispatch(replyMsg *ReplyMessage) error {
	for _,id := range replyMsg.ClientList {
		err := slf.tcpService.SendRawMsg(id,replyMsg.Msg)
		if err != nil {
			log.Debug("SendRawMsg fail:%+v!",err)
		}
	}

	return nil
}


