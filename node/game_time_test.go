package node

import (
	"context"
	"errors"
	"math"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/duanhf2012/origin/v3/errs"
	originlog "github.com/duanhf2012/origin/v3/log"
	"github.com/duanhf2012/origin/v3/service"
)

// gameTimeService 只用于取得真实 Node 生命周期和 Service Runtime，不覆盖任何业务行为。
type gameTimeService struct{ service.Service }

// gameTimeLifecycleService 验证 Service 在 OnInit/OnStart 中已经可以取得当前 Node 并调整时间。
type gameTimeLifecycleService struct {
	service.Service
	target time.Time
}

func (target *gameTimeLifecycleService) OnInit() error {
	return target.GetNode().SetTime(target.target)
}

func (target *gameTimeLifecycleService) OnStart(context.Context) error {
	return target.GetNode().AddTime(time.Hour)
}

// gameTimeStoppingService 使 Node 稳定停留在 Stopping，便于验证时间修改准入边界。
type gameTimeStoppingService struct {
	service.Service
	stopStarted chan struct{}
	releaseStop chan struct{}
}

func (target *gameTimeStoppingService) OnStop(context.Context) error {
	close(target.stopStarted)
	<-target.releaseStop
	return nil
}

// newStartedGameTimeNode 创建并启动包含指定 Service 的真实 Node，供跨 Scheduler 时间重排
// 测试使用；清理严格走正常 Stop，验证 Node 拥有全部 Timer 生命周期。
func newStartedGameTimeNode(
	t testing.TB,
	id string,
	targets ...*gameTimeService,
) *Node {
	t.Helper()
	bindings := make([]ServiceBinding, 0, len(targets))
	services := make([]string, 0, len(targets))
	for index, target := range targets {
		name := "GameTimeService" + string(rune('A'+index))
		services = append(services, name)
		bindings = append(bindings, ServiceBinding{
			Name:     name,
			Template: "GameTimeService",
			Service:  target,
		})
	}
	current, err := New(
		Config{ID: id, Services: services},
		bindings,
		originlog.NewNop(),
		Options{MaxTimersPerNode: 64, TimerLocation: time.UTC},
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := current.Start(context.Background()); err != nil {
		_ = current.Rollback(context.Background())
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = current.Stop(context.Background()) })
	return current
}

// newGameTimeNode 创建 UTC 时区的独立 Node，使时间断言不依赖测试机本地时区。
func newGameTimeNode(t testing.TB, id string) *Node {
	t.Helper()
	target := &gameTimeService{}
	current, err := New(
		Config{ID: id, Services: []string{"GameTimeService"}},
		[]ServiceBinding{{
			Name:     "GameTimeService",
			Template: "GameTimeService",
			Service:  target,
		}},
		originlog.NewNop(),
		Options{MaxTimersPerNode: 64, TimerLocation: time.UTC},
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() { _ = current.Rollback(context.Background()) })
	return current
}

// TestNodeGameTimeSetAndAdd 固定 Node 逻辑时间默认跟随真实时间、Set 后继续自然前进，
// Add 只修改偏移且允许负数的公开契约。
func TestNodeGameTimeSetAndAdd(t *testing.T) {
	current := newGameTimeNode(t, "game-1")

	realBefore := time.Now().UTC()
	logical := current.Now()
	realAfter := time.Now().UTC()
	if logical.Before(realBefore) || logical.After(realAfter) {
		t.Fatalf("默认 Node.Now() = %v, real range = [%v, %v]", logical, realBefore, realAfter)
	}

	target := time.Date(2030, 1, 2, 3, 4, 5, 0, time.FixedZone("UTC+8", 8*60*60))
	if err := current.SetTime(target); err != nil {
		t.Fatalf("SetTime() error = %v", err)
	}
	setNow := current.Now()
	if setNow.Location() != time.UTC || setNow.Before(target.In(time.UTC)) ||
		setNow.After(target.In(time.UTC).Add(100*time.Millisecond)) {
		t.Fatalf("SetTime 后 Now() = %v, target = %v", setNow, target)
	}

	if err := current.AddTime(24 * time.Hour); err != nil {
		t.Fatalf("AddTime(+24h) error = %v", err)
	}
	advanced := current.Now()
	if delta := advanced.Sub(setNow); delta < 24*time.Hour || delta > 24*time.Hour+100*time.Millisecond {
		t.Fatalf("AddTime(+24h) delta = %v", delta)
	}

	if err := current.AddTime(-48 * time.Hour); err != nil {
		t.Fatalf("AddTime(-48h) error = %v", err)
	}
	rewound := current.Now()
	if delta := rewound.Sub(advanced); delta < -48*time.Hour || delta > -48*time.Hour+100*time.Millisecond {
		t.Fatalf("AddTime(-48h) delta = %v", delta)
	}

	beforeNoop := current.Now()
	if err := current.AddTime(0); err != nil {
		t.Fatalf("AddTime(0) error = %v", err)
	}
	afterNoop := current.Now()
	if delta := afterNoop.Sub(beforeNoop); delta < 0 || delta > 100*time.Millisecond {
		t.Fatalf("AddTime(0) changed offset, elapsed = %v", delta)
	}
}

