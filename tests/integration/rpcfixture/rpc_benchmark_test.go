package rpcfixture

import (
	"context"
	"testing"
	"time"

	originlog "github.com/duanhf2012/origin/v3/log"
	"github.com/duanhf2012/origin/v3/node"
	"github.com/duanhf2012/origin/v3/rpc"
	"github.com/duanhf2012/origin/v3/service"
)

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
