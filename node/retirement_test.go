package node

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	publicprovider "github.com/duanhf2012/origin/v3/discovery/provider"
	"github.com/duanhf2012/origin/v3/errs"
	internaldiscovery "github.com/duanhf2012/origin/v3/internal/discovery"
	originlog "github.com/duanhf2012/origin/v3/log"
	"github.com/duanhf2012/origin/v3/rpc"
	"github.com/duanhf2012/origin/v3/service"
)

type retirementService struct {
	service.Service
	label               string
	changes             *[]string
	events              chan service.ServiceStateChanged
	synchronousEventID  service.EventID
	synchronousRetireTo chan error
}

func (target *retirementService) OnInit() error {
	if err := target.SubscribeEvent(
		service.ServiceStateChangedEventID,
		func(_ context.Context, raw service.Event) error {
			event := raw.(service.ServiceStateChanged)
			*target.changes = append(*target.changes, target.label+":"+event.Current.String())
			if target.events != nil {
				target.events <- event
			}
			return nil
		},
	); err != nil {
		return err
	}
	if target.synchronousEventID == 0 {
		return nil
	}
	return target.SubscribeEvent(
		target.synchronousEventID,
		func(ctx context.Context, _ service.Event) error {
			err := target.Retire(ctx)
			target.synchronousRetireTo <- err
			return err
		},
	)
}

func newRetirementNode(
	t *testing.T,
	source *internaldiscovery.Source,
	services ...*retirementService,
) *Node {
	t.Helper()
	bindings := make([]ServiceBinding, len(services))
	configured := make([]string, len(services))
	for index, target := range services {
		configured[index] = target.label
		bindings[index] = ServiceBinding{
			Name: target.label, Template: "retirementService", Service: target,
		}
	}
	current, err := New(
		Config{ID: "retirement-node", Services: configured},
		bindings,
		originlog.NewNop(),
		Options{
			MaxTimersPerNode: 64,
			TimerLocation:    time.UTC,
			DiscoverySource:  source,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := current.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if current.State() == StateReady {
			_ = current.Stop(context.Background())
		}
	})
	return current
}

