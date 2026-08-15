package rpcfixture

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/duanhf2012/origin/v3/errs"
	"github.com/duanhf2012/origin/v3/internal/bufferpool"
	internaldiscovery "github.com/duanhf2012/origin/v3/internal/discovery"
	originlog "github.com/duanhf2012/origin/v3/log"
	"github.com/duanhf2012/origin/v3/node"
	"github.com/duanhf2012/origin/v3/rpc"
	"github.com/duanhf2012/origin/v3/service"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

// remoteRPCFixture 持有通过真实 TCP 相连的两个 Node。
type remoteRPCFixture struct {
	callerNode   *node.Node
	caller       *CallerService
	targetNode   *node.Node
	player       *PlayerService
	pool         *bufferpool.Pool
	callerConfig rpc.Config
	targetConfig rpc.Config
	discovery    *internaldiscovery.Source
}

// startAwaitCallerService 验证 OnStart 可以在普通 Runner 尚未激活时等待发现并顺序调用 RPC。
type startAwaitCallerService struct {
	CallerService
	targetNodeID string
	result       string
}

// OnStart 使用生命周期私有 Context 复用正式 Await 外观，不创建临时业务 Runner。
func (target *startAwaitCallerService) OnStart(ctx context.Context) error {
	if err := target.AwaitNodeService(
		ctx,
		target.targetNodeID,
		"PlayerService",
	); err != nil {
		return err
	}
	client := NewPlayerRPCClient(
		target,
		rpc.ToServiceOnNode(target.targetNodeID, "PlayerService"),
	)
	for {
		result, err := client.AwaitEchoName(ctx, "on-start")
		if err == nil {
			target.result = result
			return nil
		}
		if !errors.Is(err, errs.ErrTransportUnavailable) {
			return err
		}

		// 发现事实先于 TCP 连接就绪发布；幂等启动查询在生命周期 Context 内有界退避。
		if err := target.Await(ctx, func(waitCtx context.Context) error {
			timer := time.NewTimer(10 * time.Millisecond)
			defer timer.Stop()
			select {
			case <-timer.C:
				return nil
			case <-waitCtx.Done():
				return waitCtx.Err()
			}
		}); err != nil {
			return err
		}
	}
}

// newRemoteRPCFixture 先启动服务端 Node，再启动调用端 Node。
func newRemoteRPCFixture(t testing.TB) *remoteRPCFixture {
	t.Helper()
	pool := bufferpool.NewPool(bufferpool.Options{TrackUsage: true})
	targetConfig := testRPCConfig(t)
	callerConfig := testRPCConfig(t)
	player := &PlayerService{}
	caller := &CallerService{}
	discoverySource := internaldiscovery.NewSource()
	targetNode := newRemoteFixtureNode(
		t,
		"player-1",
		targetConfig,
		pool,
		discoverySource,
		node.ServiceBinding{
			Name:     "PlayerService",
			Template: "PlayerService",
			Service:  player,
		},
	)
	callerNode := newRemoteFixtureNode(
		t,
		"gateway-1",
		callerConfig,
		pool,
		discoverySource,
		node.ServiceBinding{
			Name:     "CallerService",
			Template: "CallerService",
			Service:  caller,
		},
	)
	if err := targetNode.Start(context.Background()); err != nil {
		t.Fatalf("target Node.Start() error = %v", err)
	}
	if err := callerNode.Start(context.Background()); err != nil {
		stopTestNode(t, targetNode)
		t.Fatalf("caller Node.Start() error = %v", err)
	}
	fixture := &remoteRPCFixture{
		callerNode:   callerNode,
		caller:       caller,
		targetNode:   targetNode,
		player:       player,
		pool:         pool,
		callerConfig: callerConfig,
		targetConfig: targetConfig,
		discovery:    discoverySource,
	}
	t.Cleanup(func() {
		stopTestNode(t, fixture.callerNode)
		stopTestNode(t, fixture.targetNode)
		if stats := pool.Stats(); stats.InUseBuffers != 0 {
			t.Errorf("远端 RPC Buffer 未全部归还: %+v", stats)
		}
	})
	return fixture
}

// newRemoteFixtureNode 创建只包含一个测试 Service 的 TCP Node。
func newRemoteFixtureNode(
	t testing.TB,
	nodeID string,
	rpcConfig rpc.Config,
	pool *bufferpool.Pool,
	discoverySource *internaldiscovery.Source,
	binding node.ServiceBinding,
) *node.Node {
	t.Helper()
	return newRemoteFixtureNodeWithScheduler(
		t,
		nodeID,
		rpcConfig,
		service.DefaultSchedulerConfig(),
		pool,
		discoverySource,
		binding,
	)
}

