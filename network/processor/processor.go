package processor

type IProcessor interface {
	// MsgRoute must goroutine safe
	MsgRoute(clientId string, msg interface{}) error
	// UnknownMsgRoute must goroutine safe
	UnknownMsgRoute(clientId string, msg interface{})
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
	SetUnknownMsgHandler(unknownMessageHandler UnknownRawMessageHandler)
	SetConnectedHandler(connectHandler RawConnectHandler)
	SetDisConnectedHandler(disconnectHandler RawConnectHandler)
}