func TestNodeRetireReverseResumeForwardAndPublishState(t *testing.T) {
	source := internaldiscovery.NewSource()
	var snapshotMu sync.Mutex
	var latest internaldiscovery.RawSnapshot
	subscription, err := source.Subscribe(func(snapshot internaldiscovery.RawSnapshot) error {
		snapshotMu.Lock()
		latest = snapshot
		snapshotMu.Unlock()
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer subscription.Close()
	var changes []string
	first := &retirementService{label: "First", changes: &changes, events: make(chan service.ServiceStateChanged, 4)}
	second := &retirementService{label: "Second", changes: &changes}
	current := newRetirementNode(t, source, first, second)

	if err := current.Retire(t.Context()); err != nil {
		t.Fatalf("Retire() error = %v", err)
	}
	if first.State() != service.StateRetired || second.State() != service.StateRetired {
		t.Fatalf("states = %v, %v", first.State(), second.State())
	}
	if err := current.Retire(t.Context()); err != nil {
		t.Fatalf("idempotent Retire() error = %v", err)
	}
	if err := current.Resume(t.Context()); err != nil {
		t.Fatalf("Resume() error = %v", err)
	}
	current.discoveryPublication.mu.Lock()
	desired := current.discoveryPublication.desired
	acknowledged := current.discoveryPublication.acknowledged
	current.discoveryPublication.mu.Unlock()
	if desired != 3 || acknowledged != desired {
		t.Fatalf("publication generations desired=%d acknowledged=%d", desired, acknowledged)
	}
	want := []string{
		"Second:retired", "First:retired", "First:running", "Second:running",
	}
	if len(changes) != len(want) {
		t.Fatalf("changes = %v", changes)
	}
	for index := range want {
		if changes[index] != want[index] {
			t.Fatalf("changes[%d] = %q, want %q", index, changes[index], want[index])
		}
	}
	snapshotMu.Lock()
	defer snapshotMu.Unlock()
	if len(latest.Nodes) != 1 || len(latest.Nodes[0].Services) != 2 {
		t.Fatalf("latest = %+v", latest)
	}
	for _, discovered := range latest.Nodes[0].Services {
		if discovered.State != internaldiscovery.ServiceStateRunning {
			t.Fatalf("published service = %+v", discovered)
		}
	}
}

// TestNodeInitialRetiredPublishesNoRunningWindow 验证启动参数不会先发布一个短暂 Running
// 快照。Service 仍完成正常 OnInit/OnStart 和 Scheduler 激活，只改变首次对外准入状态。
func TestNodeInitialRetiredPublishesNoRunningWindow(t *testing.T) {
	source := internaldiscovery.NewSource()
	var snapshotMu sync.Mutex
	var snapshots []internaldiscovery.RawSnapshot
	subscription, err := source.Subscribe(func(snapshot internaldiscovery.RawSnapshot) error {
		snapshotMu.Lock()
		snapshots = append(snapshots, snapshot)
		snapshotMu.Unlock()
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer subscription.Close()

	var changes []string
	target := &retirementService{label: "PlayerService", changes: &changes}
	current, err := New(
		Config{ID: "retired-node", Services: []string{"PlayerService"}},
		[]ServiceBinding{{
			Name: "PlayerService", Template: "retirementService", Service: target,
		}},
		originlog.NewNop(),
		Options{
			InitialRetired:   true,
			MaxTimersPerNode: 64,
			TimerLocation:    time.UTC,
			DiscoverySource:  source,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := current.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = current.Stop(context.Background()) })

	if target.State() != service.StateRetired {
		t.Fatalf("initial Service state = %v, want Retired", target.State())
	}
	if len(changes) != 0 {
		t.Fatalf("initial state unexpectedly emitted transition events: %v", changes)
	}
	snapshotMu.Lock()
	defer snapshotMu.Unlock()
	for _, snapshot := range snapshots {
		for _, publishedNode := range snapshot.Nodes {
			if publishedNode.NodeID != "retired-node" {
				continue
			}
			for _, publishedService := range publishedNode.Services {
				if publishedService.State != internaldiscovery.ServiceStateRetired {
					t.Fatalf("published initial state = %v, want Retired", publishedService.State)
				}
			}
		}
	}
}

func TestReadyDynamicPublicationUsesSingleCoordinator(t *testing.T) {
	var changes []string
	target := &retirementService{label: "Only", changes: &changes}
	current := newRetirementNode(t, internaldiscovery.NewSource(), target)

	current.discoveryPublication.mu.Lock()
	before := current.discoveryPublication.desired
	current.discoveryPublication.mu.Unlock()
	current.handleTransportEvent(rpc.TransportEvent{
		Kind:  rpc.TransportKindTCP,
		State: rpc.TransportStateReady,
	})

	current.discoveryPublication.mu.Lock()
	after := current.discoveryPublication.desired
	current.discoveryPublication.mu.Unlock()
	if after != before+1 {
		t.Fatalf("dynamic generation = %d, want %d", after, before+1)
	}
	current.handleTransportEvent(rpc.TransportEvent{
		Kind:  rpc.TransportKindTCP,
		State: rpc.TransportStateRecovering,
	})
	current.discoveryPublication.mu.Lock()
	afterWithdraw := current.discoveryPublication.desired
	current.discoveryPublication.mu.Unlock()
	if afterWithdraw != before+2 {
		t.Fatalf("withdraw generation = %d, want %d", afterWithdraw, before+2)
	}
}

func TestServiceEntryPublishesStateAndEnteredAtAsOneSnapshot(t *testing.T) {
	entry := &serviceEntry{}
	entry.setState(service.StateRunning)
	before := entry.loadState()
	entry.setState(service.StateRetired)
	after := entry.loadState()

	if before.State != service.StateRunning || before.EnteredAt.IsZero() {
		t.Fatalf("before = %+v", before)
	}
	if after.State != service.StateRetired || after.EnteredAt.IsZero() {
		t.Fatalf("after = %+v", after)
	}
	if after.EnteredAt.Before(before.EnteredAt) {
		t.Fatalf("entered time moved backwards: before=%v after=%v", before.EnteredAt, after.EnteredAt)
	}
	entry.setState(service.StateStopping)
	stopping := entry.loadState()
	time.Sleep(time.Millisecond)
	entry.setState(service.StateStopping)
	repeated := entry.loadState()
	if !repeated.EnteredAt.Equal(stopping.EnteredAt) {
		t.Fatalf("repeated Stopping changed EnteredAt: first=%v repeated=%v", stopping.EnteredAt, repeated.EnteredAt)
	}
}

func TestServiceResumeInsideTaskDoesNotDeadlock(t *testing.T) {
	var changes []string
	target := &retirementService{label: "Only", changes: &changes, events: make(chan service.ServiceStateChanged, 4)}
	current := newRetirementNode(t, internaldiscovery.NewSource(), target)
	if err := target.Retire(t.Context()); err != nil {
		t.Fatal(err)
	}
	retired := <-target.events
	if retired.Previous != service.StateRunning || retired.Current != service.StateRetired || retired.ChangedAt.IsZero() {
		t.Fatalf("retired event = %+v", retired)
	}
	result := make(chan error, 1)
	if err := target.DispatchAsync(func(ctx context.Context) {
		result <- target.Resume(ctx)
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("task Resume() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Task 内 Resume 死锁")
	}
	status, ok := current.ServiceStatus("Only")
	if !ok || status.State != service.StateRunning || status.EnteredAt.IsZero() {
		t.Fatalf("ServiceStatus = %+v, %v", status, ok)
	}
}

type retirementRequestEvent struct{ id service.EventID }

func (event retirementRequestEvent) EventID() service.EventID { return event.id }

// TestServiceRetireFromSynchronousEventListener 防止遗留预检查因 Retire
// 内部需要等待发现发布，而拒绝同步事件监听器中的调用。
func TestServiceRetireFromSynchronousEventListener(t *testing.T) {
	const eventID service.EventID = 77
	var changes []string
	target := &retirementService{
		label:               "Only",
		changes:             &changes,
		synchronousEventID:  eventID,
		synchronousRetireTo: make(chan error, 1),
	}
	current := newRetirementNode(t, internaldiscovery.NewSource(), target)
	notifyResult := make(chan error, 1)
	if err := target.DispatchAsync(func(ctx context.Context) {
		notifyResult <- target.NotifyEventSync(ctx, retirementRequestEvent{id: eventID})
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-target.synchronousRetireTo:
		if err != nil {
			t.Fatalf("listener Retire() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("synchronous listener did not finish Retire")
	}
	select {
	case err := <-notifyResult:
		if err != nil {
			t.Fatalf("NotifyEventSync() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("NotifyEventSync did not return")
	}
	status, ok := current.ServiceStatus("Only")
	if !ok || status.State != service.StateRetired {
		t.Fatalf("ServiceStatus = %+v, %v", status, ok)
	}
}

type failingPublicationProvider struct {
	context publicprovider.Context
	mu      sync.Mutex
	fail    bool
	panic   bool
	calls   int
}

type blockingPublicationProvider struct {
	context publicprovider.Context
	mu      sync.Mutex
	calls   int
	started chan struct{}
}

type coalescingPublicationProvider struct {
	context   publicprovider.Context
	mu        sync.Mutex
	calls     int
	withdraws int
	started   chan struct{}
	release   chan struct{}
	fail      bool
}

func (provider *coalescingPublicationProvider) Start(context.Context) error {
	if err := provider.context.Host.SetTTL(3 * time.Second); err != nil {
		return err
	}
	if err := provider.context.Host.ReplaceSnapshot(publicprovider.Snapshot{}); err != nil {
		return err
	}
	provider.context.Host.Report(publicprovider.Report{State: publicprovider.StateReady})
	return nil
}

func (provider *coalescingPublicationProvider) Publish(
	ctx context.Context,
	_ publicprovider.Node,
) error {
	provider.mu.Lock()
	provider.calls++
	call := provider.calls
	provider.mu.Unlock()
	if call != 2 {
		return nil
	}
	close(provider.started)
	select {
	case <-provider.release:
		if provider.fail {
			return errs.ErrDiscoveryUnavailable
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestFailedGenerationWaiterAcceptsNewerSuccessfulAck(t *testing.T) {
	var changes []string
	target := &retirementService{label: "Only", changes: &changes}
	var provider *coalescingPublicationProvider
	current, err := New(
		Config{ID: "publication-upgraded-ack", Services: []string{"Only"}},
		[]ServiceBinding{{Name: "Only", Template: "Only", Service: target}},
		originlog.NewNop(),
		Options{
			MaxTimersPerNode: 64,
			TimerLocation:    time.UTC,
			DiscoveryKind:    "upgraded-ack",
			DiscoveryFactory: func(context publicprovider.Context) (publicprovider.Provider, error) {
				provider = &coalescingPublicationProvider{
					context: context,
					started: make(chan struct{}),
					release: make(chan struct{}),
					fail:    true,
				}
				return provider, nil
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := current.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = current.Stop(context.Background()) })

	first := make(chan error, 1)
	go func() { first <- target.Retire(context.Background()) }()
	select {
	case <-provider.started:
	case <-time.After(time.Second):
		t.Fatal("failed generation did not start")
	}
	second := make(chan error, 1)
	go func() { second <- current.requestDiscoveryPublication(context.Background()) }()
	deadline := time.Now().Add(time.Second)
	for {
		current.discoveryPublication.mu.Lock()
		desired := current.discoveryPublication.desired
		current.discoveryPublication.mu.Unlock()
		if desired == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("desired generation = %d, want 2", desired)
		}
		time.Sleep(time.Millisecond)
	}
	close(provider.release)
	if err := <-first; err != nil {
		t.Fatalf("first waiter error = %v", err)
	}
	if err := <-second; err != nil {
		t.Fatalf("second waiter error = %v", err)
	}
	current.discoveryPublication.mu.Lock()
	acknowledged := current.discoveryPublication.acknowledged
	current.discoveryPublication.mu.Unlock()
	if acknowledged != 2 {
		t.Fatalf("acknowledged generation = %d, want 2", acknowledged)
	}
}

func TestTransportRecoveryWithdrawsAfterInflightGeneration(t *testing.T) {
	var changes []string
	target := &retirementService{label: "Only", changes: &changes}
	var provider *coalescingPublicationProvider
	current, err := New(
		Config{ID: "publication-withdraw-order", Services: []string{"Only"}},
		[]ServiceBinding{{Name: "Only", Template: "Only", Service: target}},
		originlog.NewNop(),
		Options{
			MaxTimersPerNode: 64,
			TimerLocation:    time.UTC,
			DiscoveryKind:    "withdraw-order",
			DiscoveryFactory: func(context publicprovider.Context) (publicprovider.Provider, error) {
				provider = &coalescingPublicationProvider{
					context: context,
					started: make(chan struct{}),
					release: make(chan struct{}),
				}
				return provider, nil
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := current.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = current.Stop(context.Background()) })

	retireResult := make(chan error, 1)
	go func() { retireResult <- target.Retire(context.Background()) }()
	select {
	case <-provider.started:
	case <-time.After(time.Second):
		t.Fatal("inflight generation did not start")
	}
	recoveryDone := make(chan struct{})
	go func() {
		current.handleTransportEvent(rpc.TransportEvent{
			Kind:  rpc.TransportKindTCP,
			State: rpc.TransportStateRecovering,
		})
		close(recoveryDone)
	}()
	deadline := time.Now().Add(time.Second)
	for {
		current.discoveryPublication.mu.Lock()
		desired := current.discoveryPublication.desired
		current.discoveryPublication.mu.Unlock()
		if desired == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("desired generation = %d, want 2", desired)
		}
		time.Sleep(time.Millisecond)
	}
	close(provider.release)
	if err := <-retireResult; err != nil {
		t.Fatalf("Retire() error = %v", err)
	}
	select {
	case <-recoveryDone:
	case <-time.After(time.Second):
		t.Fatal("transport recovery withdrawal did not finish")
	}
	provider.mu.Lock()
	withdraws := provider.withdraws
	provider.mu.Unlock()
	if withdraws != 1 || current.discoveryPublished.Load() {
		t.Fatalf("withdraws=%d discoveryPublished=%v", withdraws, current.discoveryPublished.Load())
	}
}

func TestRetireReservesPublicationBeforeAwaitCapacityCheck(t *testing.T) {
	var changes []string
	target := &retirementService{label: "Only", changes: &changes}
	scheduler := service.DefaultSchedulerConfig()
	scheduler.MaxAwaitTasks = 1
	current, err := New(
		Config{
			ID:        "publication-before-await",
			Services:  []string{"Only"},
			Scheduler: scheduler,
		},
		[]ServiceBinding{{Name: "Only", Template: "Only", Service: target}},
		originlog.NewNop(),
		Options{
			MaxTimersPerNode: 64,
			TimerLocation:    time.UTC,
			DiscoverySource:  internaldiscovery.NewSource(),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := current.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = current.Stop(context.Background()) })

	awaitEntered := make(chan struct{})
	releaseAwait := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseAwait) }) }
	t.Cleanup(release)
	awaitResult := make(chan error, 1)
	if err := target.DispatchAsync(func(ctx context.Context) {
		awaitResult <- target.Await(ctx, func(context.Context) error {
			close(awaitEntered)
			<-releaseAwait
			return nil
		})
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-awaitEntered:
	case <-time.After(time.Second):
		t.Fatal("capacity holder did not enter Await")
	}

	if err := target.Retire(t.Context()); !errors.Is(err, errs.ErrServiceQueueFull) {
		t.Fatalf("Retire() error = %v, want ErrServiceQueueFull", err)
	}
	if target.State() != service.StateRetired {
		t.Fatalf("State = %v, want Retired", target.State())
	}
	deadline := time.Now().Add(time.Second)
	for {
		current.discoveryPublication.mu.Lock()
		desired := current.discoveryPublication.desired
		acknowledged := current.discoveryPublication.acknowledged
		current.discoveryPublication.mu.Unlock()
		if desired == 1 && acknowledged == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("publication desired=%d acknowledged=%d, want 1/1", desired, acknowledged)
		}
		time.Sleep(time.Millisecond)
	}
	if err := current.Retire(t.Context()); err != nil {
		t.Fatalf("idempotent batch Retire() with full Await capacity error = %v", err)
	}
	current.discoveryPublication.mu.Lock()
	batchDesired := current.discoveryPublication.desired
	batchAcknowledged := current.discoveryPublication.acknowledged
	current.discoveryPublication.mu.Unlock()
	if batchDesired != 2 || batchAcknowledged != 2 {
		t.Fatalf("batch publication desired=%d acknowledged=%d, want 2/2", batchDesired, batchAcknowledged)
	}

	release()
	if err := <-awaitResult; err != nil {
		t.Fatalf("capacity holder Await() error = %v", err)
	}
	if err := target.Retire(t.Context()); err != nil {
		t.Fatalf("idempotent Retire() error = %v", err)
	}
	current.discoveryPublication.mu.Lock()
	desired := current.discoveryPublication.desired
	acknowledged := current.discoveryPublication.acknowledged
	current.discoveryPublication.mu.Unlock()
	if desired != 3 || acknowledged != 3 {
		t.Fatalf("idempotent publication desired=%d acknowledged=%d, want 3/3", desired, acknowledged)
	}
}

func (provider *coalescingPublicationProvider) Withdraw(context.Context) error {
	provider.mu.Lock()
	provider.withdraws++
	provider.mu.Unlock()
	return nil
}
func (*coalescingPublicationProvider) Close(context.Context) error { return nil }

func (provider *blockingPublicationProvider) Start(context.Context) error {
	if err := provider.context.Host.SetTTL(3 * time.Second); err != nil {
		return err
	}
	if err := provider.context.Host.ReplaceSnapshot(publicprovider.Snapshot{}); err != nil {
		return err
	}
	provider.context.Host.Report(publicprovider.Report{State: publicprovider.StateReady})
	return nil
}

func (provider *blockingPublicationProvider) Publish(
	ctx context.Context,
	_ publicprovider.Node,
) error {
	provider.mu.Lock()
	provider.calls++
	call := provider.calls
	provider.mu.Unlock()
	if call == 1 {
		return nil
	}
	select {
	case provider.started <- struct{}{}:
	default:
	}
	<-ctx.Done()
	return ctx.Err()
}

func (*blockingPublicationProvider) Withdraw(context.Context) error { return nil }
func (*blockingPublicationProvider) Close(context.Context) error    { return nil }

func (provider *failingPublicationProvider) Start(context.Context) error {
	if err := provider.context.Host.SetTTL(3 * time.Second); err != nil {
		return err
	}
	if err := provider.context.Host.ReplaceSnapshot(publicprovider.Snapshot{}); err != nil {
		return err
	}
	provider.context.Host.Report(publicprovider.Report{State: publicprovider.StateReady})
	return nil
}

func (provider *failingPublicationProvider) Publish(context.Context, publicprovider.Node) error {
	provider.mu.Lock()
	provider.calls++
	fail := provider.fail
	panicPublish := provider.panic
	provider.mu.Unlock()
	if panicPublish {
		panic("publication panic")
	}
	if fail {
		return errs.ErrDiscoveryUnavailable
	}
	return nil
}

func TestNodeBatchRetireResumePublishesOneSnapshotPerBatch(t *testing.T) {
	var changes []string
	services := []*retirementService{
		{label: "First", changes: &changes},
		{label: "Second", changes: &changes},
		{label: "Third", changes: &changes},
	}
	bindings := make([]ServiceBinding, len(services))
	names := make([]string, len(services))
	for index, target := range services {
		names[index] = target.label
		bindings[index] = ServiceBinding{Name: target.label, Template: "retirementService", Service: target}
	}
	var provider *failingPublicationProvider
	current, err := New(
		Config{ID: "publication-batch", Services: names},
		bindings,
		originlog.NewNop(),
		Options{
			MaxTimersPerNode: 64,
			TimerLocation:    time.UTC,
			DiscoveryKind:    "batch",
			DiscoveryFactory: func(context publicprovider.Context) (publicprovider.Provider, error) {
				provider = &failingPublicationProvider{context: context}
				return provider, nil
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := current.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = current.Stop(context.Background()) })
	if err := current.Retire(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := current.Resume(t.Context()); err != nil {
		t.Fatal(err)
	}
	provider.mu.Lock()
	calls := provider.calls
	provider.mu.Unlock()
	if calls != 3 {
		t.Fatalf("Provider.Publish calls = %d, want startup + retire batch + resume batch", calls)
	}
}

func TestProviderPublishPanicIsIsolatedAndLockRemainsUsable(t *testing.T) {
	var changes []string
	target := &retirementService{label: "Only", changes: &changes}
	var provider *failingPublicationProvider
	current, err := New(
		Config{ID: "publication-panic", Services: []string{"Only"}},
		[]ServiceBinding{{Name: "Only", Template: "Only", Service: target}},
		originlog.NewNop(),
		Options{
			MaxTimersPerNode: 64,
			TimerLocation:    time.UTC,
			DiscoveryKind:    "panic",
			DiscoveryFactory: func(context publicprovider.Context) (publicprovider.Provider, error) {
				provider = &failingPublicationProvider{context: context}
				return provider, nil
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := current.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = current.Stop(context.Background()) })

	provider.mu.Lock()
	provider.panic = true
	provider.mu.Unlock()
	if err := target.Retire(t.Context()); !errors.Is(err, errs.ErrDiscoveryUnavailable) {
		t.Fatalf("Retire() panic error = %v", err)
	}
	provider.mu.Lock()
	provider.panic = false
	provider.mu.Unlock()
	resumeCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := target.Resume(resumeCtx); err != nil {
		t.Fatalf("Resume() after panic error = %v", err)
	}
}

func TestDiscoveryPublicationCoalescesConcurrentGenerationsAndCanceledWaiter(t *testing.T) {
	var changes []string
	target := &retirementService{label: "Only", changes: &changes}
	var provider *coalescingPublicationProvider
	current, err := New(
		Config{ID: "publication-coalescing", Services: []string{"Only"}},
		[]ServiceBinding{{Name: "Only", Template: "Only", Service: target}},
		originlog.NewNop(),
		Options{
			MaxTimersPerNode: 64,
			TimerLocation:    time.UTC,
			DiscoveryKind:    "coalescing",
			DiscoveryFactory: func(context publicprovider.Context) (publicprovider.Provider, error) {
				provider = &coalescingPublicationProvider{
					context: context,
					started: make(chan struct{}),
					release: make(chan struct{}),
				}
				return provider, nil
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := current.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = current.Stop(context.Background()) })

	retireResult := make(chan error, 1)
	go func() { retireResult <- target.Retire(context.Background()) }()
	select {
	case <-provider.started:
	case <-time.After(time.Second):
		t.Fatal("first dynamic publication did not start")
	}

	const waiters = 32
	results := make(chan error, waiters)
	for range waiters {
		go func() {
			results <- current.requestDiscoveryPublication(context.Background())
		}()
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := current.requestDiscoveryPublication(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled publication request error = %v", err)
	}

	deadline := time.Now().Add(time.Second)
	for {
		current.discoveryPublication.mu.Lock()
		desired := current.discoveryPublication.desired
		current.discoveryPublication.mu.Unlock()
		if desired == waiters+2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("desired generation = %d, want %d", desired, waiters+2)
		}
		time.Sleep(time.Millisecond)
	}
	close(provider.release)
	if err := <-retireResult; err != nil {
		t.Fatalf("Retire() error = %v", err)
	}
	for range waiters {
		if err := <-results; err != nil {
			t.Fatalf("coalesced request error = %v", err)
		}
	}
	provider.mu.Lock()
	calls := provider.calls
	provider.mu.Unlock()
	if calls != 3 {
		t.Fatalf("Provider.Publish calls = %d, want startup + 2 coalesced publishes", calls)
	}
}

func (*failingPublicationProvider) Withdraw(context.Context) error { return nil }
func (*failingPublicationProvider) Close(context.Context) error    { return nil }

func TestRetirePublicationFailureDoesNotRollbackLocalState(t *testing.T) {
	var changes []string
	target := &retirementService{label: "Only", changes: &changes}
	var provider *failingPublicationProvider
	current, err := New(
		Config{ID: "publication-failure", Services: []string{"Only"}},
		[]ServiceBinding{{Name: "Only", Template: "Only", Service: target}},
		originlog.NewNop(),
		Options{
			MaxTimersPerNode: 64,
			TimerLocation:    time.UTC,
			DiscoveryKind:    "failure",
			DiscoveryFactory: func(context publicprovider.Context) (publicprovider.Provider, error) {
				provider = &failingPublicationProvider{context: context}
				return provider, nil
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := current.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = current.Stop(context.Background()) })
	provider.mu.Lock()
	provider.fail = true
	provider.mu.Unlock()
	if err := target.Retire(t.Context()); !errors.Is(err, errs.ErrDiscoveryUnavailable) {
		t.Fatalf("Retire() error = %v", err)
	}
	if target.State() != service.StateRetired {
		t.Fatalf("State = %v", target.State())
	}
}

func TestNodeStopWinsBlockedRetirePublication(t *testing.T) {
	var changes []string
	target := &retirementService{label: "Only", changes: &changes}
	var provider *blockingPublicationProvider
	current, err := New(
		Config{ID: "stop-wins", Services: []string{"Only"}},
		[]ServiceBinding{{Name: "Only", Template: "Only", Service: target}},
		originlog.NewNop(),
		Options{
			MaxTimersPerNode: 64,
			TimerLocation:    time.UTC,
			DiscoveryKind:    "blocking",
			DiscoveryFactory: func(context publicprovider.Context) (publicprovider.Provider, error) {
				provider = &blockingPublicationProvider{
					context: context,
					started: make(chan struct{}, 1),
				}
				return provider, nil
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := current.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	retireResult := make(chan error, 1)
	go func() { retireResult <- target.Retire(context.Background()) }()
	select {
	case <-provider.started:
	case <-time.After(time.Second):
		t.Fatal("Retire 发布未开始")
	}
	stopCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := current.Stop(stopCtx); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	select {
	case err := <-retireResult:
		if err == nil {
			t.Fatal("被 Stop 中断的 Retire 未返回错误")
		}
	case <-time.After(time.Second):
		t.Fatal("Retire 调用未被 Stop 唤醒")
	}
	if current.State() != StateStopped || target.State() != service.StateStopped {
		t.Fatalf("final states = %v, %v", current.State(), target.State())
	}
}
