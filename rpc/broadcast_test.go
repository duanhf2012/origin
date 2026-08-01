package rpc

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"

	publicdiscovery "github.com/duanhf2012/origin/v3/discovery"
	"github.com/duanhf2012/origin/v3/errs"
	"github.com/duanhf2012/origin/v3/internal/bufferpool"
	originlog "github.com/duanhf2012/origin/v3/log"
	"github.com/duanhf2012/origin/v3/service"
)

type broadcastTestSelector struct {
	calls atomic.Int64
}

func (selector *broadcastTestSelector) Select(RouteCandidates) (int, bool) {
	selector.calls.Add(1)
	return 0, true
}

type broadcastTestSnapshot struct {
	candidates []RemoteCandidate
}

func (snapshot *broadcastTestSnapshot) Len(serviceName string) int {
	if serviceName != "PlayerService" {
		return 0
	}
	return len(snapshot.candidates)
}

func (snapshot *broadcastTestSnapshot) Candidate(
	serviceName string,
	index int,
) (RemoteCandidate, bool) {
	if serviceName != "PlayerService" || index < 0 || index >= len(snapshot.candidates) {
		return RemoteCandidate{}, false
	}
	return snapshot.candidates[index], true
}

func (snapshot *broadcastTestSnapshot) Find(
	nodeID string,
	serviceName string,
) (RemoteCandidate, bool) {
	if serviceName != "PlayerService" {
		return RemoteCandidate{}, false
	}
	for _, candidate := range snapshot.candidates {
		if candidate.NodeID == nodeID {
			return candidate, true
		}
	}
	return RemoteCandidate{}, false
}

type broadcastTestResolver struct {
	snapshot *broadcastTestSnapshot
}

type broadcastCaptureService struct {
	service.Service
	afterDispatch func()
}

func (target *broadcastCaptureService) DispatchAsync(
	fn func(context.Context),
) error {
	if fn == nil {
		return errs.ErrInvalidArgument
	}
	// 单元测试同步执行任务，精确保留“准入成功后由目标消费 Buffer”的所有权边界。
	fn(context.Background())
	if target.afterDispatch != nil {
		target.afterDispatch()
	}
	return nil
}

type broadcastCaptureDispatcher struct {
	payloads [][]byte
}

func (*broadcastCaptureDispatcher) ContractID() ContractID {
	return 1
}

func (*broadcastCaptureDispatcher) Fingerprint() ContractFingerprint {
	return runtimeTestFingerprint
}

func (dispatcher *broadcastCaptureDispatcher) Dispatch(
	_ context.Context,
	methodID MethodID,
	kind CallKind,
	payload []byte,
	response ResponseWriter,
) (ResponseWriter, error) {
	if methodID != 1 || kind != CallNotify {
		return response, errs.ErrInvalidArgument
	}
	// Dispatcher 只能在任务期间借用 payload，因此测试立即深复制观测结果。
	dispatcher.payloads = append(dispatcher.payloads, append([]byte(nil), payload...))
	return response, nil
}

func (resolver *broadcastTestResolver) Snapshot() RemoteSnapshot {
	return resolver.snapshot
}

func (resolver *broadcastTestResolver) ResolveRemote(
	nodeID string,
	serviceName string,
	contractID ContractID,
	fingerprint ContractFingerprint,
) (RemoteRoute, error) {
	candidate, exists := resolver.snapshot.Find(nodeID, serviceName)
	if !exists {
		return RemoteRoute{}, errs.ErrRPCNoRoute
	}
	if candidate.ContractID != contractID || candidate.Fingerprint != fingerprint {
		return RemoteRoute{}, errs.ErrRPCContractMismatch
	}
	return RemoteRoute{
		NodeID:    candidate.NodeID,
		SessionID: candidate.SessionID,
		Transport: candidate.Transport,
		Address:   candidate.Address,
	}, nil
}

