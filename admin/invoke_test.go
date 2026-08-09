package admin

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/duanhf2012/origin/v3/errs"
	"github.com/duanhf2012/origin/v3/internal/timerwheel"
	originlog "github.com/duanhf2012/origin/v3/log"
	"github.com/duanhf2012/origin/v3/service"
)

const adminInvokeTestTimeout = 5 * time.Second

// adminInvokeService 使用真实 ServiceScheduler 验证管理 Handler 的执行身份与串行性。
type adminInvokeService struct {
	service.Service
	value int
}

// adminInvokeRuntime 提供测试可控的生命周期状态和最小 Service 运行环境。
type adminInvokeRuntime struct {
	state  atomic.Uint32
	active atomic.Int64
	nextID atomic.Uint64
}

func (runtime *adminInvokeRuntime) NodeID() string      { return "node-1" }
func (runtime *adminInvokeRuntime) ServiceName() string { return "AdminInvokeService" }
func (runtime *adminInvokeRuntime) State() service.State {
	return service.State(runtime.state.Load())
}
func (runtime *adminInvokeRuntime) Logger() originlog.Logger { return originlog.NewNop() }
func (runtime *adminInvokeRuntime) LookupLocalService(string) (service.IService, bool) {
	return nil, false
}
func (runtime *adminInvokeRuntime) AcquireTimerSlot() (service.TimerID, bool) {
	runtime.active.Add(1)
	return service.TimerID(runtime.nextID.Add(1)), true
}
func (runtime *adminInvokeRuntime) ReleaseTimerSlot() { runtime.active.Add(-1) }
func (runtime *adminInvokeRuntime) TimerLimit() int   { return 1024 }
func (runtime *adminInvokeRuntime) TimerLocation() *time.Location {
	return time.Local
}
func (runtime *adminInvokeRuntime) Failure() error      { return nil }
func (runtime *adminInvokeRuntime) ReportFailure(error) {}

// adminInvokeFixture 集中拥有 Service、Runtime 和 TimerEngine，确保测试 goroutine 可回收。
type adminInvokeFixture struct {
	target  *adminInvokeService
	runtime *adminInvokeRuntime
	engine  *timerwheel.Engine
}

// trackedAfterFuncContext 记录 context.AfterFunc 是否登记并显式停止父取消回调。
type trackedAfterFuncContext struct {
	context.Context
	done             chan struct{}
	cancelOnRegister bool
	cancelOnce       sync.Once
	canceled         atomic.Bool
	callbackDone     chan struct{}
	registered       atomic.Int64
	stopped          atomic.Int64
}

func (ctx *trackedAfterFuncContext) Done() <-chan struct{} { return ctx.done }

func (ctx *trackedAfterFuncContext) Err() error {
	if ctx.canceled.Load() {
		return context.Canceled
	}
	return ctx.Context.Err()
}

// AfterFunc 实现标准库识别的取消回调接口；当前用例的父 Context 永不取消。
func (ctx *trackedAfterFuncContext) AfterFunc(callback func()) func() bool {
	ctx.registered.Add(1)
	if ctx.cancelOnRegister {
		ctx.cancelOnce.Do(func() {
			ctx.canceled.Store(true)
			close(ctx.done)
		})
		go func() {
			callback()
			close(ctx.callbackDone)
		}()
	}
	var once sync.Once
	return func() bool {
		stopped := false
		once.Do(func() {
			ctx.stopped.Add(1)
			stopped = true
		})
		return stopped
	}
}

// startAdminInvokeService 按生产启动顺序创建具有真实唯一执行槽的测试 Service。
func startAdminInvokeService(t testing.TB) *adminInvokeService {
	t.Helper()
	return startAdminInvokeFixture(t, service.SchedulerConfig{
		MaxTasks:            256,
		MaxAwaitTasks:       256,
		DefaultAwaitTimeout: adminInvokeTestTimeout,
	}).target
}

