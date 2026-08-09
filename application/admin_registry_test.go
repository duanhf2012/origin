package application

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/duanhf2012/origin/v3/admin"
	"github.com/duanhf2012/origin/v3/errs"
	"github.com/duanhf2012/origin/v3/node"
	"github.com/duanhf2012/origin/v3/service"
)

// adminRegistryAllowGuard 为冷注册测试提供最小可用 Guard。
type adminRegistryAllowGuard struct{}

func (adminRegistryAllowGuard) Authorize(
	context.Context,
	*http.Request,
	admin.Operation,
) (admin.Principal, error) {
	return admin.Principal{Subject: "test"}, nil
}

// adminRegistryTypedNilGuard 用指针方法集构造“接口非 nil、底层指针 nil”的 Guard。
type adminRegistryTypedNilGuard struct{}

func (*adminRegistryTypedNilGuard) Authorize(
	context.Context,
	*http.Request,
	admin.Operation,
) (admin.Principal, error) {
	return admin.Principal{Subject: "typed-nil"}, nil
}

// TestRegisterAdminEndpointAndGuardBeforeCommandOnly 防止管理路由在命令执行后
// 继续变化，并确保同一方法与名称不能重复、Guard 不能替换。
func TestRegisterAdminEndpointAndGuardBeforeCommandOnly(t *testing.T) {
	app := New()
	endpoint := admin.Get("build", func(
		context.Context,
		admin.Request,
	) (admin.Response, error) {
		return admin.JSON(http.StatusOK, map[string]string{"version": "test"})
	})
	if err := app.RegisterAdminEndpoint(endpoint); err != nil {
		t.Fatalf("RegisterAdminEndpoint() error = %v", err)
	}
	if err := app.RegisterAdminEndpoint(endpoint); !errors.Is(err, errs.ErrInvalidArgument) {
		t.Fatalf("duplicate RegisterAdminEndpoint() error = %v", err)
	}
	if err := app.SetAdminGuard(adminRegistryAllowGuard{}); err != nil {
		t.Fatalf("SetAdminGuard() error = %v", err)
	}
	if err := app.SetAdminGuard(adminRegistryAllowGuard{}); !errors.Is(err, errs.ErrInvalidArgument) {
		t.Fatalf("duplicate SetAdminGuard() error = %v", err)
	}

	app.commandRun = true
	if err := app.RegisterAdminEndpoint(admin.Post("reload", func(
		context.Context,
		admin.Request,
	) (admin.Response, error) {
		return admin.Response{}, nil
	})); !errors.Is(err, errs.ErrInvalidArgument) {
		t.Fatalf("late RegisterAdminEndpoint() error = %v", err)
	}

	lateGuardApp := New()
	lateGuardApp.commandRun = true
	if err := lateGuardApp.SetAdminGuard(adminRegistryAllowGuard{}); !errors.Is(err, errs.ErrInvalidArgument) {
		t.Fatalf("late SetAdminGuard() error = %v", err)
	}

	stateChangedApp := New()
	stateChangedApp.state.Store(uint32(StateStarting))
	if err := stateChangedApp.RegisterAdminEndpoint(endpoint); !errors.Is(err, errs.ErrInvalidArgument) {
		t.Fatalf("non-Created RegisterAdminEndpoint() error = %v", err)
	}
	if err := stateChangedApp.SetAdminGuard(adminRegistryAllowGuard{}); !errors.Is(err, errs.ErrInvalidArgument) {
		t.Fatalf("non-Created SetAdminGuard() error = %v", err)
	}
}

// TestRegisterAdminEndpointRejectsInvalidEndpoint 防止无效描述符进入冻结表后才暴露。
func TestRegisterAdminEndpointRejectsInvalidEndpoint(t *testing.T) {
	if err := New().RegisterAdminEndpoint(admin.Get("invalid_name", nil)); !errors.Is(err, errs.ErrInvalidArgument) {
		t.Fatalf("RegisterAdminEndpoint(invalid) error = %v", err)
	}
}

// TestSetAdminGuardRejectsTypedNilWithoutOccupyingSlot 防止 typed-nil Guard 永久占据
// Application 唯一 Guard 槽，并把后续 HTTP 授权留给一个可能 panic 的值。
func TestSetAdminGuardRejectsTypedNilWithoutOccupyingSlot(t *testing.T) {
	app := New()
	var typedNil *adminRegistryTypedNilGuard
	if err := app.SetAdminGuard(typedNil); !errors.Is(err, errs.ErrInvalidArgument) {
		t.Errorf("SetAdminGuard(typed nil) error = %v", err)
	}
	if err := app.SetAdminGuard(adminRegistryAllowGuard{}); err != nil {
		t.Errorf("SetAdminGuard(valid after typed nil) error = %v", err)
	}
}

