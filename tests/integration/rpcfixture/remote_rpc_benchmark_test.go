package rpcfixture

import (
	"context"
	"fmt"
	"testing"

	"github.com/duanhf2012/origin/v3/rpc"
	"github.com/duanhf2012/origin/v3/service"
)

// BenchmarkGeneratedRemoteLoopback 保存真实 TCP、ORP1、Service Await 和自定义 Codec 的
// 端到端基线。结果包含业务独立返回 Slice 的必要复制，不能解释为纯网络层开销。
func BenchmarkGeneratedRemoteLoopback(b *testing.B) {
	fixture := newRemoteRPCFixture(b)
	_ = awaitRemoteEcho(b, fixture, "benchmark-ready")

	for _, payloadSize := range []int{
		32,
		1024,
		64 * 1024,
		rpc.DefaultMaxPayloadSize - 128,
	} {
		b.Run(fmt.Sprintf("%dB", payloadSize), func(b *testing.B) {
			payload := make(OwnedBlob, payloadSize)
			result := make(chan error, 1)
			b.ReportAllocs()
			b.SetBytes(int64(payloadSize))
			b.ResetTimer()
			if err := fixture.caller.DispatchAsync(func(ctx context.Context) {
				client := NewPlayerRPCClient(
					fixture.caller,
					rpc.ToServiceOnNode("player-1", "PlayerService"),
				)
				for range b.N {
					value, err := client.AwaitRoundTripBlob(ctx, payload)
					if err != nil {
						result <- err
						return
					}
					if len(value) != payloadSize {
						result <- fmt.Errorf(
							"远端 payload 长度 = %d，期望 %d",
							len(value),
							payloadSize,
						)
						return
					}
				}
				result <- nil
			}); err != nil {
				b.Fatal(err)
			}
			if err := <-result; err != nil {
				b.Fatal(err)
			}
		})
	}
}

// BenchmarkGeneratedNATSLoopback 保存真实 Core NATS、Service Await 和自定义 Codec 的端到端
// 基线。与 TCP 使用完全相同的 payload 档位，便于持续比较两种 Transport 的延迟与分配。
func BenchmarkGeneratedNATSLoopback(b *testing.B) {
	fixture := newNATSRPCPair(b, service.DefaultSchedulerConfig())
	_ = awaitNATSEcho(b, fixture.caller, "benchmark-ready")

	for _, payloadSize := range []int{
		32,
		1024,
		64 * 1024,
		rpc.DefaultMaxPayloadSize - 128,
	} {
		b.Run(fmt.Sprintf("%dB", payloadSize), func(b *testing.B) {
			payload := make(OwnedBlob, payloadSize)
			result := make(chan error, 1)
			b.ReportAllocs()
			b.SetBytes(int64(payloadSize))
			b.ResetTimer()
			if err := fixture.caller.DispatchAsync(func(ctx context.Context) {
				client := NewPlayerRPCClient(
					fixture.caller,
					rpc.ToServiceOnNode("player-1", "PlayerService"),
				)
				for range b.N {
					value, err := client.AwaitRoundTripBlob(ctx, payload)
					if err != nil {
						result <- err
						return
					}
					if len(value) != payloadSize {
						result <- fmt.Errorf(
							"NATS payload 长度 = %d，期望 %d",
							len(value),
							payloadSize,
						)
						return
					}
				}
				result <- nil
			}); err != nil {
				b.Fatal(err)
			}
			if err := <-result; err != nil {
				b.Fatal(err)
			}
		})
	}
}
