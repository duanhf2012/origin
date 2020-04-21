package rpc

/*
	Seq uint64             // sequence number chosen by client
	ServiceMethod string   // format: "Service.Method"
	NoReply bool           //是否需要返回
	//packbody
	InParam []byte




type RpcResponse struct {
	//head
	Seq           uint64   // sequence number chosen by client
	Err *RpcError

	//returns
	Reply []byte
}
*/
type IRpcRequestData interface {
	GetSeq() uint64
	GetServiceMethod() string
	GetInParam() []byte
	IsReply() bool
}

type IRpcResponseData interface {
	GetSeq() uint64
	GetErr() *RpcError
	GetReply() []byte
}

type IRpcProcessor interface {
	Marshal(v interface{}) ([]byte, error)
	Unmarshal(data []byte, v interface{}) error

	MakeRpcRequest(seq uint64,serviceMethod string,noReply bool,inParam []byte) IRpcRequestData
	MakeRpcResponse(seq uint64,err *RpcError,reply []byte) IRpcResponseData
}