// TestFreezeAdminRoutesCopiesApplicationEndpoints 防止 Application 端点丢失 Method 维度，
// 并确保发布的 Map 不再依赖冷注册 Slice。
func TestFreezeAdminRoutesCopiesApplicationEndpoints(t *testing.T) {
	app := New()
	getEndpoint := admin.Get("state", func(
		context.Context,
		admin.Request,
	) (admin.Response, error) {
		return admin.Empty(http.StatusOK), nil
	})
	postEndpoint := admin.Post("state", func(
		context.Context,
		admin.Request,
	) (admin.Response, error) {
		return admin.Empty(http.StatusNoContent), nil
	})
	for _, endpoint := range []admin.Endpoint{getEndpoint, postEndpoint} {
		if err := app.RegisterAdminEndpoint(endpoint); err != nil {
			t.Fatalf("RegisterAdminEndpoint(%s) error = %v", endpoint.Method(), err)
		}
	}
	if err := app.freezeAdminRoutes(nil); err != nil {
		t.Fatalf("freezeAdminRoutes() error = %v", err)
	}
	if len(app.adminRoutes.application) != 2 {
		t.Fatalf("application routes = %d, want 2", len(app.adminRoutes.application))
	}
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		key := adminEndpointKey{method: method, endpoint: "state"}
		if _, exists := app.adminRoutes.application[key]; !exists {
			t.Fatalf("application route %+v missing", key)
		}
	}

	app.adminEndpoints[0] = admin.Get("changed", func(
		context.Context,
		admin.Request,
	) (admin.Response, error) {
		return admin.Empty(http.StatusOK), nil
	})
	key := adminEndpointKey{method: http.MethodGet, endpoint: "state"}
	if got := app.adminRoutes.application[key].Name(); got != "state" {
		t.Fatalf("frozen Application endpoint = %q, want state", got)
	}
}

// adminRegistryProviderService 记录真实实例的 Provider 调用次数与调用时状态。
type adminRegistryProviderService struct {
	service.Service
	providerCalls     int
	stateAtProvider   service.State
	providerAfterInit bool
	onInitEntered     bool
	handlerState      service.State
	provided          []admin.Endpoint
	panicValue        any
	providerEntered   chan struct{}
	providerRelease   <-chan struct{}
}

// AdminEndpoints 返回当前实例预先配置的端点 Slice，便于验证冻结副本所有权。
func (target *adminRegistryProviderService) AdminEndpoints() []admin.Endpoint {
	target.providerCalls++
	target.stateAtProvider = target.State()
	target.providerAfterInit = target.onInitEntered
	if target.providerEntered != nil {
		target.providerEntered <- struct{}{}
		<-target.providerRelease
	}
	if target.panicValue != nil {
		panic(target.panicValue)
	}
	return target.provided
}

// OnInit 记录真实生命周期进入点，与 Provider 收集时机形成独立证据。
func (target *adminRegistryProviderService) OnInit() error {
	target.onInitEntered = true
	return nil
}

// handleLifecycle 由真实 ServiceScheduler 执行，记录 Handler 取得执行权时的目标状态。
func (target *adminRegistryProviderService) handleLifecycle(
	context.Context,
	admin.Request,
) (admin.Response, error) {
	target.handlerState = target.State()
	return admin.JSON(http.StatusOK, map[string]string{
		"state": target.handlerState.String(),
	})
}