// startAdminInvokeFixture 允许边界测试控制 Scheduler 容量和公开生命周期状态。
func startAdminInvokeFixture(
	t testing.TB,
	config service.SchedulerConfig,
) *adminInvokeFixture {
	t.Helper()

	target := &adminInvokeService{}
	runtimeState := &adminInvokeRuntime{}
	runtimeState.state.Store(uint32(service.StateStarting))
	if err := service.BindRuntime(target, runtimeState); err != nil {
		t.Fatalf("BindRuntime() error = %v", err)
	}
	engine, err := timerwheel.New(timerwheel.DefaultOptions())
	if err != nil {
		t.Fatalf("timerwheel.New() error = %v", err)
	}
	if err := engine.Start(); err != nil {
		t.Fatalf("TimerEngine.Start() error = %v", err)
	}
	if err := service.PrepareScheduler(target, config, engine); err != nil {
		_ = engine.Close()
		t.Fatalf("PrepareScheduler() error = %v", err)
	}
	runtimeState.state.Store(uint32(service.StateRunning))
	if err := service.ActivateScheduler(target); err != nil {
		_ = engine.Close()
		t.Fatalf("ActivateScheduler() error = %v", err)
	}

	fixture := &adminInvokeFixture{target: target, runtime: runtimeState, engine: engine}
	t.Cleanup(func() {
		// 即使断言提前失败，也按 Scheduler、TimerEngine 的所有权逆序完成回收。
		runtimeState.state.Store(uint32(service.StateStopping))
		stopCtx, cancel := context.WithTimeout(context.Background(), adminInvokeTestTimeout)
		_ = service.StopScheduler(stopCtx, target)
		cancel()
		runtimeState.state.Store(uint32(service.StateStopped))
		_ = engine.Close()
	})
	return fixture
}

// runConcurrentInvocations 同时启动固定数量调用，并等待全部调用终态，避免测试辅助 goroutine 遗留。
func runConcurrentInvocations(t testing.TB, calls int, invoke func() error) {
	t.Helper()
	start := make(chan struct{})
	errorsFound := make(chan error, calls)
	var wait sync.WaitGroup
	wait.Add(calls)
	for range calls {
		go func() {
			defer wait.Done()
			<-start
			errorsFound <- invoke()
		}()
	}
	close(start)
	wait.Wait()
	close(errorsFound)
	for err := range errorsFound {
		if err != nil {
			t.Errorf("InvokeService() error = %v", err)
		}
	}
}

// TestInvokeServiceSerializesConcurrentMutation 防止管理 Handler 绕过 Service FIFO，
// 并用 Handler 内 Await 证明执行闭包持有真实 Service Task 身份。
func TestInvokeServiceSerializesConcurrentMutation(t *testing.T) {
	target := startAdminInvokeService(t)
	endpoint := Post("increment", func(ctx context.Context, _ Request) (Response, error) {
		if err := target.Await(ctx, func(context.Context) error { return nil }); err != nil {
			return Response{}, fmt.Errorf("admin handler does not own service task: %w", err)
		}
		current := target.value
		runtime.Gosched()
		target.value = current + 1
		return Empty(http.StatusNoContent), nil
	})

	const calls = 128
	runConcurrentInvocations(t, calls, func() error {
		_, err := InvokeService(context.Background(), target, endpoint, Request{})
		return err
	})
	if target.value != calls {
		t.Fatalf("value = %d", target.value)
	}
}

// TestInvokeServiceCanceledBeforeDispatch 防止已经取消的 HTTP 请求仍占用 Service 队列
// 或执行具有业务副作用的管理 Handler。
func TestInvokeServiceCanceledBeforeDispatch(t *testing.T) {
	target := startAdminInvokeService(t)
	var called atomic.Bool
	endpoint := Post("cancel-before-dispatch", func(context.Context, Request) (Response, error) {
		called.Store(true)
		return Empty(http.StatusNoContent), nil
	})
	callerCtx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := InvokeService(callerCtx, target, endpoint, Request{})
	if !errs.IsCode(err, errs.CodeCanceled) {
		t.Fatalf("InvokeService() error = %v", err)
	}
	if called.Load() {
		t.Fatal("调用前已取消时 Handler 仍被执行")
	}
}

// TestInvokeServiceRejectsNilCallerContext 防止公开管理入口在参数校验阶段 panic，
// 或让缺少调用方生命周期的请求进入 Service 队列并执行 Handler 副作用。
func TestInvokeServiceRejectsNilCallerContext(t *testing.T) {
	target := startAdminInvokeService(t)
	var called atomic.Bool
	_, err := InvokeService(
		nil,
		target,
		Post("nil-context", func(context.Context, Request) (Response, error) {
			called.Store(true)
			return Empty(http.StatusNoContent), nil
		}),
		Request{},
	)
	if !errors.Is(err, errs.ErrInvalidArgument) {
		t.Fatalf("InvokeService(nil) error = %v, want ErrInvalidArgument", err)
	}
	if called.Load() {
		t.Fatal("nil caller Context 仍执行 Handler")
	}
}

