package ginmodule

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/duanhf2012/origin/v3/errs"
	originlog "github.com/duanhf2012/origin/v3/log"
	"github.com/duanhf2012/origin/v3/node"
	"github.com/duanhf2012/origin/v3/service"
	"github.com/gin-gonic/gin"
)

const integrationTimeout = 5 * time.Second

type integrationService struct {
	service.Service
	module *integrationModule
}

func (target *integrationService) OnInit() error {
	return target.AddModule(target.module)
}

type integrationModule struct {
	Module
	address   string
	options   ServerOptions
	configure func(*integrationModule)
}

func (module *integrationModule) OnInit() error {
	address := module.address
	if address == "" {
		address = "127.0.0.1:0"
	}
	if err := module.Setup(address, module.options); err != nil {
		return err
	}
	if module.configure != nil {
		module.configure(module)
	}
	return nil
}

type integrationFixture struct {
	node    *node.Node
	service *integrationService
	module  *integrationModule
	client  *http.Client
	baseURL string
}

func startIntegrationFixture(
	t *testing.T,
	options ServerOptions,
	scheduler service.SchedulerConfig,
	configure func(*integrationModule),
) *integrationFixture {
	t.Helper()
	module := &integrationModule{options: options, configure: configure}
	current, owner := newIntegrationNode(t, module, scheduler)
	t.Cleanup(func() {
		rollbackContext, cancel := context.WithTimeout(context.Background(), integrationTimeout)
		defer cancel()
		_ = current.Rollback(rollbackContext)
	})
	startContext, cancelStart := context.WithTimeout(context.Background(), integrationTimeout)
	defer cancelStart()
	if err := current.Start(startContext); err != nil {
		t.Fatalf("Node.Start() error = %v", err)
	}
	if module.Addr() == nil {
		t.Fatal("ginmodule did not publish listener address")
	}
	return &integrationFixture{
		node:    current,
		service: owner,
		module:  module,
		client:  &http.Client{Timeout: integrationTimeout},
		baseURL: "http://" + module.Addr().String(),
	}
}

func newIntegrationNode(
	t *testing.T,
	module *integrationModule,
	scheduler service.SchedulerConfig,
) (*node.Node, *integrationService) {
	t.Helper()
	owner := &integrationService{module: module}
	current, err := node.New(
		node.Config{
			ID:        "ginmodule-test",
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
	return current, owner
}

func (fixture *integrationFixture) stop(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), integrationTimeout)
	defer cancel()
	if err := fixture.node.Stop(ctx); err != nil {
		t.Fatalf("Node.Stop() error = %v", err)
	}
	if fixture.module.Addr() != nil {
		t.Fatalf("Addr() after stop = %v", fixture.module.Addr())
	}
}

