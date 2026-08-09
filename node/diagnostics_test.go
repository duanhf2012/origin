package node

import (
	"context"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/duanhf2012/origin/v3/diagnostics"
	"github.com/duanhf2012/origin/v3/errs"
	internaldiscovery "github.com/duanhf2012/origin/v3/internal/discovery"
	originlog "github.com/duanhf2012/origin/v3/log"
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
	var wantExecutionRejected, wantExecutionPanic uint64
	var wantActive, wantDuePending, wantTimerReady, wantTimerRunning int
	var wantTimerRejected, wantTimerPanic, wantEventFailures uint64
	for index := range serviceCount {
		value := index + 1
		target := &diagnosticsSummaryService{
			execution: service.ExecutionStats{
				Accepted: value, Ready: value + 1, Running: index % 2, Awaiting: value + 2,
				RejectedTotal: uint64(value * 3), PanicTotal: uint64(value * 5),
			},
			timer: service.TimerStats{
				Active: value + 3, DuePending: value + 4, Ready: value + 5, Running: index % 2,
				RejectedTotal: uint64(value * 7), PanicTotal: uint64(value * 11),
			},
			event: service.EventStats{HandlerFailureTotal: uint64(value * 13)},
		}
		name := "service-" + strconv.Itoa(index)
		targets[index] = target
		configured[index] = name
		bindings[index] = ServiceBinding{Name: name, Template: "diagnosticsSummaryService", Service: target}
		wantAccepted += target.execution.Accepted
		wantReady += target.execution.Ready
		wantExecutionRunning += target.execution.Running
		wantAwaiting += target.execution.Awaiting
		wantExecutionRejected += target.execution.RejectedTotal
		wantExecutionPanic += target.execution.PanicTotal
		wantActive += target.timer.Active
		wantDuePending += target.timer.DuePending
		wantTimerReady += target.timer.Ready
		wantTimerRunning += target.timer.Running
		wantTimerRejected += target.timer.RejectedTotal
		wantTimerPanic += target.timer.PanicTotal
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
		execution.RejectedTotal != wantExecutionRejected || execution.PanicTotal != wantExecutionPanic {
		t.Fatalf("Execution aggregate = %+v", execution)
	}
	timer := summary.Services.Timer
	if timer.Active != wantActive || timer.DuePending != wantDuePending ||
		timer.Ready != wantTimerReady || timer.Running != wantTimerRunning ||
		timer.RejectedTotal != wantTimerRejected || timer.PanicTotal != wantTimerPanic {
		t.Fatalf("Timer aggregate = %+v", timer)
	}
	if summary.Services.Event.HandlerFailureTotal != wantEventFailures {
		t.Fatalf("Event aggregate = %+v", summary.Services.Event)
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
		Created: 1, Initializing: 1, Initialized: 1, Starting: 1, Running: 1,
		Retired: 1, Stopping: 1, Stopped: 1, Failed: 1, Unknown: 1,
	}) {
		t.Fatalf("state aggregate = %+v", aggregate)
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
