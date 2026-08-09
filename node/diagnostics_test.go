package node

import (
	"context"
	"encoding/json"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/duanhf2012/origin/v3/diagnostics"
	"github.com/duanhf2012/origin/v3/errs"
	internaldiscovery "github.com/duanhf2012/origin/v3/internal/discovery"
	originlog "github.com/duanhf2012/origin/v3/log"
	"github.com/duanhf2012/origin/v3/rpc"
	"github.com/duanhf2012/origin/v3/service"
)

// TestDiagnosticsPreservesNodeAndServiceOrder 防止聚合时使用 Map 遍历打乱声明顺序，
// 同时验证现有健康、生命周期和叶子统计被复制为独立 DTO。
func TestDiagnosticsPreservesNodeAndServiceOrder(t *testing.T) {
	events := make([]string, 0, 8)
	first := &lifecycleService{label: "PlayerService", events: &events}
	second := &lifecycleService{label: "SceneService", events: &events}
	current := newTestNode(t, first, second)

	created := current.Diagnostics()
	if created.NodeID != "game-1" || created.State != "created" {
		t.Fatalf("created diagnostics = %+v", created)
	}
	if len(created.Services) != 2 ||
		created.Services[0].ServiceName != "PlayerService" ||
		created.Services[1].ServiceName != "SceneService" {
		t.Fatalf("created services = %+v", created.Services)
	}

	if err := current.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	running := current.Diagnostics()
	if running.State != "ready" || !running.Health.Liveness ||
		!running.Health.Readiness || running.Health.ErrorCode != errs.CodeOK {
		t.Fatalf("running diagnostics = %+v", running)
	}
	for _, snapshot := range running.Services {
		if snapshot.State != "running" || snapshot.EnteredAt.IsZero() ||
			snapshot.ErrorCode != errs.CodeOK {
			t.Fatalf("service diagnostics = %+v", snapshot)
		}
	}
	if running.RPC != mapRPCStats(current.rpcRuntime.Stats()) {
		t.Fatalf("RPC diagnostics = %+v", running.RPC)
	}

	if err := current.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	stopped := current.Diagnostics()
	if stopped.State != "stopped" || stopped.Services[0].State != "stopped" {
		t.Fatalf("stopped diagnostics = %+v", stopped)
	}
}

// TestNilNodeDiagnostics 返回可解释失败 DTO，而不是在监控冷路径 panic。
func TestNilNodeDiagnostics(t *testing.T) {
	var current *Node
	snapshot := current.Diagnostics()
	if snapshot.State != "failed" || snapshot.Health.ErrorCode != errs.CodeInternal {
		t.Fatalf("nil diagnostics = %+v", snapshot)
	}
	summary := current.DiagnosticsSummary()
	if summary.State != "failed" || summary.Health.ErrorCode != errs.CodeInternal ||
		summary.Services.Total != 0 {
		t.Fatalf("nil diagnostics Summary = %+v", summary)
	}
}

// TestDiagnosticsConcurrentRetireResumeAndStop 验证冷路径快照不会与 Service 状态切换、
// Discovery 发布或最终停止争用可变对象；Race 模式下可直接检查所有权边界。
func TestDiagnosticsConcurrentRetireResumeAndStop(t *testing.T) {
	source := internaldiscovery.NewSource()
	var changes []string
	first := &retirementService{label: "First", changes: &changes}
	second := &retirementService{label: "Second", changes: &changes}
	current := newRetirementNode(t, source, first, second)

	stopReading := make(chan struct{})
	readerDone := make(chan struct{})
	var readers sync.WaitGroup
	for range 4 {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-stopReading:
					return
				default:
					snapshot := current.Diagnostics()
					if snapshot.NodeID != "retirement-node" || len(snapshot.Services) != 2 {
						t.Errorf("concurrent Diagnostics() = %+v", snapshot)
						return
					}
				}
			}
		}()
	}
	go func() {
		readers.Wait()
		close(readerDone)
	}()

	for range 10 {
		if err := current.Retire(context.Background()); err != nil {
			t.Fatalf("Retire() error = %v", err)
		}
		if err := current.Resume(context.Background()); err != nil {
			t.Fatalf("Resume() error = %v", err)
		}
	}
	if err := current.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	close(stopReading)
	<-readerDone
	if snapshot := current.Diagnostics(); snapshot.State != "stopped" {
		t.Fatalf("final Diagnostics() = %+v", snapshot)
	}
}

// diagnosticsSummaryService 用固定叶子快照证明 Node Summary 直接累加真实 IService API，
// 不依赖逐 Service Full DTO。
type diagnosticsSummaryService struct {
	service.Service
	execution service.ExecutionStats
	timer     service.TimerStats
	event     service.EventStats
}

