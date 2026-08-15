package core

import (
	"encoding/base64"
	"encoding/binary"
	"errors"
	"io"
	"time"

	public "github.com/duanhf2012/origin/v3/sysmodule/network"
)

const (
	maxSessionIDGenerationAttempts = 4
	// sessionIDEpochUnixSeconds 固定为 2026-01-01T00:00:00Z，32 位无符号秒数约 136 年循环一次。
	sessionIDEpochUnixSeconds int64 = 1_767_225_600
)

// newSessionID 生成 27 字符的秒级时间域与安全随机连接标识。
//
// timestamp 是相对固定 Epoch 的 32 位循环秒数，Reader 在生产中固定为 crypto/rand.Reader。
// 两者由调用方传入，使时钟边界、随机源失败和编码格式都无需替换包级状态即可测试。
func newSessionID(source io.Reader, timestamp uint32) (public.SessionID, error) {
	if source == nil {
		return "", errors.New("network SessionID 随机源不能为空")
	}

	// 前 4 字节使用大端时间域，后 16 字节保留全部 128 位安全随机性。
	var raw [20]byte
	binary.BigEndian.PutUint32(raw[:4], timestamp)
	if _, err := io.ReadFull(source, raw[4:]); err != nil {
		return "", err
	}

	// RawURLEncoding 只使用 URL 安全字符且省略末尾填充，20 字节因此固定编码为 27 字符。
	var encoded [27]byte
	base64.RawURLEncoding.Encode(encoded[:], raw[:])
	return public.SessionID(encoded[:]), nil
}

// sessionIDTimestamp 把当前时间投影到约 136 年循环的无符号秒空间。
//
// 时钟回拨、Epoch 之前的时间或整数回绕只会复用时间域，不会削弱后续完整 128 位随机数的兜底强度。
func sessionIDTimestamp(now time.Time) uint32 {
	return uint32(now.Unix() - sessionIDEpochUnixSeconds)
}
