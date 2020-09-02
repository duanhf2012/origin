package rpc

type IRpcProcessor interface {
	Marshal(v interface{}) ([]byte, error) //b表示自定义缓冲区，可以填nil，由系统自动分配
	Unmarshal(data []byte, v interface{}) error
	MakeRpcRequest(seq uint64,serviceMethod string,noReply bool,inParam []byte,additionParam interface{}) IRpcRequestData
	MakeRpcResponse(seq uint64,err *RpcError,reply []byte) IRpcResponseData

	ReleaseRpcRequest(rpcRequestData IRpcRequestData)
	ReleaseRpcRespose(rpcRequestData IRpcResponseData)
}