func newBroadcastTestCandidate(
	nodeID string,
	sessionID uint64,
	state publicdiscovery.State,
) RemoteCandidate {
	return RemoteCandidate{
		NodeID:      nodeID,
		SessionID:   sessionID,
		ServiceName: "PlayerService",
		State:       state,
		Transport:   TransportTCP,
		Address:     fmt.Sprintf("127.0.0.1:%d", 30000+sessionID),
		ContractID:  1,
		Fingerprint: runtimeTestFingerprint,
	}
}

func newBroadcastLocalRuntime(
	t testing.TB,
	snapshot *broadcastTestSnapshot,
	maxBroadcastSize int,
) (*Runtime, *bufferpool.Pool, *broadcastCaptureService, *broadcastCaptureDispatcher) {
	t.Helper()
	pool := bufferpool.NewPool(bufferpool.Options{TrackUsage: true})
	runtime, err := NewRuntime("gateway-1", pool, originlog.NewNop())
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}
	config := DefaultConfig()
	config.TCP.Listen = "127.0.0.1:31001"
	config.TCP.Advertise = "127.0.0.1:31001"
	config.MaxBroadcastSize = maxBroadcastSize
	if err := runtime.Configure(&config); err != nil {
		t.Fatalf("Configure() error = %v", err)
	}
	if err := runtime.BindRemoteResolver(&broadcastTestResolver{snapshot: snapshot}); err != nil {
		t.Fatalf("BindRemoteResolver() error = %v", err)
	}
	target := &broadcastCaptureService{}
	if err := service.BindRuntime(target, &prepareTestServiceRuntime{
		nodeID:      runtime.nodeID,
		serviceName: "PlayerService",
		state:       service.StateRunning,
		rpcRuntime:  runtime,
	}); err != nil {
		t.Fatalf("BindRuntime() error = %v", err)
	}
	dispatcher := &broadcastCaptureDispatcher{}
	if err := runtime.RegisterServiceVisibility(
		"PlayerService",
		target,
		dispatcher,
		false,
	); err != nil {
		t.Fatalf("RegisterServiceVisibility() error = %v", err)
	}
	if err := runtime.Freeze(); err != nil {
		t.Fatalf("Freeze() error = %v", err)
	}
	return runtime, pool, target, dispatcher
}

// TestPrepareBroadcastFreezesFullIntentSet 锁定自动广播范围、稳定计数和 Route 忽略规则。
func TestPrepareBroadcastFreezesFullIntentSet(t *testing.T) {
	snapshot := &broadcastTestSnapshot{candidates: []RemoteCandidate{
		newBroadcastTestCandidate("player-1", 1, publicdiscovery.StateRunning),
		newBroadcastTestCandidate("player-2", 2, publicdiscovery.StateRunning),
		newBroadcastTestCandidate("player-3", 3, publicdiscovery.StateRetired),
	}}
	runtime := newPrepareTestRuntime(t, "gateway-1", TransportTCP, nil)
	if err := runtime.BindRemoteResolver(&broadcastTestResolver{snapshot: snapshot}); err != nil {
		t.Fatalf("BindRemoteResolver() error = %v", err)
	}
	addPrepareTestLocal(
		t,
		runtime,
		"PlayerService",
		service.StateRunning,
		&runtimeTestDispatcher{},
	)
	addPrepareTestTCPConnection(t, runtime, "player-2", 2, snapshot.candidates[1].Address)
	addPrepareTestTCPConnection(t, runtime, "player-3", 3, snapshot.candidates[2].Address)
	if err := runtime.Freeze(); err != nil {
		t.Fatalf("Freeze() error = %v", err)
	}

	selector := &broadcastTestSelector{}
	prepared, err := prepareTestClient(
		runtime,
		ToService("PlayerService"),
	).RouteBy(selector).PrepareBroadcast(context.Background(), 1)
	if err != nil {
		t.Fatalf("PrepareBroadcast() error = %v", err)
	}
	if selector.calls.Load() != 0 {
		t.Fatalf("Broadcast 执行了单目标 Selector: %d", selector.calls.Load())
	}
	if prepared.prepared.transport != preparedInvalid || prepared.broadcast == nil {
		t.Fatalf("多目标没有建立广播计划: %+v", prepared)
	}
	if prepared.broadcast.intentCount != 3 ||
		prepared.broadcast.sendableCount != 2 ||
		prepared.broadcast.lastSendableRaw != 2 {
		t.Fatalf("广播计划计数错误: %+v", prepared.broadcast)
	}

	// 显式包含退休状态后只扩大生命周期范围，不改变连接或排序语义。
	included, err := prepareTestClient(
		runtime,
		ToService("PlayerService"),
	).IncludeRetired().PrepareBroadcast(context.Background(), 1)
	if err != nil {
		t.Fatalf("IncludeRetired PrepareBroadcast() error = %v", err)
	}
	if included.broadcast == nil || included.broadcast.intentCount != 4 ||
		included.broadcast.sendableCount != 3 || included.broadcast.lastSendableRaw != 3 {
		t.Fatalf("IncludeRetired 广播计划错误: %+v", included.broadcast)
	}
}

