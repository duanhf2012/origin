package rpc

import (
	"context"
	"errors"
	"testing"
	"time"

	publicdiscovery "github.com/duanhf2012/origin/v3/discovery"
	"github.com/duanhf2012/origin/v3/errs"
	"github.com/duanhf2012/origin/v3/internal/bufferpool"
	"github.com/duanhf2012/origin/v3/internal/natsnet"
	originlog "github.com/duanhf2012/origin/v3/log"
	"github.com/duanhf2012/origin/v3/service"
)

type prepareTestServiceRuntime struct {
	nodeID      string
	serviceName string
	state       service.State
	rpcRuntime  *Runtime
}

func (runtime *prepareTestServiceRuntime) NodeID() string {
	return runtime.nodeID
}

func (runtime *prepareTestServiceRuntime) ServiceName() string {
	return runtime.serviceName
}

func (runtime *prepareTestServiceRuntime) State() service.State {
	return runtime.state
}

func (*prepareTestServiceRuntime) Logger() originlog.Logger {
	return originlog.NewNop()
}

func (*prepareTestServiceRuntime) LookupService(string) (service.IService, bool) {
	return nil, false
}

func (*prepareTestServiceRuntime) AcquireTimerSlot() (service.TimerID, bool) {
	return 1, true
}

func (*prepareTestServiceRuntime) ReleaseTimerSlot() {}

func (*prepareTestServiceRuntime) TimerLimit() int {
	return 1
}

func (*prepareTestServiceRuntime) TimerLocation() *time.Location {
	return time.UTC
}

func (*prepareTestServiceRuntime) Failure() error {
	return nil
}

func (*prepareTestServiceRuntime) ReportFailure(error) {}

func (runtime *prepareTestServiceRuntime) RPC() any {
	return runtime.rpcRuntime
}

type prepareTestSnapshot struct {
	candidates []RemoteCandidate
}

func (snapshot *prepareTestSnapshot) Len(serviceName string) int {
	count := 0
	for _, candidate := range snapshot.candidates {
		if candidate.ServiceName == serviceName {
			count++
		}
	}
	return count
}

func (snapshot *prepareTestSnapshot) Candidate(
	serviceName string,
	index int,
) (RemoteCandidate, bool) {
	if index < 0 {
		return RemoteCandidate{}, false
	}
	for _, candidate := range snapshot.candidates {
		if candidate.ServiceName != serviceName {
			continue
		}
		if index == 0 {
			return candidate, true
		}
		index--
	}
	return RemoteCandidate{}, false
}

func (snapshot *prepareTestSnapshot) Find(
	nodeID string,
	serviceName string,
) (RemoteCandidate, bool) {
	for _, candidate := range snapshot.candidates {
		if candidate.NodeID == nodeID &&
			candidate.ServiceName == serviceName {
			return candidate, true
		}
	}
	return RemoteCandidate{}, false
}

type prepareTestResolver struct {
	snapshot *prepareTestSnapshot
}

func (resolver *prepareTestResolver) Snapshot() RemoteSnapshot {
	return resolver.snapshot
}

func (resolver *prepareTestResolver) ResolveRemote(
	nodeID string,
	serviceName string,
	contractID ContractID,
	fingerprint ContractFingerprint,
) (RemoteRoute, error) {
	candidate, exists := resolver.snapshot.Find(nodeID, serviceName)
	if !exists {
		return RemoteRoute{}, errs.ErrRPCNoRoute
	}
	if candidate.ContractID != contractID ||
		candidate.Fingerprint != fingerprint {
		return RemoteRoute{}, errs.ErrRPCContractMismatch
	}
	return RemoteRoute{
		NodeID:    candidate.NodeID,
		SessionID: candidate.SessionID,
		Transport: candidate.Transport,
		Address:   candidate.Address,
	}, nil
}

