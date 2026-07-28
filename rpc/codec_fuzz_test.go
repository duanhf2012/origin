package rpc

import "testing"

// FuzzReaderRejectsArbitraryPayloadWithoutPanic 把任意字节送入全部长度敏感读取入口。
//
// Fuzz 目标不是要求随机载荷成功，而是锁定“截断、伪造长度和非法 bool 只能返回稳定
// 解码错误，不能越界、整数溢出或 panic”的边界。
func FuzzReaderRejectsArbitraryPayloadWithoutPanic(f *testing.F) {
	f.Add([]byte{})
	f.Add([]byte{0xff, 0xff, 0xff, 0xff})
	f.Add([]byte{1, 0, 0, 0, 'a'})
	f.Fuzz(func(t *testing.T, payload []byte) {
		// 每个 Reader 只测试一个入口，避免前一个粘滞错误掩盖后续入口自己的边界。
		boolReader := NewRequestReader(payload)
		_, _ = boolReader.ReadBool()

		stringReader := NewRequestReader(payload)
		_, _ = stringReader.ReadString()

		bytesReader := NewRequestReader(payload)
		_, _ = bytesReader.ReadBytes()

		containerReader := NewRequestReader(payload)
		count, _, err := containerReader.ReadContainer()
		if err == nil {
			_ = containerReader.CheckElements(count, 8)
		}

		protoReader := NewResponseReader(payload)
		_, _, _ = protoReader.ReadProtoPayload()

		// 自定义 Codec 使用独立的非 nil 长度边界；任意伪造长度只能返回固定错误。
		customReader := NewRequestReader(payload)
		_, _ = customReader.ReadCustomPayload()
	})
}
