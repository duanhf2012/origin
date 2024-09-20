package rpc

import (
	"fmt"
	"github.com/duanhf2012/origin/v2/log"
	"github.com/duanhf2012/origin/v2/network"
	"math"
	"net"
	"reflect"
	"runtime"
	"strings"
	"time"
)

const Default_ReadWriteDeadline = 15 * time.Second

type RpcProcessorType uint8

const (
	RpcProcessorJson RpcProcessorType = 0
	RpcProcessorPB   RpcProcessorType = 1
)

var arrayProcessor = []IRpcProcessor{&JsonProcessor{}, &PBProcessor{}}
var arrayProcessorLen uint8 = 2
var LittleEndian bool

type IServer interface {
	Start() error
	Stop()

	selfNodeRpcHandlerGo(timeout time.Duration, processor IRpcProcessor, client *Client, noReply bool, handlerName string, rpcMethodId uint32, serviceMethod string, args interface{}, reply interface{}, rawArgs []byte) *Call
	myselfRpcHandlerGo(client *Client, handlerName string, serviceMethod string, args interface{}, callBack reflect.Value, reply interface{}) error
	selfNodeRpcHandlerAsyncGo(timeout time.Duration, client *Client, callerRpcHandler IRpcHandler, noReply bool, handlerName string, serviceMethod string, args interface{}, reply interface{}, callback reflect.Value, cancelable bool) (CancelRpc, error)
}

type writeResponse func(processor IRpcProcessor, connTag string, serviceMethod string, seq uint64, reply interface{}, rpcError RpcError)

type Server struct {
	BaseServer

	functions map[interface{}]interface{}
	rpcServer *network.TCPServer

	listenAddr     string
	maxRpcParamLen uint32
}

type RpcAgent struct {
	conn      network.Conn
	rpcServer *Server
	userData  interface{}
}

func AppendProcessor(rpcProcessor IRpcProcessor) {
	arrayProcessor = append(arrayProcessor, rpcProcessor)
	arrayProcessorLen++
}

func GetProcessorType(param interface{}) (RpcProcessorType, IRpcProcessor) {
	for i := uint8(1); i < arrayProcessorLen; i++ {
		if arrayProcessor[i].IsParse(param) == true {
			return RpcProcessorType(i), arrayProcessor[i]
		}
	}

	return RpcProcessorJson, arrayProcessor[RpcProcessorJson]
}

func GetProcessor(processorType uint8) IRpcProcessor {
	if processorType >= arrayProcessorLen {
		return nil
	}
	return arrayProcessor[processorType]
}

func (server *Server) Init(listenAddr string, maxRpcParamLen uint32, compressBytesLen int, rpcHandleFinder RpcHandleFinder) {
	server.initBaseServer(compressBytesLen, rpcHandleFinder)
	server.listenAddr = listenAddr
	server.maxRpcParamLen = maxRpcParamLen

	server.rpcServer = &network.TCPServer{}
}

func (server *Server) Start() error {
	splitAddr := strings.Split(server.listenAddr, ":")
	if len(splitAddr) != 2 {
		return fmt.Errorf("listen addr is failed,listenAddr:%s", server.listenAddr)
	}

	server.rpcServer.Addr = ":" + splitAddr[1]
	server.rpcServer.MinMsgLen = 2
	if server.maxRpcParamLen > 0 {
		server.rpcServer.MaxMsgLen = server.maxRpcParamLen
	} else {
		server.rpcServer.MaxMsgLen = math.MaxUint32
	}

	server.rpcServer.MaxConnNum = 100000
	server.rpcServer.PendingWriteNum = 2000000
	server.rpcServer.NewAgent = server.NewAgent
	server.rpcServer.LittleEndian = LittleEndian
	server.rpcServer.WriteDeadline = Default_ReadWriteDeadline
	server.rpcServer.ReadDeadline = Default_ReadWriteDeadline
	server.rpcServer.LenMsgLen = DefaultRpcLenMsgLen

	return server.rpcServer.Start()
}

func (server *Server) Stop() {
	server.rpcServer.Close()
}

func (agent *RpcAgent) OnDestroy() {}

func (agent *RpcAgent) WriteResponse(processor IRpcProcessor, connTag string, serviceMethod string, seq uint64, reply interface{}, rpcError RpcError) {
	var mReply []byte
	var errM error

	if reply != nil {
		mReply, errM = processor.Marshal(reply)
		if errM != nil {
			rpcError = ConvertError(errM)
		}
	}

	var rpcResponse RpcResponse
	rpcResponse.RpcResponseData = processor.MakeRpcResponse(seq, rpcError, mReply)
	bytes, errM := processor.Marshal(rpcResponse.RpcResponseData)
	defer processor.ReleaseRpcResponse(rpcResponse.RpcResponseData)

	if errM != nil {
		log.Error("marshal RpcResponseData failed", log.String("serviceMethod", serviceMethod), log.ErrorAttr("error", errM))
		return
	}

	var compressBuff []byte
	bCompress := uint8(0)
	if agent.rpcServer.compressBytesLen > 0 && len(bytes) >= agent.rpcServer.compressBytesLen {
		var cErr error

		compressBuff, cErr = compressor.CompressBlock(bytes)
		if cErr != nil {
			log.Error("CompressBlock failed", log.String("serviceMethod", serviceMethod), log.ErrorAttr("error", cErr))
			return
		}
		if len(compressBuff) < len(bytes) {
			bytes = compressBuff
			bCompress = 1 << 7
		}
	}

	errM = agent.conn.WriteMsg([]byte{uint8(processor.GetProcessorType()) | bCompress}, bytes)
	if cap(compressBuff) > 0 {
		compressor.CompressBufferCollection(compressBuff)
	}
	if errM != nil {
		log.Error("WriteMsg error,Rpc return is fail", log.String("serviceMethod", serviceMethod), log.ErrorAttr("error", errM))
	}
}

func (agent *RpcAgent) Run() {
	defer func() {
		if r := recover(); r != nil {
			buf := make([]byte, 4096)
			l := runtime.Stack(buf, false)
			errString := fmt.Sprint(r)
			log.Dump(string(buf[:l]), log.String("error", errString))
		}
	}()

	for {
		data, err := agent.conn.ReadMsg()
		if err != nil {
			//will close conn
			log.Error("read message is error", log.String("remoteAddress", agent.conn.RemoteAddr().String()), log.ErrorAttr("error", err))
			break
		}

		err = agent.rpcServer.processRpcRequest(data, "", agent.WriteResponse)
		if err != nil {
			//will close conn
			agent.conn.ReleaseReadMsg(data)
			log.Error("processRpcRequest is error", log.String("remoteAddress", agent.conn.RemoteAddr().String()), log.ErrorAttr("error", err))

			break
		}
		agent.conn.ReleaseReadMsg(data)
	}
}

func (agent *RpcAgent) OnClose() {
}

func (agent *RpcAgent) WriteMsg(msg interface{}) {
}

func (agent *RpcAgent) LocalAddr() net.Addr {
	return agent.conn.LocalAddr()
}

func (agent *RpcAgent) RemoteAddr() net.Addr {
	return agent.conn.RemoteAddr()
}

func (agent *RpcAgent) Close() {
	agent.conn.Close()
}

func (agent *RpcAgent) Destroy() {
	agent.conn.Destroy()
}

func (server *Server) NewAgent(c *network.TCPConn) network.Agent {
	agent := &RpcAgent{conn: c, rpcServer: server}

	return agent
}
