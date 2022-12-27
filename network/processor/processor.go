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
	IProcessor

	SetByteOrder(littleEndian bool)
	SetRawMsgHandler(handle RawMessageHandler)
	MakeRawMsg(msgType uint16,msg []byte,pbRawPackInfo *PBRawPackInfo)
	SetUnknownMsgHandler(unknownMessageHandler UnknownRawMessageHandler)
	SetConnectedHandler(connectHandler RawConnectHandler)
	SetDisConnectedHandler(disconnectHandler RawConnectHandler)
}

