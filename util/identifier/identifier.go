// Package identifier 提供可在分布式进程间共享的紧凑标识生成算法。
//
// TimeRandom ID 由 32 位秒级循环时间域和 128 位安全随机数组成，通过无填充 Base64URL
// 编码为固定 27 字符文本。它是工程上实际唯一的无中心 ID，不是中心协调下的数学绝对唯一 ID。
package identifier

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"io"
	"time"
)

const (
	// TimeRandomLength 是 NewTimeRandom 和 NewTimeRandomWith 返回的固定文本长度。
	TimeRandomLength = 27
	// timeRandomEpochUnixSeconds 固定为 2026-01-01T00:00:00Z，32 位无符号秒数约 136 年循环一次。
	timeRandomEpochUnixSeconds int64 = 1_767_225_600
)

// NewTimeRandom 使用当前系统时间和系统安全随机源生成 TimeRandom ID。
//
// 返回文本始终是 TimeRandomLength 字符的无填充 Base64URL；只有系统安全随机源不可用时才返回错误。
func NewTimeRandom() (string, error) {
	return NewTimeRandomWith(time.Now(), rand.Reader)
}

// NewTimeRandomWith 使用指定时间和安全随机源生成 TimeRandom ID。
//
// 该入口用于需要实例级依赖注入的框架组件和确定性测试。source 必须提供 16 字节密码学安全随机数；
// 不得在生产中传入弱随机源。时钟回拨、Epoch 之前的时间或时间域回绕只会复用时间域，不会削弱
// 后续完整 128 位随机数的兜底强度。
func NewTimeRandomWith(now time.Time, source io.Reader) (string, error) {
	if source == nil {
		return "", errors.New("identifier: 安全随机源不能为空")
	}

	// 前 4 字节使用大端时间域，后 16 字节保留全部 128 位安全随机性。
	var raw [20]byte
	binary.BigEndian.PutUint32(raw[:4], timeRandomTimestamp(now))
	if _, err := io.ReadFull(source, raw[4:]); err != nil {
		return "", err
	}

	// RawURLEncoding 只使用 URL 安全字符且省略末尾填充，20 字节因此固定编码为 27 字符。
	var encoded [TimeRandomLength]byte
	base64.RawURLEncoding.Encode(encoded[:], raw[:])
	return string(encoded[:]), nil
}

// timeRandomTimestamp 把指定时间投影到约 136 年循环的无符号秒空间。
func timeRandomTimestamp(now time.Time) uint32 {
	return uint32(now.Unix() - timeRandomEpochUnixSeconds)
}
