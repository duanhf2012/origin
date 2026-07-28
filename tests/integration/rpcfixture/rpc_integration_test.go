package rpcfixture

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/duanhf2012/origin/v3/errs"
	"github.com/duanhf2012/origin/v3/internal/bufferpool"
	originlog "github.com/duanhf2012/origin/v3/log"
	"github.com/duanhf2012/origin/v3/node"
	"github.com/duanhf2012/origin/v3/rpc"
	"github.com/duanhf2012/origin/v3/service"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

// rpcFixture 持有一次性 Node 和两个真实 Service 实例。
type rpcFixture struct {
	node   *node.Node
	caller *CallerService
	player *PlayerService
	pool   *bufferpool.Pool
}

func newRPCFixture(t *testing.T) *rpcFixture {
	t.Helper()
	config := service.DefaultSchedulerConfig()
	config.DefaultAwaitTimeout = time.Second
	return newRPCFixtureWithConfig(t, config)
}

func newRPCFixtureWithConfig(
	t *testing.T,
	config service.SchedulerConfig,
) *rpcFixture {
	t.Helper()
	return newRPCFixtureWithID(t, "game-1", config)
}

func newRPCFixtureWithID(
	t *testing.T,
	nodeID string,
	config service.SchedulerConfig,
) *rpcFixture {
	t.Helper()
	caller := &CallerService{}
	player := &PlayerService{}
	pool := bufferpool.NewPool(bufferpool.Options{TrackUsage: true})
	instance, err := node.New(
		node.Config{
			ID:        nodeID,
			Scheduler: config,
		},
		[]node.ServiceBinding{
			{
				Name:     "CallerService",
				Template: "CallerService",
				Service:  caller,
			},
			{
				Name:     "PlayerService",
				Template: "PlayerService",
				Service:  player,
			},
		},
		originlog.NewNop(),
		node.Options{
			MaxTimersPerNode: 1024,
			TimerLocation:    time.Local,
			BufferPool:       pool,
		},
	)
	if err != nil {
		t.Fatalf("node.New() error = %v", err)
	}
	if err := instance.Start(context.Background()); err != nil {
		t.Fatalf("Node.Start() error = %v", err)
	}
	fixture := &rpcFixture{
		node: instance, caller: caller, player: player, pool: pool,
	}
	t.Cleanup(func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := instance.Stop(stopCtx); err != nil {
			t.Errorf("Node.Stop() error = %v", err)
		}
		if stats := pool.Stats(); stats.InUseBuffers != 0 {
			t.Errorf("RPC Buffer 未全部归还: %+v", stats)
		}
	})
	return fixture
}

func TestNodeRPCRuntimeRegistriesAreIsolated(t *testing.T) {
	config := service.DefaultSchedulerConfig()
	first := newRPCFixtureWithID(t, "game-1", config)
	second := newRPCFixtureWithID(t, "game-2", config)

	firstDone := make(chan struct{})
	if err := first.caller.DispatchAsync(func(ctx context.Context) {
		defer close(firstDone)
		client := NewPlayerRPCClient(
			first.caller,
			rpc.ToService("PlayerService"),
		)
		if _, _, err := client.AwaitGetPlayer(
			ctx,
			1,
			PlayerData{Name: "first"},
			nil,
		); err != nil {
			t.Errorf("first AwaitGetPlayer() error = %v", err)
		}
	}); err != nil {
		t.Fatal(err)
	}
	secondDone := make(chan struct{})
	if err := second.caller.DispatchAsync(func(ctx context.Context) {
		defer close(secondDone)
		client := NewPlayerRPCClient(
			second.caller,
			rpc.ToService("PlayerService"),
		)
		if _, _, err := client.AwaitGetPlayer(
			ctx,
			2,
			PlayerData{Name: "second"},
			nil,
		); err != nil {
			t.Errorf("second AwaitGetPlayer() error = %v", err)
		}
	}); err != nil {
		t.Fatal(err)
	}
	awaitSignal(t, firstDone)
	awaitSignal(t, secondDone)
	if first.player.GetCount != 1 || second.player.GetCount != 1 {
		t.Fatalf(
			"Node RPC Runtime 串扰: first=%d second=%d",
			first.player.GetCount,
			second.player.GetCount,
		)
	}
}

