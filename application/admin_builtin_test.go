package application

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/duanhf2012/origin/v3/admin"
	publicprovider "github.com/duanhf2012/origin/v3/discovery/provider"
	"github.com/duanhf2012/origin/v3/errs"
	originlog "github.com/duanhf2012/origin/v3/log"
	"github.com/duanhf2012/origin/v3/node"
	"github.com/duanhf2012/origin/v3/service"
)

// startAdminRouteTestServer 把已经完成注册和路由冻结的 Application 发布为真实本地
// Admin Server，并保证测试退出时 Listener 已经停止。
func startAdminRouteTestServer(t *testing.T, app *Application) string {
	t.Helper()

	// 测试只补齐 StartAdminServer 依赖的运行快照；注册与冻结必须由各用例在此之前显式完成。
	app.mu.Lock()
	app.appName = "admin-route-test"
	app.startedAt = time.Now()
	app.resourcesReady = true
	app.state.Store(uint32(StateRunning))
	app.mu.Unlock()
	if err := app.StartAdminServer("127.0.0.1:0"); err != nil {
		t.Fatalf("StartAdminServer() error = %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := app.StopAdminServer(ctx); err != nil {
			t.Errorf("StopAdminServer() cleanup error = %v", err)
		}
	})
	address, ok := app.AdminAddress()
	if !ok {
		t.Fatal("AdminAddress() did not publish the test Listener")
	}
	return "http://" + address
}

// readAdminRouteResponse 完整读取并关闭真实 HTTP 响应，避免连接资源影响后续方法隔离请求。
func readAdminRouteResponse(t *testing.T, response *http.Response) string {
	t.Helper()
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read Admin response Body error = %v", err)
	}
	return string(body)
}

