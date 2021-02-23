package processor


type IProcessor interface {
	// must goroutine safe
	MsgRoute(msg interface{}, userData interface{}) error
	//must goroutine safe
	UnknownMsgRoute(msg interface{}, userData interface{})
	// connect event
	ConnectedRoute(userData interface{})
	DisConnectedRoute(userData interface{})

	// must goroutine safe
	Unmarshal(data []byte) (interface{}, error)
	// must goroutine safe
	Marshal(msg interface{}) ([]byte, error)
}

type IRawProcessor interface {
	SetByteOrder(littleEndian bool)
	MsgRoute(msg interface{},userdata interface{}) error
	Unmarshal(data []byte) (interface{}, error)
	Marshal(msg interface{}) ([]byte, error)

	SetRawMsgHandler(handle RawMessageHandler)
	MakeRawMsg(msgType uint16,msg []byte,pbRawPackInfo *PBRawPackInfo)
	UnknownMsgRoute(msg interface{}, userData interface{})
	ConnectedRoute(userData interface{})
	DisConnectedRoute(userData interface{})

	SetUnknownMsgHandler(unknownMessageHandler UnknownRawMessageHandler)
	SetConnectedHandler(connectHandler RawConnectHandler)
	SetDisConnectedHandler(disconnectHandler RawConnectHandler)
}

