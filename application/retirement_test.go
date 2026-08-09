package application

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/duanhf2012/origin/v3/command"
	publicprovider "github.com/duanhf2012/origin/v3/discovery/provider"
	"github.com/duanhf2012/origin/v3/errs"
	originlog "github.com/duanhf2012/origin/v3/log"
	"github.com/duanhf2012/origin/v3/node"
	"github.com/duanhf2012/origin/v3/service"
)

type applicationControlRequest struct {
	action command.ControlAction
	ctx    context.Context
	result chan error
}

func (request *applicationControlRequest) Action() command.ControlAction { return request.action }
func (request *applicationControlRequest) Context() context.Context      { return request.ctx }
func (request *applicationControlRequest) Complete(err error)            { request.result <- err }

type applicationControlEvent struct {
	nodeID      string
	serviceName string
	event       service.ServiceStateChanged
}

var applicationControlEvents chan applicationControlEvent

type applicationControlEventService struct {
	service.Service
}

func (target *applicationControlEventService) OnInit() error {
	events := applicationControlEvents
	return target.SubscribeEvent(
		service.ServiceStateChangedEventID,
		func(_ context.Context, raw service.Event) error {
			events <- applicationControlEvent{
				nodeID:      target.NodeID(),
				serviceName: target.Name(),
				event:       raw.(service.ServiceStateChanged),
			}
			return nil
		},
	)
}

type blockingRetireProvider struct {
	context publicprovider.Context
	entered chan struct{}
	once    sync.Once
}

func (provider *blockingRetireProvider) Start(context.Context) error {
	if err := provider.context.Host.SetTTL(3 * time.Second); err != nil {
		return err
	}
	if err := provider.context.Host.ReplaceSnapshot(publicprovider.Snapshot{}); err != nil {
		return err
	}
	provider.context.Host.Report(publicprovider.Report{State: publicprovider.StateReady})
	return nil
}

func (provider *blockingRetireProvider) Publish(ctx context.Context, node publicprovider.Node) error {
	for _, current := range node.Services {
		if current.State == publicprovider.ServiceStateRetired {
			provider.once.Do(func() { close(provider.entered) })
			<-ctx.Done()
			return ctx.Err()
		}
	}
	return nil
}

func (*blockingRetireProvider) Withdraw(context.Context) error { return nil }

func (provider *blockingRetireProvider) Close(context.Context) error {
	provider.context.Host.Report(publicprovider.Report{State: publicprovider.StateStopped})
	return nil
}

type applicationRetirementService struct {
	service.Service
	label   string
	changes *[]string
}

func (target *applicationRetirementService) OnInit() error {
	return target.SubscribeEvent(
		service.ServiceStateChangedEventID,
		func(_ context.Context, raw service.Event) error {
			event := raw.(service.ServiceStateChanged)
			*target.changes = append(*target.changes, target.label+":"+event.Current.String())
			return nil
		},
	)
}

func newApplicationRetirementNode(
	t *testing.T,
	id string,
	changes *[]string,
) *node.Node {
	t.Helper()
	first := &applicationRetirementService{label: id + "-a", changes: changes}
	second := &applicationRetirementService{label: id + "-b", changes: changes}
	current, err := node.New(
		node.Config{ID: id, Services: []string{first.label, second.label}},
		[]node.ServiceBinding{
			{Name: first.label, Template: "retirement", Service: first},
			{Name: second.label, Template: "retirement", Service: second},
		},
		originlog.NewNop(),
		node.Options{MaxTimersPerNode: 64, TimerLocation: time.UTC},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := current.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if current.State() == node.StateReady {
			_ = current.Stop(context.Background())
		}
	})
	return current
}

func TestApplicationRetireReverseResumeForward(t *testing.T) {
	var changes []string
	first := newApplicationRetirementNode(t, "node-1", &changes)
	second := newApplicationRetirementNode(t, "node-2", &changes)
	app := New()
	app.nodes = []*node.Node{first, second}
	app.state.Store(uint32(StateRunning))

	if err := app.Retire(t.Context()); err != nil {
		t.Fatalf("Retire() error = %v", err)
	}
	if err := app.Resume(t.Context()); err != nil {
		t.Fatalf("Resume() error = %v", err)
	}
	want := []string{
		"node-2-b:retired", "node-2-a:retired",
		"node-1-b:retired", "node-1-a:retired",
		"node-1-a:running", "node-1-b:running",
		"node-2-a:running", "node-2-b:running",
	}
	if len(changes) != len(want) {
		t.Fatalf("changes = %v", changes)
	}
	for index := range want {
		if changes[index] != want[index] {
			t.Fatalf("changes[%d] = %q, want %q", index, changes[index], want[index])
		}
	}
}

func TestApplicationRetireContinuesAfterNodeFailure(t *testing.T) {
	var changes []string
	first := newApplicationRetirementNode(t, "healthy", &changes)
	failed := newApplicationRetirementNode(t, "stopped", &changes)
	if err := failed.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
	app := New()
	app.nodes = []*node.Node{first, failed}
	app.state.Store(uint32(StateRunning))
	if err := app.Retire(t.Context()); err == nil {
		t.Fatal("停止 Node 的错误未聚合")
	}
	status, ok := first.ServiceStatus("healthy-a")
	if !ok || status.State != service.StateRetired {
		t.Fatalf("healthy ServiceStatus = %+v, %v", status, ok)
	}
}