// TestInvokeServiceCanceledWhileQueuedSkipsHandler 防止调用虽然已经进入 FIFO、但尚未取得
// 执行槽时发生的取消仍启动 Handler；队列项只负责交付取消终态并归还容量。
func TestInvokeServiceCanceledWhileQueuedSkipsHandler(t *testing.T) {
	fixture := startAdminInvokeFixture(t, service.SchedulerConfig{
		MaxTasks:            3,
		MaxAwaitTasks:       1,
		DefaultAwaitTimeout: adminInvokeTestTimeout,
	})
	blockerStarted := make(chan struct{})
	releaseBlocker := make(chan struct{})
	if err := fixture.target.DispatchAsync(func(context.Context) {
		close(blockerStarted)
		<-releaseBlocker
	}); err != nil {
		t.Fatalf("DispatchAsync(blocker) error = %v", err)
	}
	<-blockerStarted

	callerCtx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	var called atomic.Bool
	go func() {
		_, err := InvokeService(
			callerCtx,
			fixture.target,
			Post("cancel-while-queued", func(context.Context, Request) (Response, error) {
				called.Store(true)
				return Empty(http.StatusNoContent), nil
			}),
			Request{},
		)
		result <- err
	}()
	deadline := time.Now().Add(2 * time.Second)
	for fixture.target.ExecutionStats().Ready != 1 {
		if time.Now().After(deadline) {
			close(releaseBlocker)
			t.Fatal("InvokeService 任务未进入 FIFO")
		}
		runtime.Gosched()
	}
	cancel()
	if err := <-result; !errs.IsCode(err, errs.CodeCanceled) {
		close(releaseBlocker)
		t.Fatalf("InvokeService() error = %v", err)
	}
	close(releaseBlocker)

	probeDone := make(chan struct{})
	if err := fixture.target.DispatchAsync(func(context.Context) { close(probeDone) }); err != nil {
		t.Fatalf("DispatchAsync(probe) error = %v", err)
	}
	select {
	case <-probeDone:
	case <-time.After(2 * time.Second):
		t.Fatal("取消队列项未归还 Service 执行槽")
	}
	if called.Load() {
		t.Fatal("取得执行槽前已取消时 Handler 仍被执行")
	}
}

// TestInvokeServiceQueueFull 防止管理入口绕过 Scheduler 的根任务硬上限或暗中等待容量。
func TestInvokeServiceQueueFull(t *testing.T) {
	fixture := startAdminInvokeFixture(t, service.SchedulerConfig{
		MaxTasks:            1,
		MaxAwaitTasks:       1,
		DefaultAwaitTimeout: adminInvokeTestTimeout,
	})
	started := make(chan struct{})
	release := make(chan struct{})
	if err := fixture.target.DispatchAsync(func(context.Context) {
		close(started)
		<-release
	}); err != nil {
		t.Fatalf("DispatchAsync(blocker) error = %v", err)
	}
	<-started

	_, err := InvokeService(
		context.Background(),
		fixture.target,
		Post("queue-full", func(context.Context, Request) (Response, error) {
			return Empty(http.StatusNoContent), nil
		}),
		Request{},
	)
	close(release)
	if !errors.Is(err, errs.ErrServiceQueueFull) {
		t.Fatalf("InvokeService() error = %v, want ErrServiceQueueFull", err)
	}
}

// TestInvokeServiceRetiredStillExecutes 固定 Retired 只退出默认发现候选、并不拒绝明确管理目标。
func TestInvokeServiceRetiredStillExecutes(t *testing.T) {
	fixture := startAdminInvokeFixture(t, service.SchedulerConfig{
		MaxTasks:            2,
		MaxAwaitTasks:       1,
		DefaultAwaitTimeout: adminInvokeTestTimeout,
	})
	fixture.runtime.state.Store(uint32(service.StateRetired))
	var called atomic.Bool

	_, err := InvokeService(
		context.Background(),
		fixture.target,
		Post("retired", func(context.Context, Request) (Response, error) {
			called.Store(true)
			return Empty(http.StatusNoContent), nil
		}),
		Request{},
	)
	if err != nil {
		t.Fatalf("InvokeService() error = %v", err)
	}
	if !called.Load() {
		t.Fatal("Retired Service 未执行明确管理 Endpoint")
	}
}