// TestPrepareBroadcastUsesSingleTargetFastPath 验证精确或唯一目标继续复用 M19 prepared target。
func TestPrepareBroadcastUsesSingleTargetFastPath(t *testing.T) {
	candidate := newBroadcastTestCandidate("player-1", 11, publicdiscovery.StateRetired)
	snapshot := &broadcastTestSnapshot{candidates: []RemoteCandidate{candidate}}
	runtime := newPrepareTestRuntime(t, "gateway-1", TransportTCP, nil)
	if err := runtime.BindRemoteResolver(&broadcastTestResolver{snapshot: snapshot}); err != nil {
		t.Fatal(err)
	}
	addPrepareTestTCPConnection(t, runtime, candidate.NodeID, candidate.SessionID, candidate.Address)
	if err := runtime.Freeze(); err != nil {
		t.Fatal(err)
	}

	prepared, err := prepareTestClient(
		runtime,
		ToServiceOnNode("player-1", "PlayerService"),
	).PrepareBroadcast(context.Background(), 1)
	if err != nil {
		t.Fatalf("PrepareBroadcast() error = %v", err)
	}
	if prepared.broadcast != nil || prepared.prepared.nodeID != "player-1" ||
		prepared.prepared.transport != preparedTCP {
		t.Fatalf("单目标没有复用 prepared target: %+v", prepared)
	}
}

// TestPrepareBroadcastClassifiesUnavailableTargets 锁定单目标原始错误和多目标 2011 详情。
func TestPrepareBroadcastClassifiesUnavailableTargets(t *testing.T) {
	single := &broadcastTestSnapshot{candidates: []RemoteCandidate{
		newBroadcastTestCandidate("player-1", 21, publicdiscovery.StateRunning),
	}}
	runtime := newPrepareTestRuntime(t, "gateway-1", TransportTCP, nil)
	if err := runtime.BindRemoteResolver(&broadcastTestResolver{snapshot: single}); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Freeze(); err != nil {
		t.Fatal(err)
	}
	_, err := prepareTestClient(
		runtime,
		ToService("PlayerService"),
	).PrepareBroadcast(context.Background(), 1)
	if !errors.Is(err, errs.ErrTransportUnavailable) {
		t.Fatalf("单目标断开 error = %v", err)
	}

	multiple := &broadcastTestSnapshot{candidates: []RemoteCandidate{
		newBroadcastTestCandidate("player-1", 31, publicdiscovery.StateRunning),
		newBroadcastTestCandidate("player-2", 32, publicdiscovery.StateRunning),
	}}
	runtime = newPrepareTestRuntime(t, "gateway-1", TransportTCP, nil)
	if err := runtime.BindRemoteResolver(&broadcastTestResolver{snapshot: multiple}); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Freeze(); err != nil {
		t.Fatal(err)
	}
	_, err = prepareTestClient(
		runtime,
		ToService("PlayerService"),
	).PrepareBroadcast(context.Background(), 1)
	var broadcastErr *BroadcastError
	if !errors.As(err, &broadcastErr) || broadcastErr.Code() != errs.CodeRPCBroadcastFailed ||
		broadcastErr.Total() != 2 || broadcastErr.Succeeded() != 0 ||
		broadcastErr.FailureCount() != 2 {
		t.Fatalf("多目标全部断开 error = %v", err)
	}
	for index, nodeID := range []string{"player-1", "player-2"} {
		failure, ok := broadcastErr.Failure(index)
		if !ok || failure.NodeID != nodeID ||
			!errors.Is(failure.Err, errs.ErrTransportUnavailable) {
			t.Fatalf("失败详情 %d = %+v, %v", index, failure, ok)
		}
	}
}