func TestGeneratedTargetQueueFullIsImmediate(t *testing.T) {
	config := service.SchedulerConfig{
		MaxTasks:            2,
		MaxAwaitTasks:       2,
		DefaultAwaitTimeout: time.Second,
	}
	fixture := newRPCFixtureWithConfig(t, config)
	block := make(chan struct{})
	started := make(chan struct{})
	if err := fixture.player.DispatchAsync(func(context.Context) {
		close(started)
		<-block
	}); err != nil {
		t.Fatal(err)
	}
	awaitSignal(t, started)
	// 第二个目标任务占满 MaxTasks，但仍排在阻塞任务之后。
	if err := fixture.player.DispatchAsync(func(context.Context) {}); err != nil {
		t.Fatal(err)
	}

	callerDone := make(chan struct{})
	if err := fixture.caller.DispatchAsync(func(ctx context.Context) {
		defer close(callerDone)
		client := NewPlayerRPCClient(
			fixture.caller,
			rpc.ToService("PlayerService"),
		)
		err := client.NotifyPlayerOnline(ctx, 1)
		if !errors.Is(err, errs.ErrServiceQueueFull) {
			t.Errorf("queue-full Notify error = %v", err)
		}
	}); err != nil {
		t.Fatal(err)
	}
	awaitSignal(t, callerDone)
	close(block)
}

func TestGeneratedAwaitRoundTripAndOwnership(t *testing.T) {
	fixture := newRPCFixture(t)
	done := make(chan struct{})
	score := int32(99)
	seed := PlayerData{
		Name:  "before",
		Score: &score,
		Tags:  []string{"a", "b"},
		Metadata: map[string]*wrapperspb.StringValue{
			"region": {Value: "cn-east"},
		},
		Payload: []byte{1, 2, 3},
	}
	options, err := structpb.NewStruct(map[string]any{"mode": "ranked"})
	if err != nil {
		t.Fatal(err)
	}
	savePayload := []byte{9, 8, 7}

	if err := fixture.caller.DispatchAsync(func(ctx context.Context) {
		defer close(done)
		client := NewPlayerRPCClient(
			fixture.caller,
			rpc.ToService("PlayerService"),
		)
		result, echoed, callErr := client.AwaitGetPlayer(
			ctx,
			1001,
			seed,
			options,
		)
		if callErr != nil {
			t.Errorf("AwaitGetPlayer() error = %v", callErr)
			return
		}
		if result.ID != 1001 || result.Name != seed.Name ||
			result.Score == seed.Score ||
			result.Metadata["region"] == seed.Metadata["region"] ||
			&result.Payload[0] == &seed.Payload[0] {
			t.Errorf("普通结构体往返或独立所有权不正确: %+v", result)
		}
		if !proto.Equal(echoed, options) || echoed == options {
			t.Errorf("顶层 Protobuf 往返或所有权不正确: %v", echoed)
		}
		echoValue, echoErr := client.AwaitEchoName(
			ctx,
			"value",
		)
		if echoErr != nil || echoValue != "value-echo" {
			t.Errorf("AwaitEchoName() value=%q error=%v", echoValue, echoErr)
		}
		if saveErr := client.AwaitSavePlayer(
			ctx,
			PlayerData{ID: 5, Name: "await-save", Payload: savePayload},
		); saveErr != nil {
			t.Errorf("AwaitSavePlayer() error = %v", saveErr)
		}
	}); err != nil {
		t.Fatalf("Caller.DispatchAsync() error = %v", err)
	}
	awaitSignal(t, done)
	// 业务保存的是解码后的独立 []byte；修改调用方原 Slice 不能污染目标 Service 状态。
	savePayload[0] = 0
	if fixture.player.LastSaved.Payload[0] != 9 ||
		&fixture.player.LastSaved.Payload[0] == &savePayload[0] {
		t.Fatalf("目标 Service 保存了借用的请求 Slice: %+v", fixture.player.LastSaved)
	}
}