func (target *diagnosticsSummaryService) ExecutionStats() service.ExecutionStats {
	return target.execution
}

func (target *diagnosticsSummaryService) TimerStats() service.TimerStats { return target.timer }

func (target *diagnosticsSummaryService) EventStats() service.EventStats { return target.event }

// TestDiagnosticsSummaryAggregates64Services 使用 64 个真实绑定 Service 固定状态和三类叶子
// 统计的人工和；返回 DTO 只能包含一个 ServiceAggregate。
func TestDiagnosticsSummaryAggregates64Services(t *testing.T) {
	const serviceCount = 64
	bindings := make([]ServiceBinding, serviceCount)
	configured := make([]string, serviceCount)
	targets := make([]*diagnosticsSummaryService, serviceCount)
	var wantAccepted, wantReady, wantExecutionRunning, wantAwaiting int
	var wantExecutionDispatched, wantExecutionCompleted, wantExecutionRejected, wantExecutionAwaitTimeout, wantExecutionPanic uint64
	var wantActive, wantDuePending, wantTimerReady, wantTimerRunning int
	var wantTimerTriggered, wantTimerCompleted, wantTimerRejected, wantTimerPanic uint64
	var wantTimerMaxReadyDelay time.Duration
	var wantEventSync, wantEventAsync, wantEventFailures uint64
	for index := range serviceCount {
		value := index + 1
		target := &diagnosticsSummaryService{
			execution: service.ExecutionStats{
				Accepted: value, Ready: value + 1, Running: index % 2, Awaiting: value + 2,
				DispatchedTotal: uint64(value * 2), CompletedTotal: uint64(value * 3),
				RejectedTotal: uint64(value * 5), AwaitTimeoutTotal: uint64(value * 7), PanicTotal: uint64(value * 11),
			},
			timer: service.TimerStats{
				Active: value + 3, DuePending: value + 4, Ready: value + 5, Running: index % 2,
				TriggeredTotal: uint64(value * 13), CompletedTotal: uint64(value * 17),
				RejectedTotal: uint64(value * 19), PanicTotal: uint64(value * 23),
				MaxReadyDelay: time.Duration(value) * time.Millisecond,
			},
			event: service.EventStats{
				SyncNotifiedTotal: uint64(value * 29), AsyncNotifiedTotal: uint64(value * 31),
				HandlerFailureTotal: uint64(value * 37),
			},
		}
		name := "service-" + strconv.Itoa(index)
		targets[index] = target
		configured[index] = name
		bindings[index] = ServiceBinding{Name: name, Template: "diagnosticsSummaryService", Service: target}
		wantAccepted += target.execution.Accepted
		wantReady += target.execution.Ready
		wantExecutionRunning += target.execution.Running
		wantAwaiting += target.execution.Awaiting
		wantExecutionDispatched += target.execution.DispatchedTotal
		wantExecutionCompleted += target.execution.CompletedTotal
		wantExecutionRejected += target.execution.RejectedTotal
		wantExecutionAwaitTimeout += target.execution.AwaitTimeoutTotal
		wantExecutionPanic += target.execution.PanicTotal
		wantActive += target.timer.Active
		wantDuePending += target.timer.DuePending
		wantTimerReady += target.timer.Ready
		wantTimerRunning += target.timer.Running
		wantTimerTriggered += target.timer.TriggeredTotal
		wantTimerCompleted += target.timer.CompletedTotal
		wantTimerRejected += target.timer.RejectedTotal
		wantTimerPanic += target.timer.PanicTotal
		if target.timer.MaxReadyDelay > wantTimerMaxReadyDelay {
			wantTimerMaxReadyDelay = target.timer.MaxReadyDelay
		}
		wantEventSync += target.event.SyncNotifiedTotal
		wantEventAsync += target.event.AsyncNotifiedTotal
		wantEventFailures += target.event.HandlerFailureTotal
	}
	current, err := New(
		Config{ID: "summary-node", Services: configured},
		bindings,
		originlog.NewNop(),
		Options{MaxTimersPerNode: 1024, TimerLocation: time.UTC},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = current.Rollback(context.Background()) })
	if err := current.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	// 交错 Retired/Running，覆盖状态计数而不影响固定叶子统计。
	for index := 0; index < serviceCount; index += 2 {
		if err := targets[index].Retire(t.Context()); err != nil {
			t.Fatalf("Retire(%d) error = %v", index, err)
		}
	}

	summary := current.DiagnosticsSummary()
	if summary.NodeID != "summary-node" || summary.Services.Total != serviceCount ||
		summary.Services.States.Running != serviceCount/2 ||
		summary.Services.States.Retired != serviceCount/2 {
		t.Fatalf("Summary identity/states = %+v", summary)
	}
	execution := summary.Services.Execution
	if execution.Accepted != wantAccepted || execution.Ready != wantReady ||
		execution.Running != wantExecutionRunning || execution.Awaiting != wantAwaiting ||
		execution.DispatchedTotal != wantExecutionDispatched || execution.CompletedTotal != wantExecutionCompleted ||
		execution.RejectedTotal != wantExecutionRejected || execution.AwaitTimeoutTotal != wantExecutionAwaitTimeout ||
		execution.PanicTotal != wantExecutionPanic {
		t.Fatalf("Execution aggregate = %+v", execution)
	}
	timer := summary.Services.Timer
	if timer.Active != wantActive || timer.DuePending != wantDuePending ||
		timer.Ready != wantTimerReady || timer.Running != wantTimerRunning ||
		timer.TriggeredTotal != wantTimerTriggered || timer.CompletedTotal != wantTimerCompleted ||
		timer.RejectedTotal != wantTimerRejected || timer.PanicTotal != wantTimerPanic ||
		timer.MaxReadyDelay != diagnostics.Duration(wantTimerMaxReadyDelay) {
		t.Fatalf("Timer aggregate = %+v", timer)
	}
	if summary.Services.Event.SyncNotifiedTotal != wantEventSync ||
		summary.Services.Event.AsyncNotifiedTotal != wantEventAsync ||
		summary.Services.Event.HandlerFailureTotal != wantEventFailures {
		t.Fatalf("Event aggregate = %+v", summary.Services.Event)
	}
	encoded, err := json.Marshal(summary)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(encoded, &document); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), `"created"`) || strings.Contains(string(encoded), `"scheduled"`) ||
		strings.Contains(string(encoded), `"active_high_watermark"`) {
		t.Fatalf("Summary JSON unexpectedly exposes per-Service detail: %s", encoded)
	}
	if reflect.TypeOf(summary.Services).Kind() == reflect.Slice || document["services"] == nil {
		t.Fatalf("Summary allocates a per-Service DTO slice: %#v", summary.Services)
	}
}