// TestNodeGameTimeRejectsZeroTarget 固定无意义目标不会清空或破坏已经提交的逻辑时间。
func TestNodeGameTimeRejectsZeroTarget(t *testing.T) {
	current := newGameTimeNode(t, "game-1")
	if err := current.SetTime(time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("initial SetTime() error = %v", err)
	}
	before := current.Now()

	err := current.SetTime(time.Time{})
	if !errs.IsCode(err, errs.CodeInvalidArgument) {
		t.Fatalf("SetTime(zero) error = %v", err)
	}
	after := current.Now()
	if delta := after.Sub(before); delta < 0 || delta > 100*time.Millisecond {
		t.Fatalf("非法 SetTime 修改了旧值, elapsed = %v", delta)
	}
}

// TestNodeGameTimeRejectsUnrepresentableAndOverflow 固定两类极端输入都不会静默饱和，
// 也不会在返回错误后部分提交新偏移。
func TestNodeGameTimeRejectsUnrepresentableAndOverflow(t *testing.T) {
	current := newGameTimeNode(t, "game-1")
	before := current.gameTimeOffset.Load()
	tooFar := time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC)
	if err := current.SetTime(tooFar); !errs.IsCode(err, errs.CodeInvalidArgument) {
		t.Fatalf("SetTime(unrepresentable) error = %v", err)
	}
	if got := current.gameTimeOffset.Load(); got != before {
		t.Fatalf("不可表达 SetTime 后 offset = %d, want %d", got, before)
	}

	current.gameTimeOffset.Store(math.MaxInt64 - 1)
	if err := current.AddTime(2 * time.Nanosecond); !errs.IsCode(err, errs.CodeInvalidArgument) {
		t.Fatalf("AddTime(overflow) error = %v", err)
	}
	if got := current.gameTimeOffset.Load(); got != math.MaxInt64-1 {
		t.Fatalf("溢出 AddTime 后 offset = %d, want %d", got, int64(math.MaxInt64-1))
	}
}

// TestNodeGameTimeConcurrentAddAndNow 使用可精确求和的 Add 负载验证修改线性化，
// 同时频繁读取 Now，交由 race detector 检查无锁读与修改锁之间的共享状态。
func TestNodeGameTimeConcurrentAddAndNow(t *testing.T) {
	current := newGameTimeNode(t, "game-1")
	const workerCount = 8
	const additionsPerWorker = 1_000
	var workers sync.WaitGroup
	workers.Add(workerCount)
	for worker := 0; worker < workerCount; worker++ {
		go func() {
			defer workers.Done()
			for index := 0; index < additionsPerWorker; index++ {
				if err := current.AddTime(time.Nanosecond); err != nil {
					t.Errorf("AddTime() error = %v", err)
					return
				}
				_ = current.Now()
			}
		}()
	}
	workers.Wait()
	if got, want := current.gameTimeOffset.Load(), int64(workerCount*additionsPerWorker); got != want {
		t.Fatalf("concurrent offset = %d, want %d", got, want)
	}
}

