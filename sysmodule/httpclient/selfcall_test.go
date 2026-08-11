package httpclient

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	originlog "github.com/duanhf2012/origin/v3/log"
	"github.com/duanhf2012/origin/v3/node"
	"github.com/duanhf2012/origin/v3/service"
	"github.com/duanhf2012/origin/v3/sysmodule/ginmodule"
)

type selfCallService struct {
	service.Service
	module *selfCallModule
}

func (target *selfCallService) OnInit() error {
	return target.AddModule(target.module)
}

type selfCallModule struct {
	ginmodule.Module
	client       *Client
	calls        int
	slowStarted  chan struct{}
	slowFinished chan struct{}
}

func (module *selfCallModule) OnInit() error {
	if err := module.Setup("127.0.0.1:0", ginmodule.DefaultServerOptions()); err != nil {
		return err
	}
	client, err := New(DefaultOptions())
	if err != nil {
		return err
	}
	module.client = client
	module.SafePOST("/self", func(ctx *ginmodule.SafeContext) {
		module.calls++
		ctx.JSON(http.StatusCreated, map[string]int{"calls": module.calls})
	})
	module.SafeGET("/business-error", func(ctx *ginmodule.SafeContext) {
		ctx.JSON(http.StatusConflict, map[string]string{"error": "already exists"})
	})
	module.SafeGET("/slow", func(ctx *ginmodule.SafeContext) {
		close(module.slowStarted)
		<-ctx.Context().Done()
		close(module.slowFinished)
	})
	return nil
}

type selfCallFixture struct {
	node    *node.Node
	owner   *selfCallService
	module  *selfCallModule
	baseURL string
}

type selfCallResult struct {
	response Response
	err      error
}

func startSelfCallFixture(t *testing.T, scheduler service.SchedulerConfig) *selfCallFixture {
	t.Helper()
	module := &selfCallModule{
		slowStarted:  make(chan struct{}),
		slowFinished: make(chan struct{}),
	}
	owner := &selfCallService{module: module}
	current, err := node.New(
		node.Config{
			ID:        "httpclient-self-call",
			Services:  []string{"HTTPService"},
			Scheduler: scheduler,
		},
		[]node.ServiceBinding{{
			Name:     "HTTPService",
			Template: "HTTPService",
			Service:  owner,
		}},
		originlog.NewNop(),
		node.Options{MaxTimersPerNode: 64, TimerLocation: time.UTC},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		module.client.CloseIdleConnections()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = current.Rollback(ctx)
	})
	startContext, cancelStart := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelStart()
	if err := current.Start(startContext); err != nil {
		t.Fatal(err)
	}
	return &selfCallFixture{
		node:    current,
		owner:   owner,
		module:  module,
		baseURL: "http://" + module.Addr().String(),
	}
}

func (fixture *selfCallFixture) call(
	t *testing.T,
	method string,
	path string,
	requestTimeout time.Duration,
) selfCallResult {
	t.Helper()
	resultChannel := make(chan selfCallResult, 1)
	if err := fixture.owner.DispatchAsync(func(taskContext context.Context) {
		var response Response
		awaitErr := fixture.module.Await(taskContext, func(waitContext context.Context) error {
			requestContext := waitContext
			finish := func() {}
			if requestTimeout > 0 {
				requestContext, finish = context.WithTimeout(waitContext, requestTimeout)
			}
			defer finish()
			request, requestErr := http.NewRequestWithContext(
				requestContext,
				method,
				fixture.baseURL+path,
				http.NoBody,
			)
			if requestErr != nil {
				return requestErr
			}
			response, requestErr = fixture.module.client.DoBytes(request)
			return requestErr
		})
		resultChannel <- selfCallResult{response: response, err: awaitErr}
	}); err != nil {
		t.Fatal(err)
	}

	select {
	case completed := <-resultChannel:
		return completed
	case <-time.After(5 * time.Second):
		t.Fatal("same-Service HTTP self-call deadlocked")
		return selfCallResult{}
	}
}

func (fixture *selfCallFixture) stop(t *testing.T) {
	t.Helper()
	stopContext, cancelStop := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelStop()
	if err := fixture.node.Stop(stopContext); err != nil {
		t.Fatal(err)
	}
	fixture.module.client.CloseIdleConnections()
}

func TestServiceAwaitCanUseHTTPClientToCallOwnSafeRoute(t *testing.T) {
	fixture := startSelfCallFixture(t, service.DefaultSchedulerConfig())
	completed := fixture.call(t, http.MethodPost, "/self", 0)
	if completed.err != nil || completed.response.StatusCode != http.StatusCreated {
		t.Fatalf("self-call response=%+v error=%v", completed.response, completed.err)
	}
	var body map[string]int
	if err := json.Unmarshal(completed.response.Body, &body); err != nil || body["calls"] != 1 {
		t.Fatalf("self-call Body=%q decoded=%v error=%v", completed.response.Body, body, err)
	}

	completed = fixture.call(t, http.MethodGet, "/business-error", 0)
	if completed.err != nil || completed.response.StatusCode != http.StatusConflict {
		t.Fatalf("business error response=%+v error=%v", completed.response, completed.err)
	}
	fixture.stop(t)
}

func TestServiceAwaitHTTPClientCancellationConverges(t *testing.T) {
	fixture := startSelfCallFixture(t, service.DefaultSchedulerConfig())
	completed := fixture.call(t, http.MethodGet, "/slow", 40*time.Millisecond)
	if !errors.Is(completed.err, context.DeadlineExceeded) {
		t.Fatalf("canceled self-call error=%v", completed.err)
	}
	for name, channel := range map[string]<-chan struct{}{
		"start":  fixture.module.slowStarted,
		"finish": fixture.module.slowFinished,
	} {
		select {
		case <-channel:
		case <-time.After(time.Second):
			t.Fatalf("slow Safe Handler did not %s", name)
		}
	}
	fixture.stop(t)
}

func TestServiceAwaitSelfCallQueueOverloadReturns503(t *testing.T) {
	fixture := startSelfCallFixture(t, service.SchedulerConfig{
		MaxTasks:            1,
		MaxAwaitTasks:       1,
		DefaultAwaitTimeout: time.Second,
	})
	completed := fixture.call(t, http.MethodPost, "/self", 0)
	if completed.err != nil || completed.response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("overloaded self-call response=%+v error=%v", completed.response, completed.err)
	}
	fixture.stop(t)
}
