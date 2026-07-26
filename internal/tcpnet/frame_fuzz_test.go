package tcpnet

import "testing"

func FuzzFrameLengthRoundTrip(fuzz *testing.F) {
	// 种子覆盖零值、每种宽度边界和超过窄宽度的截断输入。
	for _, seed := range []uint32{0, 1, 255, 256, 65535, 65536, ^uint32(0)} {
		fuzz.Add(seed, uint8(1), false)
		fuzz.Add(seed, uint8(2), true)
		fuzz.Add(seed, uint8(4), false)
	}

	fuzz.Fuzz(func(t *testing.T, value uint32, rawWidth uint8, little bool) {
		// 把任意 Fuzz 宽度稳定映射到协议支持的一、二、四字节。
		widths := [...]int{1, 2, 4}
		width := widths[int(rawWidth)%len(widths)]
		order := BigEndian
		if little {
			order = LittleEndian
		}
		options := FrameOptions{
			LengthFieldSize: width,
			ByteOrder:       order,
		}

		// 编码函数接收 int；当前支持平台均能无损表示 uint32 对应范围。
		var header [4]byte
		encodeFrameLength(&header, int(uint64(value)), options)
		got := decodeFrameLength(header[:width], options)

		// 窄长度字段按低位表达；生产配置会保证 MaxMessageSize 不超过该范围。
		mask := maxFramePayload(width)
		want := uint64(value) & mask
		if got != want {
			t.Fatalf(
				"width=%d order=%d value=%d got=%d want=%d",
				width,
				order,
				value,
				got,
				want,
			)
		}
	})
}