// TestNodeGameTimeRejectsMutationAfterStop 固定 Stop 发布边界后已经保留的
// NodeRuntime 只能读取最终时间，不能在 TimerEngine 回收后重新登记 Deadline。
func TestNodeGameTimeRejectsMutationAfterStop(t *testing.T) {
	target := &gameTimeService{}
	current := newStartedGameTimeNode(t, "game-1", target)
	if err := current.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if err := current.AddTime(time.Hour); !errors.Is(err, errs.ErrServiceStopped) {
		t.Fatalf("AddTime() after Stop error = %v", err)
	}
	if err := current.SetTime(time.Now()); !errors.Is(err, errs.ErrServiceStopped) {
		t.Fatalf("SetTime() after Stop error = %v", err)
	}
}

// TestNodeGameTimeRejectsMutationAfterRollback 固定启动失败回滚后的 Failed 终态，
// 避免管理对象仍持有 NodeRuntime 时误以为可以恢复该一次性 Node。
func TestNodeGameTimeRejectsMutationAfterRollback(t *testing.T) {
	current := newGameTimeNode(t, "game-1")
	if err := current.Rollback(context.Background()); err != nil {
		t.Fatalf("Rollback() error = %v", err)
	}
	if err := current.AddTime(time.Hour); !errors.Is(err, errs.ErrServiceFailed) {
		t.Fatalf("AddTime() after Rollback error = %v", err)
	}
}

// TestNodeGameTimeAvailableInInitAndStart 防止 GetNode 或时间修改被错误限制到 Running 阶段，
// 导致活动服务无法在启动时恢复持久化的游戏时间。
func TestNodeGameTimeAvailableInInitAndStart(t *testing.T) {
	initial := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	target := &gameTimeLifecycleService{target: initial}
	current, err := New(
		Config{ID: "game-1", Services: []string{"GameTimeLifecycleService"}},
		[]ServiceBinding{{
			Name:     "GameTimeLifecycleService",
			Template: "GameTimeLifecycleService",
			Service:  target,
		}},
		originlog.NewNop(),
		Options{MaxTimersPerNode: 8, TimerLocation: time.UTC},
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := current.Start(context.Background()); err != nil {
		_ = current.Rollback(context.Background())
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = current.Stop(context.Background()) })
	got := current.Now()
	want := initial.Add(time.Hour)
	if got.Before(want) || got.After(want.Add(100*time.Millisecond)) {
		t.Fatalf("Now() after OnInit/OnStart = %v, want near %v", got, want)
	}
}

