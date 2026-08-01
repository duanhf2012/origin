//go:build windows

package service

import "golang.org/x/sys/windows"

// benchmarkLatencyTimestamp 保存 Windows 精确系统时钟的 100ns Tick。
//
// time.Now 在部分 Windows 测试主机上可能退化到约 15.6ms 的可观察粒度，无法逐次测量
// 微秒级事件通知。GetSystemTimePreciseAsFileTime 只用于 Benchmark，不进入生产热路径。
type benchmarkLatencyTimestamp int64

// benchmarkLatencyNow 读取 Windows 8+ 提供的精确系统时间。
func benchmarkLatencyNow() benchmarkLatencyTimestamp {
	var current windows.Filetime
	windows.GetSystemTimePreciseAsFileTime(&current)
	return benchmarkLatencyTimestamp(current.Nanoseconds())
}

// benchmarkLatencyElapsed 把两次精确系统时间读取换算为纳秒；系统时间异常回拨按零处理，
// 并由 clock-zero-samples 指标显式暴露，不能伪造成负延迟。
func benchmarkLatencyElapsed(started benchmarkLatencyTimestamp) int64 {
	elapsed := int64(benchmarkLatencyNow() - started)
	if elapsed < 0 {
		return 0
	}
	return elapsed
}
