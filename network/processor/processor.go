package processor


type IProcessor interface {
	// must goroutine safe
	MsgRoute(clientId string,msg interface{}) error
	//must goroutine safe
	UnknownMsgRoute(clientId string,msg interface{})
	// connect event
	ConnectedRoute(clientId string)
	DisConnectedRoute(clientId string)

	// must goroutine safe
	Unmarshal(clientId string,data []byte) (interface{}, error)
	// must goroutine safe
	Marshal(clientId string,msg interface{}) ([]byte, error)
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

