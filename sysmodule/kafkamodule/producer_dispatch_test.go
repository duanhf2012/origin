package kafkamodule

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/IBM/sarama"
	originlog "github.com/duanhf2012/origin/v3/log"
	"github.com/duanhf2012/origin/v3/node"
	"github.com/duanhf2012/origin/v3/service"
)

type dispatchTestService struct {
	service.Service
	module *dispatchTestModule
}

func (owner *dispatchTestService) OnInit() error { return owner.AddModule(owner.module) }

type dispatchTestModule struct {
	Producer
	runtime *fakeProducerRuntime
}

func (module *dispatchTestModule) OnInit() error {
	return module.Setup(validProducerConfig(), withProducerRuntimeFactory(func(context.Context, []string, *sarama.Config) (producerRuntime, error) {
		return module.runtime, nil
	}))
}

func startDispatchTestFixture(t *testing.T, scheduler service.SchedulerConfig) (*node.Node, *dispatchTestService, *dispatchTestModule) {
	t.Helper()
	module := &dispatchTestModule{runtime: newFakeProducerRuntime(nil)}
	owner := &dispatchTestService{module: module}
	current, err := node.New(node.Config{ID: "kafka-dispatch-test", Services: []string{"KafkaService"}, Scheduler: scheduler}, []node.ServiceBinding{{Name: "KafkaService", Template: "KafkaService", Service: owner}}, originlog.NewNop(), node.Options{MaxTimersPerNode: 32, TimerLocation: time.UTC})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = current.Rollback(ctx)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err = current.Start(ctx); err != nil {
		t.Fatal(err)
	}
	return current, owner, module
}

func TestDispatchDeliveryRunsHandlerInOwnerService(t *testing.T) {
	_, _, module := startDispatchTestFixture(t, service.DefaultSchedulerConfig())
	delivery := newDelivery()
	delivery.complete(DeliveryResult{Metadata: Metadata{Offset: 9}})
	called := make(chan bool, 1)
	err := module.DispatchDelivery(context.Background(), delivery, func(ctx context.Context, result DeliveryResult) {
		// Await 只允许在所属 Service 的任务上下文中执行，用它验证回调协程归属。
		called <- module.Await(ctx, func(context.Context) error { return nil }) == nil && result.Metadata.Offset == 9
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case valid := <-called:
		if !valid {
			t.Fatal("handler did not run in owner Service task")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("delivery handler timed out")
	}
}

func TestDispatchDeliveriesUsesOneCallbackAndPreservesOrder(t *testing.T) {
	_, _, module := startDispatchTestFixture(t, service.DefaultSchedulerConfig())
	deliveries := []*Delivery{newDelivery(), newDelivery()}
	deliveries[0].complete(DeliveryResult{Metadata: Metadata{Offset: 3}})
	deliveries[1].complete(DeliveryResult{Err: errors.New("rejected")})
	called := make(chan []DeliveryResult, 1)
	if err := module.DispatchDeliveries(context.Background(), deliveries, func(_ context.Context, results []DeliveryResult) { called <- results }); err != nil {
		t.Fatal(err)
	}
	select {
	case results := <-called:
		if len(results) != 2 || results[0].Metadata.Offset != 3 || results[1].Err == nil {
			t.Fatalf("unexpected results: %+v", results)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("batch callback timed out")
	}
}

func TestDispatchDeliveryRejectsInvalidArguments(t *testing.T) {
	_, _, module := startDispatchTestFixture(t, service.DefaultSchedulerConfig())
	if err := module.DispatchDelivery(nil, newDelivery(), func(context.Context, DeliveryResult) {}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("nil context: %v", err)
	}
	if err := module.DispatchDelivery(context.Background(), nil, func(context.Context, DeliveryResult) {}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("nil delivery: %v", err)
	}
	if err := module.DispatchDeliveries(context.Background(), nil, func(context.Context, []DeliveryResult) {}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("empty deliveries: %v", err)
	}
}