// TestAdminApplicationEndpointRoutes 防止 Application 自定义 GET/POST 未接入私有 Mux、
// Method 维度丢失，或 Principal、Query、Body 在进入 Handler 前被替换或遗漏。
func TestAdminApplicationEndpointRoutes(t *testing.T) {
	app := New()
	principal := admin.Principal{
		Subject: "operator",
		Roles:   []string{"ops"},
		Attributes: map[string]string{
			"tenant": "blue",
		},
	}
	if err := app.SetAdminGuard(adminGuardFunc(func(
		_ context.Context,
		_ *http.Request,
		operation admin.Operation,
	) (admin.Principal, error) {
		if operation.Method != http.MethodGet && operation.Method != http.MethodPost {
			t.Errorf("Guard operation Method = %q", operation.Method)
		}
		if operation.Method == http.MethodGet && operation.Endpoint != "build" ||
			operation.Method == http.MethodPost && operation.Endpoint != "reload" {
			t.Errorf("Guard operation = %+v", operation)
		}
		if operation.NodeID != "" || operation.ServiceName != "" {
			t.Errorf("Application operation target = %+v", operation)
		}
		return principal, nil
	})); err != nil {
		t.Fatalf("SetAdminGuard() error = %v", err)
	}
	buildRequests := make(chan admin.Request, 1)
	if err := app.RegisterAdminEndpoint(admin.Get("build", func(
		_ context.Context,
		request admin.Request,
	) (admin.Response, error) {
		buildRequests <- request
		return admin.JSON(http.StatusOK, map[string]string{"version": "test"})
	})); err != nil {
		t.Fatalf("Register build endpoint error = %v", err)
	}
	reloadRequests := make(chan admin.Request, 1)
	if err := app.RegisterAdminEndpoint(admin.Post("reload", func(
		_ context.Context,
		request admin.Request,
	) (admin.Response, error) {
		reloadRequests <- request
		return admin.Empty(http.StatusNoContent), nil
	})); err != nil {
		t.Fatalf("Register reload endpoint error = %v", err)
	}
	if err := app.freezeAdminRoutes(nil); err != nil {
		t.Fatalf("freezeAdminRoutes() error = %v", err)
	}
	baseURL := startAdminRouteTestServer(t, app)

	// GET 只命中同 Method 的 build 描述符，并把身份和多值 Query 原样复制给 Handler。
	response, err := http.Get(baseURL + "/admin/v1/application/endpoints/build?tag=one&tag=two")
	if err != nil {
		t.Fatalf("GET build error = %v", err)
	}
	if body := readAdminRouteResponse(t, response); response.StatusCode != http.StatusOK ||
		body != `{"version":"test"}` {
		t.Fatalf("GET build status=%d Body=%q", response.StatusCode, body)
	}
	buildRequest := <-buildRequests
	if got := buildRequest.Principal(); got.Subject != "operator" || len(got.Roles) != 1 ||
		got.Roles[0] != "ops" || got.Attributes["tenant"] != "blue" {
		t.Fatalf("build Principal = %+v", got)
	}
	if got := buildRequest.Query()["tag"]; len(got) != 2 || got[0] != "one" || got[1] != "two" {
		t.Fatalf("build Query tag = %v", got)
	}
	if body := buildRequest.Body(); len(body) != 0 {
		t.Fatalf("build Body = %q, want empty", body)
	}

	// POST 独立命中 reload 描述符；严格 JSON Body 和 Query 必须同时可由 Handler 读取。
	reload, err := http.NewRequest(
		http.MethodPost,
		baseURL+"/admin/v1/application/endpoints/reload?force=true",
		strings.NewReader(`{"reason":"test"}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	reload.Header.Set("Content-Type", "application/json")
	response, err = http.DefaultClient.Do(reload)
	if err != nil {
		t.Fatalf("POST reload error = %v", err)
	}
	if body := readAdminRouteResponse(t, response); response.StatusCode != http.StatusNoContent || body != "" {
		t.Fatalf("POST reload status=%d Body=%q", response.StatusCode, body)
	}
	reloadRequest := <-reloadRequests
	if got := reloadRequest.Principal().Subject; got != "operator" {
		t.Fatalf("reload Principal Subject = %q", got)
	}
	if got := reloadRequest.Query().Get("force"); got != "true" {
		t.Fatalf("reload Query force = %q", got)
	}
	if got := string(reloadRequest.Body()); got != `{"reason":"test"}` {
		t.Fatalf("reload Body = %q", got)
	}

	// 同名空间由 Method 和 EndpointName 共同定位；错误 Method 与未知名称都固定为 404。
	for _, request := range []struct {
		method string
		path   string
		body   string
	}{
		{method: http.MethodPost, path: "/admin/v1/application/endpoints/build", body: `{}`},
		{method: http.MethodGet, path: "/admin/v1/application/endpoints/reload"},
		{method: http.MethodGet, path: "/admin/v1/application/endpoints/missing"},
		{method: http.MethodPost, path: "/admin/v1/application/endpoints/missing", body: `{}`},
	} {
		probe, requestErr := http.NewRequest(request.method, baseURL+request.path, strings.NewReader(request.body))
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		if request.method == http.MethodPost {
			probe.Header.Set("Content-Type", "application/json")
		}
		probeResponse, requestErr := http.DefaultClient.Do(probe)
		if requestErr != nil {
			t.Fatalf("%s %s error = %v", request.method, request.path, requestErr)
		}
		_ = readAdminRouteResponse(t, probeResponse)
		if probeResponse.StatusCode != http.StatusNotFound {
			t.Fatalf("%s %s status = %d, want 404", request.method, request.path, probeResponse.StatusCode)
		}
	}
}

// TestAdminApplicationEndpointFrozenInstancesStayIsolated 防止两个 Application 的同名冻结
// Endpoint 在 Server 注册时共享闭包或误查另一个实例的 Route Table。
func TestAdminApplicationEndpointFrozenInstancesStayIsolated(t *testing.T) {
	newInstance := func(value string) *Application {
		app := New()
		if err := app.RegisterAdminEndpoint(admin.Get("identity", func(
			context.Context,
			admin.Request,
		) (admin.Response, error) {
			return admin.JSON(http.StatusOK, map[string]string{"instance": value})
		})); err != nil {
			t.Fatalf("Register identity endpoint error = %v", err)
		}
		if err := app.freezeAdminRoutes(nil); err != nil {
			t.Fatalf("freezeAdminRoutes() error = %v", err)
		}
		return app
	}
	firstURL := startAdminRouteTestServer(t, newInstance("first"))
	secondURL := startAdminRouteTestServer(t, newInstance("second"))

	for _, test := range []struct {
		url  string
		want string
	}{
		{url: firstURL, want: `{"instance":"first"}`},
		{url: secondURL, want: `{"instance":"second"}`},
	} {
		response, err := http.Get(test.url + "/admin/v1/application/endpoints/identity")
		if err != nil {
			t.Fatalf("GET identity error = %v", err)
		}
		if body := readAdminRouteResponse(t, response); response.StatusCode != http.StatusOK || body != test.want {
			t.Fatalf("GET identity status=%d Body=%q, want %q", response.StatusCode, body, test.want)
		}
	}
}

// adminBuiltinService 使用 Origin 默认生命周期实现，内置控制测试据此验证 HTTP Handler
// 直接复用 Application、Node、Service 的 Retire/Resume，而不是复制状态机。
type adminBuiltinService struct {
	service.Service
}

// postAdminControl 发送内置控制约定的单值 JSON 请求，并返回已关闭 Body 的完整结果。
func postAdminControl(t *testing.T, targetURL string) (int, string) {
	t.Helper()
	request, err := http.NewRequest(http.MethodPost, targetURL, strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("POST %s error = %v", targetURL, err)
	}
	return response.StatusCode, readAdminRouteResponse(t, response)
}

// TestAdminBuiltinControlsReuseLifecycle 固定六条控制路由的成功、重复幂等和真实状态变化；
// 每次控制成功都必须是 204 空 Body。
func TestAdminBuiltinControlsReuseLifecycle(t *testing.T) {
	app := New()
	target := &adminBuiltinService{}
	current := newAdminRegistryNode(t, app, "game-1", "player", target)
	if err := app.freezeAdminRoutes([]*node.Node{current}); err != nil {
		t.Fatalf("freezeAdminRoutes() error = %v", err)
	}
	if err := current.Start(t.Context()); err != nil {
		t.Fatalf("Node.Start() error = %v", err)
	}
	app.mu.Lock()
	app.nodes = []*node.Node{current}
	app.mu.Unlock()
	baseURL := startAdminRouteTestServer(t, app)

	controls := []struct {
		name      string
		path      string
		wantState service.State
	}{
		{name: "application retire", path: "/admin/v1/application/retire", wantState: service.StateRetired},
		{name: "application resume", path: "/admin/v1/application/resume", wantState: service.StateRunning},
		{name: "node retire", path: "/admin/v1/nodes/game-1/retire", wantState: service.StateRetired},
		{name: "node resume", path: "/admin/v1/nodes/game-1/resume", wantState: service.StateRunning},
		{
			name:      "service retire",
			path:      "/admin/v1/nodes/game-1/services/player/retire",
			wantState: service.StateRetired,
		},
		{
			name:      "service resume",
			path:      "/admin/v1/nodes/game-1/services/player/resume",
			wantState: service.StateRunning,
		},
	}
	for _, control := range controls {
		t.Run(control.name, func(t *testing.T) {
			// 同一动作执行两次；现有生命周期负责判断无变化，HTTP 层只透传两次成功。
			for attempt := 1; attempt <= 2; attempt++ {
				status, body := postAdminControl(t, baseURL+control.path)
				if status != http.StatusNoContent || body != "" {
					t.Fatalf("attempt %d status=%d Body=%q, want 204/empty", attempt, status, body)
				}
				if got := target.State(); got != control.wantState {
					t.Fatalf("attempt %d Service state=%v, want %v", attempt, got, control.wantState)
				}
			}
		})
	}
}

// TestAdminBuiltinControlsMethodAndUnknownTargets 防止固定控制被 GET 调用，或未知动态身份
// 触发扫描、近似匹配和生命周期副作用。
func TestAdminBuiltinControlsMethodAndUnknownTargets(t *testing.T) {
	app := New()
	target := &adminBuiltinService{}
	current := newAdminRegistryNode(t, app, "game-1", "player", target)
	if err := app.freezeAdminRoutes([]*node.Node{current}); err != nil {
		t.Fatal(err)
	}
	if err := current.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	app.mu.Lock()
	app.nodes = []*node.Node{current}
	app.mu.Unlock()
	baseURL := startAdminRouteTestServer(t, app)

	fixedPaths := []string{
		"/admin/v1/application/retire",
		"/admin/v1/application/resume",
		"/admin/v1/nodes/game-1/retire",
		"/admin/v1/nodes/game-1/resume",
		"/admin/v1/nodes/game-1/services/player/retire",
		"/admin/v1/nodes/game-1/services/player/resume",
	}
	for _, path := range fixedPaths {
		response, err := http.Get(baseURL + path)
		if err != nil {
			t.Fatalf("GET %s error = %v", path, err)
		}
		_ = readAdminRouteResponse(t, response)
		if response.StatusCode != http.StatusMethodNotAllowed ||
			response.Header.Get("Allow") != http.MethodPost {
			t.Fatalf("GET %s status=%d Allow=%q, want 405/POST", path, response.StatusCode, response.Header.Get("Allow"))
		}
	}

	for _, path := range []string{
		"/admin/v1/nodes/missing/retire",
		"/admin/v1/nodes/game-1/services/missing/retire",
		"/admin/v1/nodes/game-1/services/player/unknown",
		"/admin/v1/nodes/game-1/services/player/retire/extra",
	} {
		status, body := postAdminControl(t, baseURL+path)
		if status != http.StatusNotFound || body != http.StatusText(http.StatusNotFound)+"\n" {
			t.Fatalf("POST %s status=%d Body=%q, want stable 404", path, status, body)
		}
	}
	if target.State() != service.StateRunning {
		t.Fatalf("unknown control changed Service state to %v", target.State())
	}
}

// TestAdminBuiltinErrorMapping 用现有公开错误族固定安全 HTTP 分类；错误链文本不得参与状态选择。
func TestAdminBuiltinErrorMapping(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{name: "invalid", err: errs.ErrInvalidArgument, want: http.StatusBadRequest},
		{name: "unauthenticated", err: admin.ErrUnauthenticated, want: http.StatusUnauthorized},
		{name: "forbidden", err: admin.ErrForbidden, want: http.StatusForbidden},
		{name: "not found", err: errs.ErrConfigNotFound, want: http.StatusNotFound},
		{name: "state conflict", err: errs.ErrAdminStateConflict, want: http.StatusConflict},
		{name: "retired conflict", err: errs.ErrServiceRetired, want: http.StatusConflict},
		{name: "queue full", err: errs.ErrServiceQueueFull, want: http.StatusTooManyRequests},
		{name: "not ready", err: errs.ErrServiceNotReady, want: http.StatusServiceUnavailable},
		{name: "stopping", err: errs.ErrServiceStopping, want: http.StatusServiceUnavailable},
		{name: "stopped", err: errs.ErrServiceStopped, want: http.StatusServiceUnavailable},
		{name: "failed", err: errs.ErrServiceFailed, want: http.StatusServiceUnavailable},
		{name: "caller canceled", err: context.Canceled, want: http.StatusRequestTimeout},
		{name: "deadline", err: context.DeadlineExceeded, want: http.StatusGatewayTimeout},
		{name: "internal", err: errs.ErrInternal, want: http.StatusInternalServerError},
		{name: "plain internal", err: errors.New("private error"), want: http.StatusInternalServerError},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := adminInvokeErrorStatus(test.err); got != test.want {
				t.Fatalf("adminInvokeErrorStatus() = %d, want %d", got, test.want)
			}
		})
	}
}

// adminBuiltinFailedService 只稳定注入公共 ErrServiceFailed；真实 Node 和 Scheduler 仍负责
// 装配与启停。跨包测试无法安全制造私有 Scheduler 不变量，不能用反射或数据竞争破坏状态。
type adminBuiltinFailedService struct {
	service.Service
}

// Retire 模拟已经被隔离的 Service 对新控制请求返回稳定 Failed 错误。
func (*adminBuiltinFailedService) Retire(context.Context) error { return errs.ErrServiceFailed }

// adminBuiltinPublicationProvider 在真实 Node 发布启动快照后，按测试指令稳定失败或阻塞下一次
// 发布；它只替换外部发现后端，不替换 Node/Service 生命周期。
type adminBuiltinPublicationProvider struct {
	context publicprovider.Context
	mu      sync.Mutex
	fail    bool
	block   bool
	entered chan struct{}
	once    sync.Once
}

// Start 建立 Provider 就绪契约和空远端快照。
func (provider *adminBuiltinPublicationProvider) Start(context.Context) error {
	if err := provider.context.Host.SetTTL(3 * time.Second); err != nil {
		return err
	}
	if err := provider.context.Host.ReplaceSnapshot(publicprovider.Snapshot{}); err != nil {
		return err
	}
	provider.context.Host.Report(publicprovider.Report{State: publicprovider.StateReady})
	return nil
}

// Publish 在启动成功后由测试切换为失败或等待 Context，稳定覆盖控制提交后的发布结果。
func (provider *adminBuiltinPublicationProvider) Publish(
	ctx context.Context,
	_ publicprovider.Node,
) error {
	provider.mu.Lock()
	fail := provider.fail
	block := provider.block
	provider.mu.Unlock()
	if fail {
		return errs.ErrDiscoveryUnavailable
	}
	if block {
		provider.once.Do(func() { close(provider.entered) })
		<-ctx.Done()
		return ctx.Err()
	}
	return nil
}

// Withdraw 不需要额外远端资源。
func (*adminBuiltinPublicationProvider) Withdraw(context.Context) error { return nil }

// Close 发布稳定停止状态，让真实 Provider Runtime 完整回收。
func (provider *adminBuiltinPublicationProvider) Close(context.Context) error {
	provider.context.Host.Report(publicprovider.Report{State: publicprovider.StateStopped})
	return nil
}

// setFailure 让下一次发布返回现有 DiscoveryUnavailable 错误。
func (provider *adminBuiltinPublicationProvider) setFailure() {
	provider.mu.Lock()
	provider.fail = true
	provider.mu.Unlock()
}

// setBlocking 让下一次发布停在 Context 屏障，测试随后精确触发 Deadline。
func (provider *adminBuiltinPublicationProvider) setBlocking() {
	provider.mu.Lock()
	provider.block = true
	provider.mu.Unlock()
}

// newAdminBuiltinProviderNode 构造启用真实发现发布协调器的 Node，并由测试完整回收。
func newAdminBuiltinProviderNode(
	t *testing.T,
	app *Application,
	nodeID string,
	serviceName string,
	target service.IService,
) (*node.Node, *adminBuiltinPublicationProvider) {
	t.Helper()
	var provider *adminBuiltinPublicationProvider
	current, err := node.New(
		node.Config{
			ID:        nodeID,
			Services:  []string{serviceName},
			Scheduler: service.DefaultSchedulerConfig(),
		},
		[]node.ServiceBinding{{
			Name:     serviceName,
			Template: "adminBuiltinService",
			Service:  target,
		}},
		originlog.NewNop(),
		node.Options{
			Application:      app,
			MaxTimersPerNode: DefaultMaxTimersPerNode,
			TimerLocation:    time.UTC,
			DiscoveryKind:    "admin-builtin-test",
			DiscoveryFactory: func(context publicprovider.Context) (publicprovider.Provider, error) {
				provider = &adminBuiltinPublicationProvider{
					context: context,
					entered: make(chan struct{}),
				}
				return provider, nil
			},
		},
	)
	if err != nil {
		t.Fatalf("node.New() error = %v", err)
	}
	t.Cleanup(func() {
		if rollbackErr := current.Rollback(context.Background()); rollbackErr != nil &&
			!errors.Is(rollbackErr, errs.ErrDiscoveryUnavailable) {
			t.Errorf("Node.Rollback() cleanup error = %v", rollbackErr)
		}
	})
	return current, provider
}

// adminBuiltinDeadlineContext 由 Channel 屏障手动进入 DeadlineExceeded，避免测试依赖 Sleep。
type adminBuiltinDeadlineContext struct {
	done    chan struct{}
	expired atomic.Bool
}

// Deadline 交给 Endpoint 自己设置 15 秒上限；测试通过 Done/Err 精确触发更早的调用方终态。
func (*adminBuiltinDeadlineContext) Deadline() (time.Time, bool) { return time.Time{}, false }

// Done 返回测试拥有的单次关闭 Channel。
func (ctx *adminBuiltinDeadlineContext) Done() <-chan struct{} { return ctx.done }

// Err 在 expire 发布后稳定返回 DeadlineExceeded。
func (ctx *adminBuiltinDeadlineContext) Err() error {
	if ctx.expired.Load() {
		return context.DeadlineExceeded
	}
	return nil
}

// Value 不向请求注入额外值。
func (*adminBuiltinDeadlineContext) Value(any) any { return nil }

// expire 先发布错误再关闭 Channel，满足 Context 并发读取顺序。
func (ctx *adminBuiltinDeadlineContext) expire() {
	ctx.expired.Store(true)
	close(ctx.done)
}

// TestAdminBuiltinControlStateFailures 固定 Application 非 Running 与 Service Failed 的 503，
// 且响应只包含稳定 StatusText。
func TestAdminBuiltinControlStateFailures(t *testing.T) {
	t.Run("application not running", func(t *testing.T) {
		app := New()
		if err := app.freezeAdminRoutes(nil); err != nil {
			t.Fatal(err)
		}
		baseURL := startAdminRouteTestServer(t, app)
		app.state.Store(uint32(StateCreated))
		status, body := postAdminControl(t, baseURL+"/admin/v1/application/retire")
		if status != http.StatusServiceUnavailable ||
			body != http.StatusText(http.StatusServiceUnavailable)+"\n" {
			t.Fatalf("non-running Application status=%d Body=%q", status, body)
		}
		app.state.Store(uint32(StateRunning))
	})

	t.Run("service failed", func(t *testing.T) {
		app := New()
		target := &adminBuiltinFailedService{}
		current := newAdminRegistryNode(t, app, "game-1", "failed", target)
		if err := app.freezeAdminRoutes([]*node.Node{current}); err != nil {
			t.Fatal(err)
		}
		if err := current.Start(t.Context()); err != nil {
			t.Fatal(err)
		}
		app.mu.Lock()
		app.nodes = []*node.Node{current}
		app.mu.Unlock()
		baseURL := startAdminRouteTestServer(t, app)
		status, body := postAdminControl(
			t,
			baseURL+"/admin/v1/nodes/game-1/services/failed/retire",
		)
		if status != http.StatusServiceUnavailable ||
			body != http.StatusText(http.StatusServiceUnavailable)+"\n" {
			t.Fatalf("Failed Service status=%d Body=%q", status, body)
		}
	})
}

// TestAdminBuiltinPublicationFailureKeepsCommittedState 防止 HTTP 层在发现发布失败时伪造
// 本地回滚，或把 Provider 错误链写入响应。
func TestAdminBuiltinPublicationFailureKeepsCommittedState(t *testing.T) {
	app := New()
	target := &adminBuiltinService{}
	current, provider := newAdminBuiltinProviderNode(t, app, "game-1", "player", target)
	if err := app.freezeAdminRoutes([]*node.Node{current}); err != nil {
		t.Fatal(err)
	}
	if err := current.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	app.mu.Lock()
	app.nodes = []*node.Node{current}
	app.mu.Unlock()
	baseURL := startAdminRouteTestServer(t, app)
	provider.setFailure()

	status, body := postAdminControl(
		t,
		baseURL+"/admin/v1/nodes/game-1/services/player/retire",
	)
	if status != http.StatusInternalServerError ||
		body != http.StatusText(http.StatusInternalServerError)+"\n" {
		t.Fatalf("publication failure status=%d Body=%q", status, body)
	}
	if target.State() != service.StateRetired {
		t.Fatalf("publication failure rolled Service back to %v", target.State())
	}
}

// TestAdminBuiltinControlDeadlineKeepsCommittedState 用 Provider 屏障保证 Deadline 发生在本地
// Retired 已提交之后；HTTP 返回 504，但不能恢复为 Running。
func TestAdminBuiltinControlDeadlineKeepsCommittedState(t *testing.T) {
	app := New()
	target := &adminBuiltinService{}
	current, provider := newAdminBuiltinProviderNode(t, app, "game-1", "player", target)
	if err := app.freezeAdminRoutes([]*node.Node{current}); err != nil {
		t.Fatal(err)
	}
	if err := current.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	app.mu.Lock()
	app.nodes = []*node.Node{current}
	app.mu.Unlock()
	_ = startAdminRouteTestServer(t, app)
	provider.setBlocking()

	// 直接使用真实 Server Handler，才能在调用方 Context 结束后继续观察安全 HTTP 终态。
	app.adminHTTP.mu.Lock()
	handler := app.adminHTTP.server.Handler
	app.adminHTTP.mu.Unlock()
	callerCtx := &adminBuiltinDeadlineContext{done: make(chan struct{})}
	request := httptest.NewRequest(
		http.MethodPost,
		"/admin/v1/nodes/game-1/services/player/retire",
		strings.NewReader(`{}`),
	).WithContext(callerCtx)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		handler.ServeHTTP(response, request)
		close(done)
	}()
	select {
	case <-provider.entered:
	case <-done:
		t.Fatalf("control returned before publication barrier: status=%d", response.Code)
	case <-time.After(time.Second):
		t.Fatal("control did not enter publication barrier")
	}
	callerCtx.expire()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("deadline control did not return")
	}
	if response.Code != http.StatusGatewayTimeout ||
		response.Body.String() != http.StatusText(http.StatusGatewayTimeout)+"\n" {
		t.Fatalf("deadline status=%d Body=%q", response.Code, response.Body)
	}
	if target.State() != service.StateRetired {
		t.Fatalf("deadline rolled Service back to %v", target.State())
	}
}

// TestAdminBuiltinRoutesUnavailableStaySafe 固定未冻结和冻结失败都不发布半成品自定义路由；动态
// 名称不进入响应或审计，Provider/Handler 也不会在请求期被调用。
func TestAdminBuiltinRoutesUnavailableStaySafe(t *testing.T) {
	for _, test := range []struct {
		name       string
		freezeFail bool
	}{
		{name: "not frozen"},
		{name: "freeze failed", freezeFail: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			audit := &adminAuditHandler{}
			logRuntime, err := originlog.NewRuntime(originlog.Config{Mode: originlog.SyncMode}, audit)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = logRuntime.Close(context.Background()) })
			app := New()
			app.logger = logRuntime.Logger()
			var invoked atomic.Bool
			if err := app.RegisterAdminEndpoint(admin.Get("hidden", func(
				context.Context,
				admin.Request,
			) (admin.Response, error) {
				invoked.Store(true)
				return admin.Empty(http.StatusOK), nil
			})); err != nil {
				t.Fatal(err)
			}
			if test.freezeFail {
				if err := app.freezeAdminRoutes([]*node.Node{nil}); !errors.Is(err, errs.ErrInvalidConfig) {
					t.Fatalf("freezeAdminRoutes() error = %v", err)
				}
			}
			baseURL := startAdminRouteTestServer(t, app)
			for _, path := range []string{
				"/admin/v1/application/endpoints/hidden",
				"/admin/v1/nodes/hidden-node/services/hidden-service/endpoints/hidden",
			} {
				response, requestErr := http.Get(baseURL + path)
				if requestErr != nil {
					t.Fatal(requestErr)
				}
				body := readAdminRouteResponse(t, response)
				if response.StatusCode != http.StatusNotFound ||
					body != http.StatusText(http.StatusNotFound)+"\n" {
					t.Fatalf("unavailable route %s status=%d Body=%q", path, response.StatusCode, body)
				}
			}
			if invoked.Load() {
				t.Fatal("unavailable route invoked Application Handler")
			}
			records := audit.snapshot()
			if len(records) != 2 {
				t.Fatalf("unavailable route audit records = %d, want 2", len(records))
			}
			for _, record := range records {
				if record.endpoint != "unknown" || strings.Contains(record.text, "hidden") {
					t.Fatalf("unavailable route audit = %+v", records)
				}
			}
			if strings.Contains(records[0].text+records[1].text, "hidden-node") ||
				strings.Contains(records[0].text+records[1].text, "hidden-service") {
				t.Fatalf("unavailable route audit = %+v", records)
			}
		})
	}
}