// TestFreezeAdminRoutesBindsActualServiceInstances 防止冻结表使用模板名、遗漏 Method
// 维度、重新收集 Provider，或绑定到其他 Node 的同类实例。
func TestFreezeAdminRoutesBindsActualServiceInstances(t *testing.T) {
	app := New()
	app.Setup(&adminRegistryProviderService{})
	nodes, err := app.buildNodes([]node.Config{
		{
			ID:        "node-a",
			Services:  []string{"player-a:adminRegistryProviderService"},
			Scheduler: service.DefaultSchedulerConfig(),
		},
		{
			ID:        "node-b",
			Services:  []string{"player-b:adminRegistryProviderService"},
			Scheduler: service.DefaultSchedulerConfig(),
		},
	}, nil)
	if err != nil {
		t.Fatalf("buildNodes() error = %v", err)
	}
	t.Cleanup(func() {
		for index := len(nodes) - 1; index >= 0; index-- {
			if rollbackErr := nodes[index].Rollback(context.Background()); rollbackErr != nil {
				t.Errorf("Node.Rollback() error = %v", rollbackErr)
			}
		}
	})

	// 两个反射创建的真实实例各自返回同名 GET/POST，且保留可改写的原 Slice。
	targets := make([]*adminRegistryProviderService, len(nodes))
	for index, current := range nodes {
		instances := current.Services()
		if len(instances) != 1 {
			t.Fatalf("Node %q Services() count = %d", current.ID(), len(instances))
		}
		targets[index] = instances[0].(*adminRegistryProviderService)
		targets[index].provided = []admin.Endpoint{
			admin.Get("state", func(context.Context, admin.Request) (admin.Response, error) {
				return admin.Empty(http.StatusOK), nil
			}),
			admin.Post("state", func(context.Context, admin.Request) (admin.Response, error) {
				return admin.Empty(http.StatusNoContent), nil
			}),
		}
	}

	if err := app.freezeAdminRoutes(nodes); err != nil {
		t.Fatalf("freezeAdminRoutes() error = %v", err)
	}
	if err := app.freezeAdminRoutes(nodes); err != nil {
		t.Fatalf("second freezeAdminRoutes() error = %v", err)
	}
	if len(app.adminRoutes.services) != 4 {
		t.Fatalf("service routes = %d, want 4", len(app.adminRoutes.services))
	}

	for index, current := range nodes {
		target := targets[index]
		if target.providerCalls != 1 {
			t.Fatalf("Node %q Provider calls = %d, want 1", current.ID(), target.providerCalls)
		}
		if target.stateAtProvider != service.StateCreated {
			t.Fatalf("Node %q Provider state = %v, want Created", current.ID(), target.stateAtProvider)
		}
		for _, method := range []string{http.MethodGet, http.MethodPost} {
			key := serviceAdminRouteKey{
				nodeID:      current.ID(),
				serviceName: target.Name(),
				method:      method,
				endpoint:    "state",
			}
			bound, exists := app.adminRoutes.services[key]
			if !exists {
				t.Fatalf("route %+v missing", key)
			}
			if bound.target != target {
				t.Fatalf("route %+v target = %p, want %p", key, bound.target, target)
			}
		}
	}

	// 冻结后改写 Provider 原 Slice 的元素，不得改变已发布路由。
	targets[0].provided[0] = admin.Get("changed", func(
		context.Context,
		admin.Request,
	) (admin.Response, error) {
		return admin.Empty(http.StatusOK), nil
	})
	originalKey := serviceAdminRouteKey{
		nodeID:      "node-a",
		serviceName: "player-a",
		method:      http.MethodGet,
		endpoint:    "state",
	}
	if got := app.adminRoutes.services[originalKey].endpoint.Name(); got != "state" {
		t.Fatalf("frozen endpoint name = %q, want state", got)
	}
}

// TestFreezeAdminRoutesRejectsProviderConfiguration 防止业务 Provider 的非法描述符、
// 重复键或 panic 发布部分路由，并统一保留配置错误语义。
func TestFreezeAdminRoutesRejectsProviderConfiguration(t *testing.T) {
	tests := []struct {
		name       string
		endpoints  []admin.Endpoint
		panicValue any
	}{
		{
			name: "invalid endpoint",
			endpoints: []admin.Endpoint{
				admin.Get("invalid_name", nil),
			},
		},
		{
			name: "duplicate key",
			endpoints: []admin.Endpoint{
				admin.Get("state", func(context.Context, admin.Request) (admin.Response, error) {
					return admin.Empty(http.StatusOK), nil
				}),
				admin.Get("state", func(context.Context, admin.Request) (admin.Response, error) {
					return admin.Empty(http.StatusOK), nil
				}),
			},
		},
		{name: "provider panic", panicValue: "provider failed"},
	}
	for _, current := range tests {
		t.Run(current.name, func(t *testing.T) {
			target := &adminRegistryProviderService{
				provided:   current.endpoints,
				panicValue: current.panicValue,
			}
			app := New()
			nodeInstance := newAdminRegistryNode(t, app, "node-error", "actual-service", target)
			if err := app.freezeAdminRoutes([]*node.Node{nodeInstance}); !errors.Is(err, errs.ErrInvalidConfig) {
				t.Fatalf("freezeAdminRoutes() error = %v", err)
			}
			if err := app.freezeAdminRoutes([]*node.Node{nodeInstance}); !errors.Is(err, errs.ErrInvalidConfig) {
				t.Fatalf("second freezeAdminRoutes() error = %v", err)
			}
			if app.adminRoutes != nil {
				t.Fatalf("adminRoutes published after %s", current.name)
			}
			if target.providerCalls != 1 {
				t.Fatalf("Provider calls = %d, want 1", target.providerCalls)
			}
		})
	}
}

