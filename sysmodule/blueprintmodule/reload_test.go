package blueprintmodule

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestReloadPublishesNewGraphAndUpdatesStats(t *testing.T) {
	fixture := startInstanceTestFixture(t)
	err := dispatchInstanceTest(t, fixture, func(ctx context.Context) error {
		result, err := fixture.module.Reload(ctx)
		if err != nil {
			return err
		}
		if !result.Applied || result.GraphCount != 2 {
			t.Fatalf("Reload() result = %+v", result)
		}
		stats := fixture.module.Stats()
		if stats.ReloadedTotal != 1 || stats.ReloadFailedTotal != 0 {
			t.Fatalf("Stats() = %+v", stats)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestReloadKeepsOldExecutionSnapshotAndUpdatesNextRun(t *testing.T) {
	fixture := startInstanceTestFixture(t)
	var instance *Instance
	var oldExecution *Execution
	err := dispatchInstanceTest(t, fixture, func(ctx context.Context) error {
		var err error
		instance, err = fixture.module.Create("lifecycle_async")
		if err != nil {
			return err
		}
		oldExecution, err = instance.Start(ctx, 1)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	oldHandle := <-fixture.module.yielded

	// 新版本在异步入口恢复后新增一个 LifecycleNode；旧执行快照不应看见这条边。
	updated := `{
		"nodes":[
			{"id":"entry","class":"LifecycleAsyncNode_1"},
			{"id":"record","class":"LifecycleNode"}
		],
		"edges":[{"source_node_id":"entry","des_node_id":"record","source_port_id":0,"des_port_id":0}]
	}`
	if err = os.WriteFile(filepath.Join(fixture.module.graphDir, "lifecycle_async.vgf"), []byte(updated), 0o644); err != nil {
		t.Fatal(err)
	}
	if err = dispatchInstanceTest(t, fixture, func(ctx context.Context) error {
		_, reloadErr := fixture.module.Reload(ctx)
		return reloadErr
	}); err != nil {
		t.Fatal(err)
	}

	if err = oldHandle.Resume(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-oldExecution.Done():
	case <-time.After(blueprintTestTimeout):
		t.Fatal("old execution did not finish")
	}
	select {
	case <-fixture.module.executed:
		t.Fatal("old execution observed the reloaded edge")
	default:
	}

	var newExecution *Execution
	if err = dispatchInstanceTest(t, fixture, func(ctx context.Context) error {
		var startErr error
		newExecution, startErr = instance.Start(ctx, 1)
		return startErr
	}); err != nil {
		t.Fatal(err)
	}
	newHandle := <-fixture.module.yielded
	if err = newHandle.Resume(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-newExecution.Done():
	case <-time.After(blueprintTestTimeout):
		t.Fatal("new execution did not finish")
	}
	select {
	case <-fixture.module.executed:
	case <-time.After(blueprintTestTimeout):
		t.Fatal("new execution did not observe the reloaded edge")
	}
	if err = instance.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestReloadFailureKeepsOldGraphs(t *testing.T) {
	fixture := startInstanceTestFixture(t)
	brokenPath := filepath.Join(fixture.module.graphDir, "broken.vgf")
	if err := os.WriteFile(brokenPath, []byte(`{"nodes":[`), 0o644); err != nil {
		t.Fatal(err)
	}
	err := dispatchInstanceTest(t, fixture, func(ctx context.Context) error {
		result, reloadErr := fixture.module.Reload(ctx)
		if reloadErr == nil || result.Applied {
			t.Fatalf("Reload() result=%+v error=%v", result, reloadErr)
		}
		// 失败后旧图池仍然可以创建和执行，证明没有发布半成品。
		if _, runErr := fixture.module.Run(ctx, "lifecycle", 1); runErr != nil {
			return runErr
		}
		if stats := fixture.module.Stats(); stats.ReloadFailedTotal != 1 {
			t.Fatalf("Stats() = %+v", stats)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestReloadRejectsConcurrentTransaction(t *testing.T) {
	fixture := startInstanceTestFixture(t)
	fixture.module.reloadInProgress.Store(true)
	defer fixture.module.reloadInProgress.Store(false)
	err := dispatchInstanceTest(t, fixture, func(ctx context.Context) error {
		_, reloadErr := fixture.module.Reload(ctx)
		if !errors.Is(reloadErr, ErrReloadInProgress) {
			t.Fatalf("Reload() error = %v", reloadErr)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
