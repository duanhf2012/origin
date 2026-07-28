//go:build !windows

package rpcfixture

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/duanhf2012/origin/v3/rpc"
)

// BenchmarkGeneratedLocalAwaitLatency 记录同 Node 完整 Await 闭环的 P50/P95/P99。
//
// 每次调用增加两次 time.Now，因此本 Benchmark 只用于观察尾延迟分布；平均热路径成本
// 继续以 BenchmarkGeneratedLocalAwait 为准。Windows 的 time.Time 单次读取精度不足以
// 稳定测量微秒级调用，Go testing 自身也为 Windows 单独使用 QueryPerformanceCounter，
// 因此该分位数 Benchmark 只在具有高精度 time.Now 的部署平台构建，避免输出虚假的零值。
func BenchmarkGeneratedLocalAwaitLatency(b *testing.B) {
	instance, caller := newBenchmarkNode(b)
	defer stopBenchmarkNode(b, instance)

	// 样本数组在计时前一次分配，避免统计过程中产生堆扩容噪声。
	samples := make([]time.Duration, b.N)
	done := make(chan struct{})
	var benchmarkErr error
	b.ResetTimer()
	if err := caller.DispatchAsync(func(ctx context.Context) {
		defer close(done)
		client := NewPlayerRPCClient(caller, rpc.ToService("PlayerService"))
		seed := PlayerData{Name: "latency", Tags: []string{"a", "b"}}
		for index := 0; index < b.N; index++ {
			startedAt := time.Now()
			_, _, benchmarkErr = client.AwaitGetPlayer(ctx, 1001, seed, nil)
			samples[index] = time.Since(startedAt)
			if benchmarkErr != nil {
				return
			}
		}
	}); err != nil {
		b.Fatal(err)
	}
	<-done
	b.StopTimer()
	if benchmarkErr != nil {
		b.Fatal(benchmarkErr)
	}

	// 停止计时后排序并上报纳秒分位数，不把统计本身算入 RPC 平均延迟。
	slices.Sort(samples)
	b.ReportMetric(float64(percentile(samples, 50).Nanoseconds()), "ns/p50")
	b.ReportMetric(float64(percentile(samples, 95).Nanoseconds()), "ns/p95")
	b.ReportMetric(float64(percentile(samples, 99).Nanoseconds()), "ns/p99")
}

// BenchmarkGeneratedCustomTimeAwaitLatency 记录自定义 time.Time Codec 完整闭环的尾延迟。
func BenchmarkGeneratedCustomTimeAwaitLatency(b *testing.B) {
	instance, caller := newBenchmarkNode(b)
	defer stopBenchmarkNode(b, instance)

	samples := make([]time.Duration, b.N)
	value := TimeEnvelope{
		At: time.Date(2026, 7, 28, 0, 0, 0, 0, time.UTC),
	}
	done := make(chan struct{})
	var benchmarkErr error
	b.ResetTimer()
	if err := caller.DispatchAsync(func(ctx context.Context) {
		defer close(done)
		client := NewPlayerRPCClient(caller, rpc.ToService("PlayerService"))
		for index := range b.N {
			startedAt := time.Now()
			_, benchmarkErr = client.AwaitRoundTripTime(ctx, value, 0)
			samples[index] = time.Since(startedAt)
			if benchmarkErr != nil {
				return
			}
		}
	}); err != nil {
		b.Fatal(err)
	}
	<-done
	b.StopTimer()
	if benchmarkErr != nil {
		b.Fatal(benchmarkErr)
	}

	slices.Sort(samples)
	b.ReportMetric(float64(percentile(samples, 50).Nanoseconds()), "ns/p50")
	b.ReportMetric(float64(percentile(samples, 95).Nanoseconds()), "ns/p95")
	b.ReportMetric(float64(percentile(samples, 99).Nanoseconds()), "ns/p99")
}

// BenchmarkGeneratedRemoteAwaitLatency 记录真实 loopback TCP、ORP1 和 Service 恢复的
// P50/P95/P99。它只在非 Windows 平台构建，与本文件既有高精度计时规则一致。
func BenchmarkGeneratedRemoteAwaitLatency(b *testing.B) {
	fixture := newRemoteRPCFixture(b)
	_ = awaitRemoteEcho(b, fixture, "latency-ready")

	samples := make([]time.Duration, b.N)
	done := make(chan struct{})
	var benchmarkErr error
	b.ResetTimer()
	if err := fixture.caller.DispatchAsync(func(ctx context.Context) {
		defer close(done)
		client := NewPlayerRPCClient(
			fixture.caller,
			rpc.ToServiceOnNode("player-1", "PlayerService"),
		)
		for index := range b.N {
			startedAt := time.Now()
			_, benchmarkErr = client.AwaitEchoName(ctx, "12345678901234567890123456789012")
			samples[index] = time.Since(startedAt)
			if benchmarkErr != nil {
				return
			}
		}
	}); err != nil {
		b.Fatal(err)
	}
	<-done
	b.StopTimer()
	if benchmarkErr != nil {
		b.Fatal(benchmarkErr)
	}

	slices.Sort(samples)
	b.ReportMetric(float64(percentile(samples, 50).Nanoseconds()), "ns/p50")
	b.ReportMetric(float64(percentile(samples, 95).Nanoseconds()), "ns/p95")
	b.ReportMetric(float64(percentile(samples, 99).Nanoseconds()), "ns/p99")
}

// percentile 按最近秩规则返回已经升序排列的延迟样本。
func percentile(samples []time.Duration, percent int) time.Duration {
	if len(samples) == 0 {
		return 0
	}
	index := (len(samples)*percent + 99) / 100
	if index < 1 {
		index = 1
	}
	return samples[index-1]
}
