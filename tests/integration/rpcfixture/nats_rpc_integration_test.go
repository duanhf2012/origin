package rpcfixture

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/duanhf2012/origin/v3/errs"
	"github.com/duanhf2012/origin/v3/internal/bufferpool"
	internaldiscovery "github.com/duanhf2012/origin/v3/internal/discovery"
	"github.com/duanhf2012/origin/v3/node"
	"github.com/duanhf2012/origin/v3/rpc"
	"github.com/duanhf2012/origin/v3/service"
	"github.com/nats-io/nats-server/v2/server"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

// TestNATSRPCAwaitAsyncNotify 验证生成客户端在 NATS 下保持与 TCP 完全相同的调用外观。
func TestNATSRPCAwaitAsyncNotify(t *testing.T) {
	running := startRPCNATSServer(t)
	pool := bufferpool.NewPool(bufferpool.Options{TrackUsage: true})
	discoverySource := internaldiscovery.NewSource()
	player := &PlayerService{}
	caller := &CallerService{}
	targetConfig := testNATSRPCConfig(running.ClientURL())
	callerConfig := testNATSRPCConfig(running.ClientURL())
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
		t.Fatalf("target Start() error = %v", err)
	}
	if err := callerNode.Start(context.Background()); err != nil {
		stopTestNode(t, targetNode)
		t.Fatalf("caller Start() error = %v", err)
	}
	t.Cleanup(func() {
		stopTestNode(t, callerNode)
		stopTestNode(t, targetNode)
		if stats := pool.Stats(); stats.InUseBuffers != 0 {
			t.Errorf("NATS RPC Buffer 未全部归还: %+v", stats)
		}
	})

	result := make(chan struct {
		await string
		async string
		err   error
	}, 1)
	if err := caller.DispatchAsync(func(ctx context.Context) {
		client := BindPlayerRPC(caller)
		await, callErr := client.AwaitEchoName(ctx, "await")
		if callErr != nil {
			result <- struct {
				await string
				async string
				err   error
			}{err: callErr}
			return
		}
		callErr = client.AsyncEchoName(
			ctx,
			"async",
			func(_ context.Context, async string, asyncErr error) {
				result <- struct {
					await string
					async string
					err   error
				}{await: await, async: async, err: asyncErr}
			},
		)
		if callErr != nil {
			result <- struct {
				await string
				async string
				err   error
			}{await: await, err: callErr}
		}
		if notifyErr := client.NotifyPlayerOnline(ctx, 9001); notifyErr != nil {
			result <- struct {
				await string
				async string
				err   error
			}{await: await, err: notifyErr}
		}
	}); err != nil {
		t.Fatalf("Caller DispatchAsync() error = %v", err)
	}

	select {
	case got := <-result:
		if got.err != nil || got.await != "await-echo" || got.async != "async-echo" {
			t.Fatalf("NATS RPC result = %+v", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("NATS RPC 调用超时")
	}

	// 高级集成层的未 Prepare Notify 仍必须按当前发现快照走真实 NATS；生成客户端
	// 继续使用上面的 Prepare 路径。本断言防止两条路径的包头或所有权悄然分叉。
	lowLevel := rpc.NewGeneratedClient(
		caller,
		rpc.ToServiceOnNode("player-1", "PlayerService"),
		playerRPCContractID,
		playerRPCFingerprint,
	)
	request, err := encodePlayerRPCPlayerOnlineRequest(
		lowLevel,
		rpc.CallNotify,
		9002,
	)
	if err != nil {
		t.Fatalf("low-level NATS encode error = %v", err)
	}
	if err := lowLevel.Notify(
		context.Background(),
		playerRPCPlayerOnlineMethodID,
		request,
	); err != nil {
		t.Fatalf("low-level NATS Notify() error = %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		checked := make(chan bool, 1)
		if err := player.DispatchAsync(func(context.Context) {
			checked <- player.OnlineID == 9002
		}); err != nil {
			t.Fatalf("Player DispatchAsync() error = %v", err)
		}
		if <-checked {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("low-level NATS Notify OnlineID = %d", player.OnlineID)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestM19NATSRoundRobinAcrossRunningInstances(t *testing.T) {
	running := startRPCNATSServer(t)
	pool := bufferpool.NewPool(bufferpool.Options{TrackUsage: true})
	discoverySource := internaldiscovery.NewSource()
	firstPlayer := &PlayerService{EchoSuffix: "player-1"}
	secondPlayer := &PlayerService{EchoSuffix: "player-2"}
	caller := &CallerService{}
	nodes := []*node.Node{
		newRemoteFixtureNode(
			t,
			"player-1",
			testNATSRPCConfig(running.ClientURL()),
			pool,
			discoverySource,
			node.ServiceBinding{
				Name: "PlayerService", Template: "PlayerService", Service: firstPlayer,
			},
		),
		newRemoteFixtureNode(
			t,
			"player-2",
			testNATSRPCConfig(running.ClientURL()),
			pool,
			discoverySource,
			node.ServiceBinding{
				Name: "PlayerService", Template: "PlayerService", Service: secondPlayer,
			},
		),
		newRemoteFixtureNode(
			t,
			"gateway-1",
			testNATSRPCConfig(running.ClientURL()),
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
			t.Errorf("M19 NATS Buffer 未全部归还: %+v", stats)
		}
	})

	type callResult struct {
		values []string
		err    error
	}
	call := func(run func(context.Context, PlayerRPCClient) ([]string, error)) []string {
		t.Helper()
		done := make(chan callResult, 1)
		if err := caller.DispatchAsync(func(ctx context.Context) {
			values, callErr := run(ctx, BindPlayerRPC(caller))
			done <- callResult{values: values, err: callErr}
		}); err != nil {
			t.Fatalf("DispatchAsync() error = %v", err)
		}
		select {
		case result := <-done:
			if result.err != nil {
				t.Fatalf("M19 NATS route call error = %v", result.err)
			}
			return result.values
		case <-time.After(5 * time.Second):
			t.Fatal("M19 NATS route call timeout")
			return nil
		}
	}

	for _, nodeID := range []string{"player-1", "player-2"} {
		values := call(func(
			ctx context.Context,
			client PlayerRPCClient,
		) ([]string, error) {
			value, err := client.OnNode(nodeID).AwaitEchoName(ctx, "warm")
			return []string{value}, err
		})
		if len(values) != 1 || values[0] != "warm-"+nodeID {
			t.Fatalf("NATS OnNode(%q) = %v", nodeID, values)
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
		second, err := BindPlayerRPC(caller).AwaitEchoName(ctx, "rr2")
		if err != nil {
			return nil, err
		}
		third, err := client.AwaitEchoName(ctx, "rr3")
		return []string{first, second, third}, err
	})
	want := []string{"rr1-player-1", "rr2-player-2", "rr3-player-1"}
	if len(values) != len(want) {
		t.Fatalf("NATS RoundRobin = %v", values)
	}
	for index := range want {
		if values[index] != want[index] {
			t.Fatalf("NATS RoundRobin = %v, want %v", values, want)
		}
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
		t.Fatalf("NATS Retired route boundary = %v", values)
	}
}

// TestExternalNATSRPCThreeNodeCluster 使用部署在 Linux 上的真实三节点集群验证不同 Seed
// 之间的 Origin RPC 路由；未提供环境变量时保持普通 go test 可重复执行。
func TestExternalNATSRPCThreeNodeCluster(t *testing.T) {
	rawURLs := strings.TrimSpace(os.Getenv("ORIGIN_NATS_URLS"))
	if rawURLs == "" {
		t.Skip("未设置 ORIGIN_NATS_URLS，跳过外部 NATS RPC 集群测试")
	}
	urls := strings.Split(rawURLs, ",")
	for index := range urls {
		urls[index] = strings.TrimSpace(urls[index])
	}
	if len(urls) < 3 {
		t.Fatal("ORIGIN_NATS_URLS 至少需要三个地址")
	}
	configFor := func(url string) rpc.Config {
		config := testNATSRPCConfig(url)
		config.NATS.Namespace = "game-external-test"
		config.NATS.Auth.Username = os.Getenv("ORIGIN_NATS_USERNAME")
		config.NATS.Auth.Password = os.Getenv("ORIGIN_NATS_PASSWORD")
		// 已部署集群使用 4M Broker max_payload；业务上限需要给最大 NATS 包头预留空间。
		config.MaxPayloadSize = rpc.DefaultMaxPayloadSize - 1024
		return config
	}

	pool := bufferpool.NewPool(bufferpool.Options{TrackUsage: true})
	source := internaldiscovery.NewSource()
	player := &PlayerService{}
	gateway := &CallerService{}
	observer := &CallerService{}
	nodes := []*node.Node{
		newRemoteFixtureNode(
			t,
			"player-1",
			configFor(urls[0]),
			pool,
			source,
			node.ServiceBinding{
				Name: "PlayerService", Template: "PlayerService", Service: player,
			},
		),
		newRemoteFixtureNode(
			t,
			"gateway-1",
			configFor(urls[1]),
			pool,
			source,
			node.ServiceBinding{
				Name: "GatewayService", Template: "CallerService", Service: gateway,
			},
		),
		newRemoteFixtureNode(
			t,
			"observer-1",
			configFor(urls[2]),
			pool,
			source,
			node.ServiceBinding{
				Name: "ObserverService", Template: "CallerService", Service: observer,
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
			t.Errorf("外部 NATS RPC Buffer 未全部归还: %+v", stats)
		}
	})
	if got := awaitNATSEcho(t, gateway, "external-gateway"); got != "external-gateway-echo" {
		t.Fatalf("external gateway echo = %q", got)
	}
	if got := awaitNATSEcho(t, observer, "external-observer"); got != "external-observer-echo" {
		t.Fatalf("external observer echo = %q", got)
	}
}

// TestNATSRPCThreeNodeClusterAndReconnect 覆盖三个 Origin Node 跨三个 Broker 的路由、复杂
// Codec、Retired 可调用，以及调用方 Broker 断开后旧 pending 快速失败且不会重放 Request。
func TestNATSRPCThreeNodeClusterAndReconnect(t *testing.T) {
	cluster := startRPCNATSCluster(t, 3)
	pool := bufferpool.NewPool(bufferpool.Options{TrackUsage: true})
	discoverySource := internaldiscovery.NewSource()
	player := &PlayerService{}
	gateway := &CallerService{}
	observer := &CallerService{}

	playerNode := newRemoteFixtureNode(
		t,
		"player-1",
		testNATSRPCConfig(cluster[0].ClientURL()),
		pool,
		discoverySource,
		node.ServiceBinding{
			Name:     "PlayerService",
			Template: "PlayerService",
			Service:  player,
		},
	)
	gatewayNode := newRemoteFixtureNode(
		t,
		"gateway-1",
		testNATSRPCConfig(cluster[1].ClientURL()),
		pool,
		discoverySource,
		node.ServiceBinding{
			Name:     "GatewayService",
			Template: "CallerService",
			Service:  gateway,
		},
	)
	observerNode := newRemoteFixtureNode(
		t,
		"observer-1",
		testNATSRPCConfig(cluster[2].ClientURL()),
		pool,
		discoverySource,
		node.ServiceBinding{
			Name:     "ObserverService",
			Template: "CallerService",
			Service:  observer,
		},
	)
	for _, current := range []*node.Node{playerNode, gatewayNode, observerNode} {
		if err := current.Start(context.Background()); err != nil {
			t.Fatalf("Node %q Start() error = %v", current.ID(), err)
		}
	}
	t.Cleanup(func() {
		stopTestNode(t, observerNode)
		stopTestNode(t, gatewayNode)
		stopTestNode(t, playerNode)
		if stats := pool.Stats(); stats.InUseBuffers != 0 {
			t.Errorf("NATS 集群 RPC Buffer 未全部归还: %+v", stats)
		}
	})

	if got := awaitNATSEcho(t, gateway, "cluster-gateway"); got != "cluster-gateway-echo" {
		t.Fatalf("gateway echo = %q", got)
	}
	if got := awaitNATSEcho(t, observer, "cluster-observer"); got != "cluster-observer-echo" {
		t.Fatalf("observer echo = %q", got)
	}

	// 普通结构体中嵌套 Protobuf 时继续走 Go 结构体 Codec，锁定 M11 的混合序列化规则。
	complexDone := make(chan error, 1)
	if err := gateway.DispatchAsync(func(ctx context.Context) {
		client := NewPlayerRPCClient(
			gateway,
			rpc.ToServiceOnNode("player-1", "PlayerService"),
		)
		seed := PlayerData{
			Name: "mixed",
			Metadata: map[string]*wrapperspb.StringValue{
				"region": wrapperspb.String("cn-east"),
			},
		}
		result, _, callErr := client.AwaitGetPlayer(ctx, 7001, seed, nil)
		if callErr == nil &&
			(result.ID != 7001 ||
				result.Metadata["region"].Value != "cn-east") {
			callErr = fmt.Errorf("复杂结构体返回错误: %+v", result)
		}
		complexDone <- callErr
	}); err != nil {
		t.Fatalf("complex DispatchAsync() error = %v", err)
	}
	if err := <-complexDone; err != nil {
		t.Fatalf("complex NATS RPC error = %v", err)
	}

	// Retired 是可观察路由状态，不在 TCP/NATS Adapter 层自动拒绝业务请求。
	targetRecord := discoveryNodeRecord(t, discoverySource, "player-1")
	targetRecord.Services[0].State = internaldiscovery.ServiceStateRetired
	if err := discoverySource.Publish(targetRecord); err != nil {
		t.Fatalf("Publish(retired) error = %v", err)
	}
	if got := awaitNATSEcho(t, gateway, "retired"); got != "retired-echo" {
		t.Fatalf("Retired NATS echo = %q", got)
	}

	gate := make(chan struct{})
	started := make(chan struct{}, 1)
	configured := make(chan struct{}, 1)
	if err := player.DispatchAsync(func(context.Context) {
		player.Wait = gate
		player.WaitStarted = started
		player.IgnoreWaitContext = true
		configured <- struct{}{}
	}); err != nil {
		t.Fatalf("configure player wait error = %v", err)
	}
	<-configured

	pendingDone := make(chan error, 1)
	if err := gateway.DispatchAsync(func(ctx context.Context) {
		client := NewPlayerRPCClient(
			gateway,
			rpc.ToServiceOnNode("player-1", "PlayerService"),
		)
		_, _, callErr := client.AwaitGetPlayer(
			ctx,
			8001,
			PlayerData{Name: "pending"},
			nil,
		)
		pendingDone <- callErr
	}); err != nil {
		t.Fatalf("pending DispatchAsync() error = %v", err)
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("NATS pending Request 未进入目标 Service")
	}

	// gateway 仅以第二个 Broker 为 Seed，因此停止它必然触发一次真实集群重连。
	cluster[1].Shutdown()
	cluster[1].WaitForShutdown()
	time.Sleep(3 * time.Second)
	close(gate)
	select {
	case err := <-pendingDone:
		if !errors.Is(err, errs.ErrTransportUnavailable) {
			t.Fatalf("断线时旧 pending error = %v", err)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("重连后的旧 pending 未完成")
	}
	recoveryDeadline := time.Now().Add(8 * time.Second)
	for gatewayNode.TransportStatus().State != node.TransportReady {
		if time.Now().After(recoveryDeadline) {
			t.Fatalf(
				"gateway NATS Transport 未恢复: %+v",
				gatewayNode.TransportStatus(),
			)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := awaitNATSEcho(t, gateway, "after-reconnect"); got != "after-reconnect-echo" {
		t.Fatalf("重连后的新 NATS RPC = %q", got)
	}

	// 断线时调用方 pending 快速失败，Adapter 不会把旧请求重新发布；恢复后的 Echo 调用也
	// 不属于 GetPlayer。若旧 Request 被重放，目标计数会超过断线前已经确认的准确值。
	countDone := make(chan int, 1)
	if err := player.DispatchAsync(func(context.Context) {
		countDone <- player.GetCount
	}); err != nil {
		t.Fatalf("read GetCount error = %v", err)
	}
	if got := <-countDone; got != 2 {
		t.Fatalf("GetPlayer 执行次数 = %d，Request 可能被重放", got)
	}
}

func TestNATSRPCRejectsInsufficientServerPayloadAndBadAuth(t *testing.T) {
	t.Run("max payload", func(t *testing.T) {
		running := startRPCNATSServerWithOptions(t, &server.Options{
			Host:       "127.0.0.1",
			Port:       -1,
			MaxPayload: 1024,
			NoLog:      true,
			NoSigs:     true,
		})
		instance := newRemoteFixtureNode(
			t,
			"payload-1",
			testNATSRPCConfig(running.ClientURL()),
			bufferpool.NewPool(bufferpool.Options{}),
			internaldiscovery.NewSource(),
			node.ServiceBinding{
				Name:     "PlayerService",
				Template: "PlayerService",
				Service:  &PlayerService{},
			},
		)
		if err := instance.Start(context.Background()); err == nil {
			t.Fatal("Broker max_payload 不足时 Node.Start() 成功")
		}
		_ = instance.Rollback(context.Background())
	})

	t.Run("username password", func(t *testing.T) {
		running := startRPCNATSServerWithOptions(t, &server.Options{
			Host:       "127.0.0.1",
			Port:       -1,
			MaxPayload: 8 * 1024 * 1024,
			Username:   "origin-test",
			Password:   "secret",
			NoLog:      true,
			NoSigs:     true,
		})
		config := testNATSRPCConfig(running.ClientURL())
		config.NATS.Auth.Username = "origin-test"
		config.NATS.Auth.Password = "secret"
		instance := newRemoteFixtureNode(
			t,
			"auth-1",
			config,
			bufferpool.NewPool(bufferpool.Options{}),
			internaldiscovery.NewSource(),
			node.ServiceBinding{
				Name:     "PlayerService",
				Template: "PlayerService",
				Service:  &PlayerService{},
			},
		)
		if err := instance.Start(context.Background()); err != nil {
			t.Fatalf("带认证 Node.Start() error = %v", err)
		}
		stopTestNode(t, instance)

		bad := config
		bad.NATS.Auth.Password = "bad-secret"
		badNode := newRemoteFixtureNode(
			t,
			"bad-auth-1",
			bad,
			bufferpool.NewPool(bufferpool.Options{}),
			internaldiscovery.NewSource(),
			node.ServiceBinding{
				Name:     "PlayerService",
				Template: "PlayerService",
				Service:  &PlayerService{},
			},
		)
		if err := badNode.Start(context.Background()); err == nil {
			t.Fatal("错误 NATS 密码启动成功")
		}
		_ = badNode.Rollback(context.Background())
	})
}

func TestNATSRPCDeadlineErrorsAndQueueOverload(t *testing.T) {
	scheduler := service.DefaultSchedulerConfig()
	scheduler.MaxTasks = 1
	scheduler.MaxAwaitTasks = 1
	fixture := newNATSRPCPair(t, scheduler)

	// 目标业务错误与 panic 只跨网络返回稳定错误码，不泄露服务端 error 或 panic 值。
	configured := make(chan struct{}, 1)
	if err := fixture.player.DispatchAsync(func(context.Context) {
		fixture.player.ShouldFail = true
		configured <- struct{}{}
	}); err != nil {
		t.Fatal(err)
	}
	<-configured
	if err := awaitNATSGetError(t, fixture, nil); !errors.Is(
		err,
		errs.ErrInvalidArgument,
	) {
		t.Fatalf("business error = %v", err)
	}
	if err := fixture.player.DispatchAsync(func(context.Context) {
		fixture.player.ShouldFail = false
		fixture.player.ShouldPanic = true
		configured <- struct{}{}
	}); err != nil {
		t.Fatal(err)
	}
	<-configured
	if err := awaitNATSGetError(t, fixture, nil); !errors.Is(
		err,
		errs.ErrRPCExecutionPanic,
	) {
		t.Fatalf("panic error = %v", err)
	}

	// 一个正在执行的根任务占满目标容量；Request 返回明确过载，Notify 只在目标侧丢弃。
	blocked := make(chan struct{})
	blockStarted := make(chan struct{})
	if err := fixture.player.DispatchAsync(func(context.Context) {
		fixture.player.ShouldPanic = false
		close(blockStarted)
		<-blocked
	}); err != nil {
		t.Fatal(err)
	}
	<-blockStarted
	if err := awaitNATSGetError(t, fixture, nil); !errors.Is(
		err,
		errs.ErrServiceQueueFull,
	) {
		t.Fatalf("queue full Request error = %v", err)
	}
	notifyDone := make(chan error, 1)
	if err := fixture.caller.DispatchAsync(func(ctx context.Context) {
		client := NewPlayerRPCClient(
			fixture.caller,
			rpc.ToServiceOnNode("player-1", "PlayerService"),
		)
		notifyDone <- client.NotifyPlayerOnline(ctx, 999)
	}); err != nil {
		t.Fatal(err)
	}
	if err := <-notifyDone; err != nil {
		t.Fatalf("queue full Notify immediate error = %v", err)
	}
	close(blocked)
	drained := make(chan struct{})
	for {
		err := fixture.player.DispatchAsync(func(context.Context) {
			close(drained)
		})
		if err == nil {
			break
		}
		if !errors.Is(err, errs.ErrServiceQueueFull) {
			t.Fatalf("等待目标 Service 恢复 error = %v", err)
		}
		time.Sleep(time.Millisecond)
	}
	<-drained
	if got := awaitNATSEcho(t, fixture.caller, "after-overload"); got != "after-overload-echo" {
		t.Fatalf("overload 后连接不可用: %q", got)
	}

	// 显式 Context Deadline 仍只形成一次调用方等待边界和一次目标 M8 Deadline。
	gate := make(chan struct{})
	started := make(chan struct{}, 1)
	if err := fixture.player.DispatchAsync(func(context.Context) {
		fixture.player.Wait = gate
		fixture.player.WaitStarted = started
		configured <- struct{}{}
	}); err != nil {
		t.Fatal(err)
	}
	<-configured
	deadlineResult := make(chan error, 1)
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
		deadlineResult <- callErr
	}); err != nil {
		t.Fatal(err)
	}
	if err := <-deadlineResult; !errors.Is(err, errs.ErrDeadlineExceeded) {
		t.Fatalf("explicit deadline error = %v", err)
	}
	close(gate)
}

func TestNATSRPCGracefulStopWaitsAcceptedRequest(t *testing.T) {
	fixture := newNATSRPCPair(t, service.DefaultSchedulerConfig())
	_ = awaitNATSEcho(t, fixture.caller, "ready")

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
		t.Fatal("NATS 请求没有进入目标业务方法")
	}

	stopDone := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		stopDone <- fixture.callerNode.Stop(ctx)
	}()
	select {
	case err := <-stopDone:
		t.Fatalf("目标尚未完成时 NATS Node.Stop 提前返回: %v", err)
	case <-time.After(30 * time.Millisecond):
	}

	close(gate)
	if err := <-callDone; err != nil {
		t.Fatalf("优雅停止中的 NATS Await error = %v", err)
	}
	if err := <-stopDone; err != nil {
		t.Fatalf("等待已接受 NATS 请求后的 Stop error = %v", err)
	}
}

// natsRPCPair 保存单个测试 Broker 上的两 Node NATS RPC 夹具。
type natsRPCPair struct {
	playerNode *node.Node
	callerNode *node.Node
	player     *PlayerService
	caller     *CallerService
	pool       *bufferpool.Pool
}

// newNATSRPCPair 创建可覆盖自定义目标 Scheduler 容量的真实 NATS RPC 夹具。
func newNATSRPCPair(
	t testing.TB,
	targetScheduler service.SchedulerConfig,
) *natsRPCPair {
	t.Helper()
	running := startRPCNATSServer(t)
	pool := bufferpool.NewPool(bufferpool.Options{TrackUsage: true})
	source := internaldiscovery.NewSource()
	player := &PlayerService{}
	caller := &CallerService{}
	playerNode := newRemoteFixtureNodeWithScheduler(
		t,
		"player-1",
		testNATSRPCConfig(running.ClientURL()),
		targetScheduler,
		pool,
		source,
		node.ServiceBinding{
			Name:     "PlayerService",
			Template: "PlayerService",
			Service:  player,
		},
	)
	callerNode := newRemoteFixtureNode(
		t,
		"gateway-1",
		testNATSRPCConfig(running.ClientURL()),
		pool,
		source,
		node.ServiceBinding{
			Name:     "CallerService",
			Template: "CallerService",
			Service:  caller,
		},
	)
	if err := playerNode.Start(context.Background()); err != nil {
		t.Fatalf("player Node.Start() error = %v", err)
	}
	if err := callerNode.Start(context.Background()); err != nil {
		stopTestNode(t, playerNode)
		t.Fatalf("caller Node.Start() error = %v", err)
	}
	fixture := &natsRPCPair{
		playerNode: playerNode,
		callerNode: callerNode,
		player:     player,
		caller:     caller,
		pool:       pool,
	}
	t.Cleanup(func() {
		stopTestNode(t, fixture.callerNode)
		stopTestNode(t, fixture.playerNode)
		if stats := pool.Stats(); stats.InUseBuffers != 0 {
			t.Errorf("NATS RPC Pair Buffer 未全部归还: %+v", stats)
		}
	})
	return fixture
}

// awaitNATSGetError 执行一次 GetPlayer，并返回生成 Await 接口观察到的稳定终态。
func awaitNATSGetError(
	t testing.TB,
	fixture *natsRPCPair,
	callCtx context.Context,
) error {
	t.Helper()
	done := make(chan error, 1)
	if err := fixture.caller.DispatchAsync(func(ctx context.Context) {
		client := NewPlayerRPCClient(
			fixture.caller,
			rpc.ToServiceOnNode("player-1", "PlayerService"),
		)
		if callCtx == nil {
			callCtx = ctx
		}
		_, _, callErr := client.AwaitGetPlayer(
			callCtx,
			1,
			PlayerData{},
			nil,
		)
		done <- callErr
	}); err != nil {
		t.Fatalf("Caller DispatchAsync() error = %v", err)
	}
	return <-done
}

// awaitNATSEcho 在发现与集群订阅传播的短窗口内重试可恢复的路由错误。
func awaitNATSEcho(
	t testing.TB,
	caller *CallerService,
	value string,
) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		done := make(chan struct {
			value string
			err   error
		}, 1)
		if err := caller.DispatchAsync(func(ctx context.Context) {
			client := NewPlayerRPCClient(
				caller,
				rpc.ToServiceOnNode("player-1", "PlayerService"),
			)
			result, callErr := client.AwaitEchoName(ctx, value)
			done <- struct {
				value string
				err   error
			}{value: result, err: callErr}
		}); err != nil {
			t.Fatalf("Caller DispatchAsync() error = %v", err)
		}
		result := <-done
		if result.err == nil {
			return result.value
		}
		if (!errors.Is(result.err, errs.ErrRPCNoRoute) &&
			!errors.Is(result.err, errs.ErrTransportUnavailable)) ||
			time.Now().After(deadline) {
			t.Fatalf("AwaitEchoName() error = %v", result.err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// discoveryNodeRecord 读取进程内发现源中的一条独立 RawNode 副本。
func discoveryNodeRecord(
	t testing.TB,
	source *internaldiscovery.Source,
	nodeID string,
) internaldiscovery.RawNode {
	t.Helper()
	var result internaldiscovery.RawNode
	subscription, err := source.Subscribe(func(snapshot internaldiscovery.RawSnapshot) error {
		for _, current := range snapshot.Nodes {
			if current.NodeID == nodeID {
				result = current
				break
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}
	subscription.Close()
	if result.NodeID == "" || len(result.Services) == 0 {
		t.Fatalf("未找到 Node %q 发现记录", nodeID)
	}
	return result
}

// testNATSRPCConfig 返回共享同一 Broker 和 Namespace 的 Node 配置。
func testNATSRPCConfig(url string) rpc.Config {
	config := rpc.Config{
		Transport:        rpc.TransportNATS,
		MaxPayloadSize:   rpc.DefaultMaxPayloadSize,
		MaxBroadcastSize: rpc.DefaultMaxBroadcastSize,
		NATS:             rpc.DefaultNATSConfig(),
	}
	config.NATS.Namespace = "game-test"
	config.NATS.URLs = []string{url}
	return config
}

// startRPCNATSServer 启动随机端口嵌入式 NATS Server，并在测试结束时完整回收。
func startRPCNATSServer(t testing.TB) *server.Server {
	t.Helper()
	return startRPCNATSServerWithOptions(t, &server.Options{
		Host:       "127.0.0.1",
		Port:       -1,
		MaxPayload: 8 * 1024 * 1024,
		NoLog:      true,
		NoSigs:     true,
	})
}

// startRPCNATSServerWithOptions 启动调用方给定的隔离 Server 配置。
func startRPCNATSServerWithOptions(
	t testing.TB,
	options *server.Options,
) *server.Server {
	t.Helper()
	running, err := server.NewServer(options)
	if err != nil {
		t.Fatalf("server.NewServer() error = %v", err)
	}
	running.Start()
	if !running.ReadyForConnections(5 * time.Second) {
		running.Shutdown()
		t.Fatal("NATS Server 未就绪")
	}
	t.Cleanup(func() {
		running.Shutdown()
		running.WaitForShutdown()
	})
	return running
}

// startRPCNATSCluster 创建一个全互通的三节点 Core NATS 测试集群。
func startRPCNATSCluster(t testing.TB, count int) []*server.Server {
	t.Helper()
	if count < 2 {
		t.Fatalf("NATS 测试集群节点数必须至少为 2")
	}
	result := make([]*server.Server, 0, count)
	first := startRPCNATSServerWithOptions(t, &server.Options{
		Host:       "127.0.0.1",
		Port:       -1,
		MaxPayload: 8 * 1024 * 1024,
		NoLog:      true,
		NoSigs:     true,
		Cluster: server.ClusterOpts{
			Name: "origin-rpc-test",
			Host: "127.0.0.1",
			Port: -1,
		},
	})
	result = append(result, first)
	route := fmt.Sprintf("nats://127.0.0.1:%d", first.ClusterAddr().Port)
	for index := 1; index < count; index++ {
		result = append(result, startRPCNATSServerWithOptions(t, &server.Options{
			Host:       "127.0.0.1",
			Port:       -1,
			MaxPayload: 8 * 1024 * 1024,
			NoLog:      true,
			NoSigs:     true,
			Cluster: server.ClusterOpts{
				Name: "origin-rpc-test",
				Host: "127.0.0.1",
				Port: -1,
			},
			Routes: server.RoutesFromStr(route),
		}))
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		ready := first.NumRoutes() >= count-1
		for _, current := range result[1:] {
			ready = ready && current.NumRoutes() >= 1
		}
		if ready {
			return result
		}
		if time.Now().After(deadline) {
			t.Fatalf("NATS 测试集群未形成，first routes=%d", first.NumRoutes())
		}
		time.Sleep(10 * time.Millisecond)
	}
}
