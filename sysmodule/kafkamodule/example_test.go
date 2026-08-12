package kafkamodule_test

import (
	"fmt"

	"github.com/duanhf2012/origin/v3/sysmodule/kafkamodule"
)

func ExampleMessage_DecodeJSON() {
	message := &kafkamodule.Message{Value: []byte(`{"player_id":9007199254740991}`)}
	var event map[string]any
	if err := message.DecodeJSON(&event); err != nil {
		panic(err)
	}

	// interface{} 中的 JSON 整数保持 int64，不会先转成可能丢精度的 float64。
	fmt.Printf("%T %v\n", event["player_id"], event["player_id"])
	// Output: int64 9007199254740991
}