func newPrepareTestRuntime(
	t *testing.T,
	nodeID string,
	transport string,
	snapshot *prepareTestSnapshot,
) *Runtime {
	t.Helper()
	runtime, err := NewRuntime(
		nodeID,
		bufferpool.NewPool(bufferpool.Options{}),
		originlog.NewNop(),
	)
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}
	switch transport {
	case "":
		if err := runtime.Configure(nil); err != nil {
			t.Fatalf("Configure(nil) error = %v", err)
		}
	case TransportTCP:
		config := DefaultConfig()
		config.Transport = TransportTCP
		config.TCP.Listen = "127.0.0.1:21001"
		config.TCP.Advertise = "127.0.0.1:21001"
		if err := runtime.Configure(&config); err != nil {
			t.Fatalf("Configure(TCP) error = %v", err)
		}
	default:
		t.Fatalf("unsupported test transport %q", transport)
	}
	if snapshot != nil {
		if err := runtime.BindRemoteResolver(&prepareTestResolver{
			snapshot: snapshot,
		}); err != nil {
			t.Fatalf("BindRemoteResolver() error = %v", err)
		}
	}
	return runtime
}

func addPrepareTestLocal(
	t *testing.T,
	runtime *Runtime,
	serviceName string,
	state service.State,
	dispatcher Dispatcher,
) {
	t.Helper()
	target := &runtimeTestService{}
	if err := service.BindRuntime(target, &prepareTestServiceRuntime{
		nodeID:      runtime.nodeID,
		serviceName: serviceName,
		state:       state,
		rpcRuntime:  runtime,
	}); err != nil {
		t.Fatalf("BindRuntime() error = %v", err)
	}
	if err := runtime.RegisterServiceVisibility(
		serviceName,
		target,
		dispatcher,
		false,
	); err != nil {
		t.Fatalf("RegisterServiceVisibility() error = %v", err)
	}
}

func addPrepareTestTCPConnection(
	t *testing.T,
	runtime *Runtime,
	nodeID string,
	sessionID uint64,
	address string,
) *outboundSession {
	t.Helper()
	target := newRemoteTarget(runtime.remote, nodeID, sessionID, address)
	session := newOutboundSession(runtime.remote, nodeID, sessionID)
	target.current.Store(session)
	runtime.remote.mu.Lock()
	runtime.remote.targets[nodeID] = target
	runtime.remote.publishTargetsLocked()
	runtime.remote.mu.Unlock()
	return session
}

func prepareTestClient(runtime *Runtime, target Target) Client {
	return Client{
		owner:       &runtimeTestService{},
		runtime:     runtime,
		target:      target,
		contractID:  1,
		fingerprint: runtimeTestFingerprint,
	}
}

func TestPrepareNotifySelectsRunningLocalPrivateService(t *testing.T) {
	runtime := newPrepareTestRuntime(t, "gateway-1", "", nil)
	addPrepareTestLocal(
		t,
		runtime,
		"PlayerService",
		service.StateRunning,
		&runtimeTestDispatcher{},
	)
	if err := runtime.Freeze(); err != nil {
		t.Fatalf("Freeze() error = %v", err)
	}

	prepared, err := prepareTestClient(
		runtime,
		ToService("PlayerService"),
	).PrepareNotify(context.Background(), 1)
	if err != nil {
		t.Fatalf("PrepareNotify() error = %v", err)
	}
	if prepared.prepared.transport != preparedLocal ||
		prepared.prepared.nodeID != "gateway-1" ||
		prepared.prepared.serviceName != "PlayerService" {
		t.Fatalf("prepared target = %+v", prepared.prepared)
	}
}

func TestPrepareNotifyRoundRobinUsesRuntimeStateAcrossClients(t *testing.T) {
	snapshot := &prepareTestSnapshot{candidates: []RemoteCandidate{
		{
			NodeID:      "player-1",
			SessionID:   51,
			ServiceName: "PlayerService",
			State:       publicdiscovery.StateRunning,
			Transport:   TransportTCP,
			Address:     "127.0.0.1:22001",
			ContractID:  1,
			Fingerprint: runtimeTestFingerprint,
		},
		{
			NodeID:      "player-2",
			SessionID:   52,
			ServiceName: "PlayerService",
			State:       publicdiscovery.StateRunning,
			Transport:   TransportTCP,
			Address:     "127.0.0.1:22002",
			ContractID:  1,
			Fingerprint: runtimeTestFingerprint,
		},
	}}
	runtime := newPrepareTestRuntime(t, "gateway-1", TransportTCP, snapshot)
	addPrepareTestTCPConnection(t, runtime, "player-1", 51, "127.0.0.1:22001")
	addPrepareTestTCPConnection(t, runtime, "player-2", 52, "127.0.0.1:22002")
	if err := runtime.Freeze(); err != nil {
		t.Fatalf("Freeze() error = %v", err)
	}

	firstClient := prepareTestClient(runtime, ToService("PlayerService"))
	secondClient := prepareTestClient(runtime, ToService("PlayerService"))
	var got []string
	for _, client := range []Client{firstClient, secondClient, firstClient} {
		prepared, err := client.PrepareNotify(context.Background(), 1)
		if err != nil {
			t.Fatalf("PrepareNotify() error = %v", err)
		}
		got = append(got, prepared.prepared.nodeID)
	}
	want := []string{"player-1", "player-2", "player-1"}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("round robin = %v, want %v", got, want)
		}
	}
}

