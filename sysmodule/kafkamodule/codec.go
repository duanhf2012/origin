package kafkamodule

import (
	"reflect"
	"strings"

	"github.com/bytedance/sonic"
	"google.golang.org/protobuf/proto"
)

var jsonAPI = sonic.Config{UseInt64: true, ValidateString: true}.Froze()

func encodeJSON(input JSONMessage) (*encodedMessage, error) {
	value, err := jsonAPI.Marshal(input.Value)
	if err != nil {
		return nil, invalidArgument("kafkamodule JSON 编码失败: " + err.Error())
	}
	if err = validateMessage(input.Topic, input.Key, value, input.Headers, false); err != nil {
		return nil, err
	}
	size, err := messageBytes(input.Key, value, input.Headers)
	if err != nil {
		return nil, err
	}
	return &encodedMessage{topic: strings.TrimSpace(input.Topic), key: input.Key, value: value, headers: input.Headers, timestamp: input.Timestamp, payloadBytes: size}, nil
}

func encodePB(input PBMessage) (*encodedMessage, error) {
	if isNilValue(input.Value) {
		return nil, invalidArgument("kafkamodule PB Value 不能为空")
	}
	value, err := proto.Marshal(input.Value)
	if err != nil {
		return nil, invalidArgument("kafkamodule PB 编码失败: " + err.Error())
	}
	if err = validateMessage(input.Topic, input.Key, value, input.Headers, false); err != nil {
		return nil, err
	}
	size, err := messageBytes(input.Key, value, input.Headers)
	if err != nil {
		return nil, err
	}
	return &encodedMessage{topic: strings.TrimSpace(input.Topic), key: input.Key, value: value, headers: input.Headers, timestamp: input.Timestamp, payloadBytes: size}, nil
}

// DecodeJSON 使用 Sonic 将 Value 解码到非 nil 指针 destination。
// 当 destination 包含 interface{} 时，JSON 整数解码为 int64，避免默认 float64 带来的精度与类型歧义。
func (message *Message) DecodeJSON(destination any) error {
	if message == nil || isNilValue(destination) {
		return invalidArgument("kafkamodule JSON 解码目标不能为空")
	}
	if err := jsonAPI.Unmarshal(message.Value, destination); err != nil {
		return invalidArgument("kafkamodule JSON 解码失败: " + err.Error())
	}
	return nil
}

// DecodePB 使用现代 Protobuf API 将 Value 解码到非 nil destination。
func (message *Message) DecodePB(destination proto.Message) error {
	if message == nil || isNilValue(destination) {
		return invalidArgument("kafkamodule PB 解码目标不能为空")
	}
	if err := proto.Unmarshal(message.Value, destination); err != nil {
		return invalidArgument("kafkamodule PB 解码失败: " + err.Error())
	}
	return nil
}

func isNilValue(value any) bool {
	if value == nil {
		return true
	}
	current := reflect.ValueOf(value)
	switch current.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return current.IsNil()
	default:
		return false
	}
}
