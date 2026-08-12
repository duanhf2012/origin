package blueprintmodule

import (
	"context"
	"testing"
	"time"
)

func BenchmarkInstanceStartSynchronous(b *testing.B) {
	nodeDir, graphDir := writeLifecycleFixture(b)
	module, err := New(Config{NodeDir: nodeDir, GraphDir: graphDir})
	if err != nil {
		b.Fatal(err)
	}
	if err = module.RegisterNodes(
		func() IExecNode { return &lifecycleNode{} },
		func() IExecNode { return &lifecycleAsyncFixtureNode{} },
	); err != nil {
		b.Fatal(err)
	}
	if err = module.OnStart(context.Background()); err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = module.OnStop(context.Background()) })
	instance, err := module.Create("lifecycle")
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = instance.Close() })

	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		execution, startErr := instance.Start(context.Background(), 1)
		if startErr != nil || !execution.IsDone() {
			b.Fatalf("Start() execution=%v error=%v", execution, startErr)
		}
	}
}

func BenchmarkCreateClose(b *testing.B) {
	nodeDir, graphDir := writeLifecycleFixture(b)
	module, err := New(Config{NodeDir: nodeDir, GraphDir: graphDir})
	if err != nil {
		b.Fatal(err)
	}
	if err = module.RegisterNodes(
		func() IExecNode { return &lifecycleNode{} },
		func() IExecNode { return &lifecycleAsyncFixtureNode{} },
	); err != nil {
		b.Fatal(err)
	}
	if err = module.OnStart(context.Background()); err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = module.OnStop(context.Background()) })

	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		instance, createErr := module.Create("lifecycle")
		if createErr != nil {
			b.Fatal(createErr)
		}
		if closeErr := instance.Close(); closeErr != nil {
			b.Fatal(closeErr)
		}
	}
}

func BenchmarkInstanceRunSynchronousInService(b *testing.B) {
	fixture := startInstanceTestFixture(b)
	instance := benchmarkCreateInstance(b, fixture, "lifecycle")

	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		if err := benchmarkServiceCall(fixture, func(ctx context.Context) error {
			_, runErr := instance.Run(ctx, 1)
			return runErr
		}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkInstanceSuspendResumeInService(b *testing.B) {
	fixture := startInstanceTestFixture(b)
	instance := benchmarkCreateInstance(b, fixture, "lifecycle_async")

	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		var execution *Execution
		if err := benchmarkServiceCall(fixture, func(ctx context.Context) error {
			var startErr error
			execution, startErr = instance.Start(ctx, 1)
			return startErr
		}); err != nil {
			b.Fatal(err)
		}
		handle := <-fixture.module.yielded
		if err := handle.Resume(); err != nil {
			b.Fatal(err)
		}
		select {
		case <-execution.Done():
		case <-time.After(blueprintTestTimeout):
			b.Fatal("suspended execution timed out")
		}
	}
}

func BenchmarkReloadInService(b *testing.B) {
	fixture := startInstanceTestFixture(b)

	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		if err := benchmarkServiceCall(fixture, func(ctx context.Context) error {
			_, reloadErr := fixture.module.Reload(ctx)
			return reloadErr
		}); err != nil {
			b.Fatal(err)
		}
	}
}

func benchmarkCreateInstance(b *testing.B, fixture *instanceTestFixture, graphName string) *Instance {
	b.Helper()
	var instance *Instance
	if err := benchmarkServiceCall(fixture, func(context.Context) error {
		var createErr error
		instance, createErr = fixture.module.Create(graphName)
		return createErr
	}); err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = instance.Close() })
	return instance
}

func benchmarkServiceCall(fixture *instanceTestFixture, call func(context.Context) error) error {
	result := make(chan error, 1)
	if err := fixture.service.DispatchAsync(func(ctx context.Context) { result <- call(ctx) }); err != nil {
		return err
	}
	select {
	case err := <-result:
		return err
	case <-time.After(blueprintTestTimeout):
		return context.DeadlineExceeded
	}
}
