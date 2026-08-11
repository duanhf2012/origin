package lengthframe

import "testing"

func TestEncodeDecode(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		size  int
		order ByteOrder
		value int
	}{
		{name: "one byte", size: 1, order: BigEndian, value: 0xfe},
		{name: "two byte big", size: 2, order: BigEndian, value: 0xfedc},
		{name: "two byte little", size: 2, order: LittleEndian, value: 0xfedc},
		{name: "four byte big", size: 4, order: BigEndian, value: 0x12345678},
		{name: "four byte little", size: 4, order: LittleEndian, value: 0x12345678},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			options := Options{Size: test.size, ByteOrder: test.order}
			var header [4]byte
			if size := Encode(&header, test.value, options); size != test.size {
				t.Fatalf("Encode() size = %d, want %d", size, test.size)
			}
			if got := Decode(header[:test.size], options); got != uint64(test.value) {
				t.Fatalf("Decode() = %d, want %d", got, test.value)
			}
		})
	}
}

func TestInvalidSizePanics(t *testing.T) {
	t.Parallel()
	assertPanic := func(t *testing.T, call func()) {
		t.Helper()
		defer func() {
			if recover() == nil {
				t.Fatal("expected panic")
			}
		}()
		call()
	}
	assertPanic(t, func() {
		var header [4]byte
		Encode(&header, 0, Options{})
	})
	assertPanic(t, func() { Decode(nil, Options{}) })
}

// FuzzEncodeDecode 覆盖 TCP/KCP 共用长度字段的全部宽度、端序和数值边界。
func FuzzEncodeDecode(f *testing.F) {
	f.Add(byte(0), false, uint32(0))
	f.Add(byte(1), true, uint32(0xffff))
	f.Add(byte(2), false, ^uint32(0))
	f.Fuzz(func(t *testing.T, selector byte, little bool, raw uint32) {
		sizes := [...]int{1, 2, 4}
		size := sizes[int(selector)%len(sizes)]
		order := BigEndian
		if little {
			order = LittleEndian
		}
		var maximum uint32
		switch size {
		case 1:
			maximum = 1<<8 - 1
		case 2:
			maximum = 1<<16 - 1
		case 4:
			maximum = ^uint32(0)
		}
		value := raw & maximum
		options := Options{Size: size, ByteOrder: order}
		var header [4]byte
		if got := Encode(&header, int(value), options); got != size {
			t.Fatalf("Encode() size = %d, want %d", got, size)
		}
		if got := Decode(header[:size], options); got != uint64(value) {
			t.Fatalf("Decode() = %d, want %d", got, value)
		}
	})
}
