package rpc

import (
	"container/list"
	"fmt"
	"github.com/duanhf2012/origin/log"
	"github.com/duanhf2012/origin/network"
	"github.com/duanhf2012/origin/util/timer"
	"math"
	"reflect"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

type Client struct {
	id int
	bSelfNode bool
	network.TCPClient
	conn *network.TCPConn

	pendingLock          sync.RWMutex
	startSeq             uint64
	pending              map[uint64]*list.Element
	pendingTimer         *list.List
	callRpcTimeout       time.Duration
	maxCheckCallRpcCount int
	TriggerRpcEvent
}

func (client *Client) NewClientAgent(conn *network.TCPConn) network.Agent {
	client.conn = conn
	client.ResetPending()

	return client
}

func (client *Client) Connect(id int,addr string) error {
	client.id = id
	client.Addr = addr
	client.maxCheckCallRpcCount = 1000
	client.callRpcTimeout = 15*time.Second
	client.ConnNum = 1
	client.ConnectInterval = time.Second*2
	client.PendingWriteNum = 2000000
	client.AutoReconnect = true
	client.LenMsgLen = 2
	client.MinMsgLen = 2
	client.MaxMsgLen = math.MaxUint16
	client.NewAgent = client.NewClientAgent
	client.LittleEndian = LittleEndian
	client.ResetPending()
	go client.startCheckRpcCallTimer()
	if addr == "" {
		client.bSelfNode = true
		return nil
	}

	client.Start()
	return nil
}

func (client *Client) startCheckRpcCallTimer(){
	t:=timer.NewTimer(3*time.Second)
	for{
		select {
			case timer:=<- t.C:
				timer.SetupTimer(time.Now())
				client.checkRpcCallTimeout()
		}
	}

	t.Cancel()
	timer.ReleaseTimer(t)
}

func (client *Client) makeCallFail(call *Call){
	client.removePending(call.Seq)
	if call.callback!=nil && call.callback.IsValid() {
		call.rpcHandler.(*RpcHandler).callResponseCallBack <-call
	}else{
		call.done <- call
	}



}

func (client *Client) checkRpcCallTimeout(){
	now := time.Now()

	for i:=0;i< client.maxCheckCallRpcCount;i++ {
		client.pendingLock.Lock()
		pElem := client.pendingTimer.Front()
		if pElem == nil {
			client.pendingLock.Unlock()
			break
		}
		pCall := pElem.Value.(*Call)
		if now.Sub(pCall.callTime) > client.callRpcTimeout {
			pCall.Err = fmt.Errorf("RPC call takes more than %d seconds!", client.callRpcTimeout/time.Second)
			client.makeCallFail(pCall)
			client.pendingLock.Unlock()
			continue
		}
		client.pendingLock.Unlock()
	}
}

func (client *Client) ResetPending(){
	client.pendingLock.Lock()
	if client.pending != nil {
		for _,v := range client.pending {
			v.Value.(*Call).Err = fmt.Errorf("node is disconnect.")
			v.Value.(*Call).done <- v.Value.(*Call)
		}
	}

	client.pending = make(map[uint64]*list.Element,4096)
	client.pendingTimer = list.New()
	client.pendingLock.Unlock()
}

func (client *Client) AddPending(call *Call){
	client.pendingLock.Lock()
	call.callTime = time.Now()
	elemTimer := client.pendingTimer.PushBack(call)
	client.pending[call.Seq] = elemTimer //如果下面发送失败，将会一一直存在这里
	client.pendingLock.Unlock()
}

func (client *Client) RemovePending(seq uint64)  *Call{
	if seq == 0 {
		return nil
	}
	client.pendingLock.Lock()
	call := client.removePending(seq)
	client.pendingLock.Unlock()
	return call
}

func (client *Client) removePending(seq uint64) *Call{
	v,ok := client.pending[seq]
	if ok == false{
		return nil
	}
	call := v.Value.(*Call)
	client.pendingTimer.Remove(v)
	delete(client.pending,seq)
	return call
}

func (client *Client) FindPending(seq uint64) *Call{
	client.pendingLock.Lock()
	v,ok := client.pending[seq]
	if ok == false {
		client.pendingLock.Unlock()
		return nil
	}

	pCall := v.Value.(*Call)
	client.pendingLock.Unlock()

	return pCall
}

func (client *Client) generateSeq() uint64{
	return atomic.AddUint64(&client.startSeq,1)
}

func (client *Client) AsyncCall(rpcHandler IRpcHandler,serviceMethod string,callback reflect.Value, args interface{},replyParam interface{}) error {
	processorType, processor := GetProcessorType(args)
	InParam,herr := processor.Marshal(args)
	if herr != nil {
		return herr
	}

	seq := client.generateSeq()
	request:=MakeRpcRequest(processor,seq,0,serviceMethod,false,InParam)
	bytes,err := processor.Marshal(request.RpcRequestData)
	ReleaseRpcRequest(request)
	if err != nil {
		return err
	}

	if client.conn == nil {
		return fmt.Errorf("Rpc server is disconnect,call %s is fail!",serviceMethod)
	}

	call := MakeCall()
	call.Reply = replyParam
	call.callback = &callback
	call.rpcHandler = rpcHandler
	call.ServiceMethod = serviceMethod
	call.Seq = seq
	client.AddPending(call)

	err = client.conn.WriteMsg([]byte{uint8(processorType)},bytes)
	if err != nil {
		client.RemovePending(call.Seq)
		ReleaseCall(call)
		return err
	}

	return nil
}

func (client *Client) RawGo(processor IRpcProcessor,noReply bool,rpcMethodId uint32,serviceMethod string,args []byte,reply interface{}) *Call {
	call := MakeCall()
	call.ServiceMethod = serviceMethod
	call.Reply = reply
	call.Seq = client.generateSeq()

	request := MakeRpcRequest(processor,call.Seq,rpcMethodId,serviceMethod,noReply,args)
	bytes,err := processor.Marshal(request.RpcRequestData)
	ReleaseRpcRequest(request)
	if err != nil {
		call.Seq = 0
		call.Err = err
		return call
	}

	if client.conn == nil {
		call.Seq = 0
		call.Err = fmt.Errorf("call %s is fail,rpc client is disconnect.",serviceMethod)
		return call
	}

	if noReply == false {
		client.AddPending(call)
	}

	err = client.conn.WriteMsg([]byte{uint8(processor.GetProcessorType())},bytes)
	if err != nil {
		client.RemovePending(call.Seq)
		call.Seq = 0
		call.Err = err
	}

	return call
}

func (client *Client) Go(noReply bool,serviceMethod string, args interface{},reply interface{}) *Call {
	_,processor := GetProcessorType(args)
	InParam,err := processor.Marshal(args)
	if err != nil {
		call := MakeCall()
		call.Err = err
		return call
	}

	return client.RawGo(processor,noReply,0,serviceMethod,InParam,reply)
}

func (client *Client) Run(){
	defer func() {
		if r := recover(); r != nil {
			buf := make([]byte, 4096)
			l := runtime.Stack(buf, false)
			err := fmt.Errorf("%v: %s\n", r, buf[:l])
			log.Error("core dump info:%+v",err)
		}
	}()

	client.TriggerRpcEvent(true,client.GetId())
	for {
		bytes,err := client.conn.ReadMsg()
		if err != nil {
			log.Error("rpcClient %s ReadMsg error:%+v", client.Addr,err)
			return
		}

		processor := GetProcessor(uint8(bytes[0]))
		if processor==nil {
			client.conn.ReleaseReadMsg(bytes)
			log.Error("rpcClient %s ReadMsg head error:%+v", client.Addr,err)
			return
		}

		//1.解析head
		response := RpcResponse{}
		response.RpcResponseData =processor.MakeRpcResponse(0,RpcError(""),nil)

		err = processor.Unmarshal(bytes[1:], response.RpcResponseData)
		client.conn.ReleaseReadMsg(bytes)
		if err != nil {
			processor.ReleaseRpcResponse(response.RpcResponseData)
			log.Error("rpcClient Unmarshal head error,error:%+v",err)
			continue
		}

		v := client.RemovePending(response.RpcResponseData.GetSeq())
		if v == nil {
			log.Error("rpcClient cannot find seq %d in pending", response.RpcResponseData.GetSeq())
		}else  {
			v.Err = nil
			if len(response.RpcResponseData.GetReply()) >0 {
				err = processor.Unmarshal(response.RpcResponseData.GetReply(),v.Reply)
				if err != nil {
					log.Error("rpcClient Unmarshal body error,error:%+v",err)
					v.Err = err
				}
			}

			if response.RpcResponseData.GetErr() != nil {
				v.Err= response.RpcResponseData.GetErr()
			}

			if v.callback!=nil && v.callback.IsValid() {
				 v.rpcHandler.(*RpcHandler).callResponseCallBack <-v
			}else{
				v.done <- v
			}
		}

		processor.ReleaseRpcResponse(response.RpcResponseData)
	}
}

func (client *Client) OnClose(){
	client.TriggerRpcEvent(false,client.GetId())
}

func (client *Client) IsConnected() bool {
	return client.bSelfNode || (client.conn!=nil && client.conn.IsConnected()==true)
}

func (client *Client) GetId() int{
	return client.id
}
