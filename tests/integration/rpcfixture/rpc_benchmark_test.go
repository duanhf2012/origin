package rpcfixture

import (
	"context"
	"testing"
	"time"

	"github.com/duanhf2012/origin/v3/internal/bufferpool"
	originlog "github.com/duanhf2012/origin/v3/log"
	"github.com/duanhf2012/origin/v3/node"
	"github.com/duanhf2012/origin/v3/rpc"
	"github.com/duanhf2012/origin/v3/service"
)

var benchmarkPlayerRPCClientSink PlayerRPCClient

func BenchmarkBindGeneratedClient(b *testing.B) {
	instance, caller := newBenchmarkNode(b)
	defer stopBenchmarkNode(b, instance)

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		benchmarkPlayerRPCClientSink = BindPlayerRPC(caller)
	}
}

// BenchmarkFixedCustomCodecVsInt64 对比同为八字节业务值的内置整数和自定义 time.Time
// 边界成本；样本、Pool 和 Codec 都在计时前构造。
func BenchmarkFixedCustomCodecVsInt64(b *testing.B) {
	pool := bufferpool.NewPool(bufferpool.Options{})
	b.Run("builtin-int64", func(b *testing.B) {
		b.ReportAllocs()
		b.SetBytes(8)
		for b.Loop() {
			// 与自定义路径一样先执行生成代码会使用的准确大小计算，避免把 Sizer 成本
			// 只计入其中一侧而夸大四字节自定义边界的差值。
			sizer := rpc.NewSizer()
			if err := sizer.Add(8); err != nil {
				b.Fatal(err)
			}
			total, err := sizer.Size()
			if err != nil {
				b.Fatal(err)
			}
			buffer := pool.Acquire(total)
			writer := rpc.NewWriter(buffer.Bytes())
			if err := writer.WriteInt64(123); err != nil {
				buffer.Release()
				b.Fatal(err)
			}
			if err := writer.Done(); err != nil {
				buffer.Release()
				b.Fatal(err)
			}
			reader := rpc.NewResponseReader(buffer.Bytes())
			if _, err := reader.ReadInt64(); err != nil {
				buffer.Release()
				b.Fatal(err)
			}
			if err := reader.Done(); err != nil {
				buffer.Release()
				b.Fatal(err)
			}
			buffer.Release()
		}
	})

	codec := TimeCodec{}
	value := time.Date(2026, 7, 28, 0, 0, 0, 0, time.UTC)
	b.Run("custom-time", func(b *testing.B) {
		b.ReportAllocs()
		b.SetBytes(timeCodecPayloadSize)
		for b.Loop() {
			customSize, err := codec.Size(&value)
			if err != nil {
				b.Fatal(err)
			}
			sizer := rpc.NewSizer()
			if err := sizer.AddCustom(customSize); err != nil {
				b.Fatal(err)
			}
			total, err := sizer.Size()
			if err != nil {
				b.Fatal(err)
			}
			buffer := pool.Acquire(total)
			writer := rpc.NewWriter(buffer.Bytes())
			target, err := writer.ReserveCustom(customSize)
			if err != nil {
				buffer.Release()
				b.Fatal(err)
			}
			written, err := codec.MarshalTo(target, &value)
			if err != nil || written != len(target) {
				buffer.Release()
				b.Fatalf("MarshalTo() = %d, %v", written, err)
			}
			if err := writer.Done(); err != nil {
				buffer.Release()
				b.Fatal(err)
			}
			reader := rpc.NewResponseReader(buffer.Bytes())
			payload, err := reader.ReadCustomPayload()
			if err != nil {
				buffer.Release()
				b.Fatal(err)
			}
			var decoded time.Time
			if err := codec.Unmarshal(payload, &decoded); err != nil {
				buffer.Release()
				b.Fatal(err)
			}
			if err := reader.Done(); err != nil {
				buffer.Release()
				b.Fatal(err)
			}
			buffer.Release()
		}
	})
}

func BenchmarkGeneratedLocalAwait(b *testing.B) {
	instance, caller := newBenchmarkNode(b)
	defer stopBenchmarkNode(b, instance)

	done := make(chan struct{})
	var benchmarkErr error
	b.ReportAllocs()
	b.ResetTimer()
	if err := caller.DispatchAsync(func(ctx context.Context) {
		defer close(done)
		client := NewPlayerRPCClient(caller, rpc.ToService("PlayerService"))
		seed := PlayerData{Name: "benchmark", Tags: []string{"a", "b"}}
		for index := 0; index < b.N; index++ {
			_, _, benchmarkErr = client.AwaitGetPlayer(ctx, 1001, seed, nil)
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
}

// BenchmarkGeneratedLocalCall 记录普通 goroutine 阻塞外观的同 Node 完整闭环；与 Await
// 基线分开，避免把 Service 执行槽释放和 FIFO 恢复成本错误归因给 RPC 传输内核。
func BenchmarkGeneratedLocalCall(b *testing.B) {
	instance, caller := newBenchmarkNode(b)
	defer stopBenchmarkNode(b, instance)

	client := NewPlayerRPCClient(caller, rpc.ToService("PlayerService"))
	seed := PlayerData{Name: "benchmark", Tags: []string{"a", "b"}}
	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		if _, _, err := client.CallGetPlayer(nil, 1001, seed, nil); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkGeneratedCustomTimeAwait 测量固定八字节 time.Time Codec 参与的完整同 Node
// Await，包含结构体字段生成、两阶段编码、目标调度和响应解码。
func BenchmarkGeneratedCustomTimeAwait(b *testing.B) {
	instance, caller := newBenchmarkNode(b)
	defer stopBenchmarkNode(b, instance)

	done := make(chan struct{})
	var benchmarkErr error
	value := TimeEnvelope{
		At: time.Date(2026, 7, 28, 0, 0, 0, 0, time.UTC),
	}
	b.ReportAllocs()
	b.ResetTimer()
	if err := caller.DispatchAsync(func(ctx context.Context) {
		defer close(done)
		client := NewPlayerRPCClient(caller, rpc.ToService("PlayerService"))
		for range b.N {
			_, benchmarkErr = client.AwaitRoundTripTime(ctx, value, 0)
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
}

// newBenchmarkNode 建立两个真实 Service 和生成 Dispatcher 参与的同 Node RPC 环境。
func newBenchmarkNode(b *testing.B) (*node.Node, *CallerService) {
	b.Helper()
	caller := &CallerService{}
	player := &PlayerService{}
	scheduler := service.DefaultSchedulerConfig()
	instance, err := node.New(
		node.Config{ID: "bench-1", Scheduler: scheduler},
		[]node.ServiceBinding{
			{Name: "CallerService", Template: "CallerService", Service: caller},
			{Name: "PlayerService", Template: "PlayerService", Service: player},
		},
		originlog.NewNop(),
		node.Options{
			MaxTimersPerNode: 1024,
			TimerLocation:    time.Local,
		},
	)
	if err != nil {
		b.Fatal(err)
	}
	if err := instance.Start(context.Background()); err != nil {
		b.Fatal(err)
	}
	return instance, caller
}

// stopBenchmarkNode 在计时区间外完整回收 Node 的 Runner 和 TimerEngine。
func stopBenchmarkNode(b *testing.B, instance *node.Node) {
	b.Helper()
	if err := instance.Stop(context.Background()); err != nil {
		b.Error(err)
	}
}