func TestPrepareNotifyKeyAndExactRetiredBoundaries(t *testing.T) {
	snapshot := &prepareTestSnapshot{candidates: []RemoteCandidate{
		{
			NodeID:      "player-1",
			SessionID:   61,
			ServiceName: "PlayerService",
			State:       publicdiscovery.StateRetired,
			Transport:   TransportTCP,
			Address:     "127.0.0.1:23001",
			ContractID:  1,
			Fingerprint: runtimeTestFingerprint,
		},
		{
			NodeID:      "player-2",
			SessionID:   62,
			ServiceName: "PlayerService",
			State:       publicdiscovery.StateRunning,
			Transport:   TransportTCP,
			Address:     "127.0.0.1:23002",
			ContractID:  1,
			Fingerprint: runtimeTestFingerprint,
		},
	}}
	runtime := newPrepareTestRuntime(t, "gateway-1", TransportTCP, snapshot)
	addPrepareTestTCPConnection(t, runtime, "player-1", 61, "127.0.0.1:23001")
	addPrepareTestTCPConnection(t, runtime, "player-2", 62, "127.0.0.1:23002")
	if err := runtime.Freeze(); err != nil {
		t.Fatalf("Freeze() error = %v", err)
	}

	auto, err := prepareTestClient(
		runtime,
		ToService("PlayerService"),
	).Route(uint64(0)).PrepareNotify(context.Background(), 1)
	if err != nil {
		t.Fatalf("auto PrepareNotify() error = %v", err)
	}
	if auto.prepared.nodeID != "player-2" {
		t.Fatalf("auto selected %q", auto.prepared.nodeID)
	}

	exact, err := prepareTestClient(
		runtime,
		ToServiceOnNode("player-1", "PlayerService"),
	).PrepareNotify(context.Background(), 1)
	if err != nil {
		t.Fatalf("exact PrepareNotify() error = %v", err)
	}
	if exact.prepared.nodeID != "player-1" {
		t.Fatalf("exact selected %q", exact.prepared.nodeID)
	}
}

type prepareTestSelector struct {
	region string
}

func (selector prepareTestSelector) Select(candidates RouteCandidates) (int, bool) {
	for index := 0; index < candidates.Len(); index++ {
		region, ok := candidates.Label(index, "region")
		if ok && region == selector.region {
			return index, true
		}
	}
	return 0, false
}

type panicPrepareTestSelector struct{}

func (panicPrepareTestSelector) Select(RouteCandidates) (int, bool) {
	panic("selector panic")
}

