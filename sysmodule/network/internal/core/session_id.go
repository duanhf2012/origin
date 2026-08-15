package core

import (
	"encoding/base64"
	"errors"
	"io"

	public "github.com/duanhf2012/origin/v3/sysmodule/network"
)

const maxSessionIDGenerationAttempts = 4

// newSessionID 从安全随机源生成 22 字符 Base64URL 连接标识。
//
// Reader 由调用方传入，使失败和确定性格式无需替换包级状态即可测试。生产调用固定使用
// crypto/rand.Reader。编码保留全部 128 位随机性，不设置 UUID 专用位，文本末尾也不添加
// 等号填充；空字符串始终保留为未绑定或非法 Session。
func newSessionID(source io.Reader) (public.SessionID, error) {
	if source == nil {
		return "", errors.New("network SessionID 随机源不能为空")
	}
	var raw [16]byte
	if _, err := io.ReadFull(source, raw[:]); err != nil {
		return "", err
	}

	// RawURLEncoding 只使用 URL 安全字符且省略末尾填充，16 字节因此固定编码为 22 字符。
	var encoded [22]byte
	base64.RawURLEncoding.Encode(encoded[:], raw[:])
	return public.SessionID(encoded[:]), nil
}