func TestGroupAndSafeGroupExecutionScopes(t *testing.T) {
	type createRequest struct {
		Name string `json:"name" binding:"required"`
	}
	type createResponse struct {
		ID        string `json:"id"`
		Name      string `json:"name"`
		Principal string `json:"principal"`
		Trace     string `json:"trace"`
	}

	var orderMu sync.Mutex
	var order []string
	record := func(value string) {
		orderMu.Lock()
		order = append(order, value)
		orderMu.Unlock()
	}

	options := DefaultServerOptions()
	fixture := startIntegrationFixture(t, options, service.DefaultSchedulerConfig(), func(module *integrationModule) {
		api := module.Group("/api", func(ctx *gin.Context) {
			record("gin-before")
			ctx.Set("principal", "player-7")
			requestContext := context.WithValue(ctx.Request.Context(), integrationContextKey{}, "trace-9")
			ctx.Request = ctx.Request.WithContext(requestContext)
			ctx.Next()
			record("gin-after")
		})
		players := api.SafeGroup("/players", func(ctx *SafeContext) {
			record("safe-before")
			if err := module.Await(ctx.Context(), func(context.Context) error { return nil }); err != nil {
				ctx.AbortWithStatusJSON(http.StatusInternalServerError, map[string]any{"error": err.Error()})
				return
			}
			ctx.Next()
			record("safe-after")
		})
		players.POST("/:id", func(ctx *SafeContext) {
			record("handler")
			var request createRequest
			if err := ctx.ShouldBindJSON(&request); err != nil {
				ctx.JSON(http.StatusBadRequest, map[string]any{"error": err.Error()})
				return
			}
			principal, _ := ctx.Get("principal")
			trace, _ := ctx.Context().Value(integrationContextKey{}).(string)
			ctx.Header("X-Handled-By", "safe")
			ctx.JSON(http.StatusCreated, createResponse{
				ID:        ctx.Param("id"),
				Name:      request.Name,
				Principal: principal.(string),
				Trace:     trace,
			})
		})
	})

	request, err := http.NewRequest(
		http.MethodPost,
		fixture.baseURL+"/api/players/42?source=test",
		bytes.NewBufferString(`{"name":"origin"}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := fixture.client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusCreated || response.Header.Get("X-Handled-By") != "safe" {
		t.Fatalf("response status=%d header=%q body=%s", response.StatusCode, response.Header.Get("X-Handled-By"), body)
	}
	var decoded createResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded != (createResponse{ID: "42", Name: "origin", Principal: "player-7", Trace: "trace-9"}) {
		t.Fatalf("decoded response = %+v", decoded)
	}
	orderMu.Lock()
	gotOrder := append([]string(nil), order...)
	orderMu.Unlock()
	wantOrder := []string{"gin-before", "safe-before", "handler", "safe-after", "gin-after"}
	if !reflect.DeepEqual(gotOrder, wantOrder) {
		t.Fatalf("execution order = %v, want %v", gotOrder, wantOrder)
	}
	if stats := fixture.module.Stats(); stats.TotalRequests != 1 || stats.ActiveRequests != 0 {
		t.Fatalf("stats = %+v", stats)
	}
	fixture.stop(t)
}

type integrationContextKey struct{}

func TestNormalAndSafeRouteOptionalMiddlewareOrder(t *testing.T) {
	var mu sync.Mutex
	orders := make(map[string][]string)
	record := func(route, value string) {
		mu.Lock()
		orders[route] = append(orders[route], value)
		mu.Unlock()
	}
	options := DefaultServerOptions()
	fixture := startIntegrationFixture(t, options, service.DefaultSchedulerConfig(), func(module *integrationModule) {
		module.GET("/plain", func(ctx *gin.Context) {
			record("plain", "handler")
			ctx.String(http.StatusOK, "plain")
		})
		module.GET("/normal", func(ctx *gin.Context) {
			record("normal", "handler")
			err := module.Await(ctx.Request.Context(), func(context.Context) error { return nil })
			if !errors.Is(err, errs.ErrInvalidArgument) {
				ctx.String(http.StatusInternalServerError, "unexpected await: %v", err)
				return
			}
			ctx.String(http.StatusOK, "normal")
		}, func(ctx *gin.Context) {
			record("normal", "before")
			ctx.Next()
			record("normal", "after")
		})
		module.SafeGET("/safe", func(ctx *SafeContext) {
			record("safe", "handler")
			ctx.String(http.StatusOK, "safe")
		}, func(ctx *SafeContext) {
			record("safe", "before")
			ctx.Next()
			record("safe", "after")
		})
	})

	for _, route := range []string{"plain", "normal", "safe"} {
		response, err := fixture.client.Get(fixture.baseURL + "/" + route)
		if err != nil {
			t.Fatal(err)
		}
		body, readErr := io.ReadAll(response.Body)
		response.Body.Close()
		if readErr != nil || response.StatusCode != http.StatusOK || string(body) != route {
			t.Fatalf("route=%s status=%d body=%q error=%v", route, response.StatusCode, body, readErr)
		}
	}
	mu.Lock()
	got := make(map[string][]string, len(orders))
	for key, values := range orders {
		got[key] = append([]string(nil), values...)
	}
	mu.Unlock()
	if !reflect.DeepEqual(got["plain"], []string{"handler"}) ||
		!reflect.DeepEqual(got["normal"], []string{"before", "handler", "after"}) ||
		!reflect.DeepEqual(got["safe"], []string{"before", "handler", "after"}) {
		t.Fatalf("orders = %#v", got)
	}
	fixture.stop(t)
}

func TestSafeMiddlewareAbortSkipsHandler(t *testing.T) {
	var called atomic.Bool
	fixture := startIntegrationFixture(
		t,
		DefaultServerOptions(),
		service.DefaultSchedulerConfig(),
		func(module *integrationModule) {
			module.SafeGET("/forbidden", func(ctx *SafeContext) {
				called.Store(true)
				ctx.Status(http.StatusNoContent)
			}, func(ctx *SafeContext) {
				ctx.AbortWithStatusJSON(http.StatusForbidden, map[string]string{"error": "forbidden"})
			})
		},
	)
	response, err := fixture.client.Get(fixture.baseURL + "/forbidden")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusForbidden || called.Load() {
		t.Fatalf("status=%d handler_called=%v", response.StatusCode, called.Load())
	}
	fixture.stop(t)
}

func TestSafeRequestBodyLimit(t *testing.T) {
	options := DefaultServerOptions()
	options.MaxRequestBodySize = 4
	fixture := startIntegrationFixture(t, options, service.DefaultSchedulerConfig(), func(module *integrationModule) {
		module.SafePOST("/body", func(ctx *SafeContext) { ctx.Status(http.StatusNoContent) })
	})
	response, err := fixture.client.Post(fixture.baseURL+"/body", "text/plain", bytes.NewBufferString("12345"))
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d", response.StatusCode)
	}
	fixture.stop(t)
}

func TestSafeDispatchOverloadReturnsServiceUnavailable(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	options := DefaultServerOptions()
	scheduler := service.SchedulerConfig{
		MaxTasks:            1,
		MaxAwaitTasks:       1,
		DefaultAwaitTimeout: time.Second,
	}
	fixture := startIntegrationFixture(t, options, scheduler, func(module *integrationModule) {
		module.SafeGET("/block", func(ctx *SafeContext) {
			select {
			case <-started:
			default:
				close(started)
			}
			<-release
			ctx.String(http.StatusOK, "done")
		})
	})

	firstResult := make(chan error, 1)
	go func() {
		response, err := fixture.client.Get(fixture.baseURL + "/block")
		if err == nil {
			_, _ = io.Copy(io.Discard, response.Body)
			response.Body.Close()
			if response.StatusCode != http.StatusOK {
				err = errors.New("first request returned non-200")
			}
		}
		firstResult <- err
	}()
	select {
	case <-started:
	case <-time.After(integrationTimeout):
		t.Fatal("first Safe Handler did not start")
	}

	response, err := fixture.client.Get(fixture.baseURL + "/block")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("overload status = %d", response.StatusCode)
	}
	close(release)
	if err := <-firstResult; err != nil {
		t.Fatal(err)
	}
	if fixture.module.Stats().RejectedRequests == 0 {
		t.Fatal("RejectedRequests was not incremented")
	}
	fixture.stop(t)
}

func TestSafeTimeoutDoesNotLateWrite(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	completed := make(chan struct{})
	options := DefaultServerOptions()
	options.RequestTimeout = 50 * time.Millisecond
	options.WriteTimeout = time.Second
	fixture := startIntegrationFixture(t, options, service.DefaultSchedulerConfig(), func(module *integrationModule) {
		module.SafeGET("/slow", func(ctx *SafeContext) {
			close(started)
			<-release
			ctx.String(http.StatusOK, "late")
			close(completed)
		})
	})

	response, err := fixture.client.Get(fixture.baseURL + "/slow")
	if err != nil {
		t.Fatal(err)
	}
	body, readErr := io.ReadAll(response.Body)
	response.Body.Close()
	if readErr != nil || response.StatusCode != http.StatusGatewayTimeout {
		t.Fatalf("status=%d body=%q error=%v", response.StatusCode, body, readErr)
	}
	select {
	case <-started:
	default:
		t.Fatal("Safe Handler did not start")
	}
	close(release)
	select {
	case <-completed:
	case <-time.After(integrationTimeout):
		t.Fatal("late Safe Handler did not complete")
	}
	deadline := time.Now().Add(time.Second)
	for fixture.module.Stats().TimedOutRequests == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if fixture.module.Stats().TimedOutRequests == 0 {
		t.Fatal("TimedOutRequests was not incremented")
	}
	fixture.stop(t)
}

func TestQueuedSafeRequestCanceledBeforeExecutionSkipsHandler(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	firstTaskFinished := make(chan struct{})
	var canceledHandlerCalls atomic.Int64
	options := DefaultServerOptions()
	options.RequestTimeout = 50 * time.Millisecond
	options.WriteTimeout = time.Second
	scheduler := service.SchedulerConfig{
		MaxTasks:            2,
		MaxAwaitTasks:       1,
		DefaultAwaitTimeout: time.Second,
	}
	fixture := startIntegrationFixture(t, options, scheduler, func(module *integrationModule) {
		module.SafeGET("/occupy", func(ctx *SafeContext) {
			defer close(firstTaskFinished)
			close(started)
			<-release
			ctx.Status(http.StatusNoContent)
		})
		module.SafeGET("/canceled", func(ctx *SafeContext) {
			canceledHandlerCalls.Add(1)
			ctx.Status(http.StatusNoContent)
		})
	})

	firstDone := make(chan struct{})
	go func() {
		defer close(firstDone)
		response, err := fixture.client.Get(fixture.baseURL + "/occupy")
		if err == nil {
			response.Body.Close()
		}
	}()
	select {
	case <-started:
	case <-time.After(integrationTimeout):
		t.Fatal("occupying Safe Handler did not start")
	}

	response, err := fixture.client.Get(fixture.baseURL + "/canceled")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusGatewayTimeout {
		t.Fatalf("queued request status = %d", response.StatusCode)
	}
	close(release)
	select {
	case <-firstTaskFinished:
	case <-time.After(integrationTimeout):
		t.Fatal("occupying Safe Task did not finish")
	}
	select {
	case <-firstDone:
	case <-time.After(integrationTimeout):
		t.Fatal("occupying HTTP request did not finish")
	}

	barrier := make(chan struct{})
	if err := fixture.service.DispatchAsync(func(context.Context) { close(barrier) }); err != nil {
		t.Fatalf("dispatching barrier: %v", err)
	}
	select {
	case <-barrier:
	case <-time.After(integrationTimeout):
		t.Fatal("Service did not drain canceled Safe task")
	}
	if canceledHandlerCalls.Load() != 0 {
		t.Fatal("canceled queued Safe Handler executed")
	}
	fixture.stop(t)
}

func TestSafePanicReturns500AndServiceRemainsAvailable(t *testing.T) {
	fixture := startIntegrationFixture(
		t,
		DefaultServerOptions(),
		service.DefaultSchedulerConfig(),
		func(module *integrationModule) {
			module.SafeGET("/panic", func(*SafeContext) { panic("safe boom") })
			module.SafeGET("/ok", func(ctx *SafeContext) { ctx.String(http.StatusOK, "ok") })
		},
	)
	response, err := fixture.client.Get(fixture.baseURL + "/panic")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusInternalServerError {
		t.Fatalf("panic status = %d", response.StatusCode)
	}
	response, err = fixture.client.Get(fixture.baseURL + "/ok")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusOK || string(body) != "ok" {
		t.Fatalf("post-panic status=%d body=%q", response.StatusCode, body)
	}
	deadline := time.Now().Add(time.Second)
	for fixture.service.ExecutionStats().PanicTotal == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if fixture.module.Stats().PanicTotal == 0 || fixture.service.ExecutionStats().PanicTotal == 0 {
		t.Fatalf("server stats=%+v service stats=%+v", fixture.module.Stats(), fixture.service.ExecutionStats())
	}
	fixture.stop(t)
}

func TestRouteMethodFacadesAndFallbackHandlers(t *testing.T) {
	fixture := startIntegrationFixture(
		t,
		DefaultServerOptions(),
		service.DefaultSchedulerConfig(),
		func(module *integrationModule) {
			module.Use(func(ctx *gin.Context) {
				ctx.Header("X-Module-Middleware", "true")
				ctx.Next()
			})
			module.POST("/module/post", normalMethodHandler("POST"))
			module.PUT("/module/put", normalMethodHandler("PUT"))
			module.PATCH("/module/patch", normalMethodHandler("PATCH"))
			module.DELETE("/module/delete", normalMethodHandler("DELETE"))
			module.HEAD("/module/head", normalMethodHandler("HEAD"))
			module.OPTIONS("/module/options", normalMethodHandler("OPTIONS"))

			normal := module.Group("/group")
			normal.Use(func(ctx *gin.Context) {
				ctx.Header("X-Group-Middleware", "true")
				ctx.Next()
			})
			nested := normal.Group("/nested")
			nested.GET("/get", normalMethodHandler("GET"))
			normal.POST("/post", normalMethodHandler("POST"))
			normal.PUT("/put", normalMethodHandler("PUT"))
			normal.PATCH("/patch", normalMethodHandler("PATCH"))
			normal.DELETE("/delete", normalMethodHandler("DELETE"))
			normal.HEAD("/head", normalMethodHandler("HEAD"))
			normal.OPTIONS("/options", normalMethodHandler("OPTIONS"))

			module.SafePUT("/safe/put", safeMethodHandler("PUT"))
			module.SafePATCH("/safe/patch", safeMethodHandler("PATCH"))
			module.SafeDELETE("/safe/delete", safeMethodHandler("DELETE"))

			safe := module.SafeGroup("/safe-group", func(ctx *SafeContext) { ctx.Next() })
			safeNested := safe.Group("/nested", func(ctx *SafeContext) { ctx.Next() })
			safe.GET("/get", safeMethodHandler("GET"))
			safe.PUT("/put", safeMethodHandler("PUT"))
			safe.PATCH("/patch", safeMethodHandler("PATCH"))
			safe.DELETE("/delete", safeMethodHandler("DELETE"))
			safe.HEAD("/head", safeMethodHandler("HEAD"))
			safe.OPTIONS("/options", safeMethodHandler("OPTIONS"))
			safeNested.POST("/post", safeMethodHandler("POST"))

			module.GET("/only-get", normalMethodHandler("GET"))
			module.NoRoute(func(ctx *gin.Context) { ctx.String(http.StatusNotFound, "custom-404") })
			module.NoMethod(func(ctx *gin.Context) { ctx.String(http.StatusMethodNotAllowed, "custom-405") })
		},
	)

	tests := []struct {
		method       string
		path         string
		wantStatus   int
		wantBody     string
		wantGroup    bool
		wantModuleMW bool
	}{
		{http.MethodPost, "/module/post", 200, "POST", false, true},
		{http.MethodPut, "/module/put", 200, "PUT", false, true},
		{http.MethodPatch, "/module/patch", 200, "PATCH", false, true},
		{http.MethodDelete, "/module/delete", 200, "DELETE", false, true},
		{http.MethodHead, "/module/head", 200, "", false, true},
		{http.MethodOptions, "/module/options", 200, "OPTIONS", false, true},
		{http.MethodGet, "/group/nested/get", 200, "GET", true, true},
		{http.MethodPost, "/group/post", 200, "POST", true, true},
		{http.MethodPut, "/group/put", 200, "PUT", true, true},
		{http.MethodPatch, "/group/patch", 200, "PATCH", true, true},
		{http.MethodDelete, "/group/delete", 200, "DELETE", true, true},
		{http.MethodHead, "/group/head", 200, "", true, true},
		{http.MethodOptions, "/group/options", 200, "OPTIONS", true, true},
		{http.MethodPut, "/safe/put", 200, "PUT", false, true},
		{http.MethodPatch, "/safe/patch", 200, "PATCH", false, true},
		{http.MethodDelete, "/safe/delete", 200, "DELETE", false, true},
		{http.MethodGet, "/safe-group/get", 200, "GET", false, true},
		{http.MethodPut, "/safe-group/put", 200, "PUT", false, true},
		{http.MethodPatch, "/safe-group/patch", 200, "PATCH", false, true},
		{http.MethodDelete, "/safe-group/delete", 200, "DELETE", false, true},
		{http.MethodHead, "/safe-group/head", 200, "", false, true},
		{http.MethodOptions, "/safe-group/options", 200, "OPTIONS", false, true},
		{http.MethodPost, "/safe-group/nested/post", 200, "POST", false, true},
		{http.MethodGet, "/missing", 404, "custom-404", false, true},
		{http.MethodPost, "/only-get", 405, "custom-405", false, true},
	}
	for _, test := range tests {
		request, err := http.NewRequest(test.method, fixture.baseURL+test.path, nil)
		if err != nil {
			t.Fatal(err)
		}
		response, err := fixture.client.Do(request)
		if err != nil {
			t.Fatalf("%s %s: %v", test.method, test.path, err)
		}
		body, readErr := io.ReadAll(response.Body)
		response.Body.Close()
		if readErr != nil || response.StatusCode != test.wantStatus || string(body) != test.wantBody {
			t.Fatalf(
				"%s %s status=%d body=%q error=%v",
				test.method,
				test.path,
				response.StatusCode,
				body,
				readErr,
			)
		}
		if test.wantModuleMW && response.Header.Get("X-Module-Middleware") != "true" {
			t.Fatalf("%s %s missed module middleware", test.method, test.path)
		}
		if test.wantGroup && response.Header.Get("X-Group-Middleware") != "true" {
			t.Fatalf("%s %s missed group middleware", test.method, test.path)
		}
	}
	fixture.stop(t)
}

func normalMethodHandler(name string) gin.HandlerFunc {
	return func(ctx *gin.Context) { ctx.String(http.StatusOK, name) }
}

func safeMethodHandler(name string) SafeHandlerFunc {
	return func(ctx *SafeContext) {
		ctx.Data(http.StatusOK, "text/plain; charset=utf-8", []byte(name))
	}
}

func TestServiceAwaitCanCallOwnSafeHTTPRoute(t *testing.T) {
	fixture := startIntegrationFixture(
		t,
		DefaultServerOptions(),
		service.DefaultSchedulerConfig(),
		func(module *integrationModule) {
			module.SafeGET("/self", func(ctx *SafeContext) {
				ctx.String(http.StatusOK, "self-response")
			})
		},
	)
	result := make(chan error, 1)
	if err := fixture.service.DispatchAsync(func(taskContext context.Context) {
		result <- fixture.module.Await(taskContext, func(waitContext context.Context) error {
			request, err := http.NewRequestWithContext(waitContext, http.MethodGet, fixture.baseURL+"/self", nil)
			if err != nil {
				return err
			}
			response, err := fixture.client.Do(request)
			if err != nil {
				return err
			}
			defer response.Body.Close()
			body, err := io.ReadAll(response.Body)
			if err != nil {
				return err
			}
			if response.StatusCode != http.StatusOK || string(body) != "self-response" {
				return errors.New("unexpected self-call response")
			}
			return nil
		})
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("self call error = %v", err)
		}
	case <-time.After(integrationTimeout):
		t.Fatal("same-Service HTTP self-call deadlocked")
	}
	fixture.stop(t)
}

func TestMaxActiveRequestsRejectsConcurrentOrdinaryRequest(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	options := DefaultServerOptions()
	options.MaxActiveRequests = 1
	fixture := startIntegrationFixture(t, options, service.DefaultSchedulerConfig(), func(module *integrationModule) {
		module.GET("/ordinary-block", func(ctx *gin.Context) {
			select {
			case <-started:
			default:
				close(started)
			}
			<-release
			ctx.String(http.StatusOK, "done")
		})
	})
	firstResult := make(chan error, 1)
	go func() {
		response, err := fixture.client.Get(fixture.baseURL + "/ordinary-block")
		if err == nil {
			response.Body.Close()
		}
		firstResult <- err
	}()
	select {
	case <-started:
	case <-time.After(integrationTimeout):
		t.Fatal("ordinary handler did not start")
	}
	response, err := fixture.client.Get(fixture.baseURL + "/ordinary-block")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("active limit status = %d", response.StatusCode)
	}
	close(release)
	if err := <-firstResult; err != nil {
		t.Fatal(err)
	}
	fixture.stop(t)
}

func TestOrdinaryHandlerPanicBoundary(t *testing.T) {
	fixture := startIntegrationFixture(
		t,
		DefaultServerOptions(),
		service.DefaultSchedulerConfig(),
		func(module *integrationModule) {
			module.GET("/ordinary-panic", func(*gin.Context) { panic("ordinary boom") })
			module.GET("/ordinary-ok", func(ctx *gin.Context) { ctx.String(http.StatusOK, "ok") })
		},
	)
	response, err := fixture.client.Get(fixture.baseURL + "/ordinary-panic")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusInternalServerError {
		t.Fatalf("panic status = %d", response.StatusCode)
	}
	response, err = fixture.client.Get(fixture.baseURL + "/ordinary-ok")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK || fixture.module.Stats().PanicTotal == 0 {
		t.Fatalf("status=%d stats=%+v", response.StatusCode, fixture.module.Stats())
	}
	fixture.stop(t)
}

func TestSafeResponseBoundaryMapsInvalidResponses(t *testing.T) {
	options := DefaultServerOptions()
	options.MaxSafeResponseBodySize = 4
	options.SafeErrorMapper = func(error) Response {
		return Response{
			StatusCode: http.StatusUnprocessableEntity,
			Header:     http.Header{"Content-Type": {"application/json"}},
			Body:       []byte(`{"error":"invalid safe response"}`),
		}
	}
	fixture := startIntegrationFixture(t, options, service.DefaultSchedulerConfig(), func(module *integrationModule) {
		module.SafeGET("/large", func(ctx *SafeContext) {
			ctx.Data(http.StatusOK, "text/plain", []byte("12345"))
		})
		module.SafeGET("/hop-by-hop", func(ctx *SafeContext) {
			ctx.Header("Connection", "close")
			ctx.Status(http.StatusNoContent)
		})
	})

	for _, path := range []string{"/large", "/hop-by-hop"} {
		response, err := fixture.client.Get(fixture.baseURL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		body, readErr := io.ReadAll(response.Body)
		response.Body.Close()
		if readErr != nil || response.StatusCode != http.StatusInternalServerError ||
			string(body) != `{"error":"internal server error"}` {
			t.Fatalf("GET %s status=%d body=%q error=%v", path, response.StatusCode, body, readErr)
		}
	}
	fixture.stop(t)
}

func TestListenFailureIsReportedDuringStart(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()

	module := &integrationModule{
		address: occupied.Addr().String(),
		options: DefaultServerOptions(),
	}
	current, _ := newIntegrationNode(t, module, service.DefaultSchedulerConfig())
	ctx, cancel := context.WithTimeout(context.Background(), integrationTimeout)
	defer cancel()
	if err := current.Start(ctx); err == nil {
		t.Fatal("Node.Start() succeeded for an occupied address")
	}
	rollbackContext, rollbackCancel := context.WithTimeout(context.Background(), integrationTimeout)
	defer rollbackCancel()
	_ = current.Rollback(rollbackContext)
}

func TestStoppedModuleCannotRestart(t *testing.T) {
	fixture := startIntegrationFixture(
		t,
		DefaultServerOptions(),
		service.DefaultSchedulerConfig(),
		func(module *integrationModule) {
			module.GET("/health", func(ctx *gin.Context) { ctx.Status(http.StatusNoContent) })
		},
	)
	assertPanics(t, func() {
		fixture.module.GET("/late", func(ctx *gin.Context) { ctx.Status(http.StatusNoContent) })
	})
	fixture.stop(t)
	ctx, cancel := context.WithTimeout(context.Background(), integrationTimeout)
	defer cancel()
	if err := fixture.module.OnStart(ctx); err == nil {
		t.Fatal("stopped ginmodule restarted")
	}
}

func TestStopDeadlineForcesActiveConnectionClosed(t *testing.T) {
	started := make(chan struct{})
	finished := make(chan struct{})
	fixture := startIntegrationFixture(
		t,
		DefaultServerOptions(),
		service.DefaultSchedulerConfig(),
		func(module *integrationModule) {
			module.GET("/wait-for-close", func(ctx *gin.Context) {
				close(started)
				<-ctx.Request.Context().Done()
				close(finished)
			})
		},
	)
	requestDone := make(chan struct{})
	go func() {
		defer close(requestDone)
		response, err := fixture.client.Get(fixture.baseURL + "/wait-for-close")
		if err == nil {
			response.Body.Close()
		}
	}()
	select {
	case <-started:
	case <-time.After(integrationTimeout):
		t.Fatal("ordinary handler did not start")
	}

	stopContext, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if err := fixture.module.OnStop(stopContext); err == nil {
		t.Fatal("forced stop did not report the shutdown deadline")
	}
	for name, channel := range map[string]<-chan struct{}{
		"handler": finished,
		"request": requestDone,
	} {
		select {
		case <-channel:
		case <-time.After(integrationTimeout):
			t.Fatalf("%s remained blocked after forced close", name)
		}
	}
	if fixture.module.Addr() != nil {
		t.Fatalf("Addr() after forced stop = %v", fixture.module.Addr())
	}
}
