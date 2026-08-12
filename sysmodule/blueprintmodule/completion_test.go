package blueprintmodule

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/duanhf2012/origin/v3/errs"
	"github.com/duanhf2012/origin/v3/service"
)

func TestOnCompleteRunsExactlyOnceInServiceTask(t *testing.T) {
	fixture := startInstanceTestFixture(t)
	called := make(chan error, 1)
	err := dispatchInstanceTest(t, fixture, func(ctx context.Context) error {
		instance, err := fixture.module.Create("lifecycle_async")
		if err != nil {
			return err
		}
		execution, err := instance.Start(ctx, 1)
		if err != nil {
			return err
		}
		if err = execution.OnComplete(func(callbackCtx context.Context, _ PortArray, resultErr error) {
			// Await 成功证明 callback 当前拥有所属 Service 的执行权。
			if resultErr == nil {
				resultErr = fixture.module.Await(callbackCtx, func(context.Context) error { return nil })
			}
			called <- resultErr
		}); err != nil {
			return err
		}
		if err = execution.OnComplete(func(context.Context, PortArray, error) {}); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("duplicate OnComplete error = %v", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := (<-fixture.module.yielded).Resume(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-called:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(blueprintTestTimeout):
		t.Fatal("OnComplete callback timed out")
	}
}

func TestOnCompleteForSynchronousExecutionIsNotInline(t *testing.T) {
	fixture := startInstanceTestFixture(t)
	called := make(chan struct{}, 1)
	err := dispatchInstanceTest(t, fixture, func(ctx context.Context) error {
		instance, err := fixture.module.Create("lifecycle")
		if err != nil {
			return err
		}
		execution, err := instance.Start(ctx, 1)
		if err != nil {
			return err
		}
		if err = execution.OnComplete(func(context.Context, PortArray, error) { called <- struct{}{} }); err != nil {
			return err
		}
		select {
		case <-called:
			t.Fatal("OnComplete callback ran inline")
		default:
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-called:
	case <-time.After(blueprintTestTimeout):
		t.Fatal("synchronous completion callback timed out")
	}
}

func TestOnCompleteDeadlineCancelsSuspendedExecution(t *testing.T) {
	fixture := startInstanceTestFixture(t)
	called := make(chan error, 1)
	err := dispatchInstanceTest(t, fixture, func(taskCtx context.Context) error {
		ctx, cancel := context.WithTimeout(taskCtx, 20*time.Millisecond)
		t.Cleanup(cancel)
		instance, err := fixture.module.Create("lifecycle_async")
		if err != nil {
			return err
		}
		execution, err := instance.Start(ctx, 1)
		if err != nil {
			return err
		}
		return execution.OnComplete(func(_ context.Context, _ PortArray, resultErr error) {
			called <- resultErr
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	<-fixture.module.yielded
	select {
	case resultErr := <-called:
		if resultErr == nil || (!errors.Is(resultErr, context.DeadlineExceeded) && !errors.Is(resultErr, ErrExecutionCanceled)) {
			t.Fatalf("completion error = %v", resultErr)
		}
	case <-time.After(blueprintTestTimeout):
		t.Fatal("deadline completion callback timed out")
	}
}

func TestOnCompleteQueueFullDoesNotCancelExecutionAndAllowsRetry(t *testing.T) {
	fixture := startInstanceTestFixtureWithScheduler(t, service.SchedulerConfig{
		MaxTasks: 1, MaxAwaitTasks: 1, DefaultAwaitTimeout: time.Second,
	})
	var execution *Execution
	err := dispatchInstanceTest(t, fixture, func(ctx context.Context) error {
		instance, createErr := fixture.module.Create("lifecycle_async")
		if createErr != nil {
			return createErr
		}
		var startErr error
		execution, startErr = instance.Start(ctx, 1)
		if startErr != nil {
			return startErr
		}
		// 当前根任务占满唯一容量，完成任务必须立即拒绝；该失败不能偷偷取消蓝图。
		completionErr := execution.OnComplete(func(context.Context, PortArray, error) {})
		if !errors.Is(completionErr, errs.ErrServiceQueueFull) {
			t.Fatalf("OnComplete() error = %v, want queue full", completionErr)
		}
		if execution.IsDone() {
			t.Fatal("queue rejection canceled the execution")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// 登记失败会回滚单次登记标记。先取消 Execution，待原根任务释放容量后，终态 Execution 仍可重新登记。
	if !execution.Cancel() {
		t.Fatal("Cancel() did not accept suspended execution")
	}
	select {
	case <-execution.Done():
	case <-time.After(blueprintTestTimeout):
		t.Fatal("canceled execution did not finish")
	}
	called := make(chan error, 1)
	if err = execution.OnComplete(func(_ context.Context, _ PortArray, resultErr error) { called <- resultErr }); err != nil {
		t.Fatal(err)
	}
	<-fixture.module.yielded
	select {
	case callbackErr := <-called:
		if !errors.Is(callbackErr, ErrExecutionCanceled) {
			t.Fatalf("callback error = %v", callbackErr)
		}
	case <-time.After(blueprintTestTimeout):
		t.Fatal("retried completion timed out")
	}
}