// TestInvokeServiceStoppedLifecycleErrors 防止管理入口把终止阶段错误折叠成不稳定的内部错误。
func TestInvokeServiceStoppedLifecycleErrors(t *testing.T) {
	tests := []struct {
		name  string
		state service.State
		want  error
	}{
		{name: "stopping", state: service.StateStopping, want: errs.ErrServiceStopping},
		{name: "stopped", state: service.StateStopped, want: errs.ErrServiceStopped},
		{name: "failed", state: service.StateFailed, want: errs.ErrServiceFailed},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := startAdminInvokeFixture(t, service.SchedulerConfig{
				MaxTasks:            2,
				MaxAwaitTasks:       1,
				DefaultAwaitTimeout: adminInvokeTestTimeout,
			})
			fixture.runtime.state.Store(uint32(test.state))
			var called atomic.Bool

			_, err := InvokeService(
				context.Background(),
				fixture.target,
				Post("lifecycle", func(context.Context, Request) (Response, error) {
					called.Store(true)
					return Empty(http.StatusNoContent), nil
				}),
				Request{},
			)
			if !errors.Is(err, test.want) {
				t.Fatalf("InvokeService() error = %v, want %v", err, test.want)
			}
			if called.Load() {
				t.Fatal("停止阶段拒绝投递后 Handler 仍被执行")
			}
		})
	}
}

// TestInvokeServiceCanceledDoesNotRollbackCommittedMutation 固定取消只终止等待和 Handler
// Context，不撤销 Handler 在取消线性化点之前已经提交的 Service 状态。
func TestInvokeServiceCanceledDoesNotRollbackCommittedMutation(t *testing.T) {
	target := startAdminInvokeService(t)
	committed := make(chan struct{})
	callerCtx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := InvokeService(
			callerCtx,
			target,
			Post("commit-before-cancel", func(ctx context.Context, _ Request) (Response, error) {
				target.value = 1
				close(committed)
				<-ctx.Done()
				return Empty(http.StatusNoContent), nil
			}),
			Request{},
		)
		result <- err
	}()
	<-committed
	cancel()

	if err := <-result; !errs.IsCode(err, errs.CodeCanceled) {
		t.Fatalf("InvokeService() error = %v", err)
	}
	if target.value != 1 {
		t.Fatalf("取消后 value = %d, want committed value 1", target.value)
	}
}

// TestInvokeServiceHandlerCanAwait 防止管理 Handler 在 Await 期间继续占用唯一执行槽，
// 或恢复时丢失原 Task 身份并看不到等待阶段已经串行提交的新版本。
func TestInvokeServiceHandlerCanAwait(t *testing.T) {
	target := startAdminInvokeService(t)
	waiting := make(chan struct{})
	releaseWait := make(chan struct{})
	mutated := make(chan struct{})
	result := make(chan error, 1)

	go func() {
		_, err := InvokeService(
			context.Background(),
			target,
			Post("await", func(ctx context.Context, _ Request) (Response, error) {
				if err := target.Await(ctx, func(waitCtx context.Context) error {
					close(waiting)
					select {
					case <-releaseWait:
						return nil
					case <-waitCtx.Done():
						return waitCtx.Err()
					}
				}); err != nil {
					return Response{}, err
				}
				if target.value != 1 {
					return Response{}, fmt.Errorf("restored handler saw version %d", target.value)
				}
				return Empty(http.StatusNoContent), nil
			}),
			Request{},
		)
		result <- err
	}()
	<-waiting
	if err := target.DispatchAsync(func(context.Context) {
		target.value = 1
		close(mutated)
	}); err != nil {
		close(releaseWait)
		t.Fatalf("DispatchAsync(mutation) error = %v", err)
	}
	select {
	case <-mutated:
		close(releaseWait)
	case <-time.After(2 * time.Second):
		close(releaseWait)
		t.Fatal("Handler Await 未释放 Service 执行槽")
	}
	if err := <-result; err != nil {
		t.Fatalf("InvokeService() error = %v", err)
	}
}

