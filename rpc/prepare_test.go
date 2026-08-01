package rpc

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
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

type atomicPrepareTestResolver struct {
	snapshot atomic.Pointer[prepareTestSnapshot]
}

func (resolver *atomicPrepareTestResolver) Snapshot() RemoteSnapshot {
	return resolver.snapshot.Load()
}

func (resolver *atomicPrepareTestResolver) ResolveRemote(
	nodeID string,
	serviceName string,
	contractID ContractID,
	fingerprint ContractFingerprint,
) (RemoteRoute, error) {
	snapshot := resolver.snapshot.Load()
	candidate, exists := snapshot.Find(nodeID, serviceName)
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
	t testing.TB,
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
	t testing.TB,
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
	t testing.TB,
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

	restored := append([]RemoteCandidate(nil), snapshot.candidates...)
	restored[0].State = publicdiscovery.StateRunning
	runtime.remoteResolver.(*prepareTestResolver).snapshot =
		&prepareTestSnapshot{candidates: restored}
	recovered, err := prepareTestClient(
		runtime,
		ToService("PlayerService"),
	).Route(uint64(0)).PrepareNotify(context.Background(), 1)
	if err != nil {
		t.Fatalf("restored PrepareNotify() error = %v", err)
	}
	if recovered.prepared.nodeID != "player-1" {
		t.Fatalf("restored selected %q", recovered.prepared.nodeID)
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

type fixedPrepareTestSelector struct {
	index int
	ok    bool
}

func (selector fixedPrepareTestSelector) Select(RouteCandidates) (int, bool) {
	return selector.index, selector.ok
}

type inspectingPrepareTestSelector struct {
	length      int
	nodeID      string
	serviceName string
	state       publicdiscovery.State
	region      string
	labelOK     bool
}

func (selector *inspectingPrepareTestSelector) Select(
	candidates RouteCandidates,
) (int, bool) {
	selector.length = candidates.Len()
	selector.nodeID = candidates.NodeID(1)
	selector.serviceName = candidates.ServiceName(1)
	selector.state = candidates.State(1)
	selector.region, selector.labelOK = candidates.Label(1, "region")
	return 1, true
}

type connectEarlierPrepareTestSelector struct {
	t          *testing.T
	target     *remoteTarget
	session    *outboundSession
	seenNodeID string
}

func (selector *connectEarlierPrepareTestSelector) Select(
	candidates RouteCandidates,
) (int, bool) {
	selector.t.Helper()
	if candidates.Len() != 1 {
		selector.t.Fatalf("candidate count = %d", candidates.Len())
	}
	selector.seenNodeID = candidates.NodeID(0)
	selector.target.current.Store(selector.session)
	return 0, true
}

func TestPrepareNotifyKeepsCandidateIdentityWhenEarlierNodeConnects(t *testing.T) {
	candidates := []RemoteCandidate{
		{
			NodeID:      "player-1",
			SessionID:   69,
			ServiceName: "PlayerService",
			State:       publicdiscovery.StateRunning,
			Transport:   TransportTCP,
			Address:     "127.0.0.1:23999",
			ContractID:  1,
			Fingerprint: runtimeTestFingerprint,
		},
		{
			NodeID:      "player-2",
			SessionID:   70,
			ServiceName: "PlayerService",
			State:       publicdiscovery.StateRunning,
			Transport:   TransportTCP,
			Address:     "127.0.0.1:24000",
			ContractID:  1,
			Fingerprint: runtimeTestFingerprint,
		},
	}
	runtime := newPrepareTestRuntime(
		t,
		"gateway-1",
		TransportTCP,
		&prepareTestSnapshot{candidates: candidates},
	)
	disconnected, recoveredSession := addPrepareTestDisconnectedTCP(
		runtime,
		"player-1",
		69,
		"127.0.0.1:23999",
	)
	addPrepareTestTCPConnection(
		t,
		runtime,
		"player-2",
		70,
		"127.0.0.1:24000",
	)
	if err := runtime.Freeze(); err != nil {
		t.Fatalf("Freeze() error = %v", err)
	}
	selector := &connectEarlierPrepareTestSelector{
		t:       t,
		target:  disconnected,
		session: recoveredSession,
	}

	prepared, err := prepareTestClient(
		runtime,
		ToService("PlayerService"),
	).RouteBy(selector).PrepareNotify(context.Background(), 1)
	if err != nil {
		t.Fatalf("PrepareNotify() error = %v", err)
	}
	if selector.seenNodeID != "player-2" {
		t.Fatalf("selector saw %q", selector.seenNodeID)
	}
	if prepared.prepared.nodeID != selector.seenNodeID {
		t.Fatalf(
			"prepared node = %q, selector saw %q",
			prepared.prepared.nodeID,
			selector.seenNodeID,
		)
	}
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

	inspector := &inspectingPrepareTestSelector{}
	prepared, err = prepareTestClient(
		runtime,
		ToService("PlayerService"),
	).RouteBy(inspector).PrepareNotify(context.Background(), 1)
	if err != nil {
		t.Fatalf("inspecting selector error = %v", err)
	}
	if prepared.prepared.nodeID != "player-2" ||
		inspector.length != 2 ||
		inspector.nodeID != "player-2" ||
		inspector.serviceName != "PlayerService" ||
		inspector.state != publicdiscovery.StateRunning ||
		!inspector.labelOK ||
		inspector.region != "east" {
		t.Fatalf("selector observation = %+v", inspector)
	}

	_, err = prepareTestClient(
		runtime,
		ToService("PlayerService"),
	).RouteBy(panicPrepareTestSelector{}).
		PrepareNotify(context.Background(), 1)
	if !errors.Is(err, errs.ErrRPCRouteSelectorFailed) {
		t.Fatalf("panic selector error = %v", err)
	}

	_, err = prepareTestClient(
		runtime,
		ToService("PlayerService"),
	).RouteBy(fixedPrepareTestSelector{index: 999, ok: false}).
		PrepareNotify(context.Background(), 1)
	if !errors.Is(err, errs.ErrRPCNoRoute) {
		t.Fatalf("reject selector error = %v", err)
	}

	_, err = prepareTestClient(
		runtime,
		ToService("PlayerService"),
	).RouteBy(fixedPrepareTestSelector{index: 999, ok: true}).
		PrepareNotify(context.Background(), 1)
	if !errors.Is(err, errs.ErrRPCRouteSelectorFailed) {
		t.Fatalf("invalid selector index error = %v", err)
	}

	_, err = prepareTestClient(
		runtime,
		ToService("PlayerService"),
	).RouteBy(nil).PrepareNotify(context.Background(), 1)
	if !errors.Is(err, errs.ErrRPCRouteSelectorFailed) {
		t.Fatalf("nil selector error = %v", err)
	}
}

func TestPrepareNotifyKeyIsStableAndUsesCurrentCandidateCount(t *testing.T) {
	candidates := []RemoteCandidate{
		{
			NodeID:      "player-1",
			SessionID:   91,
			ServiceName: "PlayerService",
			State:       publicdiscovery.StateRunning,
			Transport:   TransportTCP,
			Address:     "127.0.0.1:26001",
			ContractID:  1,
			Fingerprint: runtimeTestFingerprint,
		},
		{
			NodeID:      "player-2",
			SessionID:   92,
			ServiceName: "PlayerService",
			State:       publicdiscovery.StateRunning,
			Transport:   TransportTCP,
			Address:     "127.0.0.1:26002",
			ContractID:  1,
			Fingerprint: runtimeTestFingerprint,
		},
		{
			NodeID:      "player-3",
			SessionID:   93,
			ServiceName: "PlayerService",
			State:       publicdiscovery.StateRunning,
			Transport:   TransportTCP,
			Address:     "127.0.0.1:26003",
			ContractID:  1,
			Fingerprint: runtimeTestFingerprint,
		},
	}
	snapshot := &prepareTestSnapshot{candidates: candidates}
	runtime := newPrepareTestRuntime(t, "gateway-1", TransportTCP, snapshot)
	for _, candidate := range candidates {
		addPrepareTestTCPConnection(
			t,
			runtime,
			candidate.NodeID,
			candidate.SessionID,
			candidate.Address,
		)
	}
	if err := runtime.Freeze(); err != nil {
		t.Fatalf("Freeze() error = %v", err)
	}
	client := prepareTestClient(
		runtime,
		ToService("PlayerService"),
	).Route(uint64(4))
	for index := 0; index < 2; index++ {
		prepared, err := client.PrepareNotify(context.Background(), 1)
		if err != nil {
			t.Fatalf("PrepareNotify() error = %v", err)
		}
		if prepared.prepared.nodeID != "player-2" {
			t.Fatalf("stable key selected %q", prepared.prepared.nodeID)
		}
	}

	runtime.remoteResolver.(*prepareTestResolver).snapshot =
		&prepareTestSnapshot{candidates: candidates[:2]}
	resized, err := client.PrepareNotify(context.Background(), 1)
	if err != nil {
		t.Fatalf("resized PrepareNotify() error = %v", err)
	}
	if resized.prepared.nodeID != "player-1" {
		t.Fatalf("resized key selected %q", resized.prepared.nodeID)
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

type countingPrepareTestSelector struct {
	calls int
}

func (selector *countingPrepareTestSelector) Select(
	RouteCandidates,
) (int, bool) {
	selector.calls++
	return 0, true
}

func TestPreparedNotifyRejectsSessionReplacementWithoutReselect(t *testing.T) {
	first := RemoteCandidate{
		NodeID:      "player-1",
		SessionID:   101,
		ServiceName: "PlayerService",
		State:       publicdiscovery.StateRunning,
		Transport:   TransportTCP,
		Address:     "127.0.0.1:27001",
		ContractID:  1,
		Fingerprint: runtimeTestFingerprint,
	}
	snapshot := &prepareTestSnapshot{candidates: []RemoteCandidate{first}}
	runtime := newPrepareTestRuntime(t, "gateway-1", TransportTCP, snapshot)
	addPrepareTestTCPConnection(t, runtime, "player-1", 101, first.Address)
	if err := runtime.Freeze(); err != nil {
		t.Fatalf("Freeze() error = %v", err)
	}
	selector := &countingPrepareTestSelector{}
	prepared, err := prepareTestClient(
		runtime,
		ToService("PlayerService"),
	).RouteBy(selector).PrepareNotify(context.Background(), 1)
	if err != nil {
		t.Fatalf("PrepareNotify() error = %v", err)
	}
	request, err := prepared.AllocateRequest(0, CallNotify)
	if err != nil {
		t.Fatalf("AllocateRequest() error = %v", err)
	}
	if got := request.Headroom(); got != wireNotifyFixedSize+len("PlayerService") {
		request.Release()
		t.Fatalf("headroom = %d", got)
	}

	second := first
	second.SessionID = 102
	runtime.remoteResolver.(*prepareTestResolver).snapshot =
		&prepareTestSnapshot{candidates: []RemoteCandidate{second}}
	runtime.remote.mu.Lock()
	runtime.remote.targets = make(map[string]*remoteTarget)
	runtime.remote.publishTargetsLocked()
	runtime.remote.mu.Unlock()

	err = prepared.Notify(context.Background(), 1, request)
	if !errors.Is(err, errs.ErrRPCNoRoute) {
		t.Fatalf("Notify() error = %v", err)
	}
	if selector.calls != 1 {
		t.Fatalf("selector calls = %d", selector.calls)
	}
}

func TestPreparedNotifyDoesNotReselectAfterDisconnect(t *testing.T) {
	candidates := []RemoteCandidate{
		{
			NodeID:      "player-1",
			SessionID:   103,
			ServiceName: "PlayerService",
			State:       publicdiscovery.StateRunning,
			Transport:   TransportTCP,
			Address:     "127.0.0.1:27003",
			ContractID:  1,
			Fingerprint: runtimeTestFingerprint,
		},
		{
			NodeID:      "player-2",
			SessionID:   104,
			ServiceName: "PlayerService",
			State:       publicdiscovery.StateRunning,
			Transport:   TransportTCP,
			Address:     "127.0.0.1:27004",
			ContractID:  1,
			Fingerprint: runtimeTestFingerprint,
		},
	}
	runtime := newPrepareTestRuntime(
		t,
		"gateway-1",
		TransportTCP,
		&prepareTestSnapshot{candidates: candidates},
	)
	for _, candidate := range candidates {
		addPrepareTestTCPConnection(
			t,
			runtime,
			candidate.NodeID,
			candidate.SessionID,
			candidate.Address,
		)
	}
	if err := runtime.Freeze(); err != nil {
		t.Fatalf("Freeze() error = %v", err)
	}
	selector := &countingPrepareTestSelector{}
	prepared, err := prepareTestClient(
		runtime,
		ToService("PlayerService"),
	).RouteBy(selector).PrepareNotify(context.Background(), 1)
	if err != nil {
		t.Fatalf("PrepareNotify() error = %v", err)
	}
	if prepared.prepared.nodeID != "player-1" {
		t.Fatalf("selected %q", prepared.prepared.nodeID)
	}
	request, err := prepared.AllocateRequest(0, CallNotify)
	if err != nil {
		t.Fatalf("AllocateRequest() error = %v", err)
	}
	runtime.remote.targets["player-1"].current.Store(nil)
	runtime.remote.publishTargetSession(
		runtime.remote.targets["player-1"],
		nil,
	)

	err = prepared.Notify(context.Background(), 1, request)
	if !errors.Is(err, errs.ErrTransportUnavailable) {
		t.Fatalf("Notify() error = %v", err)
	}
	if selector.calls != 1 {
		t.Fatalf("selector calls = %d", selector.calls)
	}
}

type prepareAwaitTestOwner struct {
	service.Service
	awaitCalls atomic.Int64
	entered    chan struct{}
}

func (owner *prepareAwaitTestOwner) Await(
	ctx context.Context,
	fn func(context.Context) error,
) error {
	owner.awaitCalls.Add(1)
	if owner.entered != nil {
		close(owner.entered)
	}
	return fn(ctx)
}

func addPrepareTestDisconnectedTCP(
	runtime *Runtime,
	nodeID string,
	sessionID uint64,
	address string,
) (*remoteTarget, *outboundSession) {
	target := newRemoteTarget(runtime.remote, nodeID, sessionID, address)
	session := newOutboundSession(runtime.remote, nodeID, sessionID)
	runtime.remote.mu.Lock()
	runtime.remote.targets[nodeID] = target
	runtime.remote.publishTargetsLocked()
	runtime.remote.mu.Unlock()
	return target, session
}

func TestPrepareAwaitWaitsForConnectedRouteEvent(t *testing.T) {
	candidate := RemoteCandidate{
		NodeID:      "player-1",
		SessionID:   111,
		ServiceName: "PlayerService",
		State:       publicdiscovery.StateRunning,
		Transport:   TransportTCP,
		Address:     "127.0.0.1:28001",
		ContractID:  1,
		Fingerprint: runtimeTestFingerprint,
	}
	runtime := newPrepareTestRuntime(
		t,
		"gateway-1",
		TransportTCP,
		&prepareTestSnapshot{candidates: []RemoteCandidate{candidate}},
	)
	target, session := addPrepareTestDisconnectedTCP(
		runtime,
		candidate.NodeID,
		candidate.SessionID,
		candidate.Address,
	)
	if err := runtime.Freeze(); err != nil {
		t.Fatalf("Freeze() error = %v", err)
	}
	owner := &prepareAwaitTestOwner{entered: make(chan struct{})}
	client := prepareTestClient(runtime, ToService("PlayerService"))
	client.owner = owner
	type result struct {
		client Client
		err    error
	}
	done := make(chan result, 1)
	go func() {
		prepared, err := client.PrepareAwait(context.Background(), 1)
		done <- result{client: prepared, err: err}
	}()

	select {
	case <-owner.entered:
	case <-time.After(time.Second):
		t.Fatal("PrepareAwait 没有进入连接等待")
	}
	target.current.Store(session)
	runtime.remote.publishTargetSession(target, session)

	select {
	case current := <-done:
		if current.err != nil {
			t.Fatalf("PrepareAwait() error = %v", current.err)
		}
		if current.client.prepared.tcpSession != session {
			t.Fatalf("prepared session = %p", current.client.prepared.tcpSession)
		}
	case <-time.After(time.Second):
		t.Fatal("连接事件没有唤醒 PrepareAwait")
	}
	if calls := owner.awaitCalls.Load(); calls != 1 {
		t.Fatalf("Await calls = %d", calls)
	}
}

func TestPrepareAsyncDoesNotWaitForDisconnectedRoute(t *testing.T) {
	candidate := RemoteCandidate{
		NodeID:      "player-1",
		SessionID:   121,
		ServiceName: "PlayerService",
		State:       publicdiscovery.StateRunning,
		Transport:   TransportTCP,
		Address:     "127.0.0.1:29001",
		ContractID:  1,
		Fingerprint: runtimeTestFingerprint,
	}
	runtime := newPrepareTestRuntime(
		t,
		"gateway-1",
		TransportTCP,
		&prepareTestSnapshot{candidates: []RemoteCandidate{candidate}},
	)
	addPrepareTestDisconnectedTCP(
		runtime,
		candidate.NodeID,
		candidate.SessionID,
		candidate.Address,
	)
	if err := runtime.Freeze(); err != nil {
		t.Fatalf("Freeze() error = %v", err)
	}
	owner := &prepareAwaitTestOwner{}
	client := prepareTestClient(runtime, ToService("PlayerService"))
	client.owner = owner

	_, err := client.PrepareAsync(context.Background(), 1)
	if !errors.Is(err, errs.ErrTransportUnavailable) {
		t.Fatalf("PrepareAsync() error = %v", err)
	}
	if calls := owner.awaitCalls.Load(); calls != 0 {
		t.Fatalf("Await calls = %d", calls)
	}
}

func TestPrepareConcurrentSnapshotAndConnectionChanges(t *testing.T) {
	runningCandidate := RemoteCandidate{
		NodeID:      "player-1",
		SessionID:   131,
		ServiceName: "PlayerService",
		State:       publicdiscovery.StateRunning,
		Transport:   TransportTCP,
		Address:     "127.0.0.1:29001",
		ContractID:  1,
		Fingerprint: runtimeTestFingerprint,
	}
	retiredCandidate := runningCandidate
	retiredCandidate.State = publicdiscovery.StateRetired
	runningSnapshot := &prepareTestSnapshot{
		candidates: []RemoteCandidate{runningCandidate},
	}
	retiredSnapshot := &prepareTestSnapshot{
		candidates: []RemoteCandidate{retiredCandidate},
	}
	resolver := &atomicPrepareTestResolver{}
	resolver.snapshot.Store(runningSnapshot)
	runtime := newPrepareTestRuntime(t, "gateway-1", TransportTCP, nil)
	if err := runtime.BindRemoteResolver(resolver); err != nil {
		t.Fatalf("BindRemoteResolver() error = %v", err)
	}
	session := addPrepareTestTCPConnection(
		t,
		runtime,
		runningCandidate.NodeID,
		runningCandidate.SessionID,
		runningCandidate.Address,
	)
	target := runtime.remote.targets[runningCandidate.NodeID]
	if err := runtime.Freeze(); err != nil {
		t.Fatalf("Freeze() error = %v", err)
	}
	client := prepareTestClient(runtime, ToService("PlayerService"))

	const iterations = 2000
	var wait sync.WaitGroup
	wait.Add(3)
	go func() {
		defer wait.Done()
		for index := 0; index < iterations; index++ {
			if index%2 == 0 {
				target.current.Store(session)
				runtime.remote.publishTargetSession(target, session)
				resolver.snapshot.Store(runningSnapshot)
			} else {
				target.current.Store(nil)
				runtime.remote.publishTargetSession(target, nil)
				resolver.snapshot.Store(retiredSnapshot)
			}
		}
	}()
	for reader := 0; reader < 2; reader++ {
		go func() {
			defer wait.Done()
			for index := 0; index < iterations; index++ {
				_, err := client.PrepareNotify(context.Background(), 1)
				if err != nil &&
					!errors.Is(err, errs.ErrRPCNoRoute) &&
					!errors.Is(err, errs.ErrTransportUnavailable) {
					t.Errorf("PrepareNotify() error = %v", err)
					return
				}
			}
		}()
	}
	wait.Wait()
}

func TestPrepareNotifyRunningLocalDoesNotAllocate(t *testing.T) {
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
	base := prepareTestClient(runtime, ToService("PlayerService"))
	tests := []struct {
		name   string
		client Client
	}{
		{name: "default", client: base},
		{name: "round-robin", client: base.RouteRoundRobin()},
		{name: "random", client: base.RouteRandom()},
		{name: "integer-key", client: base.Route(uint64(42))},
		{name: "string-key", client: base.Route("player")},
		{
			name: "custom",
			client: base.RouteBy(
				fixedPrepareTestSelector{index: 0, ok: true},
			),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := test.client.PrepareNotify(
				context.Background(),
				1,
			); err != nil {
				t.Fatalf("warm PrepareNotify() error = %v", err)
			}
			allocations := testing.AllocsPerRun(1000, func() {
				if _, err := test.client.PrepareNotify(
					context.Background(),
					1,
				); err != nil {
					panic(err)
				}
			})
			if allocations != 0 {
				t.Fatalf("PrepareNotify allocations = %v", allocations)
			}
		})
	}
}