// TestNodeGameTimeRejectsMutationWhileStopping 在 OnStop 尚未返回时直接观察公开状态边界，
// 证明停止清理期间不会再重排即将回收的 TimerEngine。
func TestNodeGameTimeRejectsMutationWhileStopping(t *testing.T) {
	target := &gameTimeStoppingService{
		stopStarted: make(chan struct{}),
		releaseStop: make(chan struct{}),
	}
	current, err := New(
		Config{ID: "game-1", Services: []string{"GameTimeStoppingService"}},
		[]ServiceBinding{{
			Name:     "GameTimeStoppingService",
			Template: "GameTimeStoppingService",
			Service:  target,
		}},
		originlog.NewNop(),
		Options{MaxTimersPerNode: 8, TimerLocation: time.UTC},
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := current.Start(context.Background()); err != nil {
		_ = current.Rollback(context.Background())
		t.Fatalf("Start() error = %v", err)
	}
	stopped := make(chan error, 1)
	go func() { stopped <- current.Stop(context.Background()) }()
	<-target.stopStarted
	mutationErr := current.AddTime(time.Hour)
	close(target.releaseStop)
	stopErr := <-stopped
	if !errors.Is(mutationErr, errs.ErrServiceStopping) {
		t.Fatalf("AddTime() while Stopping error = %v", mutationErr)
	}
	if stopErr != nil {
		t.Fatalf("Stop() error = %v", stopErr)
	}
}

// TestNodeGameTimeConcurrentRebaseAndTimerCreation 覆盖正常 Running 阶段的时间修改、
// Timer 创建/取消和 Now 读取交错，重点交由 race detector 验证 Scheduler 绑定与代次一致性。
func TestNodeGameTimeConcurrentRebaseAndTimerCreation(t *testing.T) {
	target := &gameTimeService{}
	current := newStartedGameTimeNode(t, "game-1", target)
	const creatorCount = 4
	const operationsPerCreator = 200
	var created atomic.Int64
	var workers sync.WaitGroup
	workers.Add(creatorCount + 1)
	go func() {
		defer workers.Done()
		for index := 0; index < operationsPerCreator; index++ {
			if err := current.AddTime(time.Millisecond); err != nil {
				t.Errorf("AddTime() error = %v", err)
				return
			}
			_ = current.Now()
		}
	}()
	for worker := 0; worker < creatorCount; worker++ {
		go func() {
			defer workers.Done()
			for index := 0; index < operationsPerCreator; index++ {
				id := target.AfterFunc(time.Hour, func(context.Context, service.TimerID) {})
				if id == service.InvalidTimerID {
					// 64 个 Node 共享槽位在极端交错下可以短暂用尽，拒绝本身是合法结果。
					continue
				}
				created.Add(1)
				target.CancelTimer(&id)
			}
		}()
	}
	workers.Wait()
	if created.Load() == 0 {
		t.Fatal("并发期间没有任何 Timer 创建成功")
	}
}

// TestNodeGameTimeAffectsEveryServiceTimer 固定时间所有权属于 Node：任意 Service 通过当前
// Node 调整时间后，同 Node 全部 Scheduler 的业务 Timer 都必须被重排并各触发一次。
func TestNodeGameTimeAffectsEveryServiceTimer(t *testing.T) {
	first := &gameTimeService{}
	second := &gameTimeService{}
	newStartedGameTimeNode(t, "game-1", first, second)
	firstFired := make(chan struct{}, 1)
	secondFired := make(chan struct{}, 1)
	if id := first.AfterFunc(time.Hour, func(context.Context, service.TimerID) {
		firstFired <- struct{}{}
	}); id == service.InvalidTimerID {
		t.Fatal("first AfterFunc() 创建失败")
	}
	if id := second.AfterFunc(time.Hour, func(context.Context, service.TimerID) {
		secondFired <- struct{}{}
	}); id == service.InvalidTimerID {
		t.Fatal("second AfterFunc() 创建失败")
	}

	if err := first.GetNode().AddTime(2 * time.Hour); err != nil {
		t.Fatalf("GetNode().AddTime() error = %v", err)
	}
	for name, fired := range map[string]<-chan struct{}{
		"first":  firstFired,
		"second": secondFired,
	} {
		select {
		case <-fired:
			// 每个 Service 都取得了各自串行执行权。
		case <-time.After(time.Second):
			t.Fatalf("等待 %s Service Timer 超时", name)
		}
	}
}

// TestNodeGameTimeIsolatedAcrossNodes 防止同一 Application 进程中的 Node 共享时间偏移或
// Timer 重排；只调整 game-1 时，game-2 的逻辑时间和业务 Timer 必须保持原状。
func TestNodeGameTimeIsolatedAcrossNodes(t *testing.T) {
	firstService := &gameTimeService{}
	secondService := &gameTimeService{}
	firstNode := newStartedGameTimeNode(t, "game-1", firstService)
	secondNode := newStartedGameTimeNode(t, "game-2", secondService)
	firstFired := make(chan struct{}, 1)
	secondFired := make(chan struct{}, 1)
	firstService.AfterFunc(time.Hour, func(context.Context, service.TimerID) {
		firstFired <- struct{}{}
	})
	secondService.AfterFunc(time.Hour, func(context.Context, service.TimerID) {
		secondFired <- struct{}{}
	})
	secondBefore := secondNode.Now()

	if err := firstNode.AddTime(2 * time.Hour); err != nil {
		t.Fatalf("firstNode.AddTime() error = %v", err)
	}
	select {
	case <-firstFired:
		// 目标 Node 已经跨过 After 名义点。
	case <-time.After(time.Second):
		t.Fatal("game-1 Timer 未随逻辑时间触发")
	}
	select {
	case <-secondFired:
		t.Fatal("game-2 Timer 被其他 Node 的时间调整触发")
	case <-time.After(100 * time.Millisecond):
	}
	if delta := secondNode.Now().Sub(secondBefore); delta < 0 || delta > time.Second {
		t.Fatalf("game-2 逻辑时间被修改, elapsed = %v", delta)
	}
}
