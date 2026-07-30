package rpc

import (
	"context"
	"sync/atomic"

	publicdiscovery "github.com/duanhf2012/origin/v3/discovery"
	"github.com/duanhf2012/origin/v3/errs"
	"github.com/duanhf2012/origin/v3/internal/natsnet"
	"github.com/duanhf2012/origin/v3/service"
)

type preparedTransport uint8

const (
	preparedInvalid preparedTransport = iota
	preparedLocal
	preparedTCP
	preparedNATS
)

type preparedTarget struct {
	transport   preparedTransport
	nodeID      string
	sessionID   uint64
	serviceName string
	methodID    MethodID
	kind        CallKind
	endpoint    serviceEndpoint
	tcpSession  *outboundSession
	natsView    *natsConnectionView
}

type routeCandidate struct {
	nodeID      string
	sessionID   uint64
	serviceName string
	state       publicdiscovery.State
	labels      map[string]string
	transport   preparedTransport
	address     string
	contractID  ContractID
	fingerprint ContractFingerprint
	endpoint    serviceEndpoint
	tcpSession  *outboundSession
	natsView    *natsConnectionView
}

type candidateScan struct {
	sameName  bool
	contract  bool
	lifecycle bool
	transport bool
	connected bool
}

type candidateSet struct {
	runtime     *Runtime
	target      Target
	contractID  ContractID
	fingerprint ContractFingerprint
	snapshot    RemoteSnapshot
	tcpView     *remoteTargetTable
	natsView    *natsConnectionView

	local       serviceEndpoint
	hasLocal    bool
	localInsert int
	remoteLen   int
	exact       bool

	count int
	scan  candidateScan
}

type routeCounterKey struct {
	serviceName string
	contractID  ContractID
	fingerprint ContractFingerprint
}

func (runtime *Runtime) prepareNotify(
	ctx context.Context,
	client Client,
	methodID MethodID,
) (preparedTarget, error) {
	prepared, err, _ := runtime.prepareOnce(
		ctx,
		client,
		methodID,
		CallNotify,
	)
	return prepared, err
}

func (runtime *Runtime) prepareAsync(
	ctx context.Context,
	client Client,
	methodID MethodID,
) (preparedTarget, error) {
	prepared, err, _ := runtime.prepareOnce(
		ctx,
		client,
		methodID,
		CallRequest,
	)
	return prepared, err
}

func (runtime *Runtime) prepareAwait(
	ctx context.Context,
	client Client,
	methodID MethodID,
) (preparedTarget, error) {
	signal := runtime.routeChangeSignal()
	prepared, err, waitable := runtime.prepareOnce(
		ctx,
		client,
		methodID,
		CallRequest,
	)
	if err == nil || !waitable {
		return prepared, err
	}

	var result preparedTarget
	err = client.owner.Await(ctx, func(waitCtx context.Context) error {
		for {
			select {
			case <-signal:
			case <-waitCtx.Done():
				return contextError(context.Cause(waitCtx))
			}
			signal = runtime.routeChangeSignal()
			current, currentErr, currentWaitable := runtime.prepareOnce(
				waitCtx,
				client,
				methodID,
				CallRequest,
			)
			if currentErr == nil {
				result = current
				return nil
			}
			if !currentWaitable {
				return currentErr
			}
		}
	})
	if err != nil {
		return preparedTarget{}, err
	}
	return result, nil
}

func (runtime *Runtime) prepareOnce(
	ctx context.Context,
	client Client,
	methodID MethodID,
	kind CallKind,
) (preparedTarget, error, bool) {
	if runtime == nil || ctx == nil || methodID == 0 {
		return preparedTarget{}, errs.ErrInvalidArgument, false
	}
	if kind != CallRequest && kind != CallNotify {
		return preparedTarget{}, errs.ErrInvalidArgument, false
	}
	if !runtime.frozen.Load() {
		return preparedTarget{}, errs.ErrServiceNotReady, false
	}
	if runtime.closed.Load() {
		return preparedTarget{}, errs.ErrServiceStopped, false
	}
	if client.route.err != nil {
		return preparedTarget{}, client.route.err, false
	}

	set := runtime.buildCandidateSet(
		client.target,
		client.contractID,
		client.fingerprint,
	)
	set.scanEligible()
	if set.count == 0 {
		waitable := set.scan.contract &&
			set.scan.lifecycle &&
			set.scan.transport &&
			!set.scan.connected
		return preparedTarget{}, set.routeError(), waitable
	}
	index, err := runtime.selectCandidateIndex(&set, client.route)
	if err != nil {
		return preparedTarget{}, err, false
	}
	candidate, exists := set.eligibleAt(index)
	if !exists {
		return preparedTarget{}, errs.ErrRPCRouteSelectorFailed, false
	}
	return preparedTarget{
		transport:   candidate.transport,
		nodeID:      candidate.nodeID,
		sessionID:   candidate.sessionID,
		serviceName: candidate.serviceName,
		methodID:    methodID,
		kind:        kind,
		endpoint:    candidate.endpoint,
		tcpSession:  candidate.tcpSession,
		natsView:    candidate.natsView,
	}, nil, false
}

