package rpc

type IRpcProcessor interface {
	Clone(src interface{}) (interface{},error)
	Marshal(v interface{}) ([]byte, error) //b表示自定义缓冲区，可以填nil，由系统自动分配
	Unmarshal(data []byte, v interface{}) error
	MakeRpcRequest(seq uint64,rpcMethodId uint32,serviceMethod string,noReply bool,inParam []byte) IRpcRequestData
	MakeRpcResponse(seq uint64,err RpcError,reply []byte) IRpcResponseData

	ReleaseRpcRequest(rpcRequestData IRpcRequestData)
	ReleaseRpcResponse(rpcRequestData IRpcResponseData)
	IsParse(param interface{}) bool //是否可解析
	GetProcessorType() RpcProcessorType
}



