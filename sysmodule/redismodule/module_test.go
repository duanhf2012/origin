package redismodule

import (
	"context"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/redis/go-redis/v9"
)

type passthroughHook struct{}

func (*passthroughHook) DialHook(next redis.DialHook) redis.DialHook {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		return next(ctx, network, address)
	}
}
func (*passthroughHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error { return next(ctx, cmd) }
}
func (*passthroughHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, commands []redis.Cmder) error { return next(ctx, commands) }
}

type fakeRuntime struct {
	handle   redis.UniversalClient
	pingErr  error
	closeErr error
	pings    atomic.Int32
	closes   atomic.Int32
}

type blockingRuntime struct {
	*fakeRuntime
	started chan struct{}
	release chan struct{}
}

type blockingCloseRuntime struct {
	*fakeRuntime
	started chan struct{}
	release chan struct{}
}

type observedContext struct {
	context.Context
	once     sync.Once
	observed chan struct{}
}

func (current *observedContext) Done() <-chan struct{} {
	current.once.Do(func() { close(current.observed) })
	return current.Context.Done()
}

func (runtime *blockingCloseRuntime) close() error {
	runtime.closes.Add(1)
	close(runtime.started)
	<-runtime.release
	_ = runtime.handle.Close()
	return runtime.closeErr
}

func (runtime *blockingRuntime) ping(ctx context.Context) error {
	runtime.pings.Add(1)
	close(runtime.started)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-runtime.release:
		return nil
	}
}

func newFakeRuntime() *fakeRuntime {
	return &fakeRuntime{handle: redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})}
}

func (runtime *fakeRuntime) client() redis.UniversalClient { return runtime.handle }
func (runtime *fakeRuntime) ping(context.Context) error    { runtime.pings.Add(1); return runtime.pingErr }
func (runtime *fakeRuntime) close() error {
	runtime.closes.Add(1)
	_ = runtime.handle.Close()
	return runtime.closeErr
}

func TestModuleLifecycle(t *testing.T) {
	t.Parallel()
	runtime := newFakeRuntime()
	module, err := New(Config{Addresses: []string{"127.0.0.1:6379"}}, withRuntimeFactoryForTest(func(*redis.UniversalOptions, Mode, []redis.Hook) (clientRuntime, error) { return runtime, nil }))
	if err != nil {
		t.Fatal(err)
	}
	if module.Client() != nil {
		t.Fatal("client published before start")
	}
	if err = module.OnInit(); err != nil {
		t.Fatal(err)
	}
	if err = module.OnStart(context.Background()); err != nil {
		t.Fatal(err)
	}
	if module.Client() == nil || runtime.pings.Load() != 1 {
		t.Fatal("runtime was not published")
	}
	if err = module.Ping(context.Background()); err != nil {
		t.Fatal(err)
	}
	if runtime.pings.Load() != 2 {
		t.Fatal("Ping did not use runtime")
	}
	if err = module.OnStop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if module.Client() != nil || runtime.closes.Load() != 1 {
		t.Fatal("runtime was not closed")
	}
	if err = module.OnStop(context.Background()); err != nil || runtime.closes.Load() != 1 {
		t.Fatalf("idempotent stop failed: %v", err)
	}
}

func TestModuleStartFailureRollsBack(t *testing.T) {
	t.Parallel()
	pingErr := errors.New("ping failed")
	closeErr := errors.New("close failed")
	runtime := newFakeRuntime()
	runtime.pingErr = pingErr
	runtime.closeErr = closeErr
	module, err := New(Config{Addresses: []string{"127.0.0.1:6379"}}, withRuntimeFactoryForTest(func(*redis.UniversalOptions, Mode, []redis.Hook) (clientRuntime, error) { return runtime, nil }))
	if err != nil {
		t.Fatal(err)
	}
	err = module.OnStart(context.Background())
	if !errors.Is(err, pingErr) || !errors.Is(err, closeErr) {
		t.Fatalf("lost rollback errors: %v", err)
	}
	if module.Client() != nil || runtime.closes.Load() != 1 {
		t.Fatal("failed runtime leaked")
	}
}

