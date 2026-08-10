// Package pb 提供固定 uint16 MessageID 加 protobuf Payload 的标准 Codec。
package pb

import (
	"encoding/binary"
	"reflect"

	"github.com/duanhf2012/origin/v3/errs"
	"github.com/duanhf2012/origin/v3/sysmodule/network"
	"github.com/duanhf2012/origin/v3/sysmodule/network/protocol"
	"google.golang.org/protobuf/proto"
)

// Codec 使用固定两字节 ID 和调用方选择的端序。
type Codec struct {
	order binary.ByteOrder
}

// NewCodec 创建 PB Codec；端序与 TCP/KCP 长度帧端序相互独立。
func NewCodec(order network.ByteOrder) (*Codec, error) {
	switch order {
	case network.BigEndian:
		return &Codec{order: binary.BigEndian}, nil
	case network.LittleEndian:
		return &Codec{order: binary.LittleEndian}, nil
	default:
		return nil, errs.NewMessage(errs.CodeInvalidConfig, "pb: ByteOrder 无效")
	}
}

// Decode 解析 ID，并直接反序列化到 Router 创建的目标对象。
func (codec *Codec) Decode(payload []byte, resolver protocol.Resolver) (protocol.Message, error) {
	if codec == nil || resolver == nil || len(payload) < 2 {
		return protocol.Message{}, errs.NewMessage(errs.CodeTransportProtocol, "pb: 消息不足两字节")
	}
	id := protocol.MessageID(codec.order.Uint16(payload[:2]))
	if id == 0 {
		return protocol.Message{}, errs.NewMessage(errs.CodeTransportProtocol, "pb: MessageID 不能为零")
	}
	target, exists := resolver.New(id)
	if !exists {
		return protocol.Message{ID: id}, nil
	}
	message, ok := target.(proto.Message)
	if !ok || isNilMessage(message) {
		return protocol.Message{}, errs.NewMessage(errs.CodeTransportProtocol, "pb: 注册类型没有实现 proto.Message")
	}
	if err := proto.Unmarshal(payload[2:], message); err != nil {
		return protocol.Message{}, errs.Wrap(errs.CodeTransportProtocol, err)
	}
	return protocol.Message{ID: id, Value: target}, nil
}

// Encode 直接把 protobuf 写入最终 Encoder Buffer。
func (codec *Codec) Encode(encoder *protocol.Encoder, id protocol.MessageID, value any) error {
	if codec == nil || encoder == nil || id == 0 {
		return errs.ErrInvalidArgument
	}
	message, ok := value.(proto.Message)
	if !ok || isNilMessage(message) {
		return errs.NewMessage(errs.CodeInvalidArgument, "pb: 消息没有实现 proto.Message")
	}
	size := proto.Size(message)
	region, err := encoder.Reserve(2 + size)
	if err != nil {
		return err
	}
	codec.order.PutUint16(region[:2], uint16(id))
	encoded, err := (proto.MarshalOptions{}).MarshalAppend(region[:2:len(region)], message)
	if err != nil {
		return errs.Wrap(errs.CodeTransportProtocol, err)
	}
	if len(encoded) != len(region) {
		return errs.NewMessage(errs.CodeInternal, "pb: 编码长度在 Marshal 期间发生变化")
	}
	return nil
}

var _ protocol.Codec = (*Codec)(nil)

func isNilMessage(message proto.Message) bool {
	if message == nil {
		return true
	}
	value := reflect.ValueOf(message)
	return value.Kind() == reflect.Pointer && value.IsNil()
}
