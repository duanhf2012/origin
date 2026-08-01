//go:build windows

package service

import "testing"

// TestSelectBenchmarkCPUFrequencyUsesMedian 固定校准对正向迁核离群值的处理；极端值不能
// 把逐次尾延迟的纳秒换算整体放大或缩小。
func TestSelectBenchmarkCPUFrequencyUsesMedian(t *testing.T) {
	samples := []int64{4_120_000_000, 4_119_000_000, 9_000_000_000, 4_121_000_000, 1}
	if got, want := selectBenchmarkCPUFrequency(samples), int64(4_120_000_000); got != want {
		t.Fatalf("selectBenchmarkCPUFrequency() = %d, want %d", got, want)
	}
}