// TestPrepareBroadcastRejectsGlobalPreflightErrors 验证 Context、契约和目标数在编码前失败。
func TestPrepareBroadcastRejectsGlobalPreflightErrors(t *testing.T) {
	wrongContract := newBroadcastTestCandidate("player-1", 41, publicdiscovery.StateRunning)
	wrongContract.ContractID = 2
	snapshot := &broadcastTestSnapshot{candidates: []RemoteCandidate{wrongContract}}
	runtime := newPrepareTestRuntime(t, "gateway-1", TransportTCP, nil)
	if err := runtime.BindRemoteResolver(&broadcastTestResolver{snapshot: snapshot}); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Freeze(); err != nil {
		t.Fatal(err)
	}
	client := prepareTestClient(runtime, ToService("PlayerService"))
	if _, err := client.PrepareBroadcast(context.Background(), 1); !errors.Is(
		err,
		errs.ErrRPCContractMismatch,
	) {
		t.Fatalf("契约不匹配 error = %v", err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.PrepareBroadcast(canceled, 1); !errors.Is(err, errs.ErrCanceled) {
		t.Fatalf("已取消 Context error = %v", err)
	}

	// 8192 个合法断开意图仍是允许的完整计划，必须返回逐目标 2011 而不是容量过载。
	candidates := make([]RemoteCandidate, maxRemoteTargets+1)
	for index := range candidates {
		candidates[index] = newBroadcastTestCandidate(
			fmt.Sprintf("player-%05d", index),
			uint64(index+100),
			publicdiscovery.StateRunning,
		)
	}
	runtime = newPrepareTestRuntime(t, "gateway-1", TransportTCP, nil)
	if err := runtime.BindRemoteResolver(&broadcastTestResolver{
		snapshot: &broadcastTestSnapshot{candidates: candidates[:maxRemoteTargets]},
	}); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Freeze(); err != nil {
		t.Fatal(err)
	}
	_, err := prepareTestClient(
		runtime,
		ToService("PlayerService"),
	).PrepareBroadcast(context.Background(), 1)
	var maximumErr *BroadcastError
	if !errors.As(err, &maximumErr) || maximumErr.Total() != maxRemoteTargets ||
		maximumErr.Code() != errs.CodeRPCBroadcastFailed {
		t.Fatalf("8192 目标 error = %v", err)
	}

	// 8193 个合法意图即使全部断开也必须先返回容量过载，不能截断为 8192 个失败。
	runtime = newPrepareTestRuntime(t, "gateway-1", TransportTCP, nil)
	if err := runtime.BindRemoteResolver(&broadcastTestResolver{
		snapshot: &broadcastTestSnapshot{candidates: candidates},
	}); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Freeze(); err != nil {
		t.Fatal(err)
	}
	if _, err := prepareTestClient(
		runtime,
		ToService("PlayerService"),
	).PrepareBroadcast(context.Background(), 1); !errors.Is(
		err,
		errs.ErrTransportOverloaded,
	) {
		t.Fatalf("8193 目标 error = %v", err)
	}
}

// TestBroadcastCapacityPreflightIncludesDisconnectedIntent 验证总放大按全部意图计算且超限零申请。
func TestBroadcastCapacityPreflightIncludesDisconnectedIntent(t *testing.T) {
	snapshot := &broadcastTestSnapshot{candidates: []RemoteCandidate{
		newBroadcastTestCandidate("player-1", 51, publicdiscovery.StateRunning),
	}}
	const limit = 4 * 1024 * 1024
	runtime, pool, _, _ := newBroadcastLocalRuntime(t, snapshot, limit)
	prepared, err := prepareTestClient(
		runtime,
		ToService("PlayerService"),
	).PrepareBroadcast(context.Background(), 1)
	if err != nil {
		t.Fatalf("PrepareBroadcast() error = %v", err)
	}

	// 两个意图的 2M payload 恰好等于 4M；多一个字节必须在申请前过载。
	boundary, err := prepared.AllocateRequest(2*1024*1024, CallNotify)
	if err != nil {
		t.Fatalf("边界 AllocateRequest() error = %v", err)
	}
	boundary.Release()
	if buffer, err := prepared.AllocateRequest(2*1024*1024+1, CallNotify); !errors.Is(err, errs.ErrTransportOverloaded) || buffer != nil {
		t.Fatalf("超限 AllocateRequest() buffer=%v error=%v", buffer, err)
	}
	if stats := pool.Stats(); stats.InUseBuffers != 0 {
		t.Fatalf("容量预检泄漏 Buffer: %+v", stats)
	}
}

// TestBroadcastSubmitsLocalAndReportsDisconnectedRemote 验证成功所有权和 2010 逐目标详情。
func TestBroadcastSubmitsLocalAndReportsDisconnectedRemote(t *testing.T) {
	snapshot := &broadcastTestSnapshot{candidates: []RemoteCandidate{
		newBroadcastTestCandidate("player-1", 61, publicdiscovery.StateRunning),
	}}
	runtime, pool, _, dispatcher := newBroadcastLocalRuntime(
		t,
		snapshot,
		DefaultMaxBroadcastSize,
	)
	prepared, err := prepareTestClient(
		runtime,
		ToService("PlayerService"),
	).PrepareBroadcast(context.Background(), 1)
	if err != nil {
		t.Fatalf("PrepareBroadcast() error = %v", err)
	}
	request, err := prepared.AllocateRequest(4, CallNotify)
	if err != nil {
		t.Fatalf("AllocateRequest() error = %v", err)
	}
	copy(request.Bytes(), []byte{1, 2, 3, 4})
	err = prepared.Broadcast(context.Background(), 1, request)
	var broadcastErr *BroadcastError
	if !errors.As(err, &broadcastErr) || broadcastErr.Total() != 2 ||
		broadcastErr.Succeeded() != 1 || broadcastErr.Code() != errs.CodeRPCBroadcastPartialFailed {
		t.Fatalf("Broadcast() error = %v", err)
	}
	failure, ok := broadcastErr.Failure(0)
	if !ok || failure.NodeID != "player-1" ||
		!errors.Is(failure.Err, errs.ErrTransportUnavailable) {
		t.Fatalf("失败详情 = %+v, %v", failure, ok)
	}
	if len(dispatcher.payloads) != 1 ||
		string(dispatcher.payloads[0]) != string([]byte{1, 2, 3, 4}) {
		t.Fatalf("本地 payload = %v", dispatcher.payloads)
	}
	if stats := pool.Stats(); stats.InUseBuffers != 0 {
		t.Fatalf("Broadcast 后 Buffer 未清空: %+v", stats)
	}
}

// TestBroadcastContextBoundaries 验证首次提交前取消和扇出中途取消使用不同错误外观。
func TestBroadcastContextBoundaries(t *testing.T) {
	snapshot := &broadcastTestSnapshot{candidates: []RemoteCandidate{
		newBroadcastTestCandidate("player-1", 71, publicdiscovery.StateRunning),
	}}
	runtime, pool, target, dispatcher := newBroadcastLocalRuntime(
		t,
		snapshot,
		DefaultMaxBroadcastSize,
	)
	prepared, err := prepareTestClient(
		runtime,
		ToService("PlayerService"),
	).PrepareBroadcast(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}

	before, err := prepared.AllocateRequest(1, CallNotify)
	if err != nil {
		t.Fatal(err)
	}
	beforeContext, cancelBefore := context.WithCancel(context.Background())
	cancelBefore()
	if err := prepared.Broadcast(beforeContext, 1, before); !errors.Is(err, errs.ErrCanceled) {
		t.Fatalf("首次提交前取消 error = %v", err)
	}
	if len(dispatcher.payloads) != 0 {
		t.Fatalf("首次提交前取消仍发生投递: %v", dispatcher.payloads)
	}

	middleContext, cancelMiddle := context.WithCancel(context.Background())
	target.afterDispatch = cancelMiddle
	middle, err := prepared.AllocateRequest(1, CallNotify)
	if err != nil {
		t.Fatal(err)
	}
	middle.Bytes()[0] = 9
	err = prepared.Broadcast(middleContext, 1, middle)
	var broadcastErr *BroadcastError
	if !errors.As(err, &broadcastErr) || broadcastErr.Succeeded() != 1 ||
		broadcastErr.Code() != errs.CodeRPCBroadcastPartialFailed {
		t.Fatalf("扇出中途取消 error = %v", err)
	}
	failure, ok := broadcastErr.Failure(0)
	if !ok || !errors.Is(failure.Err, errs.ErrCanceled) {
		t.Fatalf("中途取消失败详情 = %+v, %v", failure, ok)
	}
	if len(dispatcher.payloads) != 1 || dispatcher.payloads[0][0] != 9 {
		t.Fatalf("中途取消本地投递 = %v", dispatcher.payloads)
	}
	if stats := pool.Stats(); stats.InUseBuffers != 0 {
		t.Fatalf("Context 路径泄漏 Buffer: %+v", stats)
	}
}

// TestBroadcastDoesNotSwapPreparedTCPSessions 验证连接替换后失败而不改用新会话。
func TestBroadcastDoesNotSwapPreparedTCPSessions(t *testing.T) {
	snapshot := &broadcastTestSnapshot{candidates: []RemoteCandidate{
		newBroadcastTestCandidate("player-1", 81, publicdiscovery.StateRunning),
		newBroadcastTestCandidate("player-2", 82, publicdiscovery.StateRunning),
	}}
	runtime := newPrepareTestRuntime(t, "gateway-1", TransportTCP, nil)
	if err := runtime.BindRemoteResolver(&broadcastTestResolver{snapshot: snapshot}); err != nil {
		t.Fatal(err)
	}
	for _, candidate := range snapshot.candidates {
		addPrepareTestTCPConnection(
			t,
			runtime,
			candidate.NodeID,
			candidate.SessionID,
			candidate.Address,
		)
	}
	if err := runtime.Freeze(); err != nil {
		t.Fatal(err)
	}
	prepared, err := prepareTestClient(
		runtime,
		ToService("PlayerService"),
	).PrepareBroadcast(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}

	// 发布相同 Node/Session 的新对象；提交只能识别 Prepare 时捕获的旧指针并全部失败。
	for _, candidate := range snapshot.candidates {
		addPrepareTestTCPConnection(
			t,
			runtime,
			candidate.NodeID,
			candidate.SessionID,
			candidate.Address,
		)
	}
	request, err := prepared.AllocateRequest(1, CallNotify)
	if err != nil {
		t.Fatal(err)
	}
	err = prepared.Broadcast(context.Background(), 1, request)
	var broadcastErr *BroadcastError
	if !errors.As(err, &broadcastErr) || broadcastErr.Succeeded() != 0 ||
		broadcastErr.Code() != errs.CodeRPCBroadcastFailed ||
		broadcastErr.FailureCount() != 2 {
		t.Fatalf("连接替换 Broadcast() error = %v", err)
	}
}
