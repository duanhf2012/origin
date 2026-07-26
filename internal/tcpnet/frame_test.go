package tcpnet

import (
	"bytes"
	"testing"
)

func TestFrameLengthRoundTrip(t *testing.T) {
	t.Parallel()

	// 表格覆盖全部宽度、大小端、零长度和各宽度的代表性高位值。
	tests := []struct {
		name    string
		options FrameOptions
		length  int
		want    []byte
	}{
		{
			name:    "one byte zero",
			options: FrameOptions{LengthFieldSize: 1, ByteOrder: BigEndian},
			length:  0,
			want:    []byte{0},
		},
		{
			name:    "one byte max",
			options: FrameOptions{LengthFieldSize: 1, ByteOrder: LittleEndian},
			length:  255,
			want:    []byte{0xff},
		},
		{
			name:    "two byte big endian",
			options: FrameOptions{LengthFieldSize: 2, ByteOrder: BigEndian},
			length:  0x1234,
			want:    []byte{0x12, 0x34},
		},
		{
			name:    "two byte little endian",
			options: FrameOptions{LengthFieldSize: 2, ByteOrder: LittleEndian},
			length:  0x1234,
			want:    []byte{0x34, 0x12},
		},
		{
			name:    "four byte big endian",
			options: FrameOptions{LengthFieldSize: 4, ByteOrder: BigEndian},
			length:  0x12345678,
			want:    []byte{0x12, 0x34, 0x56, 0x78},
		},
		{
			name:    "four byte little endian",
			options: FrameOptions{LengthFieldSize: 4, ByteOrder: LittleEndian},
			length:  0x12345678,
			want:    []byte{0x78, 0x56, 0x34, 0x12},
		},
	}

	// 编码结果和解码回环同时断言，避免两条路径出现相互抵消的字节序错误。
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			var header [4]byte
			size := encodeFrameLength(&header, test.length, test.options)
			if size != len(test.want) {
				t.Fatalf("header size = %d，期望 %d", size, len(test.want))
			}
			if !bytes.Equal(header[:size], test.want) {
				t.Fatalf("header = %x，期望 %x", header[:size], test.want)
			}
			if got := decodeFrameLength(header[:size], test.options); got != uint64(test.length) {
				t.Fatalf("decoded = %d，期望 %d", got, test.length)
			}
		})
	}
}

func TestInvalidFrameWidthPanics(t *testing.T) {
	t.Parallel()

	// 直接调用内部热路径模拟 Options 校验被绕过，必须尽早暴露内部不变量破坏。
	assertPanics(t, func() {
		var header [4]byte
		encodeFrameLength(&header, 1, FrameOptions{LengthFieldSize: 3})
	})
	assertPanics(t, func() {
		decodeFrameLength([]byte{0, 0, 0}, FrameOptions{LengthFieldSize: 3})
	})
}

// assertPanics 验证内部不变量测试确实触发 panic。
func assertPanics(t *testing.T, call func()) {
	t.Helper()

	// recover 必须放在 defer 中；正常返回说明测试目标没有拒绝非法状态。
	defer func() {
		if recover() == nil {
			t.Fatal("期望 panic，但函数正常返回")
		}
	}()
	call()
}