func TestModuleStopCancelsConcurrentStart(t *testing.T) {
	t.Parallel()
	runtime := &blockingRuntime{fakeRuntime: newFakeRuntime(), started: make(chan struct{}), release: make(chan struct{})}
	module, err := New(Config{Addresses: []string{"127.0.0.1:6379"}}, withRuntimeFactoryForTest(func(*redis.UniversalOptions, Mode, []redis.Hook) (clientRuntime, error) {
		return runtime, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	startErr := make(chan error, 1)
	go func() { startErr <- module.OnStart(context.Background()) }()
	<-runtime.started

	stopErr := module.OnStop(context.Background())
	close(runtime.release)
	err = <-startErr
	if stopErr != nil {
		t.Fatalf("stop during start: %v", stopErr)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("start was not canceled: %v", err)
	}
	if module.Client() != nil || runtime.closes.Load() != 1 {
		t.Fatalf("stopped start published or leaked runtime: client=%v closes=%d", module.Client(), runtime.closes.Load())
	}
}

func TestModuleStopContextBoundsUncancelableFactory(t *testing.T) {
	t.Parallel()
	runtime := newFakeRuntime()
	factoryStarted := make(chan struct{})
	releaseFactory := make(chan struct{})
	module, err := New(Config{Addresses: []string{"127.0.0.1:6379"}}, withRuntimeFactoryForTest(func(*redis.UniversalOptions, Mode, []redis.Hook) (clientRuntime, error) {
		close(factoryStarted)
		<-releaseFactory
		return runtime, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	startErr := make(chan error, 1)
	go func() { startErr <- module.OnStart(context.Background()) }()
	<-factoryStarted
	stopCtx, cancel := context.WithCancel(context.Background())
	cancel()
	if err = module.OnStop(stopCtx); !errors.Is(err, context.Canceled) {
		t.Fatalf("stop did not honor context: %v", err)
	}
	close(releaseFactory)
	if err = <-startErr; !errors.Is(err, context.Canceled) {
		t.Fatalf("start was not canceled after factory returned: %v", err)
	}
	if module.Client() != nil || runtime.closes.Load() != 1 {
		t.Fatalf("canceled factory start leaked: client=%v closes=%d", module.Client(), runtime.closes.Load())
	}
}

func TestConcurrentStopsWaitForSingleClose(t *testing.T) {
	t.Parallel()
	runtime := &blockingCloseRuntime{fakeRuntime: newFakeRuntime(), started: make(chan struct{}), release: make(chan struct{})}
	runtime.closeErr = errors.New("close failed")
	module, err := New(Config{Addresses: []string{"127.0.0.1:6379"}}, withRuntimeFactoryForTest(func(*redis.UniversalOptions, Mode, []redis.Hook) (clientRuntime, error) {
		return runtime, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	if err = module.OnStart(context.Background()); err != nil {
		t.Fatal(err)
	}
	first, second := make(chan error, 1), make(chan error, 1)
	go func() { first <- module.OnStop(context.Background()) }()
	<-runtime.started
	secondCtx := &observedContext{Context: context.Background(), observed: make(chan struct{})}
	go func() { second <- module.OnStop(secondCtx) }()
	<-secondCtx.observed
	select {
	case err = <-second:
		t.Fatalf("concurrent stop returned before close completed: %v", err)
	default:
	}
	close(runtime.release)
	if err = <-first; !errors.Is(err, runtime.closeErr) {
		t.Fatalf("first stop lost close error: %v", err)
	}
	if err = <-second; !errors.Is(err, runtime.closeErr) {
		t.Fatalf("second stop lost close error: %v", err)
	}
	if runtime.closes.Load() != 1 || module.Client() != nil {
		t.Fatalf("concurrent stop closed %d times or kept client", runtime.closes.Load())
	}
}

func TestPrimaryStopContextDoesNotAbandonClose(t *testing.T) {
	t.Parallel()
	runtime := &blockingCloseRuntime{fakeRuntime: newFakeRuntime(), started: make(chan struct{}), release: make(chan struct{})}
	module, err := New(Config{Addresses: []string{"127.0.0.1:6379"}}, withRuntimeFactoryForTest(func(*redis.UniversalOptions, Mode, []redis.Hook) (clientRuntime, error) {
		return runtime, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	if err = module.OnStart(context.Background()); err != nil {
		t.Fatal(err)
	}
	stopCtx, cancel := context.WithCancel(context.Background())
	cancel()
	if err = module.OnStop(stopCtx); !errors.Is(err, context.Canceled) {
		t.Fatalf("primary stop did not honor context: %v", err)
	}
	<-runtime.started
	if module.Client() != nil {
		t.Fatal("stop context returned before revoking Client")
	}
	close(runtime.release)
	if err = module.OnStop(context.Background()); err != nil {
		t.Fatalf("cleanup did not continue after caller context: %v", err)
	}
	if runtime.closes.Load() != 1 {
		t.Fatalf("close executed %d times", runtime.closes.Load())
	}
}

func TestModuleInvalidLifecycleAndArguments(t *testing.T) {
	t.Parallel()
	var zero Module
	if !errors.Is(zero.OnInit(), ErrNotSetup) {
		t.Fatal("zero module should not initialize")
	}
	if !errors.Is(zero.Setup(Config{}), ErrNotSetup) {
		t.Fatal("unbound Setup should fail")
	}
	if !errors.Is(zero.OnStart(nil), ErrInvalidArgument) {
		t.Fatal("nil start context should fail")
	}
	if !errors.Is(zero.OnStop(nil), ErrInvalidArgument) {
		t.Fatal("nil stop context should fail")
	}
	if zero.Client() != nil {
		t.Fatal("zero module exposed client")
	}
	if _, err := zero.Get(context.Background(), "key"); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("unexpected not running error: %v", err)
	}
	if _, err := zero.Get(nil, "key"); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("unexpected nil context error: %v", err)
	}
	module, err := New(Config{Addresses: []string{"127.0.0.1:6379"}})
	if err != nil {
		t.Fatal(err)
	}
	if err = module.configure(Config{Addresses: []string{"127.0.0.1:6379"}}); !errors.Is(err, ErrAlreadySetup) {
		t.Fatalf("expected duplicate setup error: %v", err)
	}
}

func TestWithHookValidationAndOrder(t *testing.T) {
	t.Parallel()
	first, second := &passthroughHook{}, &passthroughHook{}
	var captured []redis.Hook
	module, err := New(Config{Addresses: []string{"127.0.0.1:6379"}}, WithHook(first), WithHook(second), withRuntimeFactoryForTest(func(_ *redis.UniversalOptions, _ Mode, hooks []redis.Hook) (clientRuntime, error) {
		captured = append([]redis.Hook(nil), hooks...)
		return newFakeRuntime(), nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	if err = module.OnStart(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer module.OnStop(context.Background())
	if len(captured) != 2 || captured[0] != first || captured[1] != second {
		t.Fatalf("hook order mismatch: %+v", captured)
	}
	if _, err = New(Config{Addresses: []string{"127.0.0.1:6379"}}, WithHook()); !errors.Is(err, ErrInvalidConfig) {
		t.Fatal(err)
	}
	var nilHook *passthroughHook
	if _, err = New(Config{Addresses: []string{"127.0.0.1:6379"}}, WithHook(nilHook)); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("typed nil hook accepted: %v", err)
	}
}