func (runtime *Runtime) buildCandidateSet(
	target Target,
	contractID ContractID,
	fingerprint ContractFingerprint,
) candidateSet {
	result := candidateSet{
		runtime:     runtime,
		target:      target,
		contractID:  contractID,
		fingerprint: fingerprint,
		exact:       target.mode == targetServiceOnNode,
	}
	if resolver, ok := runtime.remoteResolver.(RemoteSnapshotResolver); ok {
		result.snapshot = resolver.Snapshot()
	}
	if runtime.remote != nil {
		result.tcpView = runtime.remote.targetView.Load()
	}
	if runtime.nats != nil {
		result.natsView = runtime.nats.activeConnection.Load()
	}

	if result.exact {
		if target.nodeID == runtime.nodeID {
			result.local, result.hasLocal = runtime.endpoints[target.serviceName]
		}
		return result
	}

	result.local, result.hasLocal = runtime.endpoints[target.serviceName]
	if result.snapshot != nil {
		result.remoteLen = result.snapshot.Len(target.serviceName)
	}
	result.localInsert = result.remoteLen
	if result.hasLocal {
		for index := 0; index < result.remoteLen; index++ {
			candidate, exists := result.snapshot.Candidate(
				target.serviceName,
				index,
			)
			if exists && candidate.NodeID > runtime.nodeID {
				result.localInsert = index
				break
			}
		}
	}
	return result
}

func (set *candidateSet) scanEligible() {
	rawCount := set.rawCount()
	for index := 0; index < rawCount; index++ {
		candidate, exists := set.rawAt(index)
		if !exists {
			continue
		}
		set.scan.sameName = true
		if !set.contractMatches(candidate) {
			continue
		}
		set.scan.contract = true
		if !set.lifecycleMatches(candidate) {
			continue
		}
		set.scan.lifecycle = true
		candidate, compatible := set.transportCandidate(candidate)
		if !compatible {
			continue
		}
		set.scan.transport = true
		if !set.connected(candidate) {
			continue
		}
		set.scan.connected = true
		set.count++
	}
}

func (set *candidateSet) rawCount() int {
	if set.exact {
		if set.target.nodeID == set.runtime.nodeID {
			if set.hasLocal {
				return 1
			}
			return 0
		}
		if set.snapshot == nil {
			return 0
		}
		if _, exists := set.snapshot.Find(
			set.target.nodeID,
			set.target.serviceName,
		); exists {
			return 1
		}
		return 0
	}
	count := set.remoteLen
	if set.hasLocal {
		count++
	}
	return count
}

func (set *candidateSet) rawAt(index int) (routeCandidate, bool) {
	if index < 0 {
		return routeCandidate{}, false
	}
	if set.exact {
		if index != 0 {
			return routeCandidate{}, false
		}
		if set.target.nodeID == set.runtime.nodeID {
			if !set.hasLocal {
				return routeCandidate{}, false
			}
			return set.localCandidate(), true
		}
		if set.snapshot == nil {
			return routeCandidate{}, false
		}
		candidate, exists := set.snapshot.Find(
			set.target.nodeID,
			set.target.serviceName,
		)
		return remoteRouteCandidate(candidate), exists
	}

	if set.hasLocal {
		if index == set.localInsert {
			return set.localCandidate(), true
		}
		if index > set.localInsert {
			index--
		}
	}
	if set.snapshot == nil {
		return routeCandidate{}, false
	}
	candidate, exists := set.snapshot.Candidate(
		set.target.serviceName,
		index,
	)
	return remoteRouteCandidate(candidate), exists
}

func (set *candidateSet) localCandidate() routeCandidate {
	state := publicdiscovery.StateUnknown
	if bound := service.RuntimeOf(set.local.target); bound != nil &&
		bound.State() == service.StateRunning {
		state = publicdiscovery.StateRunning
	}
	var contractID ContractID
	var fingerprint ContractFingerprint
	if set.local.dispatcher != nil {
		contractID = set.local.dispatcher.ContractID()
		fingerprint = set.local.dispatcher.Fingerprint()
	}
	return routeCandidate{
		nodeID:      set.runtime.nodeID,
		sessionID:   set.runtime.sessionID,
		serviceName: set.local.serviceName,
		state:       state,
		labels:      set.runtime.localLabels,
		transport:   preparedLocal,
		contractID:  contractID,
		fingerprint: fingerprint,
		endpoint:    set.local,
	}
}