// newRemoteFixtureNodeWithScheduler 允许超时测试覆盖 Node 级默认 Await 配置。
func newRemoteFixtureNodeWithScheduler(
	t testing.TB,
	nodeID string,
	rpcConfig rpc.Config,
	scheduler service.SchedulerConfig,
	pool *bufferpool.Pool,
	discoverySource *internaldiscovery.Source,
	binding node.ServiceBinding,
) *node.Node {
	return newRemoteFixtureNodeConfigured(
		t,
		nodeID,
		rpcConfig,
		scheduler,
		nil,
		pool,
		discoverySource,
		binding,
	)
}

// newRemoteFixtureNodeWithLabels 为真实发现路由测试冻结当前 Node 的业务 Labels。
func newRemoteFixtureNodeWithLabels(
	t testing.TB,
	nodeID string,
	rpcConfig rpc.Config,
	labels map[string]string,
	pool *bufferpool.Pool,
	discoverySource *internaldiscovery.Source,
	binding node.ServiceBinding,
) *node.Node {
	t.Helper()
	return newRemoteFixtureNodeConfigured(
		t,
		nodeID,
		rpcConfig,
		service.DefaultSchedulerConfig(),
		labels,
		pool,
		discoverySource,
		binding,
	)
}

// newRemoteFixtureNodeConfigured 集中装配远端 RPC 测试共享的 Node 配置和所有权选项。
func newRemoteFixtureNodeConfigured(
	t testing.TB,
	nodeID string,
	rpcConfig rpc.Config,
	scheduler service.SchedulerConfig,
	labels map[string]string,
	pool *bufferpool.Pool,
	discoverySource *internaldiscovery.Source,
	binding node.ServiceBinding,
) *node.Node {
	t.Helper()
	instance, err := node.New(
		node.Config{
			ID:        nodeID,
			Scheduler: scheduler,
			RPC:       &rpcConfig,
			Labels:    labels,
		},
		[]node.ServiceBinding{binding},
		originlog.NewNop(),
		node.Options{
			MaxTimersPerNode: 1024,
			TimerLocation:    time.Local,
			BufferPool:       pool,
			DiscoverySource:  discoverySource,
		},
	)
	if err != nil {
		t.Fatalf("node.New(%q) error = %v", nodeID, err)
	}
	return instance
}

// testRPCConfig 预留一个真实回环地址，并使用较短测试超时。
func testRPCConfig(t testing.TB) rpc.Config {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve TCP address: %v", err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatalf("release TCP address: %v", err)
	}
	config := rpc.DefaultConfig()
	config.TCP.Listen = address
	config.TCP.Advertise = address
	config.TCP.ReadIdleTimeout = time.Second
	config.TCP.WriteTimeout = time.Second
	return config
}

