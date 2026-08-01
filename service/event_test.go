package service

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/duanhf2012/origin/v3/errs"
	"github.com/duanhf2012/origin/v3/internal/timerwheel"
)

type testEvent struct {
	id    EventID
	value int
}

func (event *testEvent) EventID() EventID { return event.id }

type alternateTestEvent struct{ id EventID }

func (event *alternateTestEvent) EventID() EventID { return event.id }

func newEventFixture(
	t testing.TB,
	config SchedulerConfig,
	register func(*testService) error,
) *schedulerFixture {
	t.Helper()
	target := &testService{}
	runtimeState := &schedulerTestRuntime{nodeID: "event-node", name: "EventService"}
	runtimeState.state.Store(uint32(StateInitializing))
	if err := BindRuntime(target, runtimeState); err != nil {
		t.Fatal(err)
	}
	if err := BeginModuleInitialization(target); err != nil {
		t.Fatal(err)
	}
	if err := register(target); err != nil {
		t.Fatalf("register() error = %v", err)
	}
	if err := CompleteModuleInitialization(target, true); err != nil {
		t.Fatal(err)
	}
	runtimeState.state.Store(uint32(StateInitialized))
	engine, err := timerwheel.New(timerwheel.DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.Start(); err != nil {
		t.Fatal(err)
	}
	runtimeState.state.Store(uint32(StateStarting))
	if err := PrepareScheduler(target, config, engine); err != nil {
		t.Fatal(err)
	}
	runtimeState.state.Store(uint32(StateRunning))
	if err := ActivateScheduler(target); err != nil {
		t.Fatal(err)
	}
	fixture := &schedulerFixture{service: target, runtime: runtimeState, engine: engine}
	t.Cleanup(func() {
		runtimeState.state.Store(uint32(StateStopping))
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		_ = StopScheduler(ctx, target)
		cancel()
		_ = engine.Close()
	})
	return fixture
}

func TestNotifyEventSyncRejectsDepthSixtyFive(t *testing.T) {
	deepest := 0
	fixture := newEventFixture(t, DefaultSchedulerConfig(), func(target *testService) error {
		return target.SubscribeEvent(21, func(ctx context.Context, raw Event) error {
			event := raw.(*testEvent)
			if event.value > deepest {
				deepest = event.value
			}
			if event.value >= MaxSynchronousEventDepth {
				return target.NotifyEventSync(ctx, &testEvent{id: 21, value: event.value + 1})
			}
			return target.NotifyEventSync(ctx, &testEvent{id: 21, value: event.value + 1})
		})
	})
	result := make(chan error, 1)
	if err := fixture.service.DispatchAsync(func(ctx context.Context) {
		result <- fixture.service.NotifyEventSync(ctx, &testEvent{id: 21, value: 1})
	}); err != nil {
		t.Fatal(err)
	}
	if err := <-result; !errors.Is(err, errs.ErrInvalidArgument) {
		t.Fatalf("depth 65 error = %v", err)
	}
	if deepest != MaxSynchronousEventDepth {
		t.Fatalf("deepest = %d", deepest)
	}
}

func TestNotifyEventSyncIsolatesHandlersAndForbidsAwait(t *testing.T) {
	var order []int
	var listenerAwait error
	fixture := newEventFixture(t, DefaultSchedulerConfig(), func(target *testService) error {
		if err := target.SubscribeEvent(7, func(context.Context, Event) error {
			order = append(order, 1)
			return errors.New("first")
		}); err != nil {
			return err
		}
		if err := target.SubscribeEvent(7, func(context.Context, Event) error {
			order = append(order, 2)
			panic("second")
		}); err != nil {
			return err
		}
		return target.SubscribeEvent(7, func(ctx context.Context, _ Event) error {
			order = append(order, 3)
			listenerAwait = target.Await(ctx, func(context.Context) error { return nil })
			return nil
		})
	})

	if err := fixture.service.NotifyEventSync(context.Background(), &testEvent{id: 7}); !errors.Is(err, errs.ErrInvalidArgument) {
		t.Fatalf("outside NotifyEventSync() error = %v", err)
	}
	result := make(chan error, 1)
	if err := fixture.service.DispatchAsync(func(ctx context.Context) {
		result <- fixture.service.NotifyEventSync(ctx, &testEvent{id: 7, value: 9})
	}); err != nil {
		t.Fatal(err)
	}
	if err := <-result; err == nil {
		t.Fatal("监听器错误和 panic 未聚合")
	}
	if !errors.Is(listenerAwait, errs.ErrInvalidArgument) {
		t.Fatalf("listener Await() error = %v", listenerAwait)
	}
	if !reflect.DeepEqual(order, []int{1, 2, 3}) {
		t.Fatalf("handler order = %v", order)
	}
	if err := fixture.service.NotifyEventAsync(&alternateTestEvent{id: 7}); !errors.Is(err, errs.ErrInvalidArgument) {
		t.Fatalf("payload mismatch error = %v", err)
	}
}

func TestNotifyEventAsyncUsesOneTaskAndAllowsAwait(t *testing.T) {
	completed := make(chan struct{}, 1)
	var payload Event
	fixture := newEventFixture(t, DefaultSchedulerConfig(), func(target *testService) error {
		return target.SubscribeEvent(11, func(ctx context.Context, event Event) error {
			payload = event
			if err := target.Await(ctx, func(context.Context) error { return nil }); err != nil {
				return err
			}
			completed <- struct{}{}
			return nil
		})
	})
	event := &testEvent{id: 11, value: 42}
	before := fixture.service.ExecutionStats().DispatchedTotal
	if err := fixture.service.NotifyEventAsync(event); err != nil {
		t.Fatalf("NotifyEventAsync() error = %v", err)
	}
	select {
	case <-completed:
	case <-time.After(time.Second):
		t.Fatal("异步监听器未完成")
	}
	after := fixture.service.ExecutionStats().DispatchedTotal
	if after-before != 1 {
		t.Fatalf("DispatchedTotal delta = %d", after-before)
	}
	if payload != event {
		t.Fatal("异步监听器未借用原 Event 实例")
	}
}

func TestEventRegistrationLimitsAndPhase(t *testing.T) {
	target := &Service{}
	runtime := &testRuntime{nodeID: "node", name: "service", state: StateInitializing}
	if err := BindRuntime(target, runtime); err != nil {
		t.Fatal(err)
	}
	if err := BeginModuleInitialization(target); err != nil {
		t.Fatal(err)
	}
	if err := target.SubscribeEvent(0, func(context.Context, Event) error { return nil }); err == nil {
		t.Fatal("EventID 0 未拒绝")
	}
	if err := target.SubscribeEvent(1, nil); err == nil {
		t.Fatal("nil EventHandler 未拒绝")
	}
	handler := func(context.Context, Event) error { return nil }
	if err := target.SubscribeEvent(1, handler); err != nil {
		t.Fatal(err)
	}
	for eventID := EventID(2); eventID <= MaxEventIDsPerService; eventID++ {
		if err := target.SubscribeEvent(eventID, handler); err != nil {
			t.Fatalf("SubscribeEvent(%d) error = %v", eventID, err)
		}
	}
	if err := target.SubscribeEvent(MaxEventIDsPerService+1, handler); err == nil {
		t.Fatal("超过 EventID 上限未拒绝")
	}
	if err := CompleteModuleInitialization(target, true); err != nil {
		t.Fatal(err)
	}
	if err := target.SubscribeEvent(2, func(context.Context, Event) error { return nil }); err == nil {
		t.Fatal("OnInit 结束后订阅未拒绝")
	}

	listenerOwner := &Service{}
	listenerRuntime := &testRuntime{nodeID: "node", name: "listeners", state: StateInitializing}
	if err := BindRuntime(listenerOwner, listenerRuntime); err != nil {
		t.Fatal(err)
	}
	if err := BeginModuleInitialization(listenerOwner); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < MaxEventListenersPerService; index++ {
		if err := listenerOwner.SubscribeEvent(1, handler); err != nil {
			t.Fatalf("listener %d error = %v", index, err)
		}
	}
	if err := listenerOwner.SubscribeEvent(1, handler); err == nil {
		t.Fatal("超过监听器上限未拒绝")
	}
}

func TestNotifyEventAsyncReusesSchedulerCapacity(t *testing.T) {
	fixture := newEventFixture(t, SchedulerConfig{
		MaxTasks:            1,
		MaxAwaitTasks:       1,
		DefaultAwaitTimeout: time.Second,
	}, func(target *testService) error {
		return target.SubscribeEvent(31, func(context.Context, Event) error { return nil })
	})
	started := make(chan struct{})
	release := make(chan struct{})
	if err := fixture.service.DispatchAsync(func(context.Context) {
		close(started)
		<-release
	}); err != nil {
		t.Fatal(err)
	}
	<-started
	if err := fixture.service.NotifyEventAsync(&testEvent{id: 31}); !errors.Is(err, errs.ErrServiceQueueFull) {
		t.Fatalf("queue full error = %v", err)
	}
	close(release)
}

func TestModuleEventSubscriptionBelongsToModuleScope(t *testing.T) {
	owner := &Service{}
	runtime := &testRuntime{nodeID: "node", name: "service", state: StateInitializing}
	if err := BindRuntime(owner, runtime); err != nil {
		t.Fatal(err)
	}
	if err := BeginModuleInitialization(owner); err != nil {
		t.Fatal(err)
	}
	module := &testModule{}
	module.init = func() error {
		return module.SubscribeEvent(41, func(context.Context, Event) error { return nil })
	}
	if err := owner.AddModule(module); err != nil {
		t.Fatal(err)
	}
	if err := CompleteModuleInitialization(owner, true); err != nil {
		t.Fatal(err)
	}
	listener := owner.events[41].listeners[0]
	if !listener.active.Load() {
		t.Fatal("listener prematurely inactive")
	}
	module.cleanupScope()
	if listener.active.Load() {
		t.Fatal("Module scope cleanup 未移除事件监听器")
	}
}

func TestEventSuccessFanoutDoesNotAllocatePerListener(t *testing.T) {
	owner := &Service{}
	slot := &eventSlot{id: 51}
	for index := 0; index < 128; index++ {
		listener := &eventListener{handler: func(context.Context, Event) error { return nil }}
		listener.active.Store(true)
		slot.listeners = append(slot.listeners, listener)
	}
	event := &testEvent{id: 51}
	allocations := testing.AllocsPerRun(1000, func() {
		result, failures := owner.notifyEventHandlers(context.Background(), slot, event)
		if result != nil || failures != 0 {
			panic("unexpected event result")
		}
	})
	if allocations != 0 {
		t.Fatalf("fanout allocations = %.2f", allocations)
	}
}