func TestGeneratedAsyncNotifyBroadcastAndExactTarget(t *testing.T) {
	fixture := newRPCFixture(t)
	callbackDone := make(chan struct{})
	submitDone := make(chan struct{})
	player := PlayerData{Name: "async", Tags: []string{}}

	if err := fixture.caller.DispatchAsync(func(ctx context.Context) {
		client := NewPlayerRPCClient(
			fixture.caller,
			rpc.ToServiceOnNode("game-1", "PlayerService"),
		)
		err := client.AsyncGetPlayer(
			ctx,
			7,
			player,
			nil,
			func(_ context.Context, result PlayerData, _ *structpb.Struct, err error) {
				defer close(callbackDone)
				if err != nil || result.ID != 7 {
					t.Errorf("AsyncGetPlayer() result=%+v error=%v", result, err)
				}
			},
		)
		if err != nil {
			t.Errorf("AsyncGetPlayer() immediate error = %v", err)
		}
		select {
		case <-callbackDone:
			t.Error("Async 回调抢占了当前 Service 根任务")
		default:
		}
		close(submitDone)
	}); err != nil {
		t.Fatalf("Caller.DispatchAsync() error = %v", err)
	}
	awaitSignal(t, submitDone)
	awaitSignal(t, callbackDone)

	asyncSaveDone := make(chan struct{})
	if err := fixture.caller.DispatchAsync(func(ctx context.Context) {
		client := NewPlayerRPCClient(
			fixture.caller,
			rpc.ToService("PlayerService"),
		)
		if err := client.AsyncSavePlayer(
			ctx,
			PlayerData{ID: 77, Name: "async-save"},
			func(_ context.Context, err error) {
				defer close(asyncSaveDone)
				if err != nil {
					t.Errorf("AsyncSavePlayer() callback error = %v", err)
				}
			},
		); err != nil {
			t.Errorf("AsyncSavePlayer() immediate error = %v", err)
			close(asyncSaveDone)
		}
	}); err != nil {
		t.Fatal(err)
	}
	awaitSignal(t, asyncSaveDone)

	// Notify 和 Broadcast 只等待本地队列接受；随后投递屏障任务观察目标 FIFO 结果。
	notifyDone := make(chan struct{})
	if err := fixture.caller.DispatchAsync(func(ctx context.Context) {
		client := NewPlayerRPCClient(
			fixture.caller,
			rpc.ToService("PlayerService"),
		)
		if err := client.NotifyPlayerOnline(ctx, 88); err != nil {
			t.Errorf("NotifyPlayerOnline() error = %v", err)
		}
		if err := client.BroadcastSavePlayer(
			ctx,
			PlayerData{ID: 99, Name: "broadcast"},
		); err != nil {
			t.Errorf("BroadcastSavePlayer() error = %v", err)
		}
		if err := client.NotifyGetPlayer(
			ctx,
			66,
			PlayerData{Name: "discard-result"},
			nil,
		); err != nil {
			t.Errorf("NotifyGetPlayer() error = %v", err)
		}
		if err := client.BroadcastPlayerOnline(ctx, 89); err != nil {
			t.Errorf("BroadcastPlayerOnline() error = %v", err)
		}
		close(notifyDone)
	}); err != nil {
		t.Fatal(err)
	}
	awaitSignal(t, notifyDone)
	barrier := make(chan struct{})
	if err := fixture.player.DispatchAsync(func(context.Context) {
		close(barrier)
	}); err != nil {
		t.Fatal(err)
	}
	awaitSignal(t, barrier)
	if fixture.player.OnlineID != 89 ||
		fixture.player.LastSaved.Name != "broadcast" {
		t.Fatalf(
			"Notify/Broadcast FIFO 结果错误: online=%d saved=%+v",
			fixture.player.OnlineID,
			fixture.player.LastSaved,
		)
	}
}

// TestGeneratedAsyncRejectsNilCallback 验证生成外观在编码和任务准入前拒绝 nil callback。
func TestGeneratedAsyncRejectsNilCallback(t *testing.T) {
	fixture := newRPCFixture(t)
	done := make(chan struct{})
	if err := fixture.caller.DispatchAsync(func(ctx context.Context) {
		defer close(done)
		client := NewPlayerRPCClient(
			fixture.caller,
			rpc.ToService("PlayerService"),
		)
		if err := client.AsyncGetPlayer(
			ctx,
			1,
			PlayerData{Name: "nil-callback"},
			nil,
			nil,
		); !errors.Is(err, errs.ErrInvalidArgument) {
			t.Errorf("nil callback error = %v", err)
		}
	}); err != nil {
		t.Fatal(err)
	}
	awaitSignal(t, done)
	if fixture.player.GetCount != 0 {
		t.Fatalf("nil callback 仍调用了目标 Service: %d", fixture.player.GetCount)
	}
}

