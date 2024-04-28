package kafkamodule

type Sasl struct {
	UserName   string `json:"UserName"`
	Passwd     string `json:"Passwd"`
	InstanceId string `json:"InstanceId"`
}
