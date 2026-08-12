package blueprintmodule

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/duanhf2012/origin/v3/errs"
	"github.com/duanhf2012/origin/v3/service"
)

func TestRunSuspensionReleasesServiceAndResumeCompletes(t *testing.T) {
	fixture := startInstanceTestFixture(t)
	result := make(chan error, 1)
	if err := fixture.service.DispatchAsync(func(ctx context.Context) {
		_, runErr := fixture.module.Run(ctx, "lifecycle_async", 1)
		result <- runErr
	}); err != nil {
		t.Fatal(err)
	}

	// Yield 发生后 Run 正在 Await；另一个 Service 任务仍必须能够获得执行权。
	var handle *YieldHandle
	select {
	case handle = <-fixture.module.yielded:
	case <-time.After(blueprintTestTimeout):
		t.Fatal("async node did not yield")
	}
	progressed := make(chan struct{}, 1)
	if err := fixture.service.DispatchAsync(func(context.Context) { progressed <- struct{}{} }); err != nil {
		t.Fatal(err)
	}
	select {
	case <-progressed:
	case <-time.After(blueprintTestTimeout):
		t.Fatal("Service did not process another task while Run awaited")
	}

	// Resume 来自测试 goroutine；后续 VM 片段必须由 Dispatcher 投递回 Service。
	if err := handle.Resume(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(blueprintTestTimeout):
		t.Fatal("Run did not finish after Resume")
	}
}

func TestStartReturnsSuspendedExecution(t *testing.T) {
	fixture := startInstanceTestFixture(t)
	var execution *Execution
	err := dispatchInstanceTest(t, fixture, func(ctx context.Context) error {
		instance, err := fixture.module.Create("lifecycle_async")
		if err != nil {
			return err
		}
		execution, err = instance.Start(ctx, 1)
		if err != nil {
			return err
		}
		if execution.State() != ExecutionSuspended || execution.IsDone() {
			t.Fatalf("Start state=%v done=%v", execution.State(), execution.IsDone())
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	handle := <-fixture.module.yielded
	if err := handle.Resume(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-execution.Done():
	case <-time.After(blueprintTestTimeout):
		t.Fatal("Execution did not complete")
	}
}

func TestResumeQueueFullCanRetryWithoutConsumingHandle(t *testing.T) {
	fixture := startInstanceTestFixtureWithScheduler(t, service.SchedulerConfig{
		MaxTasks: 1, MaxAwaitTasks: 1, DefaultAwaitTimeout: time.Second,
	})
	var execution *Execution
	if err := dispatchInstanceTest(t, fixture, func(ctx context.Context) error {
		instance, createErr := fixture.module.Create("lifecycle_async")
		if createErr != nil {
			return createErr
		}
		var startErr error
		execution, startErr = instance.Start(ctx, 1)
		return startErr
	}); err != nil {
		t.Fatal(err)
	}
	handle := <-fixture.module.yielded

	// 用一个运行中的任务占满唯一根任务容量，模拟生产过载边界。
	entered := make(chan struct{})
	release := make(chan struct{})
	if err := fixture.service.DispatchAsync(func(context.Context) {
		close(entered)
		<-release
	}); err != nil {
		t.Fatal(err)
	}
	<-entered
	resumeErr := handle.Resume()
	close(release)
	if !errors.Is(resumeErr, errs.ErrServiceQueueFull) {
		t.Fatalf("Resume() error = %v, want queue full", resumeErr)
	}
	if execution.IsDone() {
		t.Fatal("queue rejection consumed or finished suspended execution")
	}
	// 等一个探针任务完整返回，确认占位任务已经从 accepted 计数释放，避免把调度器收尾竞争当成重试失败。
	probe := make(chan struct{}, 1)
	deadline := time.After(blueprintTestTimeout)
	for {
		dispatchErr := fixture.service.DispatchAsync(func(context.Context) { probe <- struct{}{} })
		if dispatchErr == nil {
			break
		}
		if !errors.Is(dispatchErr, errs.ErrServiceQueueFull) {
			t.Fatal(dispatchErr)
		}
		select {
		case <-deadline:
			t.Fatal("Service capacity was not released")
		case <-time.After(time.Millisecond):
		}
	}
	<-probe
	// ResumeTo 在提交失败时回滚句柄 used 状态；容量释放后同一个句柄可以再次提交。
	if err := handle.Resume(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-execution.Done():
	case <-time.After(blueprintTestTimeout):
		t.Fatal("retried Resume did not finish execution")
	}
}

func TestServiceStopCancelsSuspendedExecutionAndRejectsLateResume(t *testing.T) {
	fixture := startInstanceTestFixture(t)
	var execution *Execution
	if err := dispatchInstanceTest(t, fixture, func(ctx context.Context) error {
		instance, createErr := fixture.module.Create("lifecycle_async")
		if createErr != nil {
			return createErr
		}
		var startErr error
		execution, startErr = instance.Start(ctx, 1)
		return startErr
	}); err != nil {
		t.Fatal(err)
	}
	handle := <-fixture.module.yielded
	ctx, cancel := context.WithTimeout(context.Background(), blueprintTestTimeout)
	defer cancel()
	if err := fixture.node.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-execution.Done():
		_, resultErr := execution.Result()
		if !errors.Is(resultErr, ErrBlueprintClosed) {
			t.Fatalf("stopped Execution error = %v", resultErr)
		}
	case <-time.After(blueprintTestTimeout):
		t.Fatal("Service stop did not cancel suspended execution")
	}
	if err := handle.Resume(); !errors.Is(err, ErrBlueprintClosed) {
		t.Fatalf("late Resume() error = %v", err)
	}
}
