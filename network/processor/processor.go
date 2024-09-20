package processor

type IProcessor interface {
	// MsgRoute must goroutine safe
	MsgRoute(clientId string, msg interface{}, recyclerReaderBytes func(data []byte)) error
	// UnknownMsgRoute must goroutine safe
	UnknownMsgRoute(clientId string, msg interface{}, recyclerReaderBytes func(data []byte))
	// ConnectedRoute connect event
	ConnectedRoute(clientId string)
	DisConnectedRoute(clientId string)

	// Unmarshal must goroutine safe
	Unmarshal(clientId string, data []byte) (interface{}, error)
	// Marshal must goroutine safe
	Marshal(clientId string, msg interface{}) ([]byte, error)
}

type IRawProcessor interface {
	IProcessor

	SetByteOrder(littleEndian bool)
	SetRawMsgHandler(handle RawMessageHandler)
	MakeRawMsg(msgType uint16, msg []byte, pbRawPackInfo *PBRawPackInfo)
	SetUnknownMsgHandler(unknownMessageHandler UnknownRawMessageHandler)
	SetConnectedHandler(connectHandler RawConnectHandler)
	SetDisConnectedHandler(disconnectHandler RawConnectHandler)
}
