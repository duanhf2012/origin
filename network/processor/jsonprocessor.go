package processor

type JsonProcessor struct {
	//SetByteOrder(littleEndian bool)
	//SetMsgLen(lenMsgLen int, minMsgLen uint32, maxMsgLen uint32)


}

func (slf *JsonProcessor) Unmarshal(data []byte) (interface{}, error) {
	return nil,nil
}


func (slf *JsonProcessor) Marshal(msg interface{}) ([][]byte, error) {
	return nil,nil
}