// TestInvokeServiceCanceledCallerDoesNotLeakResultSender 防止调用方取消后任务阻塞在结果发送，
// 通过同一 Service 的后续探针 Task 证明原任务已完整退出并归还唯一执行槽。
func TestInvokeServiceCanceledCallerDoesNotLeakResultSender(t *testing.T) {
	target := startAdminInvokeService(t)
	started := make(chan struct{})
	release := make(chan struct{})
	callerCtx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := InvokeService(
			callerCtx,
			target,
			Post("cancel-result", func(context.Context, Request) (Response, error) {
				close(started)
				<-release
				return Empty(http.StatusNoContent), nil
			}),
			Request{},
		)
		result <- err
	}()
	<-started
	cancel()
	select {
	case err := <-result:
		if !errs.IsCode(err, errs.CodeCanceled) {
			t.Fatalf("InvokeService() error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("调用方取消后 InvokeService 未及时返回")
	}

	close(release)
	probeDone := make(chan struct{})
	if err := target.DispatchAsync(func(context.Context) { close(probeDone) }); err != nil {
		t.Fatalf("DispatchAsync(probe) error = %v", err)
	}
	select {
	case <-probeDone:
	case <-time.After(2 * time.Second):
		t.Fatal("取消后的结果发送阻塞了 Service 执行槽")
	}
}

// TestInvokeServiceStopsCancellationPropagation 防止每次调用完成后仍由父 Context 持有
// Handler 取消闭包，形成与 HTTP 请求生命周期等长的无效引用。
func TestInvokeServiceStopsCancellationPropagation(t *testing.T) {
	target := startAdminInvokeService(t)
	callerCtx := &trackedAfterFuncContext{
		Context: context.Background(),
		done:    make(chan struct{}),
	}
	_, err := InvokeService(
		callerCtx,
		target,
		Post("stop-cancel-propagation", func(context.Context, Request) (Response, error) {
			return Empty(http.StatusNoContent), nil
		}),
		Request{},
	)
	if err != nil {
		t.Fatalf("InvokeService() error = %v", err)
	}
	if callerCtx.registered.Load() != 1 || callerCtx.stopped.Load() != 1 {
		t.Fatalf(
			"AfterFunc registered/stopped = %d/%d, want 1/1",
			callerCtx.registered.Load(),
			callerCtx.stopped.Load(),
		)
	}
}

// TestInvokeServiceCancellationDuringRegistrationSkipsHandler 固定 AfterFunc 登记与 Handler
// 启动之间的取消线性化点：登记时已经终止的调用不能再进入业务 Handler。
func TestInvokeServiceCancellationDuringRegistrationSkipsHandler(t *testing.T) {
	target := startAdminInvokeService(t)
	callerCtx := &trackedAfterFuncContext{
		Context:          context.Background(),
		done:             make(chan struct{}),
		cancelOnRegister: true,
		callbackDone:     make(chan struct{}),
	}
	var called atomic.Bool

	_, err := InvokeService(
		callerCtx,
		target,
		Post("cancel-during-registration", func(context.Context, Request) (Response, error) {
			called.Store(true)
			return Empty(http.StatusNoContent), nil
		}),
		Request{},
	)
	if !errs.IsCode(err, errs.CodeCanceled) {
		t.Fatalf("InvokeService() error = %v", err)
	}
	<-callerCtx.callbackDone
	probeDone := make(chan struct{})
	if dispatchErr := target.DispatchAsync(func(context.Context) { close(probeDone) }); dispatchErr != nil {
		t.Fatalf("DispatchAsync(probe) error = %v", dispatchErr)
	}
	select {
	case <-probeDone:
	case <-time.After(2 * time.Second):
		t.Fatal("登记期取消的调用未归还 Service 执行槽")
	}
	if called.Load() {
		t.Fatal("AfterFunc 登记时已取消仍执行 Handler")
	}
}

// TestInvokeServicePanicCompletesOnce 防止 Handler panic 逃逸到 Scheduler 后丢失结果，
// 导致调用只能等待 HTTP Deadline；同一调用只能执行一次并返回一个 Internal 终态。
func TestInvokeServicePanicCompletesOnce(t *testing.T) {
	target := startAdminInvokeService(t)
	var calls atomic.Int64
	callerCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err := InvokeService(
		callerCtx,
		target,
		Post("panic", func(context.Context, Request) (Response, error) {
			calls.Add(1)
			panic("admin invoke panic")
		}),
		Request{},
	)
	if !errs.IsCode(err, errs.CodeInternal) {
		t.Fatalf("InvokeService() error = %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("Handler calls = %d, want 1", calls.Load())
	}
}