// TestFreezeAdminRoutesCollectsBeforeOnInitAndInvokesWhenRunning 防止 Provider 依赖 OnInit
// 或 Handler 绕过 Service 调度器在非 Running 状态执行。
func TestFreezeAdminRoutesCollectsBeforeOnInitAndInvokesWhenRunning(t *testing.T) {
	app := New()
	target := &adminRegistryProviderService{}
	nodeInstance := newAdminRegistryNode(t, app, "node-lifecycle", "actual-service", target)
	target.provided = []admin.Endpoint{admin.Get("lifecycle", target.handleLifecycle)}

	if err := app.freezeAdminRoutes([]*node.Node{nodeInstance}); err != nil {
		t.Fatalf("freezeAdminRoutes() error = %v", err)
	}
	if target.stateAtProvider != service.StateCreated || target.providerAfterInit {
		t.Fatalf(
			"Provider observed state=%v after_init=%t, want Created before OnInit",
			target.stateAtProvider,
			target.providerAfterInit,
		)
	}
	if err := nodeInstance.Start(context.Background()); err != nil {
		t.Fatalf("Node.Start() error = %v", err)
	}

	key := serviceAdminRouteKey{
		nodeID:      "node-lifecycle",
		serviceName: "actual-service",
		method:      http.MethodGet,
		endpoint:    "lifecycle",
	}
	bound := app.adminRoutes.services[key]
	response, err := admin.InvokeService(
		context.Background(),
		bound.target,
		bound.endpoint,
		admin.Request{},
	)
	if err != nil {
		t.Fatalf("admin.InvokeService() error = %v", err)
	}
	if target.handlerState != service.StateRunning {
		t.Fatalf("Handler state = %v, want Running", target.handlerState)
	}
	if got := string(response.Body()); got != `{"state":"running"}` {
		t.Fatalf("Handler response = %s", got)
	}
	if err := nodeInstance.Stop(context.Background()); err != nil {
		t.Fatalf("Node.Stop() error = %v", err)
	}
}

// TestFreezeAdminRoutesConcurrentIdempotence 防止并发启动路径二次收集 Provider
// 或竞争发布不同的冻结表。
func TestFreezeAdminRoutesConcurrentIdempotence(t *testing.T) {
	app := New()
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	target := &adminRegistryProviderService{
		provided: []admin.Endpoint{admin.Get("state", func(
			context.Context,
			admin.Request,
		) (admin.Response, error) {
			return admin.Empty(http.StatusOK), nil
		})},
		providerEntered: entered,
		providerRelease: release,
	}
	nodeInstance := newAdminRegistryNode(t, app, "node-concurrent", "actual-service", target)

	const callers = 8
	start := make(chan struct{})
	errorsChannel := make(chan error, callers)
	for index := 0; index < callers; index++ {
		go func() {
			<-start
			errorsChannel <- app.freezeAdminRoutes([]*node.Node{nodeInstance})
		}()
	}
	close(start)
	<-entered
	close(release)
	for index := 0; index < callers; index++ {
		if err := <-errorsChannel; err != nil {
			t.Fatalf("freezeAdminRoutes() error = %v", err)
		}
	}
	if target.providerCalls != 1 {
		t.Fatalf("Provider calls = %d, want 1", target.providerCalls)
	}
}

// newAdminRegistryNode 创建并注册清理真实 Node，让冻结测试经过生产 Runtime 绑定。
func newAdminRegistryNode(
	t *testing.T,
	app *Application,
	nodeID string,
	serviceName string,
	target service.IService,
) *node.Node {
	t.Helper()
	current, err := node.New(
		node.Config{
			ID:        nodeID,
			Services:  []string{serviceName},
			Scheduler: service.DefaultSchedulerConfig(),
		},
		[]node.ServiceBinding{{
			Name:     serviceName,
			Template: "adminRegistryProviderService",
			Service:  target,
		}},
		app.logger,
		node.Options{
			Application:      app,
			MaxTimersPerNode: DefaultMaxTimersPerNode,
			TimerLocation:    app.options.Timer.Location,
		},
	)
	if err != nil {
		t.Fatalf("node.New() error = %v", err)
	}
	t.Cleanup(func() {
		if rollbackErr := current.Rollback(context.Background()); rollbackErr != nil {
			t.Errorf("Node.Rollback() error = %v", rollbackErr)
		}
	})
	return current
}
