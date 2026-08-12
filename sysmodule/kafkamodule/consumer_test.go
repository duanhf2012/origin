package kafkamodule

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/IBM/sarama"
	originlog "github.com/duanhf2012/origin/v3/log"
	"github.com/duanhf2012/origin/v3/node"
	"github.com/duanhf2012/origin/v3/service"
)

type fakeConsumerRuntimeDriver struct {
	setupGate      chan struct{}
	errorsChannel  chan error
	closeOnce      sync.Once
	closed         atomic.Int32
	pauseAllCalls  atomic.Int32
	resumeAllCalls atomic.Int32
	pauseMu        sync.Mutex
	paused         map[string][]int32
}

func newFakeConsumerRuntimeDriver() *fakeConsumerRuntimeDriver {
	return &fakeConsumerRuntimeDriver{setupGate: make(chan struct{}), errorsChannel: make(chan error, 8)}
}
func (runtime *fakeConsumerRuntimeDriver) consume(ctx context.Context, _ []string, handler sarama.ConsumerGroupHandler) error {
	select {
	case <-runtime.setupGate:
	case <-ctx.Done():
		return ctx.Err()
	}
	session := &fakeConsumerSession{ctx: ctx}
	if err := handler.Setup(session); err != nil {
		return err
	}
	<-ctx.Done()
	return ctx.Err()
}
func (runtime *fakeConsumerRuntimeDriver) errors() <-chan error { return runtime.errorsChannel }
func (runtime *fakeConsumerRuntimeDriver) close() error {
	runtime.closeOnce.Do(func() { runtime.closed.Add(1); close(runtime.errorsChannel) })
	return nil
}
func (runtime *fakeConsumerRuntimeDriver) pause(partitions map[string][]int32) {
	runtime.pauseMu.Lock()
	runtime.paused = partitions
	runtime.pauseMu.Unlock()
}
func (runtime *fakeConsumerRuntimeDriver) resume(partitions map[string][]int32) {
	runtime.pause(partitions)
}
func (runtime *fakeConsumerRuntimeDriver) pauseAll()  { runtime.pauseAllCalls.Add(1) }
func (runtime *fakeConsumerRuntimeDriver) resumeAll() { runtime.resumeAllCalls.Add(1) }

type managedConsumerTestService struct {
	service.Service
	module *managedConsumerTestModule
}

func (owner *managedConsumerTestService) OnInit() error { return owner.AddModule(owner.module) }

type managedConsumerTestModule struct {
	Consumer
	runtime *fakeConsumerRuntimeDriver
}

func (module *managedConsumerTestModule) OnInit() error {
	return module.Setup(validConsumerConfig(), func(context.Context, *Message) error { return nil }, withConsumerRuntimeFactory(func(context.Context, []string, string, *sarama.Config) (consumerRuntime, error) {
		return module.runtime, nil
	}))
}

func newManagedConsumerTestNode(t *testing.T, runtime *fakeConsumerRuntimeDriver) (*node.Node, *managedConsumerTestModule) {
	t.Helper()
	module := &managedConsumerTestModule{runtime: runtime}
	owner := &managedConsumerTestService{module: module}
	current, err := node.New(node.Config{ID: "kafka-consumer-test", Services: []string{"KafkaConsumer"}, Scheduler: service.DefaultSchedulerConfig()}, []node.ServiceBinding{{Name: "KafkaConsumer", Template: "KafkaConsumer", Service: owner}}, originlog.NewNop(), node.Options{MaxTimersPerNode: 32, TimerLocation: time.UTC})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = current.Rollback(ctx)
	})
	return current, module
}

func TestConsumerOnStartWaitsForFirstSessionAndPauseResume(t *testing.T) {
	runtime := newFakeConsumerRuntimeDriver()
	current, module := newManagedConsumerTestNode(t, runtime)
	started := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		started <- current.Start(ctx)
	}()
	select {
	case err := <-started:
		t.Fatalf("start returned before Session Setup: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	close(runtime.setupGate)
	if err := <-started; err != nil {
		t.Fatal(err)
	}
	if !module.Stats().Running {
		t.Fatal("consumer did not publish running state")
	}
	if err := module.PauseAll(); err != nil {
		t.Fatal(err)
	}
	if err := module.ResumeAll(); err != nil {
		t.Fatal(err)
	}
	partitions := map[string][]int32{"events": {0, 1}}
	if err := module.Pause(partitions); err != nil {
		t.Fatal(err)
	}
	partitions["events"][0] = 99
	runtime.pauseMu.Lock()
	captured := runtime.paused["events"][0]
	runtime.pauseMu.Unlock()
	if captured != 0 {
		t.Fatalf("Pause did not copy caller map: %d", captured)
	}
	if runtime.pauseAllCalls.Load() != 1 || runtime.resumeAllCalls.Load() != 1 {
		t.Fatalf("pause calls=%d resume calls=%d", runtime.pauseAllCalls.Load(), runtime.resumeAllCalls.Load())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := current.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if runtime.closed.Load() != 1 || module.Stats().Running {
		t.Fatalf("closed=%d stats=%+v", runtime.closed.Load(), module.Stats())
	}
}

func TestConsumerStartCancellationClosesRuntime(t *testing.T) {
	runtime := newFakeConsumerRuntimeDriver()
	current, _ := newManagedConsumerTestNode(t, runtime)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	err := current.Start(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("unexpected start error: %v", err)
	}
	if runtime.closed.Load() != 1 {
		t.Fatalf("cancelled start leaked runtime: %d", runtime.closed.Load())
	}
}

func TestConsumerRejectsInvalidPauseAndUnconfiguredLifecycle(t *testing.T) {
	consumer := &Consumer{}
	if err := consumer.OnInit(); !errors.Is(err, ErrNotSetup) {
		t.Fatalf("unconfigured init: %v", err)
	}
	if err := consumer.Pause(map[string][]int32{"": {0}}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("invalid pause: %v", err)
	}
	if err := consumer.Resume(map[string][]int32{"events": {-1}}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("invalid resume: %v", err)
	}
}