func TestGeneratedErrorsPanicAndSelfCall(t *testing.T) {
	fixture := newRPCFixture(t)
	done := make(chan struct{})
	if err := fixture.caller.DispatchAsync(func(ctx context.Context) {
		defer close(done)
		wrongNode := NewPlayerRPCClient(
			fixture.caller,
			rpc.ToServiceOnNode("game-2", "PlayerService"),
		)
		_, _, err := wrongNode.AwaitGetPlayer(
			ctx,
			1,
			PlayerData{Name: "x"},
			nil,
		)
		if !errors.Is(err, errs.ErrRPCNoRoute) {
			t.Errorf("wrong-node error = %v", err)
		}
		mismatch := NewPlayerRPCClient(
			fixture.caller,
			rpc.ToService("CallerService"),
		)
		_, _, err = mismatch.AwaitGetPlayer(
			ctx,
			1,
			PlayerData{Name: "x"},
			nil,
		)
		if !errors.Is(err, errs.ErrRPCContractMismatch) {
			t.Errorf("contract-mismatch error = %v", err)
		}

		fixture.player.ShouldFail = true
		client := NewPlayerRPCClient(
			fixture.caller,
			rpc.ToService("PlayerService"),
		)
		_, _, err = client.AwaitGetPlayer(
			ctx,
			2,
			PlayerData{Name: "x"},
			nil,
		)
		if !errors.Is(err, errs.ErrInvalidArgument) {
			t.Errorf("business error = %v", err)
		}
		fixture.player.ShouldFail = false
		fixture.player.ShouldPanic = true
		_, _, err = client.AwaitGetPlayer(
			ctx,
			3,
			PlayerData{Name: "x"},
			nil,
		)
		if !errors.Is(err, errs.ErrRPCExecutionPanic) {
			t.Errorf("panic error = %v", err)
		}
		fixture.player.ShouldPanic = false
	}); err != nil {
		t.Fatal(err)
	}
	awaitSignal(t, done)

	// 目标 Service 调用自身 RPC 时，Await 必须释放执行权，让同一 FIFO 处理请求后恢复。
	selfDone := make(chan struct{})
	if err := fixture.player.DispatchAsync(func(ctx context.Context) {
		defer close(selfDone)
		client := NewPlayerRPCClient(
			fixture.player,
			rpc.ToService("PlayerService"),
		)
		result, _, err := client.AwaitGetPlayer(
			ctx,
			42,
			PlayerData{Name: "self"},
			nil,
		)
		if err != nil || result.ID != 42 {
			t.Errorf("self AwaitGetPlayer() result=%+v error=%v", result, err)
		}
	}); err != nil {
		t.Fatal(err)
	}
	awaitSignal(t, selfDone)
}

func TestGeneratedTimeoutAndAsyncImmediateFailure(t *testing.T) {
	fixture := newRPCFixture(t)
	block := make(chan struct{})
	fixture.player.Wait = block

	awaitDone := make(chan struct{})
	if err := fixture.caller.DispatchAsync(func(ctx context.Context) {
		defer close(awaitDone)
		explicit, cancel := context.WithTimeout(ctx, 30*time.Millisecond)
		defer cancel()
		client := NewPlayerRPCClient(
			fixture.caller,
			rpc.ToService("PlayerService"),
		)
		_, _, err := client.AwaitGetPlayer(
			explicit,
			1,
			PlayerData{Name: "timeout"},
			nil,
		)
		if !errors.Is(err, errs.ErrDeadlineExceeded) {
			t.Errorf("Await timeout error = %v", err)
		}
	}); err != nil {
		t.Fatal(err)
	}
	awaitSignal(t, awaitDone)
	close(block)
	// Await 超时允许目标响应稍后到达；目标 FIFO 屏障确保晚到完成和 Buffer 释放已发生。
	targetBarrier := make(chan struct{})
	if err := fixture.player.DispatchAsync(func(context.Context) {
		close(targetBarrier)
	}); err != nil {
		t.Fatal(err)
	}
	awaitSignal(t, targetBarrier)

	// 路由立即失败前已经预留的内部回调任务必须自行消失，不能调用业务 callback。
	callbackCalled := make(chan struct{}, 1)
	immediateDone := make(chan struct{})
	if err := fixture.caller.DispatchAsync(func(ctx context.Context) {
		defer close(immediateDone)
		client := NewPlayerRPCClient(
			fixture.caller,
			rpc.ToServiceOnNode("missing-node", "PlayerService"),
		)
		err := client.AsyncGetPlayer(
			ctx,
			2,
			PlayerData{Name: "no-route"},
			nil,
			func(
				context.Context,
				PlayerData,
				*structpb.Struct,
				error,
			) {
				callbackCalled <- struct{}{}
			},
		)
		if !errors.Is(err, errs.ErrRPCNoRoute) {
			t.Errorf("Async immediate error = %v", err)
		}
	}); err != nil {
		t.Fatal(err)
	}
	awaitSignal(t, immediateDone)

	// 同一调用方 FIFO 屏障位于内部抑制任务之后；屏障执行后 callback 仍必须为空。
	barrier := make(chan struct{})
	if err := fixture.caller.DispatchAsync(func(context.Context) {
		close(barrier)
	}); err != nil {
		t.Fatal(err)
	}
	awaitSignal(t, barrier)
	select {
	case <-callbackCalled:
		t.Fatal("Async 立即失败后仍执行了 callback")
	default:
	}
}

