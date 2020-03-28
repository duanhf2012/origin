package rpc

import (

	"encoding/json"

)

type JsonProcessor struct {
}



func (slf *JsonProcessor) Marshal(v interface{}) ([]byte, error){
	return json.Marshal(v)
}

func (slf *JsonProcessor) Unmarshal(data []byte, v interface{}) error{

	return json.Unmarshal(data,v)
}

