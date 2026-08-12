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
	// Topic 是目标 Topic，不能为空。
	Topic string
	// Key 是可选的分区键；发送 Tombstone 时必须非空。
	Key []byte
	// Value 是已经编码的消息体；nil 表示 Tombstone。
	Value []byte
	// Headers 是随 Record 发送的可选 Header。
	Headers []Header
	// Timestamp 是可选事件时间；零值表示交由 Sarama/Kafka 处理。
	Timestamp time.Time
}

// JSONMessage 是由 kafkamodule 使用 Sonic 编码的 JSON 消息。
// Value 是 Go 值而不是预编码字符串；nil 编码为 JSON null，普通 string 编码为 JSON String。
// 成功接受前会编码 Value 并复制 Key 和 Header Value，调用返回后可安全修改原输入。
type JSONMessage struct {
	// Topic 是目标 Topic，不能为空。
	Topic string
	// Key 是可选的分区键。
	Key []byte
	// Value 是待 JSON 编码的 Go 值。
	Value any
	// Headers 是随 Record 发送的可选 Header。
	Headers []Header
	// Timestamp 是可选事件时间；零值表示交由 Sarama/Kafka 处理。
	Timestamp time.Time
}

// PBMessage 是使用现代 google.golang.org/protobuf 编码的 Protobuf 消息。
// Value 必须是生成的非 nil proto.Message；成功接受前会编码 Value 并复制 Key 和 Header Value。
type PBMessage struct {
	// Topic 是目标 Topic，不能为空。
	Topic string
	// Key 是可选的分区键。
	Key []byte
	// Value 是待 Protobuf 编码的非 nil 消息。
	Value proto.Message
	// Headers 是随 Record 发送的可选 Header。
	Headers []Header
	// Timestamp 是可选事件时间；零值表示交由 Sarama/Kafka 处理。
	Timestamp time.Time
}

// Message 是交给 Consumer Handler 的一条 Kafka 消息。
// Key、Value 和 Header Value 至少在 Handler 返回前有效；如需跨回调保存，应显式复制。
type Message struct {
	// Topic 是消息所在 Topic。
	Topic string
	// Partition 是消息所在分区。
	Partition int32
	// Offset 是消息在分区内的 Offset。
	Offset int64
	// Key 是消息的原始 Key。
	Key []byte
	// Value 是消息的原始 Value；Tombstone 时为 nil。
	Value []byte
	// Headers 是消息携带的 Header。
	Headers []Header
	// Timestamp 是 Broker 返回的消息时间。
	Timestamp time.Time
	// HighWatermark 是当前分区消费时观察到的高水位 Offset。
	HighWatermark int64
}

// Metadata 描述 Broker 对一条已接受消息返回的位置和时间信息。
type Metadata struct {
	// Topic 是 Broker 接受消息的 Topic。
	Topic string
	// Partition 是 Broker 选择的分区。
	Partition int32
	// Offset 是 Broker 分配的 Offset。
	Offset int64
	// Timestamp 是 Broker/Sarama 返回的消息时间。
	Timestamp time.Time
}

// DeliveryResult 是一条消息最终的 Delivery 元数据或错误。
type DeliveryResult struct {
	// Metadata 在发送成功时包含最终 Topic、Partition、Offset 和时间。
	Metadata Metadata
	// Err 是最终发送错误；发送成功时为 nil。
	Err error
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
