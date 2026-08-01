package rpcfixture

import (
	"context"
	"testing"
	"time"

	"github.com/duanhf2012/origin/v3/diagnostics"
	"github.com/duanhf2012/origin/v3/rpc"
	"github.com/duanhf2012/origin/v3/service"
)

// TestLocalAsyncDiagnosticsCompleteAfterCallback 防止 Async 在响应到达或 callback 开始时提前
// 增加 completed；callback 返回前 pending 必须仍保留。
func TestLocalAsyncDiagnosticsCompleteAfterCallback(t *testing.T) {
	fixture := newRPCFixture(t)
	before := fixture.node.Diagnostics().RPC.Local
	during := make(chan diagnostics.RPCTransportSnapshot, 1)
	submit := make(chan error, 1)
	if err := fixture.caller.DispatchAsync(func(ctx context.Context) {
		client := BindPlayerRPC(fixture.caller)
		submit <- client.AsyncEchoName(
			ctx,
			"stats",
			func(context.Context, string, error) {
				during <- fixture.node.Diagnostics().RPC.Local
			},
		)
	}); err != nil {
		t.Fatal(err)
	}
	if err := <-submit; err != nil {
		t.Fatalf("AsyncEchoName() submit error = %v", err)
	}
	inside := <-during
	if inside.OutboundAccepted-before.OutboundAccepted != 1 ||
		inside.Pending-before.Pending != 1 ||
		inside.OutboundCompleted-before.OutboundCompleted != 0 {
		t.Fatalf("stats inside callback before=%+v inside=%+v", before, inside)
	}
	final := waitRPCStats(t, func() diagnostics.RPCTransportSnapshot {
		return fixture.node.Diagnostics().RPC.Local
	}, func(stats diagnostics.RPCTransportSnapshot) bool {
		return stats.OutboundCompleted-before.OutboundCompleted == 1
	})
	if final.Pending != before.Pending ||
		final.InboundAccepted-before.InboundAccepted != 1 ||
		final.InboundCompleted-before.InboundCompleted != 1 {
		t.Fatalf("final Local stats before=%+v final=%+v", before, final)
	}
}

// TestTCPDiagnosticsCountAwaitAsyncAndNotify 使用真实 TCP 连接验证双端固定计数和 Notify 口径。
func TestTCPDiagnosticsCountAwaitAsyncAndNotify(t *testing.T) {
	fixture := newRemoteRPCFixture(t)
	_ = awaitRemoteEcho(t, fixture, "ready")
	callerBefore := fixture.callerNode.Diagnostics().RPC.TCP
	targetBefore := fixture.targetNode.Diagnostics().RPC.TCP
	runDiagnosticCalls(t, fixture.caller, "tcp")

	callerAfter := waitRPCStats(t, func() diagnostics.RPCTransportSnapshot {
		return fixture.callerNode.Diagnostics().RPC.TCP
	}, func(stats diagnostics.RPCTransportSnapshot) bool {
		return stats.OutboundCompleted-callerBefore.OutboundCompleted == 3
	})
	targetAfter := waitRPCStats(t, func() diagnostics.RPCTransportSnapshot {
		return fixture.targetNode.Diagnostics().RPC.TCP
	}, func(stats diagnostics.RPCTransportSnapshot) bool {
		return stats.InboundCompleted-targetBefore.InboundCompleted == 3
	})
	assertRPCDiagnosticDelta(t, "TCP caller", callerBefore, callerAfter, true)
	assertRPCDiagnosticDelta(t, "TCP target", targetBefore, targetAfter, false)
}

// TestNATSDiagnosticsCountAwaitAsyncAndNotify 使用真实 Broker 验证 NATS 与 TCP 采用同一口径。
func TestNATSDiagnosticsCountAwaitAsyncAndNotify(t *testing.T) {
	fixture := newNATSRPCPair(t, service.DefaultSchedulerConfig())
	_ = awaitNATSEcho(t, fixture.caller, "ready")
	callerBefore := fixture.callerNode.Diagnostics().RPC.NATS
	targetBefore := fixture.playerNode.Diagnostics().RPC.NATS
	runDiagnosticCalls(t, fixture.caller, "nats")

	callerAfter := waitRPCStats(t, func() diagnostics.RPCTransportSnapshot {
		return fixture.callerNode.Diagnostics().RPC.NATS
	}, func(stats diagnostics.RPCTransportSnapshot) bool {
		return stats.OutboundCompleted-callerBefore.OutboundCompleted == 3
	})
	targetAfter := waitRPCStats(t, func() diagnostics.RPCTransportSnapshot {
		return fixture.playerNode.Diagnostics().RPC.NATS
	}, func(stats diagnostics.RPCTransportSnapshot) bool {
		return stats.InboundCompleted-targetBefore.InboundCompleted == 3
	})
	assertRPCDiagnosticDelta(t, "NATS caller", callerBefore, callerAfter, true)
	assertRPCDiagnosticDelta(t, "NATS target", targetBefore, targetAfter, false)
}

func runDiagnosticCalls(t *testing.T, caller *CallerService, label string) {
	t.Helper()
	done := make(chan error, 2)
	if err := caller.DispatchAsync(func(ctx context.Context) {
		client := NewPlayerRPCClient(
			caller,
			rpc.ToServiceOnNode("player-1", "PlayerService"),
		)
		_, err := client.AwaitEchoName(ctx, label+"-await")
		if err != nil {
			done <- err
			return
		}
		err = client.AsyncEchoName(
			ctx,
			label+"-async",
			func(_ context.Context, _ string, callbackErr error) { done <- callbackErr },
		)
		if err != nil {
			done <- err
			return
		}
		if err := client.NotifyPlayerOnline(ctx, 42); err != nil {
			done <- err
		}
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("diagnostic RPC calls error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("diagnostic RPC calls timed out")
	}
}

func waitRPCStats(
	t *testing.T,
	read func() diagnostics.RPCTransportSnapshot,
	ready func(diagnostics.RPCTransportSnapshot) bool,
) diagnostics.RPCTransportSnapshot {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		stats := read()
		if ready(stats) {
			return stats
		}
		if time.Now().After(deadline) {
			t.Fatalf("waiting for RPC diagnostics timed out; last=%+v", stats)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func assertRPCDiagnosticDelta(
	t *testing.T,
	name string,
	before diagnostics.RPCTransportSnapshot,
	after diagnostics.RPCTransportSnapshot,
	outbound bool,
) {
	t.Helper()
	if outbound {
		if after.OutboundAccepted-before.OutboundAccepted != 3 ||
			after.OutboundCompleted-before.OutboundCompleted != 3 ||
			after.Pending != before.Pending ||
			after.PayloadSentBytes <= before.PayloadSentBytes ||
			after.PayloadReceivedBytes <= before.PayloadReceivedBytes {
			t.Fatalf("%s stats before=%+v after=%+v", name, before, after)
		}
		return
	}
	if after.InboundAccepted-before.InboundAccepted != 3 ||
		after.InboundCompleted-before.InboundCompleted != 3 ||
		after.PayloadSentBytes <= before.PayloadSentBytes ||
		after.PayloadReceivedBytes <= before.PayloadReceivedBytes {
		t.Fatalf("%s stats before=%+v after=%+v", name, before, after)
	}
}
