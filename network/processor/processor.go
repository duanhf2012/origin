package processor

type IProcessor interface {
	//SetByteOrder(littleEndian bool)
	//SetMsgLen(lenMsgLen int, minMsgLen uint32, maxMsgLen uint32)

	Unmarshal(data []byte) (interface{}, error)
	// must goroutine safe
	Marshal(msg interface{}) ([][]byte, error)
}

