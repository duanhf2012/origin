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


func (gateService *TcpGateService) OnInit() error {
	gateService.OnLoad()
	//注册监听客户连接断开事件
	gateService.processor.SetDisConnectedHandler(gateService.router.OnDisconnected)
	//注册监听客户连接事件
	gateService.processor.SetConnectedHandler(gateService.router.OnConnected)

	//注册监听消息类型MsgType_MsgReq，并注册回调
	gateService.processor.SetRawMsgHandler(gateService.router.RouterMessage)
	//将protobuf消息处理器设置到TcpService服务中
	gateService.tcpService.SetProcessor(gateService.processor, gateService.GetEventHandler())
	
	return nil
}

func (gateService *TcpGateService) SetEventChannel(channelNum int){
	gateService.GetEventProcessor().SetEventChannel(channelNum)
}

func (gateService *TcpGateService) OnLoad() {
	//设置默认LoadBalance
	if gateService.loadBalance == nil {
		gateService.loadBalance = &LoadBalance{}
	}

	//设置默认Router
	if gateService.router == nil {
		gateService.router = NewRouter(gateService.loadBalance, gateService, gateService.GetServiceCfg())
	}

	//新建内置的protobuf处理器，您也可以自定义路由器，比如json
	if gateService.processor == nil {
		gateService.processor = processor.NewPBRawProcessor()
	}

	//加载路由
	gateService.router.Load()

	//设置默认的TcpService服务
	if gateService.tcpService == nil {
		gateService.tcpService =  node.GetService("TcpService").(*tcpservice.TcpService)
	}

	if gateService.tcpService == nil {
		panic("TcpService is not installed!")
	}
}

func (gateService *TcpGateService) SetLoadBalance(loadBalance ILoadBalance){
	gateService.loadBalance = loadBalance
}

func (gateService *TcpGateService) SetRouter(router IRouter){
	gateService.router = router
}

func (gateService *TcpGateService) SetRawProcessor(processor processor.IRawProcessor){
	gateService.processor = processor
}

func (gateService *TcpGateService) SetTcpGateService(tcpService *tcpservice.TcpService){
	gateService.tcpService = tcpService
}

func (gateService *TcpGateService) RPC_Dispatch(replyMsg *ReplyMessage) error {
	for _,id := range replyMsg.ClientList {
		err := gateService.tcpService.SendRawMsg(id,replyMsg.Msg)
		if err != nil {
			log.Debug("SendRawMsg fail:%+v!",err)
		}
	}

	return nil
}


