package processor


type IProcessor interface {
	// must goroutine safe
	MsgRoute(clientId uint64,msg interface{}) error
	//must goroutine safe
	UnknownMsgRoute(clientId uint64,msg interface{})
	// connect event
	ConnectedRoute(clientId uint64)
	DisConnectedRoute(clientId uint64)

	// must goroutine safe
	Unmarshal(clientId uint64,data []byte) (interface{}, error)
	// must goroutine safe
	Marshal(clientId uint64,msg interface{}) ([]byte, error)
}

type IRawProcessor interface {
	SetByteOrder(littleEndian bool)
	MsgRoute(clientId uint64,msg interface{}) error
	Unmarshal(clientId uint64,data []byte) (interface{}, error)
	Marshal(clientId uint64,msg interface{}) ([]byte, error)

	SetRawMsgHandler(handle RawMessageHandler)
	MakeRawMsg(msgType uint16,msg []byte,pbRawPackInfo *PBRawPackInfo)
	UnknownMsgRoute(clientId uint64,msg interface{})
	ConnectedRoute(clientId uint64)
	DisConnectedRoute(clientId uint64)

	SetUnknownMsgHandler(unknownMessageHandler UnknownRawMessageHandler)
	SetConnectedHandler(connectHandler RawConnectHandler)
	SetDisConnectedHandler(disconnectHandler RawConnectHandler)
}

