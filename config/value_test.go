package config

import (
	"math"
	"strconv"
	"testing"
	"time"
)

func TestDurationUnmarshalText(t *testing.T) {
	t.Parallel()

	// 样本覆盖零值、单单位、复合单位和纳秒精度。
	tests := []struct {
		input string
		want  time.Duration
	}{
		{input: "0s", want: 0},
		{input: "500us", want: 500 * time.Microsecond},
		{input: "15s", want: 15 * time.Second},
		{input: "2h30m", want: 2*time.Hour + 30*time.Minute},
		{input: "14d", want: 14 * 24 * time.Hour},
		{input: "1d2h3m4s5ms6us7ns", want: 26*time.Hour + 3*time.Minute + 4*time.Second + 5*time.Millisecond + 6*time.Microsecond + 7*time.Nanosecond},
	}
	// 每个合法文本都应精确换算为标准库 Duration。
	for _, test := range tests {
		test := test
		t.Run(test.input, func(t *testing.T) {
			t.Parallel()
			var value Duration
			if err := value.UnmarshalText([]byte(test.input)); err != nil {
				t.Fatalf("UnmarshalText(%q) 返回错误: %v", test.input, err)
			}
			if got := value.Duration(); got != test.want {
				t.Fatalf("Duration() = %v，期望 %v", got, test.want)
			}
		})
	}
}

func TestDurationRejectsInvalidValues(t *testing.T) {
	t.Parallel()

	// 覆盖空值、非规范零、负数、小数、单位错误、乱序、重复和溢出。
	inputs := []string{
		"",
		"0",
		"0h",
		"-1s",
		"1.5s",
		"1S",
		"1day",
		"1w",
		"1s1m",
		"1s2s",
		"1h30",
		"999999999999999999999999d",
	}
	// 非法文本不能静默得到零值或截断结果。
	for _, input := range inputs {
		input := input
		t.Run(input, func(t *testing.T) {
			t.Parallel()
			var value Duration
			if err := value.UnmarshalText([]byte(input)); err == nil {
				t.Fatalf("UnmarshalText(%q) 应返回错误", input)
			}
		})
	}
}

func TestByteSizeUnmarshalText(t *testing.T) {
	t.Parallel()

	// 样本覆盖规范零值和全部五种受支持单位。
	tests := []struct {
		input string
		want  int64
	}{
		{input: "0B", want: 0},
		{input: "16B", want: 16},
		{input: "64KB", want: 64 << 10},
		{input: "4M", want: 4 << 20},
		{input: "2G", want: 2 << 30},
		{input: "1T", want: 1 << 40},
	}
	// 单位必须按固定二进制倍数换算。
	for _, test := range tests {
		test := test
		t.Run(test.input, func(t *testing.T) {
			t.Parallel()
			var value ByteSize
			if err := value.UnmarshalText([]byte(test.input)); err != nil {
				t.Fatalf("UnmarshalText(%q) 返回错误: %v", test.input, err)
			}
			if got := value.Bytes(); got != test.want {
				t.Fatalf("Bytes() = %d，期望 %d", got, test.want)
			}
		})
	}
}

func TestByteSizeRejectsInvalidValues(t *testing.T) {
	t.Parallel()

	// 覆盖空值、非规范零、负数、小数、别名、复合值和数字溢出。
	inputs := []string{
		"",
		"0",
		"0M",
		"-1M",
		"1.5M",
		"1K",
		"1MB",
		"1MiB",
		"1m",
		"1M1KB",
		"9223372036854775808B",
	}
	// 所有非契约格式都必须返回错误。
	for _, input := range inputs {
		input := input
		t.Run(input, func(t *testing.T) {
			t.Parallel()
			var value ByteSize
			if err := value.UnmarshalText([]byte(input)); err == nil {
				t.Fatalf("UnmarshalText(%q) 应返回错误", input)
			}
		})
	}

	// 单独构造“数字可解析但乘以 T 后溢出 int64”的边界。
	overflow := uint64(math.MaxInt64)/(1<<40) + 1
	var value ByteSize
	if err := value.UnmarshalText([]byte(strconv.FormatUint(overflow, 10) + "T")); err == nil {
		t.Fatal("超过 int64 的 T 值应返回错误")
	}
}
