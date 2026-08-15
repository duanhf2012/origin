package core

import (
	"encoding/hex"
	"errors"
	"io"

	public "github.com/duanhf2012/origin/v3/sysmodule/network"
)

const maxSessionIDGenerationAttempts = 4

// newSessionID 从安全随机源生成小写 RFC 9562 UUID v4 文本。
//
// Reader 由调用方传入，使失败和确定性格式无需替换包级状态即可测试。生产调用固定使用
// crypto/rand.Reader；空字符串始终保留为未绑定或非法 Session。
func newSessionID(source io.Reader) (public.SessionID, error) {
	if source == nil {
		return "", errors.New("network SessionID 随机源不能为空")
	}
	var raw [16]byte
	if _, err := io.ReadFull(source, raw[:]); err != nil {
		return "", err
	}
	// Version 4 使用随机 UUID，Variant 10xx 遵循 RFC 9562。
	raw[6] = raw[6]&0x0f | 0x40
	raw[8] = raw[8]&0x3f | 0x80

	var encoded [36]byte
	hex.Encode(encoded[0:8], raw[0:4])
	encoded[8] = '-'
	hex.Encode(encoded[9:13], raw[4:6])
	encoded[13] = '-'
	hex.Encode(encoded[14:18], raw[6:8])
	encoded[18] = '-'
	hex.Encode(encoded[19:23], raw[8:10])
	encoded[23] = '-'
	hex.Encode(encoded[24:36], raw[10:16])
	return public.SessionID(encoded[:]), nil
}