func remoteRouteCandidate(candidate RemoteCandidate) routeCandidate {
	transport := preparedInvalid
	switch candidate.Transport {
	case TransportTCP:
		transport = preparedTCP
	case TransportNATS:
		transport = preparedNATS
	}
	return routeCandidate{
		nodeID:      candidate.NodeID,
		sessionID:   candidate.SessionID,
		serviceName: candidate.ServiceName,
		state:       candidate.State,
		labels:      candidate.Labels,
		transport:   transport,
		address:     candidate.Address,
		contractID:  candidate.ContractID,
		fingerprint: candidate.Fingerprint,
	}
}

func (set *candidateSet) contractMatches(candidate routeCandidate) bool {
	return candidate.contractID != 0 &&
		candidate.contractID == set.contractID &&
		candidate.fingerprint == set.fingerprint
}

func (set *candidateSet) lifecycleMatches(candidate routeCandidate) bool {
	if candidate.state == publicdiscovery.StateRunning {
		return true
	}
	return set.exact && candidate.state == publicdiscovery.StateRetired
}

func (set *candidateSet) transportCandidate(
	candidate routeCandidate,
) (routeCandidate, bool) {
	switch candidate.transport {
	case preparedLocal:
		return candidate, true
	case preparedTCP:
		if set.runtime.remote == nil ||
			validateAdvertiseAddress(candidate.address) != nil ||
			set.tcpView == nil {
			return routeCandidate{}, false
		}
		target := set.tcpView.byNode[candidate.nodeID]
		if target == nil || target.sessionID != candidate.sessionID {
			return candidate, true
		}
		candidate.tcpSession = target.currentSession()
		return candidate, true
	case preparedNATS:
		if set.runtime.nats == nil {
			return routeCandidate{}, false
		}
		candidate.natsView = set.natsView
		return candidate, true
	default:
		return routeCandidate{}, false
	}
}

func (set *candidateSet) connected(candidate routeCandidate) bool {
	switch candidate.transport {
	case preparedLocal:
		return true
	case preparedTCP:
		return candidate.tcpSession != nil
	case preparedNATS:
		return candidate.natsView != nil &&
			candidate.natsView.conn != nil &&
			candidate.natsView.generation != 0 &&
			candidate.natsView.conn.Status() == natsnet.StatusConnected
	default:
		return false
	}
}

func (set *candidateSet) eligibleAt(want int) (routeCandidate, bool) {
	if want < 0 || want >= set.count {
		return routeCandidate{}, false
	}
	rawCount := set.rawCount()
	for index := 0; index < rawCount; index++ {
		candidate, exists := set.rawAt(index)
		if !exists ||
			!set.contractMatches(candidate) ||
			!set.lifecycleMatches(candidate) {
			continue
		}
		candidate, compatible := set.transportCandidate(candidate)
		if !compatible || !set.connected(candidate) {
			continue
		}
		if want == 0 {
			return candidate, true
		}
		want--
	}
	return routeCandidate{}, false
}

func (set *candidateSet) routeError() error {
	switch {
	case !set.scan.sameName:
		return errs.ErrRPCNoRoute
	case !set.scan.contract:
		return errs.ErrRPCContractMismatch
	case !set.scan.lifecycle:
		return errs.ErrRPCNoRoute
	default:
		return errs.ErrTransportUnavailable
	}
}

func (runtime *Runtime) selectCandidateIndex(
	set *candidateSet,
	route routeSpec,
) (int, error) {
	switch route.mode {
	case routeDefault, routeRoundRobin:
		key := routeCounterKey{
			serviceName: set.target.serviceName,
			contractID:  set.contractID,
			fingerprint: set.fingerprint,
		}
		created := &atomic.Uint64{}
		current, _ := runtime.routeCounters.LoadOrStore(key, created)
		counter := current.(*atomic.Uint64)
		return int((counter.Add(1) - 1) % uint64(set.count)), nil
	case routeRandom:
		value := runtime.routeRandom.Add(0x9e3779b97f4a7c15)
		value = splitmix64(value)
		return int(value % uint64(set.count)), nil
	case routeKey:
		return int(route.hash % uint64(set.count)), nil
	case routeCustom:
		if route.selector == nil {
			return 0, errs.ErrRPCRouteSelectorFailed
		}
		index, ok, panicked := callRouteSelector(
			route.selector,
			RouteCandidates{set: set},
		)
		if panicked || index < 0 || index >= set.count {
			return 0, errs.ErrRPCRouteSelectorFailed
		}
		if !ok {
			return 0, errs.ErrRPCNoRoute
		}
		return index, nil
	default:
		return 0, errs.ErrInvalidArgument
	}
}

func splitmix64(value uint64) uint64 {
	value = (value ^ (value >> 30)) * 0xbf58476d1ce4e5b9
	value = (value ^ (value >> 27)) * 0x94d049bb133111eb
	return value ^ (value >> 31)
}

func callRouteSelector(
	selector RouteSelector,
	candidates RouteCandidates,
) (index int, ok bool, panicked bool) {
	defer func() {
		if recover() != nil {
			panicked = true
		}
	}()
	index, ok = selector.Select(candidates)
	return index, ok, false
}
