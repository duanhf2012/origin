package rpc

import (
	"container/list"
	"fmt"
	"github.com/duanhf2012/origin/log"
	"github.com/duanhf2012/origin/network"
	"math"
	"reflect"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

type Client struct {
	bSelfNode bool
	network.TCPClient
	conn *network.TCPConn

	pendingLock sync.RWMutex
	startSeq uint64
	pending map[uint64]*list.Element
	pendingTimer *list.List
	callRpcTimerout time.Duration
	maxCheckCallRpcCount int
}

func (slf *Client) NewClientAgent(conn *network.TCPConn) network.Agent {
	slf.conn = conn
	slf.ResetPending()

	return slf
}

func (slf *Client) Connect(addr string) error {
	slf.Addr = addr
	slf.maxCheckCallRpcCount = 100
	slf.callRpcTimerout = 15*time.Second
	slf.ConnNum = 1
	slf.ConnectInterval = time.Second*2
	slf.PendingWriteNum = 2000000
	slf.AutoReconnect = true
	slf.LenMsgLen = 2
	slf.MinMsgLen = 2
	slf.MaxMsgLen = math.MaxUint16
	slf.NewAgent = slf.NewClientAgent
	slf.LittleEndian = LittleEndian
	slf.ResetPending()
	go slf.startCheckRpcCallTimer()
	if addr == "" {
		slf.bSelfNode = true
		return nil
	}

	slf.Start()
	return nil
}

func (slf *Client) startCheckRpcCallTimer(){
	tick :=time.NewTicker( 3 * time.Second)

	for{
		select {
			case <- tick.C:
				slf.checkRpcCallTimerout()
		}
	}
	tick.Stop()
}

func (slf *Client) makeCallFail(call *Call){
	if call.callback!=nil && call.callback.IsValid() {
		call.rpcHandler.(*RpcHandler).callResponeCallBack<-call
	}else{
		call.done <- call
	}
	slf.removePending(call.Seq)
}

func (slf *Client) checkRpcCallTimerout(){
	tnow := time.Now()

	for i:=0;i<slf.maxCheckCallRpcCount;i++ {
		slf.pendingLock.Lock()
		pElem := slf.pendingTimer.Front()
		if pElem == nil {
			slf.pendingLock.Unlock()
			break
		}
		pCall := pElem.Value.(*Call)
		if tnow.Sub(pCall.calltime) > slf.callRpcTimerout {
			pCall.Err = fmt.Errorf("RPC call takes more than %d seconds!",slf.callRpcTimerout/time.Second)
			slf.makeCallFail(pCall)
			slf.pendingLock.Unlock()
			continue
		}
		slf.pendingLock.Unlock()
	}
}

func (slf *Client) ResetPending(){
	slf.pendingLock.Lock()
	if slf.pending != nil {
		for _,v := range slf.pending {
			v.Value.(*Call).Err = fmt.Errorf("node is disconnect.")
			v.Value.(*Call).done <- v.Value.(*Call)
		}
	}

	slf.pending = map[uint64]*list.Element{}
	slf.pendingTimer = list.New()
	slf.pendingLock.Unlock()
}

func (slf *Client) AddPending(call *Call){
	slf.pendingLock.Lock()
	call.calltime = time.Now()
	elemTimer := slf.pendingTimer.PushBack(call)
	slf.pending[call.Seq] = elemTimer//如果下面发送失败，将会一一直存在这里
	slf.pendingLock.Unlock()
}

func (slf *Client) RemovePending(seq uint64)  *Call{
	slf.pendingLock.Lock()
	call := slf.removePending(seq)
	slf.pendingLock.Unlock()
	return call
}

func (slf *Client) removePending(seq uint64) *Call{
	v,ok := slf.pending[seq]
	if ok == false{
		return nil
	}
	slf.pendingTimer.Remove(v)
	delete(slf.pending,seq)
	return v.Value.(*Call)
}


func (slf *Client) FindPending(seq uint64) *Call{
	slf.pendingLock.Lock()
	v,ok := slf.pending[seq]
	if ok == false {
		slf.pendingLock.Unlock()
		return nil
	}

	pCall := v.Value.(*Call)
	slf.pendingLock.Unlock()

	return pCall
}

func (slf *Client) generateSeq() uint64{
	return atomic.AddUint64(&slf.startSeq,1)
}

func (slf *Client) AsycCall(rpcHandler IRpcHandler,serviceMethod string,callback reflect.Value, args interface{},replyParam interface{}) error {
	call := MakeCall()
	call.Reply = replyParam
	call.callback = &callback
	call.rpcHandler = rpcHandler
	call.ServiceMethod = serviceMethod

	InParam,herr := processor.Marshal(args)
	if herr != nil {
		ReleaseCall(call)
		return herr
	}

	request := &RpcRequest{}
	call.Arg = args
	call.Seq = slf.generateSeq()
	request.RpcRequestData = processor.MakeRpcRequest(slf.startSeq,serviceMethod,false,InParam,nil)
	slf.AddPending(call)

	bytes,err := processor.Marshal(request.RpcRequestData)
	processor.ReleaseRpcRequest(request.RpcRequestData)
	if err != nil {
		slf.RemovePending(call.Seq)
		ReleaseCall(call)
		return err
	}

	if slf.conn == nil {
		slf.RemovePending(call.Seq)
		ReleaseCall(call)
		return fmt.Errorf("Rpc server is disconnect,call %s is fail!",serviceMethod)
	}

	err = slf.conn.WriteMsg(bytes)
	if err != nil {
		slf.RemovePending(call.Seq)
		ReleaseCall(call)
	}

	return err
}

func (slf *Client) RawGo(noReply bool,serviceMethod string,args []byte,additionParam interface{},reply interface{}) *Call {
	call := MakeCall()
	call.ServiceMethod = serviceMethod
	call.Reply = reply

	request := &RpcRequest{}
	call.Arg = args
	call.Seq = slf.generateSeq()
	if noReply == false {
		slf.AddPending(call)
	}
	request.RpcRequestData = processor.MakeRpcRequest(slf.startSeq,serviceMethod,noReply,args,additionParam)
	bytes,err := processor.Marshal(request.RpcRequestData)
	processor.ReleaseRpcRequest(request.RpcRequestData)
	if err != nil {
		call.Err = err
		slf.RemovePending(call.Seq)
		return call
	}

	if slf.conn == nil {
		call.Err = fmt.Errorf("call %s is fail,rpc client is disconnect.",serviceMethod)
		slf.RemovePending(call.Seq)
		return call
	}

	err = slf.conn.WriteMsg(bytes)
	if err != nil {
		slf.RemovePending(call.Seq)
		call.Err = err
	}

	return call
}

func (slf *Client) Go(noReply bool,serviceMethod string, args interface{},reply interface{}) *Call {
	InParam,err := processor.Marshal(args)
	if err != nil {
		call := MakeCall()
		call.Err = err
	}

	return slf.RawGo(noReply,serviceMethod,InParam,nil,reply)
}

func (slf *Client) Run(){
	defer func() {
		if r := recover(); r != nil {
			buf := make([]byte, 4096)
			l := runtime.Stack(buf, false)
			err := fmt.Errorf("%v: %s\n", r, buf[:l])
			log.Error("core dump info:%+v",err)
		}
	}()

	for {
		bytes,err := slf.conn.ReadMsg()
		if err != nil {
			log.Error("rpcClient %s ReadMsg error:%+v",slf.Addr,err)
			return
		}
		//1.解析head
		respone := &RpcResponse{}
		respone.RpcResponeData =processor.MakeRpcResponse(0,nil,nil)

		err = processor.Unmarshal(bytes,respone.RpcResponeData)
		if err != nil {
			processor.ReleaseRpcRespose(respone.RpcResponeData)
			log.Error("rpcClient Unmarshal head error,error:%+v",err)
			continue
		}

		v := slf.RemovePending(respone.RpcResponeData.GetSeq())
		if v == nil {
			log.Error("rpcClient cannot find seq %d in pending",respone.RpcResponeData.GetSeq())
		}else  {
			v.Err = nil
			if len(respone.RpcResponeData.GetReply()) >0 {
				err = processor.Unmarshal(respone.RpcResponeData.GetReply(),v.Reply)
				if err != nil {
					log.Error("rpcClient Unmarshal body error,error:%+v",err)
					v.Err = err
				}
			}

			if respone.RpcResponeData.GetErr() != nil {
				v.Err= respone.RpcResponeData.GetErr()
			}

			if v.callback!=nil && v.callback.IsValid() {
				 v.rpcHandler.(*RpcHandler).callResponeCallBack<-v
			}else{
				v.done <- v
			}
		}

		processor.ReleaseRpcRespose(respone.RpcResponeData)
	}
}

func (slf *Client) OnClose(){
}

func (slf *Client) IsConnected() bool {
	return slf.conn!=nil && slf.conn.IsConnected()==true
}