//go:build !windows

package service

import "time"

// benchmarkLatencyTimestamp 在非 Windows 平台保留 time.Time 的单调时钟部分。
type benchmarkLatencyTimestamp struct {
	value time.Time
}

// benchmarkLatencyNow 读取 Go Runtime 提供的单调时钟。
func benchmarkLatencyNow() benchmarkLatencyTimestamp {
	return benchmarkLatencyTimestamp{value: time.Now()}
}

// benchmarkLatencyElapsed 返回一次独立公开调用的纳秒耗时。
func benchmarkLatencyElapsed(started benchmarkLatencyTimestamp) int64 {
	return time.Since(started.value).Nanoseconds()
}

// benchmarkLatencyFrequency 返回纳秒时钟的固定频率。
func benchmarkLatencyFrequency() int64 {
	return int64(time.Second)
}
