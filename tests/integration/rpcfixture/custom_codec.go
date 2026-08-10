package rpcfixture

import (
	"encoding/binary"
	"time"

	"github.com/duanhf2012/origin/v3/errs"
	"github.com/duanhf2012/origin/v3/rpc"
)

const timeCodecPayloadSize = 8

//origin:rpc-codec id=origin.test.time-unixnano version=1
type TimeCodec struct{}

// 编译期断言只帮助测试夹具及时发现手写签名漂移；生成热路径仍直接调用 TimeCodec。
var _ rpc.StaticCodec[time.Time] = TimeCodec{}

// Size 返回固定八字节 UnixNano payload，并为集成测试提供可稳定触发的大小错误。
func (TimeCodec) Size(value *time.Time) (int, error) {
	if value == nil || value.Year() == 2200 {
		return 0, errs.ErrInvalidArgument
	}
	return timeCodecPayloadSize, nil
}

// MarshalTo 把时间点直接写入最终 Buffer，不建立中间字节切片。
func (TimeCodec) MarshalTo(
	dst []byte,
	value *time.Time,
) (int, error) {
	if value == nil || len(dst) != timeCodecPayloadSize ||
		value.Year() == 2201 {
		return 0, errs.ErrInvalidArgument
	}
	binary.LittleEndian.PutUint64(dst, uint64(value.UnixNano()))
	if value.Year() == 2202 {
		// 故意返回错误长度，验证生成代码不会发送未完整确认的池内数据。
		return timeCodecPayloadSize - 1, nil
	}
	return timeCodecPayloadSize, nil
}

// Unmarshal 从借用的八字节 payload 恢复 UTC 时间，并为集成测试提供固定解码错误。
func (TimeCodec) Unmarshal(
	src []byte,
	value *time.Time,
) error {
	if value == nil || len(src) != timeCodecPayloadSize {
		return errs.ErrInvalidArgument
	}
	decoded := time.Unix(
		0,
		int64(binary.LittleEndian.Uint64(src)),
	).UTC()
	if decoded.Year() == 2203 {
		return errs.ErrInvalidArgument
	}
	*value = decoded
	return nil
}

// PackedPlayerID 是可按 uint64 编码、但由自定义 Codec 显式替换线表示的具名类型。
type PackedPlayerID uint64

//origin:rpc-codec id=origin.test.player-id-u32 version=1
type PackedPlayerIDCodec struct{}

// 该断言锁定 Provider 与目标类型的公开方法形状。
var _ rpc.StaticCodec[PackedPlayerID] = PackedPlayerIDCodec{}

// Size 把允许范围内的 PlayerID 固定压缩为四字节。
func (PackedPlayerIDCodec) Size(value *PackedPlayerID) (int, error) {
	if value == nil || uint64(*value) > uint64(^uint32(0)) {
		return 0, errs.ErrInvalidArgument
	}
	return 4, nil
}

// MarshalTo 直接写入小端四字节 PlayerID。
func (PackedPlayerIDCodec) MarshalTo(
	dst []byte,
	value *PackedPlayerID,
) (int, error) {
	if value == nil || len(dst) != 4 ||
		uint64(*value) > uint64(^uint32(0)) {
		return 0, errs.ErrInvalidArgument
	}
	binary.LittleEndian.PutUint32(dst, uint32(*value))
	return len(dst), nil
}

// Unmarshal 从四字节 payload 恢复具名 PlayerID。
func (PackedPlayerIDCodec) Unmarshal(
	src []byte,
	value *PackedPlayerID,
) error {
	if value == nil || len(src) != 4 {
		return errs.ErrInvalidArgument
	}
	*value = PackedPlayerID(binary.LittleEndian.Uint32(src))
	return nil
}

// OwnedBlob 是用于验证自定义 Codec 解码结果独立持有内存的具名 Slice。
type OwnedBlob []byte

//origin:rpc-codec id=origin.test.owned-blob version=1
type OwnedBlobCodec struct{}

// 该断言锁定变长自定义 Codec 的公开方法形状。
var _ rpc.StaticCodec[OwnedBlob] = OwnedBlobCodec{}

// Size 使用一个自定义 presence 字节保留 nil 与非 nil 空 Slice 的区别。
func (OwnedBlobCodec) Size(value *OwnedBlob) (int, error) {
	if value == nil {
		return 0, errs.ErrInvalidArgument
	}
	return 1 + len(*value), nil
}

// MarshalTo 直接把 presence 和业务字节写入最终 Buffer。
func (OwnedBlobCodec) MarshalTo(
	dst []byte,
	value *OwnedBlob,
) (int, error) {
	if value == nil || len(dst) != 1+len(*value) {
		return 0, errs.ErrInvalidArgument
	}
	if *value == nil {
		dst[0] = 0
		return 1, nil
	}
	dst[0] = 1
	copy(dst[1:], *value)
	return len(dst), nil
}

// Unmarshal 复制业务字节，确保结果在 Origin 释放输入 Buffer 后仍然有效。
func (OwnedBlobCodec) Unmarshal(
	src []byte,
	value *OwnedBlob,
) error {
	if value == nil || len(src) == 0 || src[0] > 1 {
		return errs.ErrInvalidArgument
	}
	if src[0] == 0 {
		if len(src) != 1 {
			return errs.ErrInvalidArgument
		}
		*value = nil
		return nil
	}
	result := make(OwnedBlob, len(src)-1)
	copy(result, src[1:])
	*value = result
	return nil
}
