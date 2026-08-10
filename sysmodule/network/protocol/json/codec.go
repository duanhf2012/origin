// Package json 提供固定 id/data Envelope 的标准 JSON Codec。
package json

import (
	stdjson "encoding/json"

	"github.com/duanhf2012/origin/v3/errs"
	"github.com/duanhf2012/origin/v3/sysmodule/network/protocol"
)

// Codec 使用标准库 JSON 行为，不增加私有字段规则。
type Codec struct{}

// NewCodec 创建无状态、可共享的 JSON Codec。
func NewCodec() *Codec { return &Codec{} }

type decodeEnvelope struct {
	ID   protocol.MessageID `json:"id"`
	Data stdjson.RawMessage `json:"data"`
}

type encodeEnvelope struct {
	ID   protocol.MessageID `json:"id"`
	Data any                `json:"data"`
}

// Decode 先读取 Envelope，再把 data 解码到 Router 注册对象。
func (*Codec) Decode(payload []byte, resolver protocol.Resolver) (protocol.Message, error) {
	if resolver == nil {
		return protocol.Message{}, errs.ErrInvalidArgument
	}
	var envelope decodeEnvelope
	if err := stdjson.Unmarshal(payload, &envelope); err != nil {
		return protocol.Message{}, errs.Wrap(errs.CodeTransportProtocol, err)
	}
	if envelope.ID == 0 {
		return protocol.Message{}, errs.NewMessage(errs.CodeTransportProtocol, "json: MessageID 不能为零")
	}
	target, exists := resolver.New(envelope.ID)
	if !exists {
		return protocol.Message{ID: envelope.ID}, nil
	}
	if len(envelope.Data) == 0 {
		return protocol.Message{}, errs.NewMessage(errs.CodeTransportProtocol, "json: 缺少 data 字段")
	}
	if err := stdjson.Unmarshal(envelope.Data, target); err != nil {
		return protocol.Message{}, errs.Wrap(errs.CodeTransportProtocol, err)
	}
	return protocol.Message{ID: envelope.ID, Value: target}, nil
}

// Encode 使用固定小写 id/data 字段并写入最终 Encoder。
func (*Codec) Encode(encoder *protocol.Encoder, id protocol.MessageID, value any) error {
	if encoder == nil || id == 0 || value == nil {
		return errs.ErrInvalidArgument
	}
	payload, err := stdjson.Marshal(encodeEnvelope{ID: id, Data: value})
	if err != nil {
		return errs.Wrap(errs.CodeTransportProtocol, err)
	}
	return encoder.Append(payload)
}

var _ protocol.Codec = (*Codec)(nil)