// stopTestNode 使用有界 Context 幂等停止测试 Node。
func stopTestNode(t testing.TB, instance *node.Node) {
	t.Helper()
	if instance == nil ||
		instance.State() == node.StateStopped ||
		instance.State() == node.StateCreated {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := instance.Stop(ctx); err != nil {
		t.Errorf("Node %q Stop() error = %v", instance.ID(), err)
	}
}

// awaitRemoteEcho 在连接重试冷路径中等待一次真实 Await 成功。
func awaitRemoteEcho(
	t testing.TB,
	fixture *remoteRPCFixture,
	value string,
) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		done := make(chan struct {
			value string
			err   error
		}, 1)
		err := fixture.caller.DispatchAsync(func(ctx context.Context) {
			client := NewPlayerRPCClient(
				fixture.caller,
				rpc.ToServiceOnNode("player-1", "PlayerService"),
			)
			result, callErr := client.AwaitEchoName(ctx, value)
			done <- struct {
				value string
				err   error
			}{value: result, err: callErr}
		})
		if err != nil {
			t.Fatalf("caller DispatchAsync() error = %v", err)
		}
		result := <-done
		if result.err == nil {
			return result.value
		}
		if !errors.Is(result.err, errs.ErrRPCNoRoute) &&
			!errors.Is(result.err, errs.ErrTransportUnavailable) {
			t.Fatalf("AwaitEchoName() error = %v", result.err)
		}
		if time.Now().After(deadline) {
			t.Fatalf("等待远端 RPC 就绪超时，最后错误 = %v", result.err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestTCPHeartbeatKeepsIdleRPCSessionUsable 验证连接空闲超过 ReadIdleTimeout 后，应用层
// Ping/Pong 能保持真实双 Node 会话存活，后续业务调用仍复用同一可用链路。
func TestTCPHeartbeatKeepsIdleRPCSessionUsable(t *testing.T) {
	fixture := newRemoteRPCFixture(t)
	if result := awaitRemoteEcho(t, fixture, "before-idle"); result != "before-idle-echo" {
		t.Fatalf("initial remote echo = %q", result)
	}

	// 测试配置的心跳周期是 1s/3；等待超过完整 ReadIdleTimeout，确保至少一轮
	// Ping/Pong 已经发生，而不是依赖业务流量刷新读空闲计时。
	time.Sleep(1200 * time.Millisecond)
	if result := awaitRemoteEcho(t, fixture, "after-idle"); result != "after-idle-echo" {
		t.Fatalf("post-heartbeat remote echo = %q", result)
	}
}

func TestGeneratedRemoteAwaitAsyncNotifyAndReconnect(t *testing.T) {
	fixture := newRemoteRPCFixture(t)
	if result := awaitRemoteEcho(t, fixture, "first"); result != "first-echo" {
		t.Fatalf("remote AwaitEchoName() = %q", result)
	}

	// 一个真实远端调用同时覆盖普通结构体、嵌套 Protobuf 和顶层 Protobuf。
	metadata := map[string]*wrapperspb.StringValue{
		"region": wrapperspb.String("cn-east"),
	}
	options, err := structpb.NewStruct(map[string]any{"trace": "remote"})
	if err != nil {
		t.Fatal(err)
	}
	getResult := make(chan struct {
		player  PlayerData
		options *structpb.Struct
		err     error
	}, 1)
	if err := fixture.caller.DispatchAsync(func(ctx context.Context) {
		client := NewPlayerRPCClient(
			fixture.caller,
			rpc.ToServiceOnNode("player-1", "PlayerService"),
		)
		player, returnedOptions, callErr := client.AwaitGetPlayer(
			ctx,
			1001,
			PlayerData{Name: "remote", Metadata: metadata},
			options,
		)
		getResult <- struct {
			player  PlayerData
			options *structpb.Struct
			err     error
		}{player: player, options: returnedOptions, err: callErr}
	}); err != nil {
		t.Fatal(err)
	}
	remotePlayer := <-getResult
	if remotePlayer.err != nil ||
		remotePlayer.player.ID != 1001 ||
		remotePlayer.player.Metadata["region"].Value != "cn-east" ||
		remotePlayer.options.GetFields()["trace"].GetStringValue() != "remote" {
		t.Fatalf("remote AwaitGetPlayer() = %+v", remotePlayer)
	}

	// Async 的提交和 callback 都位于调用方 Service 调度语义内，响应由 RequestID 关联。
	asyncResult := make(chan struct {
		value string
		err   error
	}, 1)
	submitDone := make(chan error, 1)
	if err := fixture.caller.DispatchAsync(func(ctx context.Context) {
		client := NewPlayerRPCClient(
			fixture.caller,
			rpc.ToServiceOnNode("player-1", "PlayerService"),
		)
		submitDone <- client.AsyncEchoName(
			ctx,
			"async",
			func(_ context.Context, value string, err error) {
				asyncResult <- struct {
					value string
					err   error
				}{value: value, err: err}
			},
		)
	}); err != nil {
		t.Fatal(err)
	}
	if err := <-submitDone; err != nil {
		t.Fatalf("AsyncEchoName() submit error = %v", err)
	}
	async := <-asyncResult
	if async.err != nil || async.value != "async-echo" {
		t.Fatalf("AsyncEchoName() value=%q error=%v", async.value, async.err)
	}

	// Notify 只确认发送队列准入；目标 FIFO 屏障负责读取已经完成的业务状态。
	notifyDone := make(chan error, 1)
	if err := fixture.caller.DispatchAsync(func(ctx context.Context) {
		client := NewPlayerRPCClient(
			fixture.caller,
			rpc.ToServiceOnNode("player-1", "PlayerService"),
		)
		notifyDone <- client.NotifyPlayerOnline(ctx, 7788)
	}); err != nil {
		t.Fatal(err)
	}
	if err := <-notifyDone; err != nil {
		t.Fatalf("NotifyPlayerOnline() error = %v", err)
	}
	notifyDeadline := time.Now().Add(3 * time.Second)
	for {
		onlineID := make(chan int64, 1)
		if err := fixture.player.DispatchAsync(func(context.Context) {
			onlineID <- fixture.player.OnlineID
		}); err != nil {
			t.Fatal(err)
		}
		if value := <-onlineID; value == 7788 {
			break
		} else if time.Now().After(notifyDeadline) {
			t.Fatalf("remote Notify PlayerOnline ID = %d", value)
		}
		time.Sleep(10 * time.Millisecond)
	}

	// 业务 error 和 panic 都只传稳定错误码，不传动态错误文本或 Go error 指针。
	setTargetMode := func(fail, panicCall bool) {
		t.Helper()
		done := make(chan struct{}, 1)
		if err := fixture.player.DispatchAsync(func(context.Context) {
			fixture.player.ShouldFail = fail
			fixture.player.ShouldPanic = panicCall
			done <- struct{}{}
		}); err != nil {
			t.Fatal(err)
		}
		<-done
	}
	callGet := func() error {
		t.Helper()
		done := make(chan error, 1)
		if err := fixture.caller.DispatchAsync(func(ctx context.Context) {
			client := NewPlayerRPCClient(
				fixture.caller,
				rpc.ToServiceOnNode("player-1", "PlayerService"),
			)
			_, _, callErr := client.AwaitGetPlayer(
				ctx,
				1,
				PlayerData{},
				nil,
			)
			done <- callErr
		}); err != nil {
			t.Fatal(err)
		}
		return <-done
	}
	setTargetMode(true, false)
	if err := callGet(); !errors.Is(err, errs.ErrInvalidArgument) {
		t.Fatalf("remote business error = %v", err)
	}
	setTargetMode(false, true)
	if err := callGet(); !errors.Is(err, errs.ErrRPCExecutionPanic) {
		t.Fatalf("remote panic error = %v", err)
	}
	setTargetMode(false, false)

	// 原目标正常停止后，以相同 NodeID 和地址创建新实例；调用端只重连，不重放旧调用。
	stopTestNode(t, fixture.targetNode)
	nextPlayer := &PlayerService{}
	nextTarget := newRemoteFixtureNode(
		t,
		"player-1",
		fixture.targetConfig,
		fixture.pool,
		fixture.discovery,
		node.ServiceBinding{
			Name:     "PlayerService",
			Template: "PlayerService",
			Service:  nextPlayer,
		},
	)
	fixture.targetNode = nextTarget
	fixture.player = nextPlayer
	if err := nextTarget.Start(context.Background()); err != nil {
		t.Fatalf("replacement target Start() error = %v", err)
	}
	if result := awaitRemoteEcho(t, fixture, "reconnected"); result != "reconnected-echo" {
		t.Fatalf("reconnected AwaitEchoName() = %q", result)
	}
}

// TestRemoteOnStartAwaitsDiscoveryAndRPC 验证 Provider、TCP 与 Deadline 基础设施在 OnStart
// 阶段持续工作，而普通业务 Runner 仍等待整个 Node 越过统一就绪屏障。
func TestRemoteOnStartAwaitsDiscoveryAndRPC(t *testing.T) {
	pool := bufferpool.NewPool(bufferpool.Options{TrackUsage: true})
	discoverySource := internaldiscovery.NewSource()
	targetConfig := testRPCConfig(t)
	callerConfig := testRPCConfig(t)
	player := &PlayerService{}
	caller := &startAwaitCallerService{targetNodeID: "player-1"}

	targetNode := newRemoteFixtureNode(
		t,
		"player-1",
		targetConfig,
		pool,
		discoverySource,
		node.ServiceBinding{
			Name:     "PlayerService",
			Template: "PlayerService",
			Service:  player,
		},
	)
	callerNode := newRemoteFixtureNode(
		t,
		"gateway-1",
		callerConfig,
		pool,
		discoverySource,
		node.ServiceBinding{
			Name:     "CallerService",
			Template: "CallerService",
			Service:  caller,
		},
	)
	t.Cleanup(func() {
		stopTestNode(t, callerNode)
		stopTestNode(t, targetNode)
	})

	// 先启动依赖方，使 AwaitNodeService 确实进入等待；目标稍后发布后应唤醒原 OnStart。
	callerDone := make(chan error, 1)
	go func() {
		callerDone <- callerNode.Start(context.Background())
	}()
	time.Sleep(20 * time.Millisecond)
	if err := targetNode.Start(context.Background()); err != nil {
		t.Fatalf("target Node.Start() error = %v", err)
	}
	select {
	case err := <-callerDone:
		if err != nil {
			t.Fatalf("caller Node.Start() error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("OnStart 等待发现和 RPC 超时")
	}
	if caller.result != "on-start-echo" {
		t.Fatalf("OnStart RPC result = %q", caller.result)
	}
}

// TestRemoteRetiredServiceRemainsRoutable 锁定 Retired 仅作为可观察状态，不自动拒绝 RPC。
func TestRemoteRetiredServiceRemainsRoutable(t *testing.T) {
	fixture := newRemoteRPCFixture(t)
	_ = awaitRemoteEcho(t, fixture, "running")

	var targetRecord internaldiscovery.RawNode
	subscription, err := fixture.discovery.Subscribe(
		func(snapshot internaldiscovery.RawSnapshot) error {
			for _, record := range snapshot.Nodes {
				if record.NodeID == fixture.targetNode.ID() {
					targetRecord = record
					break
				}
			}
			return nil
		},
	)
	if err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}
	subscription.Close()
	if len(targetRecord.Services) != 1 {
		t.Fatalf("目标发现记录 = %+v", targetRecord)
	}
	targetRecord.Services[0].State = internaldiscovery.ServiceStateRetired
	if err := fixture.discovery.Publish(targetRecord); err != nil {
		t.Fatalf("Publish(retired) error = %v", err)
	}

	if result := awaitRemoteEcho(t, fixture, "retired"); result != "retired-echo" {
		t.Fatalf("Retired AwaitEchoName() = %q", result)
	}
}

func TestM19TCPGeneratedBindingRoutesAcrossRunningInstances(t *testing.T) {
	pool := bufferpool.NewPool(bufferpool.Options{TrackUsage: true})
	discoverySource := internaldiscovery.NewSource()
	firstPlayer := &PlayerService{EchoSuffix: "player-1"}
	secondPlayer := &PlayerService{EchoSuffix: "player-2"}
	caller := &CallerService{}
	nodes := []*node.Node{
		newRemoteFixtureNodeWithLabels(
			t,
			"player-1",
			testRPCConfig(t),
			map[string]string{"scope": "area", "real_area_id": "1"},
			pool,
			discoverySource,
			node.ServiceBinding{
				Name: "PlayerService", Template: "PlayerService", Service: firstPlayer,
			},
		),
		newRemoteFixtureNodeWithLabels(
			t,
			"player-2",
			testRPCConfig(t),
			map[string]string{"scope": "area", "real_area_id": "2"},
			pool,
			discoverySource,
			node.ServiceBinding{
				Name: "PlayerService", Template: "PlayerService", Service: secondPlayer,
			},
		),
		newRemoteFixtureNode(
			t,
			"gateway-1",
			testRPCConfig(t),
			pool,
			discoverySource,
			node.ServiceBinding{
				Name: "CallerService", Template: "CallerService", Service: caller,
			},
		),
	}
	for _, current := range nodes {
		if err := current.Start(context.Background()); err != nil {
			t.Fatalf("Node %q Start() error = %v", current.ID(), err)
		}
	}
	t.Cleanup(func() {
		for index := len(nodes) - 1; index >= 0; index-- {
			stopTestNode(t, nodes[index])
		}
		if stats := pool.Stats(); stats.InUseBuffers != 0 {
			t.Errorf("M19 TCP Buffer 未全部归还: %+v", stats)
		}
	})

	type callResults struct {
		values []string
		err    error
	}
	call := func(run func(context.Context, PlayerRPCClient) ([]string, error)) []string {
		t.Helper()
		done := make(chan callResults, 1)
		if err := caller.DispatchAsync(func(ctx context.Context) {
			values, callErr := run(ctx, BindPlayerRPC(caller))
			done <- callResults{values: values, err: callErr}
		}); err != nil {
			t.Fatalf("DispatchAsync() error = %v", err)
		}
		select {
		case result := <-done:
			if result.err != nil {
				t.Fatalf("M19 route call error = %v", result.err)
			}
			return result.values
		case <-time.After(5 * time.Second):
			t.Fatal("M19 route call timeout")
			return nil
		}
	}

	// 精确 Await 会等待各自连接就绪，并验证 OnNode 沿用默认 PlayerService 名称。
	for _, nodeID := range []string{"player-1", "player-2"} {
		values := call(func(
			ctx context.Context,
			client PlayerRPCClient,
		) ([]string, error) {
			value, err := client.OnNode(nodeID).AwaitEchoName(ctx, "warm")
			return []string{value}, err
		})
		if len(values) != 1 || values[0] != "warm-"+nodeID {
			t.Fatalf("OnNode(%q) = %v", nodeID, values)
		}
	}

	values := call(func(
		ctx context.Context,
		client PlayerRPCClient,
	) ([]string, error) {
		first, err := client.AwaitEchoName(ctx, "rr1")
		if err != nil {
			return nil, err
		}
		// 临时重新绑定仍复用 Runtime 级 RoundRobin 状态。
		second, err := BindPlayerRPC(caller).AwaitEchoName(ctx, "rr2")
		if err != nil {
			return nil, err
		}
		third, err := client.AwaitEchoName(ctx, "rr3")
		return []string{first, second, third}, err
	})
	want := []string{"rr1-player-1", "rr2-player-2", "rr3-player-1"}
	if len(values) != len(want) {
		t.Fatalf("RoundRobin = %v", values)
	}
	for index := range want {
		if values[index] != want[index] {
			t.Fatalf("RoundRobin = %v, want %v", values, want)
		}
	}

	values = call(func(
		ctx context.Context,
		client PlayerRPCClient,
	) ([]string, error) {
		value, err := client.Route(uint64(1)).AwaitEchoName(ctx, "key")
		return []string{value}, err
	})
	if values[0] != "key-player-2" {
		t.Fatalf("Route(1) = %v", values)
	}

	// WhereLabels 读取真实发现快照中的 Node Labels，再由稳定 Key 在过滤结果中单选。
	values = call(func(
		ctx context.Context,
		client PlayerRPCClient,
	) ([]string, error) {
		areaOne, err := client.WhereLabels(map[string]string{
			"scope": "area", "real_area_id": "1",
		}).AwaitEchoName(ctx, "labels-one")
		if err != nil {
			return nil, err
		}
		areaTwo, err := client.WhereLabels(map[string]string{
			"scope": "area", "real_area_id": "2",
		}).Route(uint64(0)).AwaitEchoName(ctx, "labels-two")
		return []string{areaOne, areaTwo}, err
	})
	if len(values) != 2 ||
		values[0] != "labels-one-player-1" ||
		values[1] != "labels-two-player-2" {
		t.Fatalf("WhereLabels route = %v", values)
	}

	firstRecord := discoveryNodeRecord(t, discoverySource, "player-1")
	firstRecord.Services[0].State = internaldiscovery.ServiceStateRetired
	if err := discoverySource.Publish(firstRecord); err != nil {
		t.Fatalf("Publish(retired) error = %v", err)
	}
	values = call(func(
		ctx context.Context,
		client PlayerRPCClient,
	) ([]string, error) {
		auto, err := client.AwaitEchoName(ctx, "retired-auto")
		if err != nil {
			return nil, err
		}
		exact, err := client.OnNode("player-1").
			AwaitEchoName(ctx, "retired-exact")
		return []string{auto, exact}, err
	})
	if values[0] != "retired-auto-player-2" ||
		values[1] != "retired-exact-player-1" {
		t.Fatalf("Retired route boundary = %v", values)
	}
}

func TestRemoteGracefulStopWaitsAcceptedRequest(t *testing.T) {
	fixture := newRemoteRPCFixture(t)
	_ = awaitRemoteEcho(t, fixture, "ready")

	gate := make(chan struct{})
	started := make(chan struct{}, 1)
	configured := make(chan struct{}, 1)
	if err := fixture.player.DispatchAsync(func(context.Context) {
		fixture.player.Wait = gate
		fixture.player.WaitStarted = started
		fixture.player.IgnoreWaitContext = true
		configured <- struct{}{}
	}); err != nil {
		t.Fatal(err)
	}
	<-configured

	callDone := make(chan error, 1)
	if err := fixture.caller.DispatchAsync(func(ctx context.Context) {
		client := NewPlayerRPCClient(
			fixture.caller,
			rpc.ToServiceOnNode("player-1", "PlayerService"),
		)
		_, _, callErr := client.AwaitGetPlayer(
			ctx,
			9001,
			PlayerData{Name: "persist"},
			nil,
		)
		callDone <- callErr
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("远端请求没有进入目标业务方法")
	}

	// 优雅停止先阻止新入站，再等待已经存在的 Await；此时不能提前关闭出站连接。
	stopDone := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		stopDone <- fixture.callerNode.Stop(ctx)
	}()
	select {
	case err := <-stopDone:
		t.Fatalf("目标尚未完成时 Stop 提前返回: %v", err)
	case <-time.After(30 * time.Millisecond):
	}

	// 目标完成后响应仍可沿旧连接返回，随后调用端完成排空并关闭连接。
	close(gate)
	if err := <-callDone; err != nil {
		t.Fatalf("优雅停止中的 Await error = %v", err)
	}
	if err := <-stopDone; err != nil {
		t.Fatalf("等待已接受请求后的 Stop error = %v", err)
	}

	completed := make(chan int, 1)
	if err := fixture.player.DispatchAsync(func(context.Context) {
		completed <- fixture.player.Completed
	}); err != nil {
		t.Fatal(err)
	}
	if count := <-completed; count != 1 {
		t.Fatalf("调用方断线后目标完成次数 = %d", count)
	}
}

func TestRemoteExplicitDeadlineUsesSingleAwaitBoundary(t *testing.T) {
	fixture := newRemoteRPCFixture(t)
	_ = awaitRemoteEcho(t, fixture, "ready")

	gate := make(chan struct{})
	configured := make(chan struct{}, 1)
	if err := fixture.player.DispatchAsync(func(context.Context) {
		fixture.player.Wait = gate
		configured <- struct{}{}
	}); err != nil {
		t.Fatal(err)
	}
	<-configured

	result := make(chan error, 1)
	if err := fixture.caller.DispatchAsync(func(ctx context.Context) {
		callCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
		defer cancel()
		client := NewPlayerRPCClient(
			fixture.caller,
			rpc.ToServiceOnNode("player-1", "PlayerService"),
		)
		_, _, callErr := client.AwaitGetPlayer(
			callCtx,
			1,
			PlayerData{},
			nil,
		)
		result <- callErr
	}); err != nil {
		t.Fatal(err)
	}
	if err := <-result; !errors.Is(err, errs.ErrDeadlineExceeded) {
		t.Fatalf("explicit deadline error = %v", err)
	}
	close(gate)
}

func TestRemoteUsesServiceDefaultAwaitTimeout(t *testing.T) {
	pool := bufferpool.NewPool(bufferpool.Options{TrackUsage: true})
	targetConfig := testRPCConfig(t)
	callerConfig := testRPCConfig(t)
	player := &PlayerService{}
	caller := &CallerService{}
	discoverySource := internaldiscovery.NewSource()
	target := newRemoteFixtureNode(
		t,
		"player-1",
		targetConfig,
		pool,
		discoverySource,
		node.ServiceBinding{
			Name:     "PlayerService",
			Template: "PlayerService",
			Service:  player,
		},
	)
	scheduler := service.DefaultSchedulerConfig()
	scheduler.DefaultAwaitTimeout = 50 * time.Millisecond
	source := newRemoteFixtureNodeWithScheduler(
		t,
		"gateway-1",
		callerConfig,
		scheduler,
		pool,
		discoverySource,
		node.ServiceBinding{
			Name:     "CallerService",
			Template: "CallerService",
			Service:  caller,
		},
	)
	if err := target.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := source.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		stopTestNode(t, source)
		stopTestNode(t, target)
	})
	readyDeadline := time.Now().Add(3 * time.Second)
	for {
		ready := make(chan error, 1)
		if err := caller.DispatchAsync(func(ctx context.Context) {
			client := NewPlayerRPCClient(
				caller,
				rpc.ToServiceOnNode("player-1", "PlayerService"),
			)
			ready <- client.NotifyPlayerOnline(ctx, 1)
		}); err != nil {
			t.Fatal(err)
		}
		err := <-ready
		if err == nil {
			break
		}
		if !errors.Is(err, errs.ErrTransportUnavailable) ||
			time.Now().After(readyDeadline) {
			t.Fatalf("等待默认超时测试连接就绪: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}

	gate := make(chan struct{})
	configured := make(chan struct{}, 1)
	if err := player.DispatchAsync(func(context.Context) {
		player.Wait = gate
		configured <- struct{}{}
	}); err != nil {
		t.Fatal(err)
	}
	<-configured
	result := make(chan error, 1)
	if err := caller.DispatchAsync(func(ctx context.Context) {
		client := NewPlayerRPCClient(
			caller,
			rpc.ToServiceOnNode("player-1", "PlayerService"),
		)
		_, _, callErr := client.AwaitGetPlayer(
			ctx,
			1,
			PlayerData{},
			nil,
		)
		result <- callErr
	}); err != nil {
		t.Fatal(err)
	}
	if err := <-result; !errors.Is(err, errs.ErrDeadlineExceeded) {
		t.Fatalf("default Await timeout error = %v", err)
	}
	close(gate)
}

func TestRemoteDuplicateNodeIDKeepsFirstConnection(t *testing.T) {
	pool := bufferpool.NewPool(bufferpool.Options{TrackUsage: true})
	targetConfig := testRPCConfig(t)
	firstConfig := testRPCConfig(t)
	secondConfig := testRPCConfig(t)
	player := &PlayerService{}
	firstCaller := &CallerService{}
	secondCaller := &CallerService{}
	discoverySource := internaldiscovery.NewSource()
	target := newRemoteFixtureNode(
		t,
		"player-1",
		targetConfig,
		pool,
		discoverySource,
		node.ServiceBinding{
			Name:     "PlayerService",
			Template: "PlayerService",
			Service:  player,
		},
	)
	first := newRemoteFixtureNode(
		t,
		"duplicate-gateway",
		firstConfig,
		pool,
		discoverySource,
		node.ServiceBinding{
			Name:     "CallerService",
			Template: "CallerService",
			Service:  firstCaller,
		},
	)
	second := newRemoteFixtureNode(
		t,
		"duplicate-gateway",
		secondConfig,
		pool,
		discoverySource,
		node.ServiceBinding{
			Name:     "CallerService",
			Template: "CallerService",
			Service:  secondCaller,
		},
	)
	if err := target.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := first.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		stopTestNode(t, second)
		stopTestNode(t, first)
		stopTestNode(t, target)
	})

	firstFixture := &remoteRPCFixture{
		callerNode: first,
		caller:     firstCaller,
		targetNode: target,
		player:     player,
		pool:       pool,
	}
	if result := awaitRemoteEcho(t, firstFixture, "owner"); result != "owner-echo" {
		t.Fatalf("first connection result = %q", result)
	}
	if err := second.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	// 给第二个连接至少一次握手机会；它必须持续不可用，不能替换第一个连接。
	time.Sleep(300 * time.Millisecond)
	secondResult := make(chan error, 1)
	if err := secondCaller.DispatchAsync(func(ctx context.Context) {
		client := NewPlayerRPCClient(
			secondCaller,
			rpc.ToServiceOnNode("player-1", "PlayerService"),
		)
		callErr := client.AsyncEchoName(
			ctx,
			"rejected",
			func(context.Context, string, error) {
				t.Error("提交失败的 Async 不应执行 callback")
			},
		)
		secondResult <- callErr
	}); err != nil {
		t.Fatal(err)
	}
	if err := <-secondResult; !errors.Is(err, errs.ErrTransportUnavailable) {
		t.Fatalf("duplicate NodeID call error = %v", err)
	}
	if result := awaitRemoteEcho(t, firstFixture, "still-owner"); result != "still-owner-echo" {
		t.Fatalf("existing connection was disturbed: %q", result)
	}
}

func TestRemotePrivateServiceIsNotAdvertised(t *testing.T) {
	pool := bufferpool.NewPool(bufferpool.Options{TrackUsage: true})
	targetConfig := testRPCConfig(t)
	callerConfig := testRPCConfig(t)
	player := &PlayerService{}
	caller := &CallerService{}
	discoverySource := internaldiscovery.NewSource()
	target := newRemoteFixtureNode(
		t,
		"player-1",
		targetConfig,
		pool,
		discoverySource,
		node.ServiceBinding{
			Name:     "PlayerService",
			Template: "PlayerService",
			Private:  true,
			Service:  player,
		},
	)
	source := newRemoteFixtureNode(
		t,
		"gateway-1",
		callerConfig,
		pool,
		discoverySource,
		node.ServiceBinding{
			Name:     "CallerService",
			Template: "CallerService",
			Service:  caller,
		},
	)
	if err := target.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := source.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		stopTestNode(t, source)
		stopTestNode(t, target)
	})

	deadline := time.Now().Add(3 * time.Second)
	for {
		result := make(chan error, 1)
		if err := caller.DispatchAsync(func(ctx context.Context) {
			client := NewPlayerRPCClient(
				caller,
				rpc.ToServiceOnNode("player-1", "PlayerService"),
			)
			_, callErr := client.AwaitEchoName(ctx, "private")
			result <- callErr
		}); err != nil {
			t.Fatal(err)
		}
		err := <-result
		if errors.Is(err, errs.ErrRPCNoRoute) {
			break
		}
		if !errors.Is(err, errs.ErrTransportUnavailable) {
			t.Fatalf("private Service call error = %v", err)
		}
		if time.Now().After(deadline) {
			t.Fatalf("等待私有 Service 握手目录超时: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestRemoteContractFingerprintMismatchFailsBeforeSend(t *testing.T) {
	fixture := newRemoteRPCFixture(t)
	_ = awaitRemoteEcho(t, fixture, "ready")
	result := make(chan error, 1)
	if err := fixture.caller.DispatchAsync(func(ctx context.Context) {
		fingerprint := playerRPCFingerprint
		fingerprint[0] ^= 0xFF
		client := rpc.NewGeneratedClient(
			fixture.caller,
			rpc.ToServiceOnNode("player-1", "PlayerService"),
			playerRPCContractID,
			fingerprint,
		)
		prepared, prepareErr := client.PrepareAwait(
			ctx,
			playerRPCEchoNameMethodID,
		)
		if prepareErr == nil {
			prepared.FinishInvocation()
		}
		result <- prepareErr
	}); err != nil {
		t.Fatal(err)
	}
	if err := <-result; !errors.Is(err, errs.ErrRPCContractMismatch) {
		t.Fatalf("remote fingerprint mismatch error = %v", err)
	}
}