func TestGeneratedAsyncTimeoutCallbackExactlyOnce(t *testing.T) {
	fixture := newRPCFixture(t)
	block := make(chan struct{})
	fixture.player.Wait = block
	callbackDone := make(chan struct{})
	var callbackCount atomic.Int32

	submitted := make(chan struct{})
	if err := fixture.caller.DispatchAsync(func(ctx context.Context) {
		defer close(submitted)
		explicit, cancel := context.WithTimeout(ctx, 30*time.Millisecond)
		// Async 返回后仍由 Context 自己管理 Deadline；回调完成前不能提前 cancel。
		client := NewPlayerRPCClient(
			fixture.caller,
			rpc.ToService("PlayerService"),
		)
		err := client.AsyncGetPlayer(
			explicit,
			3,
			PlayerData{Name: "async-timeout"},
			nil,
			func(
				_ context.Context,
				_ PlayerData,
				_ *structpb.Struct,
				err error,
			) {
				defer cancel()
				if callbackCount.Add(1) == 1 {
					close(callbackDone)
				}
				if !errors.Is(err, errs.ErrDeadlineExceeded) {
					t.Errorf("Async timeout callback error = %v", err)
				}
			},
		)
		if err != nil {
			cancel()
			t.Errorf("Async timeout immediate error = %v", err)
		}
	}); err != nil {
		t.Fatal(err)
	}
	awaitSignal(t, submitted)
	awaitSignal(t, callbackDone)

	// 释放已经观察到取消的目标任务，并用目标 FIFO 屏障等待晚到完成清理。
	close(block)
	targetBarrier := make(chan struct{})
	if err := fixture.player.DispatchAsync(func(context.Context) {
		close(targetBarrier)
	}); err != nil {
		t.Fatal(err)
	}
	awaitSignal(t, targetBarrier)
	if count := callbackCount.Load(); count != 1 {
		t.Fatalf("Async timeout callback count = %d", count)
	}
}

// TestGeneratedAsyncCancelBeforeCompletionTask 验证 Async 已被目标接受、但调用方完成任务尚未
// 开始时取消 Context 的所有权边界。目标的快速响应无论先于还是晚于放弃发生都必须归还。
func TestGeneratedAsyncCancelBeforeCompletionTask(t *testing.T) {
	fixture := newRPCFixture(t)
	submitted := make(chan struct{})
	callbackDone := make(chan struct{})
	var callbackCount atomic.Int32

	// 当前调用方根任务在 Async 返回后立即取消；内部完成任务排在当前任务之后，所以它
	// 进入 Service.Await 时会稳定观察到预取消 Context，等待函数不会被调用。
	if err := fixture.caller.DispatchAsync(func(ctx context.Context) {
		explicit, cancel := context.WithCancel(ctx)
		client := NewPlayerRPCClient(
			fixture.caller,
			rpc.ToService("PlayerService"),
		)
		err := client.AsyncEchoName(
			explicit,
			"cancel-before-completion",
			func(_ context.Context, _ string, err error) {
				if !errors.Is(err, errs.ErrCanceled) {
					t.Errorf("Async pre-completion cancel error = %v", err)
				}
				if callbackCount.Add(1) == 1 {
					close(callbackDone)
				}
			},
		)
		if err != nil {
			t.Errorf("AsyncEchoName() immediate error = %v", err)
		}
		cancel()
		close(submitted)
	}); err != nil {
		t.Fatal(err)
	}
	awaitSignal(t, submitted)
	awaitSignal(t, callbackDone)

	// 目标 FIFO 屏障保证快速响应已经执行 complete；调用方屏障保证完成任务已经结束。
	// 两侧都完成后 Pool 中不能残留请求或响应 Buffer。
	targetBarrier := make(chan struct{})
	if err := fixture.player.DispatchAsync(func(context.Context) {
		close(targetBarrier)
	}); err != nil {
		t.Fatal(err)
	}
	callerBarrier := make(chan struct{})
	if err := fixture.caller.DispatchAsync(func(context.Context) {
		close(callerBarrier)
	}); err != nil {
		t.Fatal(err)
	}
	awaitSignal(t, targetBarrier)
	awaitSignal(t, callerBarrier)
	if count := callbackCount.Load(); count != 1 {
		t.Fatalf("Async pre-completion cancel callback count = %d", count)
	}
	if stats := fixture.pool.Stats(); stats.InUseBuffers != 0 {
		t.Fatalf("Async pre-completion cancel Buffer 未归还: %+v", stats)
	}
}

func awaitSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(5 * time.Second):
		t.Fatal("等待集成测试信号超时")
	}
}