// TestDiagnosticsSummaryCountsEveryServiceState 固定全部生命周期枚举和未知值各自只进入一个桶。
func TestDiagnosticsSummaryCountsEveryServiceState(t *testing.T) {
	states := []service.State{
		service.StateCreated,
		service.StateInitializing,
		service.StateInitialized,
		service.StateStarting,
		service.StateRunning,
		service.StateRetired,
		service.StateStopping,
		service.StateStopped,
		service.StateFailed,
		service.State(255),
	}
	var aggregate diagnostics.ServiceAggregate
	for _, state := range states {
		aggregateService(
			&aggregate,
			state,
			service.ExecutionStats{},
			service.TimerStats{},
			service.EventStats{},
		)
	}
	if aggregate.Total != len(states) || aggregate.States != (diagnostics.ServiceStateAggregate{
		Running: 1, Retired: 1, Failed: 1, Unknown: 7,
	}) {
		t.Fatalf("state aggregate = %+v", aggregate)
	}
}

// TestDiagnosticsSummaryAggregatesTransportStats verifies that default
// monitoring sees one node-level work aggregate, not a Local/TCP/NATS tree.
func TestDiagnosticsSummaryAggregatesTransportStats(t *testing.T) {
	stats := rpc.Stats{
		Local: rpc.TransportStats{Pending: 2, PendingHighWater: 4, OutboundAccepted: 99, OutboundCompleted: 3, OutboundFailed: 5, OutboundTimeout: 7, OutboundRejected: 11, InboundAccepted: 98, InboundCompleted: 13, InboundFailed: 17, InboundTimeout: 19, InboundRejected: 23, PayloadSentBytes: 29, PayloadReceivedBytes: 31},
		TCP:   rpc.TransportStats{Pending: 37, PendingHighWater: 41, OutboundAccepted: 97, OutboundCompleted: 43, OutboundFailed: 47, OutboundTimeout: 53, OutboundRejected: 59, InboundAccepted: 96, InboundCompleted: 61, InboundFailed: 67, InboundTimeout: 71, InboundRejected: 73, PayloadSentBytes: 79, PayloadReceivedBytes: 83},
		NATS:  rpc.TransportStats{Pending: 89, PendingHighWater: 97, OutboundAccepted: 95, OutboundCompleted: 101, OutboundFailed: 103, OutboundTimeout: 107, OutboundRejected: 109, InboundAccepted: 94, InboundCompleted: 113, InboundFailed: 127, InboundTimeout: 131, InboundRejected: 137, PayloadSentBytes: 139, PayloadReceivedBytes: 149},
	}
	got := mapRPCSummary(stats)
	want := diagnostics.RPCSummary{
		Pending: 128, PendingHighWater: 97,
		OutboundCompleted: 147, OutboundFailed: 155, OutboundTimeout: 167, OutboundRejected: 179,
		InboundCompleted: 187, InboundFailed: 211, InboundTimeout: 221, InboundRejected: 233,
		PayloadSentBytes: 247, PayloadReceivedBytes: 263,
	}
	if got != want {
		t.Fatalf("aggregate RPC = %+v, want %+v", got, want)
	}
}

