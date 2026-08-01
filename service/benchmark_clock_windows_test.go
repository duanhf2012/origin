//go:build windows

package service

import (
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
// RDTSCP/RDTSC。M22 只在受控构建机保存该基线，异常迁移由零值计数和时钟开销门禁拒绝。
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

// calibrateBenchmarkCPUFrequency 用一次 100ms 的 Windows 性能计数器区间校准硬件 Tick 频率。
// 慢 syscall 只在测试进程初始化时执行两次，不进入任何事件样本。
func calibrateBenchmarkCPUFrequency() int64 {
	performanceFrequency := benchmarkQueryPerformanceFrequency()
	if performanceFrequency <= 0 {
		panic("QueryPerformanceFrequency 返回无效频率")
	}
	performanceStarted := benchmarkQueryPerformanceCounter()
	ticksStarted := benchmarkCPUTicks()
	time.Sleep(100 * time.Millisecond)
	ticksElapsed := benchmarkCPUTicks() - ticksStarted
	performanceElapsed := benchmarkQueryPerformanceCounter() - performanceStarted
	if ticksElapsed <= 0 || performanceElapsed <= 0 ||
		ticksElapsed > (1<<63-1)/performanceFrequency {
		panic("CPU Tick 频率校准失败")
	}
	frequency := ticksElapsed * performanceFrequency / performanceElapsed
	if frequency <= 0 {
		panic("CPU Tick 频率校准结果无效")
	}
	return frequency
}
