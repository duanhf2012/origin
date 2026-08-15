package identifier

import (
	"bytes"
	cryptorand "crypto/rand"
	"errors"
	"fmt"
	"io"
	"testing"
	"time"
)

func TestNewTimeRandomWithEncodesTimestampAndRandomBytes(t *testing.T) {
	// 固定时间域为 0x01020304，随机部分为连续字节，锁定 20 字节布局和 27 字符编码。
	source := []byte{
		0x00, 0x01, 0x02, 0x03,
		0x04, 0x05,
		0x06, 0x07,
		0x08, 0x09,
		0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f,
	}
	now := time.Unix(timeRandomEpochUnixSeconds+0x01020304, 0)
	id, err := NewTimeRandomWith(now, bytes.NewReader(source))
	if err != nil {
		t.Fatal(err)
	}
	const want = "AQIDBAABAgMEBQYHCAkKCwwNDg8"
	if id != want || len(id) != TimeRandomLength {
		t.Fatalf(
			"NewTimeRandomWith() = %q length=%d, want %q length=%d",
			id,
			len(id),
			want,
			TimeRandomLength,
		)
	}
}

func TestTimeRandomTimestampUsesFixedEpochAndUint32Cycle(t *testing.T) {
	// 覆盖 Epoch 之前、起点、32 位边界和自然回绕，防止未来替换成不兼容的截断语义。
	tests := []struct {
		name string
		unix int64
		want uint32
	}{
		{name: "before epoch", unix: timeRandomEpochUnixSeconds - 1, want: ^uint32(0)},
		{name: "at epoch", unix: timeRandomEpochUnixSeconds, want: 0},
		{name: "last second", unix: timeRandomEpochUnixSeconds + 1<<32 - 1, want: ^uint32(0)},
		{name: "cycle", unix: timeRandomEpochUnixSeconds + 1<<32, want: 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := timeRandomTimestamp(time.Unix(test.unix, 0)); got != test.want {
				t.Fatalf("timeRandomTimestamp(%d) = %d, want %d", test.unix, got, test.want)
			}
		})
	}
}

func TestNewTimeRandomWithSeparatesTimestampDomains(t *testing.T) {
	// 固定全部随机字节，只改变秒级时间域，确认跨秒 ID 不会因随机部分相同而重复。
	first, err := NewTimeRandomWith(
		time.Unix(timeRandomEpochUnixSeconds+1, 0),
		bytes.NewReader(make([]byte, 16)),
	)
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewTimeRandomWith(
		time.Unix(timeRandomEpochUnixSeconds+2, 0),
		bytes.NewReader(make([]byte, 16)),
	)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatalf("different timestamp domains produced the same ID %q", first)
	}
}

func TestNewTimeRandomWithRejectsUnavailableRandomSource(t *testing.T) {
	// nil、短读和显式错误都必须返回空 ID，不得用时间域单独降级生成。
	tests := []struct {
		name   string
		source io.Reader
	}{
		{name: "nil"},
		{name: "short", source: bytes.NewReader(make([]byte, 15))},
		{name: "error", source: errorReader{}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			id, err := NewTimeRandomWith(time.Unix(timeRandomEpochUnixSeconds, 0), test.source)
			if err == nil || id != "" {
				t.Fatalf("NewTimeRandomWith() = %q, %v", id, err)
			}
		})
	}
}

func TestNewTimeRandomEntrypointsDoNotCollideAcrossIndependentCalls(t *testing.T) {
	// 大样本同时覆盖便捷入口和可注入入口，并确认所有输出都满足固定长度与非空契约。
	const count = 16_384
	seen := make(map[string]struct{}, count+1)
	first, err := NewTimeRandom()
	if err != nil {
		t.Fatal(err)
	}
	seen[first] = struct{}{}
	for index := 0; index < count; index++ {
		id, err := NewTimeRandomWith(time.Unix(timeRandomEpochUnixSeconds, 0), cryptorand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		if len(id) != TimeRandomLength {
			t.Fatalf("NewTimeRandomWith() length = %d, want %d", len(id), TimeRandomLength)
		}
		if _, exists := seen[id]; exists {
			t.Fatalf("duplicate ID %q", id)
		}
		seen[id] = struct{}{}
	}
}

func BenchmarkNewTimeRandom(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		id, err := NewTimeRandom()
		if err != nil {
			b.Fatal(err)
		}
		benchmarkSink = id
	}
}

type errorReader struct{}

func (errorReader) Read([]byte) (int, error) {
	return 0, errors.New("random source unavailable")
}

var benchmarkSink string

func ExampleNewTimeRandom() {
	// 普通调用方只需使用系统时钟和安全随机源的便捷入口。
	value, err := NewTimeRandom()
	fmt.Println(err == nil, len(value))
	// Output:
	// true 27
}
