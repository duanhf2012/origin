package kafkamodule

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"unicode/utf8"

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
	return &encodedMessage{topic: strings.TrimSpace(input.Topic), key: cloneBytes(input.Key), value: value, headers: cloneHeaders(input.Headers), timestamp: input.Timestamp, payloadBytes: size}, nil
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
	return &encodedMessage{topic: strings.TrimSpace(input.Topic), key: cloneBytes(input.Key), value: value, headers: cloneHeaders(input.Headers), timestamp: input.Timestamp, payloadBytes: size}, nil
}

func cloneBytes(input []byte) []byte { return append([]byte(nil), input...) }

func cloneHeaders(input []Header) []Header {
	if input == nil {
		return nil
	}
	result := make([]Header, len(input))
	for index, header := range input {
		result[index] = Header{Key: header.Key, Value: cloneBytes(header.Value)}
	}
	return result
}

// DecodeJSON 使用标准库 Decoder 将 Value 解码到非 nil 指针 destination。
//
// 编码仍使用 Sonic；解码改用 UseNumber 后再把 interface{} 中可以安全表示的整数递归归一化
// 为 int64。Sonic v1.15.2 在 Go 1.27 使用标准库兼容路径，该路径不会应用 Config.UseInt64；
// 此处显式补偿以保持既有整数精度与类型契约。目标结构体的显式 int/float 字段仍由标准库
// 直接按字段类型解码。
func (message *Message) DecodeJSON(destination any) error {
	if message == nil || isNilValue(destination) {
		return invalidArgument("kafkamodule JSON 解码目标不能为空")
	}
	// encoding/json 会把原始非法 UTF-8 替换为 U+FFFD；这里显式拒绝，保持
	// jsonAPI 的 ValidateString 契约，避免消息内容在解码过程中被静默改写。
	if !utf8.Valid(message.Value) {
		return invalidArgument("kafkamodule JSON 解码失败: JSON 包含非法 UTF-8")
	}
	decoder := json.NewDecoder(bytes.NewReader(message.Value))
	decoder.UseNumber()
	if err := decoder.Decode(destination); err != nil {
		return invalidArgument("kafkamodule JSON 解码失败: " + err.Error())
	}
	if err := rejectTrailingJSON(decoder); err != nil {
		return invalidArgument("kafkamodule JSON 解码失败: " + err.Error())
	}
	if err := normalizeJSONNumbers(reflect.ValueOf(destination)); err != nil {
		return invalidArgument("kafkamodule JSON 解码失败: " + err.Error())
	}
	return nil
}

func rejectTrailingJSON(decoder *json.Decoder) error {
	// 完整消息只允许一个 JSON 值；第二次 Decode 必须精确到达 EOF。
	var trailing any
	err := decoder.Decode(&trailing)
	if err == io.EOF {
		return nil
	}
	if err == nil {
		return errors.New("JSON 包含多个顶层值")
	}
	return err
}

func normalizeJSONNumbers(value reflect.Value) error {
	if !value.IsValid() {
		return nil
	}
	if value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return nil
		}
		return normalizeJSONNumbers(value.Elem())
	}
	if value.Kind() == reflect.Interface {
		if value.IsNil() {
			return nil
		}
		current := value.Elem()
		if number, ok := current.Interface().(json.Number); ok {
			if strings.ContainsAny(number.String(), ".eE") {
				floating, err := number.Float64()
				if err != nil {
					return err
				}
				value.Set(reflect.ValueOf(floating))
				return nil
			}
			integer, err := number.Int64()
			if err != nil {
				// 纯整数字面量不得回退为 float64，否则大整数会在成功返回时静默丢失精度。
				return errors.New("JSON 整数超出 int64 范围: " + number.String())
			}
			value.Set(reflect.ValueOf(integer))
			return nil
		}
		return normalizeJSONNumbers(current)
	}
	switch value.Kind() {
	case reflect.Map:
		iterator := value.MapRange()
		for iterator.Next() {
			current := reflect.New(iterator.Value().Type()).Elem()
			current.Set(iterator.Value())
			if err := normalizeJSONNumbers(current); err != nil {
				return err
			}
			value.SetMapIndex(iterator.Key(), current)
		}
	case reflect.Slice, reflect.Array:
		for index := 0; index < value.Len(); index++ {
			if err := normalizeJSONNumbers(value.Index(index)); err != nil {
				return err
			}
		}
	case reflect.Struct:
		for index := 0; index < value.NumField(); index++ {
			if value.Field(index).CanSet() {
				if err := normalizeJSONNumbers(value.Field(index)); err != nil {
					return err
				}
			}
		}
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
