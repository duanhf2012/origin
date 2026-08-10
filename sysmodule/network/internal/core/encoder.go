// Package core 实现网络 Module 共享但不向业务公开的 Session、容量和编码所有权。
package core

import (
	"github.com/duanhf2012/origin/v3/errs"
	"github.com/duanhf2012/origin/v3/internal/bufferpool"
	public "github.com/duanhf2012/origin/v3/sysmodule/network"
)

const initialEncoderCapacity = 256

// Encoder 是协议 Codec 使用的有界、框架所有字节写入器。
//
// Encoder 只在一次同步 Encode 调用内有效，不得复制或跨 goroutine 保存。
type Encoder struct {
	pool   *bufferpool.Pool
	buffer *bufferpool.Buffer
	length int
	limit  int
}

// EncodeAndSend 为内部 Session 建立 Encoder，成功后原子转移最终 Buffer 所有权。
func EncodeAndSend(
	session public.Session,
	encode func(*Encoder) error,
) (err error) {
	// 只有框架创建的 Session 具有内部所有权能力；伪造实现不能取得 Buffer Pool。
	target, ok := session.(*Session)
	if !ok || target == nil || encode == nil {
		return errs.ErrInvalidArgument
	}
	encoder := &Encoder{
		pool:  target.runtime.pool,
		limit: target.runtime.options.MaxMessageSize,
	}
	defer func() {
		// Codec panic 转成稳定内部错误；尚未转移的 Buffer 始终由当前函数释放。
		if value := recover(); value != nil {
			encoder.release()
			err = errs.NewMessage(errs.CodeInternal, "network protocol encode panic")
		}
	}()
	if err := encode(encoder); err != nil {
		encoder.release()
		return err
	}
	buffer := encoder.take()
	if err := target.sendOwned(buffer); err != nil {
		buffer.Release()
		return err
	}
	return nil
}

// Len 返回当前已经提交的编码字节数。
func (encoder *Encoder) Len() int {
	if encoder == nil {
		return 0
	}
	return encoder.length
}

// Append 复制 data 到最终框架 Buffer。
func (encoder *Encoder) Append(data []byte) error {
	region, err := encoder.Reserve(len(data))
	if err != nil {
		return err
	}
	copy(region, data)
	return nil
}

// AppendByte 追加一个字节。
func (encoder *Encoder) AppendByte(value byte) error {
	region, err := encoder.Reserve(1)
	if err != nil {
		return err
	}
	region[0] = value
	return nil
}

// Reserve 扩展 size 字节并返回必须在调用返回前写满的最终区域。
func (encoder *Encoder) Reserve(size int) ([]byte, error) {
	// 负数、nil Encoder 和整数溢出都在修改状态前拒绝。
	if encoder == nil || size < 0 || encoder.length < 0 ||
		encoder.length > encoder.limit || size > encoder.limit-encoder.length {
		return nil, errs.ErrTransportMessageTooLarge
	}
	needed := encoder.length + size
	if needed > encoder.limit {
		return nil, errs.ErrTransportMessageTooLarge
	}
	if err := encoder.ensureCapacity(needed); err != nil {
		return nil, err
	}
	start := encoder.length
	encoder.length = needed
	if !encoder.buffer.Resize(needed) {
		panic("network core: Encoder 容量检查后 Resize 失败")
	}
	return encoder.buffer.Bytes()[start:needed], nil
}

// Truncate 把已提交长度缩短到 size，供流式编码器移除确定性尾部分隔符。
func (encoder *Encoder) Truncate(size int) error {
	if encoder == nil || size < 0 || size > encoder.length {
		return errs.ErrInvalidArgument
	}
	encoder.length = size
	if encoder.buffer != nil && !encoder.buffer.Resize(size) {
		panic("network core: Encoder Truncate 违反容量不变量")
	}
	return nil
}

// ensureCapacity 通过 2 倍增长取得能够容纳 needed 的 Pool Buffer。
func (encoder *Encoder) ensureCapacity(needed int) error {
	if needed == 0 {
		return nil
	}
	if encoder.buffer != nil && encoder.buffer.Capacity() >= needed {
		return nil
	}

	// 首次至少取得 256B，后续按两倍增长并在最后一步截断到消息硬上限。
	capacity := initialEncoderCapacity
	if encoder.buffer != nil {
		capacity = encoder.buffer.Capacity() * 2
	}
	if capacity < needed {
		capacity = needed
	}
	if capacity > encoder.limit {
		capacity = encoder.limit
	}
	if capacity < needed {
		return errs.ErrTransportMessageTooLarge
	}

	next := encoder.pool.Acquire(capacity)
	if !next.Resize(encoder.length) {
		next.Release()
		panic("network core: 新 Encoder Buffer 无法恢复有效长度")
	}
	if encoder.buffer != nil {
		copy(next.Bytes(), encoder.buffer.Bytes())
		encoder.buffer.Release()
	}
	encoder.buffer = next
	return nil
}

// take 结束 Encoder 所有权并返回最终有效 Buffer。
func (encoder *Encoder) take() *bufferpool.Buffer {
	if encoder.buffer == nil {
		return encoder.pool.Acquire(0)
	}
	buffer := encoder.buffer
	encoder.buffer = nil
	encoder.length = 0
	return buffer
}

// release 释放尚未转移的临时编码 Buffer。
func (encoder *Encoder) release() {
	if encoder == nil || encoder.buffer == nil {
		return
	}
	encoder.buffer.Release()
	encoder.buffer = nil
	encoder.length = 0
}
