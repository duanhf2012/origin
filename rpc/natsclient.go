package rpc

import (
	"github.com/duanhf2012/origin/v2/log"
	"github.com/duanhf2012/origin/v2/network"
	"github.com/nats-io/nats.go"
	"reflect"
	"time"
)

// NatsClient 跨结点连接的Client
type NatsClient struct {
	localNodeId    string
	notifyEventFun NotifyEventFun

	natsConn *nats.Conn
	client   *Client
}

func (nc *NatsClient) Start(natsConn *nats.Conn) error {
	nc.natsConn = natsConn
	_, err := nc.natsConn.QueueSubscribe("oc."+nc.localNodeId, "oc", nc.onSubscribe)

	return err
}

func (nc *NatsClient) onSubscribe(msg *nats.Msg) {
	//处理消息
	nc.client.processRpcResponse(msg.Data)
}

func (nc *NatsClient) SetConn(conn *network.TCPConn) {
}

func (nc *NatsClient) Close(waitDone bool) {
}

func (nc *NatsClient) Run() {
}

func (nc *NatsClient) OnClose() {
}

func (nc *NatsClient) Bind(server IServer) {
	s := server.(*NatsServer)
	nc.natsConn = s.natsConn
}

func (nc *NatsClient) Go(nodeId string, timeout time.Duration, rpcHandler IRpcHandler, noReply bool, serviceMethod string, args interface{}, reply interface{}) *Call {
	_, processor := GetProcessorType(args)
	InParam, err := processor.Marshal(args)
	if err != nil {
		log.Error("Marshal is fail", log.ErrorAttr("error", err))
		call := MakeCall()
		call.DoError(err)
		return call
	}

	return nc.client.rawGo(nodeId, nc, timeout, rpcHandler, processor, noReply, 0, serviceMethod, InParam, reply)
}

func (nc *NatsClient) RawGo(nodeId string, timeout time.Duration, rpcHandler IRpcHandler, processor IRpcProcessor, noReply bool, rpcMethodId uint32, serviceMethod string, rawArgs []byte, reply interface{}) *Call {
	return nc.client.rawGo(nodeId, nc, timeout, rpcHandler, processor, noReply, rpcMethodId, serviceMethod, rawArgs, reply)
}

func (nc *NatsClient) AsyncCall(nodeId string, timeout time.Duration, rpcHandler IRpcHandler, serviceMethod string, callback reflect.Value, args interface{}, replyParam interface{}, cancelable bool) (CancelRpc, error) {
	cancelRpc, err := nc.client.asyncCall(nodeId, nc, timeout, rpcHandler, serviceMethod, callback, args, replyParam, cancelable)
	if err != nil {
		callback.Call([]reflect.Value{reflect.ValueOf(replyParam), reflect.ValueOf(err)})
	}

	return cancelRpc, nil
}

func (nc *NatsClient) WriteMsg(nodeId string, args ...[]byte) error {
	buff := make([]byte, 0, 4096)
	for _, ar := range args {
		buff = append(buff, ar...)
	}

	var msg nats.Msg
	msg.Subject = "os." + nodeId
	msg.Data = buff
	msg.Header = nats.Header{}
	msg.Header.Set("fnode", nc.localNodeId)
	return nc.natsConn.PublishMsg(&msg)
}

func (nc *NatsClient) IsConnected() bool {
	return nc.natsConn != nil && nc.natsConn.Status() == nats.CONNECTED
}
