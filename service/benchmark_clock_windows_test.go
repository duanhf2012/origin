//go:build windows

package service

import (
	"sort"
	"time"
	_ "unsafe"
)

// benchmarkQueryPerformanceCounter 和 benchmarkQueryPerformanceFrequency 只供测试基准使用。
// Go Runtime 已将这两个 Windows API 包装函数导出给标准库 internal/syscall/windows；这里
// 使用同一固定签名，避免通用 syscall.Proc.Call 约 10µs 的额外开销污染微秒级尾延迟。
// 该 linkname 不进入生产包二进制；Go 工具链升级时必须由 Benchmark 编译和时钟开销门禁复核。
//
//go:linkname benchmarkQueryPerformanceCounter internal/syscall/windows.QueryPerformanceCounter
func benchmarkQueryPerformanceCounter() int64

// benchmarkQueryPerformanceFrequency 返回同一性能计数器每秒的 Tick 数。
//
//go:linkname benchmarkQueryPerformanceFrequency internal/syscall/windows.QueryPerformanceFrequency
func benchmarkQueryPerformanceFrequency() int64

// benchmarkCPUTicks 返回 Runtime 当前架构用于调度诊断的低开销 CPU Tick。
//
//go:linkname benchmarkCPUTicks runtime.cputicks
func benchmarkCPUTicks() int64

// benchmarkCPUFrequency 是进程启动时校准并冻结的 CPU Tick 每秒频率。
var benchmarkCPUFrequency = calibrateBenchmarkCPUFrequency()

// benchmarkLatencyTimestamp 保存 Windows 当前架构的 Runtime CPU Tick。
type benchmarkLatencyTimestamp int64

// benchmarkLatencyNow 读取 Go Runtime 的低开销 CPU Tick；amd64 使用已序列化的
// RDTSCP/RDTSC。跨 CPU 不单调的极少数样本由 benchmarkLatencyElapsed 归零并显式统计。
func benchmarkLatencyNow() benchmarkLatencyTimestamp {
	return benchmarkLatencyTimestamp(benchmarkCPUTicks())
}

// benchmarkLatencyElapsed 把性能计数器 Tick 差值安全换算为纳秒。
func benchmarkLatencyElapsed(started benchmarkLatencyTimestamp) int64 {
	delta := int64(benchmarkLatencyNow() - started)
	if delta < 0 {
		return 0
	}
	seconds := delta / benchmarkCPUFrequency
	remainder := delta % benchmarkCPUFrequency
	return seconds*int64(1e9) + remainder*int64(1e9)/benchmarkCPUFrequency
}

// benchmarkLatencyFrequency 返回当前测试时钟每秒的 Tick 数，便于确认校准没有漂移。
func benchmarkLatencyFrequency() int64 {
	return benchmarkCPUFrequency
}

// calibrateBenchmarkCPUFrequency 用多个 20ms 区间校准硬件 Tick 频率。runtime.cputicks 不
// 保证跨 CPU 单调，因此单个迁核样本只能被丢弃；五个有效样本的中位数还能隔离正向偏移。
// QPC 只在测试进程初始化时使用，不进入事件基准的逐次采样热路径。
func calibrateBenchmarkCPUFrequency() int64 {
	performanceFrequency := benchmarkQueryPerformanceFrequency()
	if performanceFrequency <= 0 {
		panic("QueryPerformanceFrequency 返回无效频率")
	}
	const (
		requiredSamples = 5
		maximumAttempts = 15
		sampleDuration  = 20 * time.Millisecond
	)
	frequencies := make([]int64, 0, requiredSamples)
	for attempt := 0; attempt < maximumAttempts && len(frequencies) < requiredSamples; attempt++ {
		performanceStarted := benchmarkQueryPerformanceCounter()
		ticksStarted := benchmarkCPUTicks()
		time.Sleep(sampleDuration)
		ticksElapsed := benchmarkCPUTicks() - ticksStarted
		performanceElapsed := benchmarkQueryPerformanceCounter() - performanceStarted
		if ticksElapsed <= 0 || performanceElapsed <= 0 ||
			ticksElapsed > (1<<63-1)/performanceFrequency {
			continue
		}
		frequency := ticksElapsed * performanceFrequency / performanceElapsed
		if frequency > 0 {
			frequencies = append(frequencies, frequency)
		}
	}
	return selectBenchmarkCPUFrequency(frequencies)
}

// selectBenchmarkCPUFrequency 要求至少三个有效区间，并以中位数隔离调度迁核导致的离群值。
func selectBenchmarkCPUFrequency(frequencies []int64) int64 {
	if len(frequencies) < 3 {
		panic("CPU Tick 频率校准失败")
	}
	sort.Slice(frequencies, func(left, right int) bool {
		return frequencies[left] < frequencies[right]
	})
	return frequencies[len(frequencies)/2]
}
