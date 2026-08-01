package rpcfixture

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/duanhf2012/origin/v3/errs"
	"github.com/duanhf2012/origin/v3/internal/bufferpool"
	internaldiscovery "github.com/duanhf2012/origin/v3/internal/discovery"
	"github.com/duanhf2012/origin/v3/node"
	"github.com/duanhf2012/origin/v3/rpc"
)

type broadcastIntegrationCluster struct {
	nodes     []*node.Node
	players   []*PlayerService
	caller    *CallerService
	pool      *bufferpool.Pool
	discovery *internaldiscovery.Source
}

func newBroadcastIntegrationCluster(
	t testing.TB,
	firstConfig rpc.Config,
	secondConfig rpc.Config,
	callerConfig rpc.Config,
) *broadcastIntegrationCluster {
	t.Helper()
	pool := bufferpool.NewPool(bufferpool.Options{TrackUsage: true})
	discoverySource := internaldiscovery.NewSource()
	players := []*PlayerService{{EchoSuffix: "player-1"}, {EchoSuffix: "player-2"}}
	caller := &CallerService{}
	nodes := []*node.Node{
		newRemoteFixtureNode(
			t,
			"player-1",
			firstConfig,
			pool,
			discoverySource,
			node.ServiceBinding{
				Name: "PlayerService", Template: "PlayerService", Service: players[0],
			},
		),
		newRemoteFixtureNode(
			t,
			"player-2",
			secondConfig,
			pool,
			discoverySource,
			node.ServiceBinding{
				Name: "PlayerService", Template: "PlayerService", Service: players[1],
			},
		),
		newRemoteFixtureNode(
			t,
			"gateway-1",
			callerConfig,
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
	cluster := &broadcastIntegrationCluster{
		nodes:     nodes,
		players:   players,
		caller:    caller,
		pool:      pool,
		discovery: discoverySource,
	}
	t.Cleanup(func() {
		for index := len(nodes) - 1; index >= 0; index-- {
			stopTestNode(t, nodes[index])
		}
		if stats := pool.Stats(); stats.InUseBuffers != 0 {
			t.Errorf("M20 Broadcast Buffer 未全部归还: %+v", stats)
		}
	})
	return cluster
}

func runBroadcastClient(
	t testing.TB,
	caller *CallerService,
	run func(context.Context, PlayerRPCClient) error,
) error {
	t.Helper()
	done := make(chan error, 1)
	if err := caller.DispatchAsync(func(ctx context.Context) {
		done <- run(ctx, BindPlayerRPC(caller))
	}); err != nil {
		t.Fatalf("Caller DispatchAsync() error = %v", err)
	}
	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("Broadcast 调用超时")
		return errs.ErrDeadlineExceeded
	}
}

func warmBroadcastTargets(t testing.TB, cluster *broadcastIntegrationCluster) {
	t.Helper()
	if err := runBroadcastClient(t, cluster.caller, func(
		ctx context.Context,
		client PlayerRPCClient,
	) error {
		for _, nodeID := range []string{"player-1", "player-2"} {
			if _, err := client.OnNode(nodeID).AwaitEchoName(ctx, "warm"); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("预热 Broadcast 目标 error = %v", err)
	}
}

func setPlayerOnlineID(t testing.TB, player *PlayerService, playerID int64) {
	t.Helper()
	done := make(chan struct{}, 1)
	if err := player.DispatchAsync(func(context.Context) {
		player.OnlineID = playerID
		done <- struct{}{}
	}); err != nil {
		t.Fatalf("重置 PlayerOnline error = %v", err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("重置 PlayerOnline 超时")
	}
}

func awaitPlayerOnlineID(t testing.TB, player *PlayerService, want int64) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		done := make(chan int64, 1)
		if err := player.DispatchAsync(func(context.Context) {
			done <- player.OnlineID
		}); err != nil {
			t.Fatalf("读取 PlayerOnline error = %v", err)
		}
		select {
		case got := <-done:
			if got == want {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("PlayerOnline ID = %d, want %d", got, want)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("读取 PlayerOnline 超时")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func assertPlayerOnlineIDStable(
	t testing.TB,
	player *PlayerService,
	want int64,
	duration time.Duration,
) {
	t.Helper()
	deadline := time.Now().Add(duration)
	for {
		done := make(chan int64, 1)
		if err := player.DispatchAsync(func(context.Context) {
			done <- player.OnlineID
		}); err != nil {
			t.Fatalf("稳定读取 PlayerOnline error = %v", err)
		}
		select {
		case got := <-done:
			if got != want {
				t.Fatalf("PlayerOnline ID = %d, want stable %d", got, want)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("稳定读取 PlayerOnline 超时")
		}
		if time.Now().After(deadline) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func verifyBroadcastRetiredRange(
	t testing.TB,
	cluster *broadcastIntegrationCluster,
) {
	t.Helper()
	firstRecord := discoveryNodeRecord(t, cluster.discovery, "player-1")
	firstRecord.Services[0].State = internaldiscovery.ServiceStateRetired
	if err := cluster.discovery.Publish(firstRecord); err != nil {
		t.Fatalf("Publish(retired) error = %v", err)
	}
	for _, player := range cluster.players {
		setPlayerOnlineID(t, player, 0)
	}

	if err := runBroadcastClient(t, cluster.caller, func(
		ctx context.Context,
		client PlayerRPCClient,
	) error {
		return client.BroadcastPlayerOnline(ctx, 202)
	}); err != nil {
		t.Fatalf("默认 Retired Broadcast error = %v", err)
	}
	awaitPlayerOnlineID(t, cluster.players[1], 202)
	assertPlayerOnlineIDStable(t, cluster.players[0], 0, 100*time.Millisecond)

	if err := runBroadcastClient(t, cluster.caller, func(
		ctx context.Context,
		client PlayerRPCClient,
	) error {
		return client.IncludeRetired().BroadcastPlayerOnline(ctx, 303)
	}); err != nil {
		t.Fatalf("IncludeRetired Broadcast error = %v", err)
	}
	for _, player := range cluster.players {
		awaitPlayerOnlineID(t, player, 303)
	}
}

// TestM20TCPBroadcastFanoutAndPartialFailure 使用真实 TCP 连接验证多目标、Retired 和断线详情。
func TestM20TCPBroadcastFanoutAndPartialFailure(t *testing.T) {
	cluster := newBroadcastIntegrationCluster(
		t,
		testRPCConfig(t),
		testRPCConfig(t),
		testRPCConfig(t),
	)
	warmBroadcastTargets(t, cluster)

	if err := runBroadcastClient(t, cluster.caller, func(
		ctx context.Context,
		client PlayerRPCClient,
	) error {
		return client.RouteBy(nil).BroadcastPlayerOnline(ctx, 101)
	}); err != nil {
		t.Fatalf("TCP Broadcast error = %v", err)
	}
	for _, player := range cluster.players {
		awaitPlayerOnlineID(t, player, 101)
	}

	// 发布一个契约合法但地址不可连接的 Running Node，必须保留为失败意图且不影响两个健康目标。
	ghostConfig := testRPCConfig(t)
	const ghostSessionID = 9901
	if err := cluster.discovery.Publish(internaldiscovery.RawNode{
		NodeID:    "player-3",
		SessionID: ghostSessionID,
		Transport: internaldiscovery.TransportTCP,
		Address:   ghostConfig.TCP.Advertise,
		Services: []internaldiscovery.RawService{{
			ServiceName:         "PlayerService",
			State:               internaldiscovery.ServiceStateRunning,
			ContractID:          uint64(playerRPCContractID),
			ContractFingerprint: [32]byte(playerRPCFingerprint),
		}},
	}); err != nil {
		t.Fatalf("Publish(disconnected) error = %v", err)
	}
	err := runBroadcastClient(t, cluster.caller, func(
		ctx context.Context,
		client PlayerRPCClient,
	) error {
		return client.BroadcastPlayerOnline(ctx, 111)
	})
	var broadcastErr *rpc.BroadcastError
	if !errors.As(err, &broadcastErr) ||
		broadcastErr.Code() != errs.CodeRPCBroadcastPartialFailed ||
		broadcastErr.Total() != 3 || broadcastErr.Succeeded() != 2 ||
		broadcastErr.FailureCount() != 1 {
		t.Fatalf("TCP 部分失败 error = %v", err)
	}
	failure, ok := broadcastErr.Failure(0)
	if !ok || failure.NodeID != "player-3" ||
		!errors.Is(failure.Err, errs.ErrTransportUnavailable) {
		t.Fatalf("TCP 部分失败详情 = %+v, %v", failure, ok)
	}
	for _, player := range cluster.players {
		awaitPlayerOnlineID(t, player, 111)
	}
	if !cluster.discovery.Withdraw("player-3", ghostSessionID) {
		t.Fatal("Withdraw(disconnected) = false")
	}

	verifyBroadcastRetiredRange(t, cluster)
}

// TestM20NATSBroadcastFanoutAndRetired 使用真实 Broker 验证逐 Node Subject 扇出和生命周期范围。
func TestM20NATSBroadcastFanoutAndRetired(t *testing.T) {
	running := startRPCNATSServer(t)
	cluster := newBroadcastIntegrationCluster(
		t,
		testNATSRPCConfig(running.ClientURL()),
		testNATSRPCConfig(running.ClientURL()),
		testNATSRPCConfig(running.ClientURL()),
	)
	warmBroadcastTargets(t, cluster)
	if err := runBroadcastClient(t, cluster.caller, func(
		ctx context.Context,
		client PlayerRPCClient,
	) error {
		return client.BroadcastPlayerOnline(ctx, 401)
	}); err != nil {
		t.Fatalf("NATS Broadcast error = %v", err)
	}
	for _, player := range cluster.players {
		awaitPlayerOnlineID(t, player, 401)
	}
	verifyBroadcastRetiredRange(t, cluster)
}
