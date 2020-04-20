package network


type Processor interface {
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