func TestApplicationControlRetireResumeAndEvents(t *testing.T) {
	directory := writeApplicationConfig(t, `
nodes:
  - id: node-1
    services:
      - node-1-a:applicationControlEventService
      - node-1-b:applicationControlEventService
  - id: node-2
    services:
      - node-2-a:applicationControlEventService
      - node-2-b:applicationControlEventService
`)
	applicationControlEvents = make(chan applicationControlEvent, 16)
	defer func() { applicationControlEvents = nil }()
	controls := make(chan command.ControlRequest, 2)
	app := newSilentApplication()
	app.Setup(&applicationControlEventService{})
	runCtx, cancelRun := context.WithCancel(t.Context())
	runResult := make(chan error, 1)
	go func() {
		runResult <- app.run(runCtx, command.StartRequest{
			AppName:   "control-events",
			ConfigDir: directory,
			Controls:  controls,
		})
	}()
	waitForState(t, app, StateRunning)

	retire := sendApplicationControl(t, controls, command.ControlActionRetire)
	if err := receiveApplicationControlResult(t, retire.result); err != nil {
		t.Fatalf("Retire control error = %v", err)
	}
	wantRetired := []string{"node-2/node-2-b", "node-2/node-2-a", "node-1/node-1-b", "node-1/node-1-a"}
	assertApplicationControlEvents(
		t,
		applicationControlEvents,
		wantRetired,
		service.StateRunning,
		service.StateRetired,
	)

	idempotent := sendApplicationControl(t, controls, command.ControlActionRetire)
	if err := receiveApplicationControlResult(t, idempotent.result); err != nil {
		t.Fatalf("idempotent Retire control error = %v", err)
	}
	select {
	case event := <-applicationControlEvents:
		t.Fatalf("idempotent Retire emitted event: %+v", event)
	case <-time.After(100 * time.Millisecond):
	}

	resume := sendApplicationControl(t, controls, command.ControlActionResume)
	if err := receiveApplicationControlResult(t, resume.result); err != nil {
		t.Fatalf("Resume control error = %v", err)
	}
	wantRunning := []string{"node-1/node-1-a", "node-1/node-1-b", "node-2/node-2-a", "node-2/node-2-b"}
	assertApplicationControlEvents(
		t,
		applicationControlEvents,
		wantRunning,
		service.StateRetired,
		service.StateRunning,
	)

	cancelRun()
	if err := receiveApplicationControlResult(t, runResult); err != nil {
		t.Fatalf("Application run error = %v", err)
	}
}

func TestApplicationStopCancelsControl(t *testing.T) {
	directory := writeApplicationConfig(t, `
discovery:
  type: blocking
  blocking: {}
nodes:
  - id: game-1
    services: [applicationControlEventService]
`)
	applicationControlEvents = make(chan applicationControlEvent, 4)
	defer func() { applicationControlEvents = nil }()
	controls := make(chan command.ControlRequest, 1)
	providerEntered := make(chan struct{})
	app := newSilentApplication()
	app.Setup(&applicationControlEventService{})
	if err := app.RegisterDiscoveryProvider(
		"blocking",
		func(context publicprovider.Context) (publicprovider.Provider, error) {
			return &blockingRetireProvider{context: context, entered: providerEntered}, nil
		},
	); err != nil {
		t.Fatal(err)
	}
	runCtx, cancelRun := context.WithCancel(t.Context())
	runResult := make(chan error, 1)
	go func() {
		runResult <- app.run(runCtx, command.StartRequest{
			AppName:   "cancel-control",
			ConfigDir: directory,
			Controls:  controls,
		})
	}()
	waitForState(t, app, StateRunning)

	retire := sendApplicationControl(t, controls, command.ControlActionRetire)
	select {
	case <-providerEntered:
	case <-time.After(3 * time.Second):
		t.Fatal("Retire did not enter blocking Provider")
	}
	cancelRun()
	controlErr := receiveApplicationControlResult(t, retire.result)
	if !errs.IsCode(controlErr, errs.CodeCanceled) &&
		!errs.IsCode(controlErr, errs.CodeServiceStopping) {
		t.Fatalf("Retire result = %v, want canceled or stopping", controlErr)
	}
	if err := receiveApplicationControlResult(t, runResult); err != nil {
		t.Fatalf("Application run error = %v", err)
	}
	if app.State() != StateStopped {
		t.Fatalf("Application state = %v, want Stopped", app.State())
	}
}

func sendApplicationControl(
	t *testing.T,
	controls chan<- command.ControlRequest,
	action command.ControlAction,
) *applicationControlRequest {
	t.Helper()
	request := &applicationControlRequest{
		action: action,
		ctx:    t.Context(),
		result: make(chan error, 1),
	}
	controls <- request
	return request
}

func receiveApplicationControlResult(t *testing.T, result <-chan error) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for Application control result")
		return nil
	}
}

func assertApplicationControlEvents(
	t *testing.T,
	events <-chan applicationControlEvent,
	want []string,
	previous service.State,
	current service.State,
) {
	t.Helper()
	for index, identity := range want {
		select {
		case event := <-events:
			got := event.nodeID + "/" + event.serviceName
			if got != identity {
				t.Fatalf("event[%d] identity = %q, want %q", index, got, identity)
			}
			if event.event.Previous != previous || event.event.Current != current ||
				event.event.ChangedAt.IsZero() {
				t.Fatalf("event[%d] = %+v", index, event.event)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("timed out waiting for event[%d]", index)
		}
	}
}
