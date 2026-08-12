package blueprintmodule

import (
	"context"
	"errors"
	"testing"
	"time"

	originlog "github.com/duanhf2012/origin/v3/log"
	"github.com/duanhf2012/origin/v3/node"
	"github.com/duanhf2012/origin/v3/service"
)

const blueprintTestTimeout = 5 * time.Second

type instanceTestService struct {
	service.Service
	module *instanceTestModule
}

func (owner *instanceTestService) OnInit() error { return owner.AddModule(owner.module) }

type instanceTestModule struct {
	Module
	nodeDir  string
	graphDir string
	executed chan struct{}
	yielded  chan *YieldHandle
}

func (module *instanceTestModule) OnInit() error {
	if err := module.Setup(Config{NodeDir: module.nodeDir, GraphDir: module.graphDir}); err != nil {
		return err
	}
	return module.RegisterNodes(
		func() IExecNode { return &instanceTestNode{module: module} },
		func() IExecNode { return &instanceAsyncNode{module: module} },
	)
}

type instanceTestNode struct {
	BaseExecNode
	module *instanceTestModule
}

type instanceAsyncNode struct {
	BaseExecNode
	module *instanceTestModule
}

func (*instanceAsyncNode) GetName() string { return "LifecycleAsyncNode" }
func (node *instanceAsyncNode) Exec() (int, error) {
	handle, err := node.Yield(0)
	if err != nil {
		return -1, err
	}
	node.module.yielded <- handle
	return -1, ErrExecutionSuspended
}

func (*instanceTestNode) GetName() string { return "LifecycleNode" }
func (node *instanceTestNode) Exec() (int, error) {
	select {
	case node.module.executed <- struct{}{}:
	default:
	}
	return 0, nil
}

type instanceTestFixture struct {
	node    *node.Node
	service *instanceTestService
	module  *instanceTestModule
}

func startInstanceTestFixture(t testing.TB) *instanceTestFixture {
	return startInstanceTestFixtureWithScheduler(t, service.DefaultSchedulerConfig())
}

func startInstanceTestFixtureWithScheduler(t testing.TB, scheduler service.SchedulerConfig) *instanceTestFixture {
	t.Helper()
	nodeDir, graphDir := writeLifecycleFixture(t)
	module := &instanceTestModule{
		nodeDir: nodeDir, graphDir: graphDir,
		executed: make(chan struct{}, 8), yielded: make(chan *YieldHandle, 8),
	}
	owner := &instanceTestService{module: module}
	current, err := node.New(
		node.Config{ID: "blueprintmodule-test", Scheduler: scheduler, Services: []string{"BlueprintService"}},
		[]node.ServiceBinding{{Name: "BlueprintService", Template: "BlueprintService", Service: owner}},
		originlog.NewNop(),
		node.Options{MaxTimersPerNode: 32, TimerLocation: time.UTC},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), blueprintTestTimeout)
		defer cancel()
		_ = current.Rollback(ctx)
	})
	ctx, cancel := context.WithTimeout(context.Background(), blueprintTestTimeout)
	defer cancel()
	if err = current.Start(ctx); err != nil {
		t.Fatal(err)
	}
	return &instanceTestFixture{node: current, service: owner, module: module}
}

func dispatchInstanceTest(t *testing.T, fixture *instanceTestFixture, fn func(context.Context) error) error {
	t.Helper()
	result := make(chan error, 1)
	if err := fixture.service.DispatchAsync(func(ctx context.Context) { result <- fn(ctx) }); err != nil {
		return err
	}
	select {
	case err := <-result:
		return err
	case <-time.After(blueprintTestTimeout):
		t.Fatal("blueprint Service task timed out")
		return context.DeadlineExceeded
	}
}

func TestCreateRunAndCloseInstance(t *testing.T) {
	fixture := startInstanceTestFixture(t)
	err := dispatchInstanceTest(t, fixture, func(ctx context.Context) error {
		instance, err := fixture.module.Create("lifecycle", WithKey("battle:10001"))
		if err != nil {
			return err
		}
		if instance.ID() == 0 || instance.Name() != "lifecycle" || instance.Key() != "battle:10001" {
			t.Fatalf("unexpected instance diagnostics: id=%d name=%q key=%q", instance.ID(), instance.Name(), instance.Key())
		}
		if _, err = instance.Run(ctx, 1); err != nil {
			return err
		}
		if err = instance.Close(); err != nil {
			return err
		}
		if err = instance.Close(); err != nil {
			t.Fatalf("repeat Close() error = %v", err)
		}
		if _, err = instance.Start(ctx, 1); !errors.Is(err, ErrInstanceClosed) {
			t.Fatalf("Start after Close error = %v", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-fixture.module.executed:
	default:
		t.Fatal("blueprint node did not execute")
	}
}

func TestModuleRunReleasesTemporaryInstanceAndMissingGraphIsExplicit(t *testing.T) {
	fixture := startInstanceTestFixture(t)
	err := dispatchInstanceTest(t, fixture, func(ctx context.Context) error {
		if _, err := fixture.module.Run(ctx, "lifecycle", 1); err != nil {
			return err
		}
		if stats := fixture.module.Stats(); stats.ActiveInstances != 0 || stats.CreatedTotal != 1 || stats.ClosedTotal != 1 {
			t.Fatalf("unexpected Stats after temporary Run: %+v", stats)
		}
		if _, err := fixture.module.Create("missing"); !errors.Is(err, ErrGraphNotFound) {
			t.Fatalf("Create missing graph error = %v", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestServiceStopClosesLeakedInstance(t *testing.T) {
	fixture := startInstanceTestFixture(t)
	var instance *Instance
	if err := dispatchInstanceTest(t, fixture, func(context.Context) error {
		var err error
		instance, err = fixture.module.Create("lifecycle")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), blueprintTestTimeout)
	defer cancel()
	if err := fixture.node.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := instance.Start(context.Background(), 1); !errors.Is(err, ErrInstanceClosed) {
		t.Fatalf("Start after Service Stop error = %v", err)
	}
	if stats := fixture.module.Stats(); stats.ActiveInstances != 0 || stats.ClosedTotal != 1 {
		t.Fatalf("Stats after stop = %+v", stats)
	}
}