type diagnosticsSummaryRaceEvent struct{ sequence uint64 }

func (diagnosticsSummaryRaceEvent) EventID() service.EventID { return 4001 }

type diagnosticsSummaryRaceService struct{ service.Service }

func (target *diagnosticsSummaryRaceService) OnInit() error {
	return target.SubscribeEvent(4001, func(context.Context, service.Event) error { return nil })
}

// TestDiagnosticsSummaryConcurrentLeaves 在 Retire/Resume、Timer 和 Event 同时变化时反复采集；
// Race 模式验证所有叶子统计各自由现有同步边界保护。
func TestDiagnosticsSummaryConcurrentLeaves(t *testing.T) {
	target := &diagnosticsSummaryRaceService{}
	current, err := New(
		Config{ID: "summary-race", Services: []string{"worker"}},
		[]ServiceBinding{{Name: "worker", Template: "diagnosticsSummaryRaceService", Service: target}},
		originlog.NewNop(),
		Options{MaxTimersPerNode: 512, TimerLocation: time.UTC},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := current.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = current.Stop(context.Background()) })

	stop := make(chan struct{})
	var readers sync.WaitGroup
	readers.Add(2)
	for range 2 {
		go func() {
			defer readers.Done()
			for {
				select {
				case <-stop:
					return
				default:
					if got := current.DiagnosticsSummary(); got.Services.Total != 1 {
						t.Errorf("concurrent Summary = %+v", got)
						return
					}
				}
			}
		}()
	}
	for index := range 100 {
		if err := target.Retire(t.Context()); err != nil {
			t.Fatal(err)
		}
		if err := target.Resume(t.Context()); err != nil {
			t.Fatal(err)
		}
		_ = target.NotifyEventAsync(diagnosticsSummaryRaceEvent{sequence: uint64(index)})
		_ = target.AfterFunc(time.Nanosecond, func(context.Context, service.TimerID) {})
	}
	close(stop)
	readers.Wait()
}

// TestDiagnosticsSummaryDirectoryIsOneSnapshot 交替发布不同 Service 总量的远端目录；一次 Node
// Summary 的 Services/Running/Retired 必须来自同一个 Snapshot，不能先 Stats 再 All 拼接。
func TestDiagnosticsSummaryDirectoryIsOneSnapshot(t *testing.T) {
	current := newTestNode(t, &lifecycleService{label: "local"})
	directory := current.discovery.directory
	first := diagnosticsDirectoryRawSnapshot(101, 1, 0)
	second := diagnosticsDirectoryRawSnapshot(102, 0, 64)
	if _, _, err := directory.ApplySnapshot(first); err != nil {
		t.Fatal(err)
	}

	var readers sync.WaitGroup
	var stop atomic.Bool
	var invalid atomic.Bool
	for range 8 {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for !stop.Load() {
				directorySummary := current.DiagnosticsSummary().Directory
				if directorySummary.Running+directorySummary.Retired != directorySummary.Services {
					invalid.Store(true)
					return
				}
			}
		}()
	}
	for index := range 2_000 {
		candidate := first
		if index%2 == 0 {
			candidate = second
		}
		if _, _, err := directory.ApplySnapshot(candidate); err != nil {
			stop.Store(true)
			readers.Wait()
			t.Fatal(err)
		}
	}
	stop.Store(true)
	readers.Wait()
	if invalid.Load() {
		t.Fatal("DiagnosticsSummary observed Directory fields from different snapshots")
	}
}

// diagnosticsDirectoryRawSnapshot 构造一个具有指定 Running/Retired 组成的远端目录快照。
func diagnosticsDirectoryRawSnapshot(sessionID uint64, running, retired int) internaldiscovery.RawSnapshot {
	services := make([]internaldiscovery.RawService, 0, running+retired)
	for index := range running {
		services = append(services, internaldiscovery.RawService{
			ServiceName: "running-" + strconv.Itoa(index),
			State:       internaldiscovery.ServiceStateRunning,
		})
	}
	for index := range retired {
		services = append(services, internaldiscovery.RawService{
			ServiceName: "retired-" + strconv.Itoa(index),
			State:       internaldiscovery.ServiceStateRetired,
		})
	}
	return internaldiscovery.RawSnapshot{Nodes: []internaldiscovery.RawNode{{
		NodeID:    "remote-1",
		SessionID: sessionID,
		Transport: internaldiscovery.TransportNone,
		Services:  services,
	}}}
}
