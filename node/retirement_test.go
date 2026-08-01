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
	"github.com/duanhf2012/origin/v3/service"
)

type retirementService struct {
	service.Service
	label   string
	changes *[]string
	events  chan service.ServiceStateChanged
}

func (target *retirementService) OnInit() error {
	return target.SubscribeEvent(
		service.ServiceStateChangedEventID,
		func(_ context.Context, raw service.Event) error {
			event := raw.(service.ServiceStateChanged)
			*target.changes = append(*target.changes, target.label+":"+event.Current.String())
			if target.events != nil {
				target.events <- event
			}
			return nil
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
	if desired != 4 || acknowledged != desired {
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

type failingPublicationProvider struct {
	context publicprovider.Context
	mu      sync.Mutex
	fail    bool
}

type blockingPublicationProvider struct {
	context publicprovider.Context
	mu      sync.Mutex
	calls   int
	started chan struct{}
}

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
	fail := provider.fail
	provider.mu.Unlock()
	if fail {
		return errs.ErrDiscoveryUnavailable
	}
	return nil
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
