package rpc
import (
	"github.com/duanhf2012/origin/v2/network"
	"reflect"
	"time"
	"github.com/nats-io/nats.go"
	"github.com/duanhf2012/origin/v2/log"
)

//跨结点连接的Client
type NatsClient struct {
	localNodeId string
	notifyEventFun NotifyEventFun
	
	natsConn *nats.Conn
	client *Client
}

func (nc *NatsClient) Start(natsConn *nats.Conn) error{
	nc.natsConn = natsConn
	_,err := nc.natsConn.QueueSubscribe("oc."+nc.localNodeId, "oc",nc.onSubscribe)

	return err
}

func (nc *NatsClient) onSubscribe(msg *nats.Msg){
	//处理消息
	nc.client.processRpcResponse(msg.Data)
}

func (nc *NatsClient) SetConn(conn *network.TCPConn){
}

func (nc *NatsClient) Close(waitDone bool){
}

func (nc *NatsClient) Run(){
}

func (nc *NatsClient)  OnClose(){
}

func (rc *NatsClient) Bind(server IServer){
	s := server.(*NatsServer)
	rc.natsConn = s.natsConn
}

func (rc *NatsClient) Go(nodeId string,timeout time.Duration,rpcHandler IRpcHandler,noReply bool, serviceMethod string, args interface{}, reply interface{}) *Call {
	_, processor := GetProcessorType(args)
	InParam, err := processor.Marshal(args)
	if err != nil {
		log.Error("Marshal is fail",log.ErrorAttr("error",err))
		call := MakeCall()
		call.DoError(err)
		return call
	}

	return rc.client.rawGo(nodeId,rc,timeout,rpcHandler,processor, noReply, 0, serviceMethod, InParam, reply)
}

func (rc *NatsClient) RawGo(nodeId string,timeout time.Duration,rpcHandler IRpcHandler,processor IRpcProcessor, noReply bool, rpcMethodId uint32, serviceMethod string, rawArgs []byte, reply interface{}) *Call {
	return rc.client.rawGo(nodeId,rc,timeout,rpcHandler,processor, noReply, rpcMethodId, serviceMethod, rawArgs, reply)
}

func (rc *NatsClient) AsyncCall(nodeId string,timeout time.Duration,rpcHandler IRpcHandler, serviceMethod string, callback reflect.Value, args interface{}, replyParam interface{},cancelable bool)  (CancelRpc,error) {
	cancelRpc,err := rc.client.asyncCall(nodeId,rc,timeout,rpcHandler, serviceMethod, callback, args, replyParam,cancelable)
	if err != nil {
		callback.Call([]reflect.Value{reflect.ValueOf(replyParam), reflect.ValueOf(err)})
	}

	return cancelRpc,nil
}

func (rc *NatsClient) WriteMsg (nodeId string,args ...[]byte) error{
	buff := make([]byte,0,4096)
	for _,ar := range args {
		buff = append(buff,ar...)
	}

	var msg nats.Msg
	msg.Subject = "os."+nodeId
	msg.Data = buff
	msg.Header = nats.Header{}
	msg.Header.Set("fnode",rc.localNodeId)
	return rc.natsConn.PublishMsg(&msg)
}

func (rc *NatsClient) IsConnected() bool{
	return rc.natsConn!=nil && rc.natsConn.Status() == nats.CONNECTED
}
