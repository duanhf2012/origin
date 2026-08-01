package application

import (
	"context"
	"testing"
	"time"

	originlog "github.com/duanhf2012/origin/v3/log"
	"github.com/duanhf2012/origin/v3/node"
	"github.com/duanhf2012/origin/v3/service"
)

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
