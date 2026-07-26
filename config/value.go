package config

import (
	"fmt"
	"math"
	"strconv"
	"time"
)

// Duration 是配置文件使用的固定时间长度。
//
// 支持 ns、us、ms、s、m、h、d，d 固定等于 24h。
type Duration time.Duration

// Duration 返回标准库 time.Duration。
func (value Duration) Duration() time.Duration {
	return time.Duration(value)
}

// UnmarshalText 按 Origin 的严格时间格式解析文本。
func (value *Duration) UnmarshalText(text []byte) error {
	parsed, err := parseDuration(string(text))
	if err != nil {
		return err
	}
	*value = Duration(parsed)
	return nil
}

// ByteSize 是配置文件使用的字节容量。
//
// 支持 B、KB、M、G、T，并且所有非 B 单位都采用 1024 倍数。
type ByteSize int64

// Bytes 返回容量对应的字节数。
func (value ByteSize) Bytes() int64 {
	return int64(value)
}

// UnmarshalText 按 Origin 的严格容量格式解析文本。
func (value *ByteSize) UnmarshalText(text []byte) error {
	parsed, err := parseByteSize(string(text))
	if err != nil {
		return err
	}
	*value = ByteSize(parsed)
	return nil
}

func parseDuration(input string) (time.Duration, error) {
	if input == "" {
		return 0, fmt.Errorf("时间不能为空")
	}

	var total uint64
	previousRank := math.MaxInt
	for index := 0; index < len(input); {
		numberStart := index
		for index < len(input) && input[index] >= '0' && input[index] <= '9' {
			index++
		}
		if numberStart == index {
			return 0, fmt.Errorf("时间 %q 必须由非负整数和单位组成", input)
		}
		number, err := strconv.ParseUint(input[numberStart:index], 10, 64)
		if err != nil {
			return 0, fmt.Errorf("时间 %q 的数值超出范围", input)
		}

		unitStart := index
		for index < len(input) && (input[index] < '0' || input[index] > '9') {
			index++
		}
		unitName := input[unitStart:index]
		rank, multiplier, exists := durationUnit(unitName)
		if !exists {
			return 0, fmt.Errorf("时间 %q 使用了不支持的单位 %q", input, unitName)
		}
		if rank >= previousRank {
			return 0, fmt.Errorf("时间 %q 的单位必须从大到小且不能重复", input)
		}
		previousRank = rank

		if number > uint64(math.MaxInt64)/multiplier {
			return 0, fmt.Errorf("时间 %q 超出 time.Duration 范围", input)
		}
		part := number * multiplier
		if total > uint64(math.MaxInt64)-part {
			return 0, fmt.Errorf("时间 %q 超出 time.Duration 范围", input)
		}
		total += part
	}
	if total == 0 && input != "0s" {
		return 0, fmt.Errorf("零时间必须写成 0s")
	}
	return time.Duration(total), nil
}

func durationUnit(name string) (rank int, multiplier uint64, exists bool) {
	switch name {
	case "d":
		return 7, uint64(24 * time.Hour), true
	case "h":
		return 6, uint64(time.Hour), true
	case "m":
		return 5, uint64(time.Minute), true
	case "s":
		return 4, uint64(time.Second), true
	case "ms":
		return 3, uint64(time.Millisecond), true
	case "us":
		return 2, uint64(time.Microsecond), true
	case "ns":
		return 1, uint64(time.Nanosecond), true
	default:
		return 0, 0, false
	}
}

func parseByteSize(input string) (int64, error) {
	if input == "" {
		return 0, fmt.Errorf("字节容量不能为空")
	}
	index := 0
	for index < len(input) && input[index] >= '0' && input[index] <= '9' {
		index++
	}
	if index == 0 || index == len(input) {
		return 0, fmt.Errorf("字节容量 %q 必须由非负整数和单位组成", input)
	}
	number, err := strconv.ParseUint(input[:index], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("字节容量 %q 的数值超出范围", input)
	}
	unitName := input[index:]
	multiplier, exists := byteSizeUnit(unitName)
	if !exists {
		return 0, fmt.Errorf("字节容量 %q 使用了不支持的单位 %q", input, unitName)
	}
	if number > uint64(math.MaxInt64)/multiplier {
		return 0, fmt.Errorf("字节容量 %q 超出 int64 范围", input)
	}
	result := number * multiplier
	if result == 0 && input != "0B" {
		return 0, fmt.Errorf("零字节容量必须写成 0B")
	}
	return int64(result), nil
}

func byteSizeUnit(name string) (uint64, bool) {
	switch name {
	case "B":
		return 1, true
	case "KB":
		return 1 << 10, true
	case "M":
		return 1 << 20, true
	case "G":
		return 1 << 30, true
	case "T":
		return 1 << 40, true
	default:
		return 0, false
	}
}
