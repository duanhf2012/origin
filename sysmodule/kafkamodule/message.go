package kafkamodule

import (
	"math"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"
)

// Header 是一项 Kafka Record Header。Key 必须非空，Value 可以为空。
// 受管 Producer 按 Raw 零拷贝所有权规则持有 Value，Delivery 完成前调用方不得修改。
type Header struct {
	// Key 是 Header 名称。
	Key string
	// Value 是 Header 原始字节。
	Value []byte
}

// ProducerMessage 是已经编码的 Raw Kafka 消息。
//
// Produce 方法成功接受后会一直持有 Key、Value 和 Header Value 到 Delivery 完成，不复制这些
// Buffer；调用方在此期间不得修改或复用。Value 为 nil 表示 compacted Topic Tombstone，此时 Key 必须非空。
type ProducerMessage struct {
	Topic     string
	Key       []byte
	Value     []byte
	Headers   []Header
	Timestamp time.Time
}

// JSONMessage 是由 kafkamodule 使用 Sonic 编码的 JSON 消息。
// Value 是 Go 值而不是预编码字符串；nil 编码为 JSON null，普通 string 编码为 JSON String。
type JSONMessage struct {
	Topic     string
	Key       []byte
	Value     any
	Headers   []Header
	Timestamp time.Time
}

// PBMessage 是使用现代 google.golang.org/protobuf 编码的 Protobuf 消息。
// Value 必须是生成的非 nil proto.Message。
type PBMessage struct {
	Topic     string
	Key       []byte
	Value     proto.Message
	Headers   []Header
	Timestamp time.Time
}

// Message 是交给 Consumer Handler 的一条 Kafka 消息。
// Key、Value 和 Header Value 至少在 Handler 返回前有效；如需跨回调保存，应显式复制。
type Message struct {
	Topic         string
	Partition     int32
	Offset        int64
	Key           []byte
	Value         []byte
	Headers       []Header
	Timestamp     time.Time
	HighWatermark int64
}

// Metadata 描述 Broker 对一条已接受消息返回的位置和时间信息。
type Metadata struct {
	Topic     string
	Partition int32
	Offset    int64
	Timestamp time.Time
}

// DeliveryResult 是一条消息最终的 Delivery 元数据或错误。
type DeliveryResult struct {
	Metadata Metadata
	Err      error
}

type encodedMessage struct {
	topic        string
	key          []byte
	value        []byte
	headers      []Header
	timestamp    time.Time
	payloadBytes int64
}

func encodeRaw(input ProducerMessage) (*encodedMessage, error) {
	if err := validateMessage(input.Topic, input.Key, input.Value, input.Headers, true); err != nil {
		return nil, err
	}
	size, err := messageBytes(input.Key, input.Value, input.Headers)
	if err != nil {
		return nil, err
	}
	return &encodedMessage{topic: strings.TrimSpace(input.Topic), key: input.Key, value: input.Value, headers: input.Headers, timestamp: input.Timestamp, payloadBytes: size}, nil
}

func validateMessage(topic string, key, value []byte, headers []Header, tombstone bool) error {
	if strings.TrimSpace(topic) == "" {
		return invalidArgument("kafkamodule Topic 不能为空")
	}
	if tombstone && value == nil && len(key) == 0 {
		return invalidArgument("kafkamodule Tombstone 必须提供非空 Key")
	}
	for _, header := range headers {
		if strings.TrimSpace(header.Key) == "" {
			return invalidArgument("kafkamodule Header Key 不能为空")
		}
	}
	return nil
}

func messageBytes(key, value []byte, headers []Header) (int64, error) {
	total := int64(len(key)) + int64(len(value))
	for _, header := range headers {
		part := int64(len(header.Key)) + int64(len(header.Value))
		if total > math.MaxInt64-part {
			return 0, invalidArgument("kafkamodule 消息 Payload 字节数溢出")
		}
		total += part
	}
	return total, nil
}
