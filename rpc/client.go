package rpc

import (
	"container/list"
	"errors"
	"github.com/duanhf2012/origin/log"
	"github.com/duanhf2012/origin/network"
	"github.com/duanhf2012/origin/util/timer"
	"math"
	"reflect"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

type Client struct {
	clientSeq uint32
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

var clientSeq uint32

func (client *Client) NewClientAgent(conn *network.TCPConn) network.Agent {
	client.conn = conn
	client.ResetPending()

	return client
}

func (client *Client) Connect(id int,addr string) error {
	client.clientSeq = atomic.AddUint32(&clientSeq,1)
	client.id = id
	client.Addr = addr
	client.maxCheckCallRpcCount = 1000
	client.callRpcTimeout = 15*time.Second
	client.ConnNum = 1
	client.ConnectInterval = time.Second*2
	client.PendingWriteNum = 200000
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
			strTimeout := strconv.FormatInt(int64(client.callRpcTimeout/time.Second), 10)
			pCall.Err = errors.New("RPC call takes more than "+strTimeout+ " seconds")
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
			v.Value.(*Call).Err = errors.New("node is disconnect")
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
		return errors.New("Rpc server is disconnect,call "+serviceMethod)
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
		call.Err = errors.New(serviceMethod+"  was called failed,rpc client is disconnect")
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
			log.SError("core dump info:",r,":", string(buf[:l]))
		}
	}()

	client.TriggerRpcEvent(true,client.GetClientSeq(),client.GetId())
	for {
		bytes,err := client.conn.ReadMsg()
		if err != nil {
			log.SError("rpcClient ",client.Addr," ReadMsg error:",err.Error())
			return
		}

		processor := GetProcessor(bytes[0])
		if processor==nil {
			client.conn.ReleaseReadMsg(bytes)
			log.SError("rpcClient ",client.Addr," ReadMsg head error:",err.Error())
			return
		}

		//1.解析head
		response := RpcResponse{}
		response.RpcResponseData =processor.MakeRpcResponse(0,"",nil)

		err = processor.Unmarshal(bytes[1:], response.RpcResponseData)
		client.conn.ReleaseReadMsg(bytes)
		if err != nil {
			processor.ReleaseRpcResponse(response.RpcResponseData)
			log.SError("rpcClient Unmarshal head error:",err.Error())
			continue
		}

		v := client.RemovePending(response.RpcResponseData.GetSeq())
		if v == nil {
			log.SError("rpcClient cannot find seq ",response.RpcResponseData.GetSeq()," in pending")
		}else  {
			v.Err = nil
			if len(response.RpcResponseData.GetReply()) >0 {
				err = processor.Unmarshal(response.RpcResponseData.GetReply(),v.Reply)
				if err != nil {
					log.SError("rpcClient Unmarshal body error:",err.Error())
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
	client.TriggerRpcEvent(false,client.GetClientSeq(),client.GetId())
}

func (client *Client) IsConnected() bool {
	return client.bSelfNode || (client.conn!=nil && client.conn.IsConnected()==true)
}

func (client *Client) GetId() int{
	return client.id
}

func (client *Client) Close(waitDone bool){
	client.TCPClient.Close(waitDone)
}

func (client *Client) GetClientSeq() uint32 {
	return client.clientSeq
}
