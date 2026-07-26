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
	// 底层单位与 time.Duration 相同，直接转换且不产生分配。
	return time.Duration(value)
}

// UnmarshalText 按 Origin 的严格时间格式解析文本。
func (value *Duration) UnmarshalText(text []byte) error {
	// 先完整解析到临时值，错误时保持接收者原值不变。
	parsed, err := parseDuration(string(text))
	if err != nil {
		return err
	}
	// 仅在语法和范围全部合法后提交结果。
	*value = Duration(parsed)
	return nil
}

// ByteSize 是配置文件使用的字节容量。
//
// 支持 B、KB、M、G、T，并且所有非 B 单位都采用 1024 倍数。
type ByteSize int64

// Bytes 返回容量对应的字节数。
func (value ByteSize) Bytes() int64 {
	// ByteSize 内部已经统一保存字节数。
	return int64(value)
}

// UnmarshalText 按 Origin 的严格容量格式解析文本。
func (value *ByteSize) UnmarshalText(text []byte) error {
	// 先完整解析到临时值，错误时保持接收者原值不变。
	parsed, err := parseByteSize(string(text))
	if err != nil {
		return err
	}
	// 仅在单位和溢出校验通过后提交结果。
	*value = ByteSize(parsed)
	return nil
}

// parseDuration 按 Origin 严格、非负、降序单位语法解析固定时长。
func parseDuration(input string) (time.Duration, error) {
	// 空字符串没有单位和数值，必须明确拒绝。
	if input == "" {
		return 0, fmt.Errorf("时间不能为空")
	}

	// 使用无符号累计值简化非负语义，并用 previousRank 检查单位降序。
	var total uint64
	previousRank := math.MaxInt
	// 每轮解析一个“整数 + 单位”片段，直到消费完整字符串。
	for index := 0; index < len(input); {
		// 第一阶段提取连续十进制数字。
		numberStart := index
		for index < len(input) && input[index] >= '0' && input[index] <= '9' {
			index++
		}
		if numberStart == index {
			return 0, fmt.Errorf("时间 %q 必须由非负整数和单位组成", input)
		}
		// ParseUint 同时拒绝符号并检查 uint64 数值范围。
		number, err := strconv.ParseUint(input[numberStart:index], 10, 64)
		if err != nil {
			return 0, fmt.Errorf("时间 %q 的数值超出范围", input)
		}

		// 第二阶段读取直到下一个数字前的单位文本。
		unitStart := index
		for index < len(input) && (input[index] < '0' || input[index] > '9') {
			index++
		}
		unitName := input[unitStart:index]
		// 单位查表返回严格排序等级和纳秒乘数。
		rank, multiplier, exists := durationUnit(unitName)
		if !exists {
			return 0, fmt.Errorf("时间 %q 使用了不支持的单位 %q", input, unitName)
		}
		if rank >= previousRank {
			return 0, fmt.Errorf("时间 %q 的单位必须从大到小且不能重复", input)
		}
		// 当前单位合法后更新顺序边界，后续只能使用更小单位。
		previousRank = rank

		// 先检查单片段乘法，再检查与累计值相加，避免 uint64 回绕。
		if number > uint64(math.MaxInt64)/multiplier {
			return 0, fmt.Errorf("时间 %q 超出 time.Duration 范围", input)
		}
		part := number * multiplier
		if total > uint64(math.MaxInt64)-part {
			return 0, fmt.Errorf("时间 %q 超出 time.Duration 范围", input)
		}
		total += part
	}
	// 总值为零只允许唯一规范写法 0s，避免出现多套等价配置。
	if total == 0 && input != "0s" {
		return 0, fmt.Errorf("零时间必须写成 0s")
	}
	// 已保证不超过 MaxInt64，可以安全转换为 time.Duration。
	return time.Duration(total), nil
}

// durationUnit 返回单位的降序等级和纳秒乘数。
func durationUnit(name string) (rank int, multiplier uint64, exists bool) {
	// 显式 switch 保持支持集合固定且无包级可变 Map。
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
		// exists=false 由解析器生成带完整输入上下文的错误。
		return 0, 0, false
	}
}

// parseByteSize 按 Origin 严格二进制简写语法解析字节容量。
func parseByteSize(input string) (int64, error) {
	// 空字符串没有数值和单位，直接拒绝。
	if input == "" {
		return 0, fmt.Errorf("字节容量不能为空")
	}
	// 容量只允许一个十进制整数片段。
	index := 0
	for index < len(input) && input[index] >= '0' && input[index] <= '9' {
		index++
	}
	if index == 0 || index == len(input) {
		return 0, fmt.Errorf("字节容量 %q 必须由非负整数和单位组成", input)
	}
	// ParseUint 拒绝负号和小数，并验证数字本身不溢出。
	number, err := strconv.ParseUint(input[:index], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("字节容量 %q 的数值超出范围", input)
	}
	// 剩余文本必须完整匹配一个受支持单位。
	unitName := input[index:]
	multiplier, exists := byteSizeUnit(unitName)
	if !exists {
		return 0, fmt.Errorf("字节容量 %q 使用了不支持的单位 %q", input, unitName)
	}
	// 在乘法前检查 int64 上限，内部统一使用有符号字节数。
	if number > uint64(math.MaxInt64)/multiplier {
		return 0, fmt.Errorf("字节容量 %q 超出 int64 范围", input)
	}
	result := number * multiplier
	// 零容量只允许 0B，避免 0M 等多种等价写法。
	if result == 0 && input != "0B" {
		return 0, fmt.Errorf("零字节容量必须写成 0B")
	}
	// 范围已经验证，可以安全转换为 int64。
	return int64(result), nil
}

// byteSizeUnit 返回 Origin 简写单位对应的固定二进制乘数。
func byteSizeUnit(name string) (uint64, bool) {
	// 不接受 MB/MiB 等别名，避免项目间出现不同解释。
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
		// false 由上层转换为包含原输入的配置错误。
		return 0, false
	}
}
