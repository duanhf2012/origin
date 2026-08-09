package application

import (
	"context"
	"errors"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/duanhf2012/origin/v3/admin"
	"github.com/duanhf2012/origin/v3/command"
	"github.com/duanhf2012/origin/v3/errs"
	originlog "github.com/duanhf2012/origin/v3/log"
	"github.com/duanhf2012/origin/v3/node"
	"github.com/duanhf2012/origin/v3/service"
)

var httpLifecycleStartSeen chan bool
var httpLifecycleInitSeen chan bool
var httpLifecycleStopSeen chan bool
var httpLifecycleProviderSeen chan struct{}

type httpLifecycleService struct {
	service.Service
}

var invalidAdminLifecycleInitSeen chan struct{}

type invalidAdminLifecycleService struct {
	service.Service
}

func (*invalidAdminLifecycleService) AdminEndpoints() []admin.Endpoint {
	return []admin.Endpoint{admin.Get("invalid_name", nil)}
}

func (*invalidAdminLifecycleService) OnInit() error {
	invalidAdminLifecycleInitSeen <- struct{}{}
	return nil
}

func (*httpLifecycleService) AdminEndpoints() []admin.Endpoint {
	httpLifecycleProviderSeen <- struct{}{}
	return nil
}

func (target *httpLifecycleService) OnInit() error {
	providerBeforeInit := false
	select {
	case <-httpLifecycleProviderSeen:
		providerBeforeInit = true
	default:
	}
	httpLifecycleInitSeen <- providerBeforeInit && adminDiagnosticsReachable(target.Application())
	return nil
}

func (target *httpLifecycleService) OnStart(context.Context) error {
	httpLifecycleStartSeen <- adminDiagnosticsReachable(target.Application())
	return nil
}

func (target *httpLifecycleService) OnStop(context.Context) error {
	application := target.Application()
	_, adminOK := application.AdminAddress()
	_, pprofOK := application.PprofAddress()
	httpLifecycleStopSeen <- adminOK && pprofOK
	return nil
}

func adminDiagnosticsReachable(application service.ApplicationRuntime) bool {
	if application == nil {
		return false
	}
	adminAddress, adminOK := application.AdminAddress()
	_, pprofOK := application.PprofAddress()
	if !adminOK || !pprofOK {
		return false
	}
	client := &http.Client{Timeout: time.Second}
	response, err := client.Get("http://" + adminAddress + "/admin/v1/diagnostics")
	if err != nil {
		return false
	}
	_ = response.Body.Close()
	return response.StatusCode == http.StatusOK
}

// TestInitialHTTPServersSurroundNodeLifecycle 防止命令行 Listener 在 Node 启动后才绑定，或在
// Node OnStop 之前提前关闭。run 返回时两个实际端口必须已经释放。
func TestInitialHTTPServersSurroundNodeLifecycle(t *testing.T) {
	directory := writeApplicationConfig(t, `
nodes:
  - id: gateway-1
    services:
      - httpLifecycleService
`)
	httpLifecycleProviderSeen = make(chan struct{}, 1)
	httpLifecycleInitSeen = make(chan bool, 1)
	httpLifecycleStartSeen = make(chan bool, 1)
	httpLifecycleStopSeen = make(chan bool, 1)
	app := New(Options{
		LogHandlerFactory: func(originlog.Config) (originlog.Handler, error) {
			return &silentHandler{}, nil
		},
	})
	app.Setup(&httpLifecycleService{})

	runCtx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		result <- app.run(runCtx, command.StartRequest{
			AppName:      "http-lifecycle",
			ConfigDir:    directory,
			AdminAddress: "127.0.0.1:0",
			PprofAddress: "127.0.0.1:0",
		})
	}()
	if !receiveApplicationValue(t, httpLifecycleInitSeen) {
		t.Fatal("Admin Provider was not frozen before OnInit or Listener was unreachable from OnInit")
	}
	if !receiveApplicationValue(t, httpLifecycleStartSeen) {
		t.Fatal("HTTP servers were unavailable from Service.OnStart")
	}
	adminAddress, adminOK := app.AdminAddress()
	pprofAddress, pprofOK := app.PprofAddress()
	if !adminOK || !pprofOK {
		t.Fatalf("addresses admin=%q/%v pprof=%q/%v", adminAddress, adminOK, pprofAddress, pprofOK)
	}

	cancel()
	if !receiveApplicationValue(t, httpLifecycleStopSeen) {
		t.Fatal("HTTP servers were unavailable from Service.OnStop")
	}
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("run() error = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Application run() did not return")
	}
	if _, ok := app.AdminAddress(); ok {
		t.Fatal("Admin remained active after run")
	}
	if _, ok := app.PprofAddress(); ok {
		t.Fatal("pprof remained active after run")
	}
	assertAddressReleased(t, adminAddress)
	assertAddressReleased(t, pprofAddress)
}

