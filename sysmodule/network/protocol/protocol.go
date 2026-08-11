// Package protocol 提供可选的类型消息路由，不改变底层网络传输的 Raw 消息语义。
package protocol

import (
	"github.com/duanhf2012/origin/v3/sysmodule/network/internal/core"
)

// MessageID 是 PB、JSON 和自定义 Codec 共享的非零消息标识。
type MessageID uint16

// Message 是 Codec 解码后的消息标识和值。
type Message struct {
	ID    MessageID
	Value any
}

// Resolver 按消息标识创建 Router 注册的目标对象。
type Resolver interface {
	New(MessageID) (any, bool)
}

// Codec 在一条完整 Raw 消息和类型消息之间转换。
//
// Decode 由所属 Service 在 OnMessage 中串行调用；Encode 在 Router.Send 的调用 goroutine 中同步
// 执行。跨 goroutine 调用 Router.Send 时 Codec 和消息值必须并发安全。Encode 只能把最终字节写入
// Encoder，不能保存 Encoder。
type Codec interface {
	Decode([]byte, Resolver) (Message, error)
	Encode(*Encoder, MessageID, any) error
}

// Encoder 是 Codec 使用的有界、框架所有的最终消息写入器。
type Encoder struct {
	core *core.Encoder
}

// Len 返回已经写入的字节数。
func (encoder *Encoder) Len() int {
	if encoder == nil || encoder.core == nil {
		return 0
	}
	return encoder.core.Len()
}

// Append 把 data 复制到最终消息 Buffer。
func (encoder *Encoder) Append(data []byte) error {
	if encoder == nil || encoder.core == nil {
		return invalidArgument("protocol: Encoder 不能为空")
	}
	return encoder.core.Append(data)
}

// AppendByte 追加一个字节。
func (encoder *Encoder) AppendByte(value byte) error {
	if encoder == nil || encoder.core == nil {
		return invalidArgument("protocol: Encoder 不能为空")
	}
	return encoder.core.AppendByte(value)
}

// Reserve 预留并返回必须在当前 Encode 调用内写满的最终区域。
func (encoder *Encoder) Reserve(size int) ([]byte, error) {
	if encoder == nil || encoder.core == nil {
		return nil, invalidArgument("protocol: Encoder 不能为空")
	}
	return encoder.core.Reserve(size)
}
