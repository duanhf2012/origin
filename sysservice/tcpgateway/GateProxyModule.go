package tcpgateway

import (
	"fmt"
	"github.com/duanhf2012/origin/service"
	"github.com/duanhf2012/origin/sysservice/tcpservice"
	"github.com/golang/protobuf/proto"
)

type GateProxyModule struct{
	service.Module
	defaultGateRpc string
}

func NewGateProxyModule() *GateProxyModule{
	return &GateProxyModule{defaultGateRpc:"TcpGateService.RPC_Dispatch"}
}

func (gate *GateProxyModule) Send(clientId interface{},msgType uint16,msg proto.Message) error {
	//对agentId进行分组
	mapNodeClientId := map[int][]uint64{}
	switch clientId.(type) {
	case uint64:
		id := clientId.(uint64)
		nodeId := tcpservice.GetNodeId(id)
		mapNodeClientId[nodeId] = append(mapNodeClientId[nodeId],id)

	case []uint64:
		idList := clientId.([]uint64)
		for _,id := range idList{
			nodeId := tcpservice.GetNodeId(id)
			mapNodeClientId[nodeId] = append(mapNodeClientId[nodeId],id)
		}
	}

	bData,err := proto.Marshal(msg)
	if err!=nil {
		return err
	}
	var replyMsg ReplyMessage
	replyMsg.MsgType = proto.Uint32(uint32(msgType))
	replyMsg.Msg = bData

	for nodeId,clientIdList := range mapNodeClientId {
		if nodeId <0 || nodeId>tcpservice.MaxNodeId {
			fmt.Errorf("nodeid is error %d",nodeId)
			continue
		}

		replyMsg.ClientList = clientIdList
		gate.GetService().GetRpcHandler().GoNode(nodeId,gate.defaultGateRpc,&replyMsg)
	}

	return nil
}

func (gate *GateProxyModule) SetDefaultGateRpcMethodName(rpcMethodName string){
	gate.defaultGateRpc = rpcMethodName
}

func (gate *GateProxyModule) send(clientId uint64,msgType uint16,msg []byte) error {
	nodeId := tcpservice.GetNodeId(clientId)
	if nodeId <0 || nodeId>tcpservice.MaxNodeId {
		return fmt.Errorf("nodeid is error %d",nodeId)
	}

	var replyMsg ReplyMessage
	replyMsg.MsgType = proto.Uint32(uint32(msgType))
	replyMsg.Msg = msg
	replyMsg.ClientList = append(replyMsg.ClientList ,clientId)

	return gate.GetService().GetRpcHandler().GoNode(nodeId, gate.defaultGateRpc,&replyMsg)
}
