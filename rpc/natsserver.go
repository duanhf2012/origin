package rpc

import (
	"github.com/nats-io/nats.go"
	"github.com/duanhf2012/origin/v2/log"
	"time"
)

type NatsServer struct {
	BaseServer
	natsUrl string
	
	natsConn *nats.Conn
	NoRandomize bool

	nodeSubTopic string
	compressBytesLen int
	notifyEventFun NotifyEventFun
}

const reconnectWait = 3*time.Second
func (ns *NatsServer) Start() error{
	var err error
	var options []nats.Option

	options = append(options,nats.DisconnectErrHandler(func(nc *nats.Conn, err error) {
		log.Error("nats is disconnected",log.String("connUrl",nc.ConnectedUrl()))
		ns.notifyEventFun(&NatsConnEvent{IsConnect:false})
	}))

	options = append(options,nats.ConnectHandler(func(nc *nats.Conn) {
		log.Info("nats is connected",log.String("connUrl",nc.ConnectedUrl()))
		ns.notifyEventFun(&NatsConnEvent{IsConnect:true})
		//log.Error("nats is connect",log.String("connUrl",nc.ConnectedUrl()))
	}))

	options = append(options,nats.ReconnectHandler(func(nc *nats.Conn) {
		ns.notifyEventFun(&NatsConnEvent{IsConnect:true})
		log.Info("nats is reconnected",log.String("connUrl",nc.ConnectedUrl()))
	}))

	options = append(options,nats.MaxReconnects(-1))
	options = append(options,nats.ReconnectWait(reconnectWait))

	if ns.NoRandomize {
		options = append(options,nats.DontRandomize())
	}

	ns.natsConn,err = nats.Connect(ns.natsUrl,options...)
	if err != nil {
		log.Error("Connect to nats fail",log.String("natsUrl",ns.natsUrl),log.ErrorAttr("err",err))
		return err
	}

	//开始订阅
	_,err = ns.natsConn.QueueSubscribe(ns.nodeSubTopic,"os", func(msg *nats.Msg) {
		ns.processRpcRequest(msg.Data,msg.Header.Get("fnode"),ns.WriteResponse)
	})

	return err
}

func (ns *NatsServer) WriteResponse(processor IRpcProcessor, nodeId string,serviceMethod string, seq uint64, reply interface{}, rpcError RpcError){
	var mReply []byte
	var err error

	if reply != nil {
		mReply, err = processor.Marshal(reply)
		if err != nil {
			rpcError = ConvertError(err)
		}
	}

	var rpcResponse RpcResponse
	rpcResponse.RpcResponseData = processor.MakeRpcResponse(seq, rpcError, mReply)
	bytes, err := processor.Marshal(rpcResponse.RpcResponseData)
	defer processor.ReleaseRpcResponse(rpcResponse.RpcResponseData)

	if err != nil {
		log.Error("mashal RpcResponseData failed",log.String("serviceMethod",serviceMethod),log.ErrorAttr("error",err))
		return
	}

	var compressBuff[]byte
	bCompress := uint8(0)
	if ns.compressBytesLen >0 && len(bytes) >= ns.compressBytesLen {
		compressBuff,err = compressor.CompressBlock(bytes)
		if err != nil {
			log.Error("CompressBlock failed",log.String("serviceMethod",serviceMethod),log.ErrorAttr("error",err))
			return
		}
		if len(compressBuff) < len(bytes) {
			bytes = compressBuff
			bCompress = 1<<7
		}
	}

	sendData := make([]byte,0,4096)
	byteTypeAndCompress := []byte{uint8(processor.GetProcessorType())|bCompress}
	sendData = append(sendData,byteTypeAndCompress...)
	sendData = append(sendData,bytes...)
	err = ns.natsConn.PublishMsg(&nats.Msg{Subject: "oc."+nodeId, Data: sendData})

	if cap(compressBuff) >0 {
		compressor.CompressBufferCollection(compressBuff)
	}

	if err != nil {
		log.Error("WriteMsg error,Rpc return is fail",log.String("nodeId",nodeId),log.String("serviceMethod",serviceMethod),log.ErrorAttr("error",err))
	}
}

func (ns *NatsServer) Stop(){
	if ns.natsConn != nil {
		ns.natsConn.Close()
	}
}

func (ns *NatsServer) initServer(natsUrl string, noRandomize bool,localNodeId string,compressBytesLen int,rpcHandleFinder RpcHandleFinder,notifyEventFun NotifyEventFun){
	ns.natsUrl = natsUrl
	ns.NoRandomize = noRandomize
	ns.localNodeId = localNodeId
	ns.compressBytesLen = compressBytesLen
	ns.notifyEventFun = notifyEventFun
	ns.initBaseServer(compressBytesLen,rpcHandleFinder)
	ns.nodeSubTopic = "os."+localNodeId //服务器
}