// TestInitialAdminBindFailureRollsBackBuiltNodes 防止 Admin Listener 绑定失败后进入任何
// Service 生命周期，并固定已装配 Node 的底层资源由 Rollback 回收。
func TestInitialAdminBindFailureRollsBackBuiltNodes(t *testing.T) {
	directory := writeApplicationConfig(t, `
nodes:
  - id: gateway-1
    services:
      - httpLifecycleService
`)
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	httpLifecycleProviderSeen = make(chan struct{}, 1)
	httpLifecycleInitSeen = make(chan bool, 1)
	httpLifecycleStartSeen = make(chan bool, 1)
	httpLifecycleStopSeen = make(chan bool, 1)
	app := New(Options{
		LogHandlerFactory: func(originlog.Config) (originlog.Handler, error) {
			return &silentHandler{}, nil
		},
	})
	app.Setup(&httpLifecycleService{})
	err = app.run(context.Background(), command.StartRequest{
		AppName:      "http-rollback",
		ConfigDir:    directory,
		AdminAddress: occupied.Addr().String(),
		PprofAddress: "127.0.0.1:0",
	})
	if !errors.Is(err, errs.ErrAdminUnavailable) {
		t.Fatalf("run() error = %v", err)
	}
	select {
	case <-httpLifecycleInitSeen:
		t.Fatal("Service.OnInit entered after Admin bind failure")
	default:
	}
	select {
	case <-httpLifecycleStartSeen:
		t.Fatal("Service.OnStart entered after Admin bind failure")
	default:
	}
	if _, ok := app.AdminAddress(); ok {
		t.Fatal("Admin leaked after bind failure")
	}
	if _, ok := app.PprofAddress(); ok {
		t.Fatal("pprof reported active after bind failure")
	}
	nodes := app.Nodes()
	if len(nodes) != 1 || nodes[0].State() != node.StateFailed {
		t.Fatalf("built Node rollback states = %#v", nodes)
	}
}

// TestInitialPprofBindFailureRollsBackAdminAndBuiltNodes 防止第二个 Listener 绑定失败时
// 泄漏已启动的 Admin，或让任何 Service 生命周期越过启动事务边界。
func TestInitialPprofBindFailureRollsBackAdminAndBuiltNodes(t *testing.T) {
	directory := writeApplicationConfig(t, `
nodes:
  - id: gateway-1
    services:
      - httpLifecycleService
`)
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	httpLifecycleProviderSeen = make(chan struct{}, 1)
	httpLifecycleInitSeen = make(chan bool, 1)
	httpLifecycleStartSeen = make(chan bool, 1)
	httpLifecycleStopSeen = make(chan bool, 1)
	app := New(Options{
		LogHandlerFactory: func(originlog.Config) (originlog.Handler, error) {
			return &silentHandler{}, nil
		},
	})
	app.Setup(&httpLifecycleService{})
	err = app.run(context.Background(), command.StartRequest{
		AppName:      "pprof-rollback",
		ConfigDir:    directory,
		AdminAddress: "127.0.0.1:0",
		PprofAddress: occupied.Addr().String(),
	})
	if !errors.Is(err, errs.ErrDiagnosticsUnavailable) {
		t.Fatalf("run() error = %v", err)
	}
	select {
	case <-httpLifecycleInitSeen:
		t.Fatal("Service.OnInit entered after pprof bind failure")
	default:
	}
	select {
	case <-httpLifecycleStartSeen:
		t.Fatal("Service.OnStart entered after pprof bind failure")
	default:
	}
	if _, ok := app.AdminAddress(); ok {
		t.Fatal("Admin leaked after pprof bind failure")
	}
	if _, ok := app.PprofAddress(); ok {
		t.Fatal("pprof reported active after bind failure")
	}
	nodes := app.Nodes()
	if len(nodes) != 1 || nodes[0].State() != node.StateFailed {
		t.Fatalf("built Node rollback states = %#v", nodes)
	}
}

// TestAdminFreezeFailureRollsBackBuiltNodesBeforeLifecycle 固定 Provider 收集或端点校验失败
// 与 Listener 绑定失败共享同一启动事务：不得进入 OnInit，构建资源必须回滚。
func TestAdminFreezeFailureRollsBackBuiltNodesBeforeLifecycle(t *testing.T) {
	directory := writeApplicationConfig(t, `
nodes:
  - id: gateway-1
    services:
      - invalidAdminLifecycleService
`)
	invalidAdminLifecycleInitSeen = make(chan struct{}, 1)
	app := New(Options{
		LogHandlerFactory: func(originlog.Config) (originlog.Handler, error) {
			return &silentHandler{}, nil
		},
	})
	app.Setup(&invalidAdminLifecycleService{})
	err := app.run(context.Background(), command.StartRequest{
		AppName:      "freeze-rollback",
		ConfigDir:    directory,
		AdminAddress: "127.0.0.1:0",
	})
	if !errors.Is(err, errs.ErrInvalidConfig) {
		t.Fatalf("run() error = %v", err)
	}
	select {
	case <-invalidAdminLifecycleInitSeen:
		t.Fatal("Service.OnInit entered after Admin freeze failure")
	default:
	}
	if _, ok := app.AdminAddress(); ok {
		t.Fatal("Admin reported active after freeze failure")
	}
	nodes := app.Nodes()
	if len(nodes) != 1 || nodes[0].State() != node.StateFailed {
		t.Fatalf("built Node rollback states = %#v", nodes)
	}
}

func receiveApplicationValue[T any](t *testing.T, source <-chan T) T {
	t.Helper()
	select {
	case value := <-source:
		return value
	case <-time.After(2 * time.Second):
		t.Fatal("waiting for Application lifecycle value timed out")
		var zero T
		return zero
	}
}

func assertAddressReleased(t *testing.T, address string) {
	t.Helper()
	listener, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatalf("address %q not released: %v", address, err)
	}
	_ = listener.Close()
}