func TestPrepareNotifyCustomSelectorReadsImmutableCandidateView(t *testing.T) {
	snapshot := &prepareTestSnapshot{candidates: []RemoteCandidate{
		{
			NodeID:      "player-1",
			SessionID:   71,
			ServiceName: "PlayerService",
			State:       publicdiscovery.StateRunning,
			Labels:      map[string]string{"region": "west"},
			Transport:   TransportTCP,
			Address:     "127.0.0.1:24001",
			ContractID:  1,
			Fingerprint: runtimeTestFingerprint,
		},
		{
			NodeID:      "player-2",
			SessionID:   72,
			ServiceName: "PlayerService",
			State:       publicdiscovery.StateRunning,
			Labels:      map[string]string{"region": "east"},
			Transport:   TransportTCP,
			Address:     "127.0.0.1:24002",
			ContractID:  1,
			Fingerprint: runtimeTestFingerprint,
		},
	}}
	runtime := newPrepareTestRuntime(t, "gateway-1", TransportTCP, snapshot)
	addPrepareTestTCPConnection(t, runtime, "player-1", 71, "127.0.0.1:24001")
	addPrepareTestTCPConnection(t, runtime, "player-2", 72, "127.0.0.1:24002")
	if err := runtime.Freeze(); err != nil {
		t.Fatalf("Freeze() error = %v", err)
	}

	prepared, err := prepareTestClient(
		runtime,
		ToService("PlayerService"),
	).RouteBy(prepareTestSelector{region: "east"}).
		PrepareNotify(context.Background(), 1)
	if err != nil {
		t.Fatalf("PrepareNotify() error = %v", err)
	}
	if prepared.prepared.nodeID != "player-2" {
		t.Fatalf("selected %q", prepared.prepared.nodeID)
	}

	_, err = prepareTestClient(
		runtime,
		ToService("PlayerService"),
	).RouteBy(panicPrepareTestSelector{}).
		PrepareNotify(context.Background(), 1)
	if !errors.Is(err, errs.ErrRPCRouteSelectorFailed) {
		t.Fatalf("panic selector error = %v", err)
	}
}

func TestPrepareNotifyClassifiesCandidateFailures(t *testing.T) {
	tests := []struct {
		name       string
		candidates []RemoteCandidate
		transport  string
		want       error
	}{
		{
			name: "no service",
			want: errs.ErrRPCNoRoute,
		},
		{
			name: "contract mismatch",
			candidates: []RemoteCandidate{{
				NodeID:      "player-1",
				SessionID:   81,
				ServiceName: "PlayerService",
				State:       publicdiscovery.StateRunning,
				Transport:   TransportTCP,
				Address:     "127.0.0.1:25001",
				ContractID:  2,
				Fingerprint: ContractFingerprint{2},
			}},
			transport: TransportTCP,
			want:      errs.ErrRPCContractMismatch,
		},
		{
			name: "retired only",
			candidates: []RemoteCandidate{{
				NodeID:      "player-1",
				SessionID:   82,
				ServiceName: "PlayerService",
				State:       publicdiscovery.StateRetired,
				Transport:   TransportTCP,
				Address:     "127.0.0.1:25002",
				ContractID:  1,
				Fingerprint: runtimeTestFingerprint,
			}},
			transport: TransportTCP,
			want:      errs.ErrRPCNoRoute,
		},
		{
			name: "transport incompatible",
			candidates: []RemoteCandidate{{
				NodeID:      "player-1",
				SessionID:   83,
				ServiceName: "PlayerService",
				State:       publicdiscovery.StateRunning,
				Transport:   TransportTCP,
				Address:     "127.0.0.1:25003",
				ContractID:  1,
				Fingerprint: runtimeTestFingerprint,
			}},
			want: errs.ErrTransportUnavailable,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			snapshot := &prepareTestSnapshot{candidates: test.candidates}
			runtime := newPrepareTestRuntime(
				t,
				"gateway-1",
				test.transport,
				snapshot,
			)
			if err := runtime.Freeze(); err != nil {
				t.Fatalf("Freeze() error = %v", err)
			}
			_, err := prepareTestClient(
				runtime,
				ToService("PlayerService"),
			).PrepareNotify(context.Background(), 1)
			if !errors.Is(err, test.want) {
				t.Fatalf("PrepareNotify() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestNATSDisconnectedClearsRouteConnectionImmediately(t *testing.T) {
	owner := newPrepareTestRuntime(t, "gateway-1", "", nil)
	runtime := newNATSRuntime(owner, DefaultConfig())
	runtime.mu.Lock()
	runtime.generation = 91
	runtime.started = true
	runtime.mu.Unlock()
	runtime.activeConnection.Store(&natsConnectionView{generation: 91})
	signal := owner.routeChangeSignal()

	runtime.handleGenerationEvent(91, natsnet.Event{
		Type: natsnet.EventDisconnected,
	})

	if runtime.activeConnection.Load() != nil {
		t.Fatal("Disconnected 后 NATS 仍在路由候选中")
	}
	select {
	case <-signal:
	default:
		t.Fatal("Disconnected 没有唤醒路由等待者")
	}
}
