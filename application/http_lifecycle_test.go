package application

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/duanhf2012/origin/v3/command"
	"github.com/duanhf2012/origin/v3/errs"
	originlog "github.com/duanhf2012/origin/v3/log"
	"github.com/duanhf2012/origin/v3/service"
)

var httpLifecycleStartSeen chan bool
var httpLifecycleStopSeen chan bool

type httpLifecycleService struct {
	service.Service
}

func (target *httpLifecycleService) OnStart(context.Context) error {
	application := target.Application()
	_, diagnosticsOK := application.DiagnosticsAddress()
	_, pprofOK := application.PprofAddress()
	httpLifecycleStartSeen <- diagnosticsOK && pprofOK
	return nil
}

func (target *httpLifecycleService) OnStop(context.Context) error {
	application := target.Application()
	_, diagnosticsOK := application.DiagnosticsAddress()
	_, pprofOK := application.PprofAddress()
	httpLifecycleStopSeen <- diagnosticsOK && pprofOK
	return nil
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
			AppName:            "http-lifecycle",
			ConfigDir:          directory,
			DiagnosticsAddress: "127.0.0.1:0",
			PprofAddress:       "127.0.0.1:0",
		})
	}()
	if !receiveApplicationValue(t, httpLifecycleStartSeen) {
		t.Fatal("HTTP servers were unavailable from Service.OnStart")
	}
	diagnosticsAddress, diagnosticsOK := app.DiagnosticsAddress()
	pprofAddress, pprofOK := app.PprofAddress()
	if !diagnosticsOK || !pprofOK {
		t.Fatalf("addresses diagnostics=%q/%v pprof=%q/%v", diagnosticsAddress, diagnosticsOK, pprofAddress, pprofOK)
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
	if _, ok := app.DiagnosticsAddress(); ok {
		t.Fatal("Diagnostics remained active after run")
	}
	if _, ok := app.PprofAddress(); ok {
		t.Fatal("pprof remained active after run")
	}
	assertAddressReleased(t, diagnosticsAddress)
	assertAddressReleased(t, pprofAddress)
}

// TestInitialPprofBindFailureRollsBackDiagnostics 防止第二个 Listener 失败时泄漏第一个端口或
// 继续进入 Service 生命周期。
func TestInitialPprofBindFailureRollsBackDiagnostics(t *testing.T) {
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
	httpLifecycleStartSeen = make(chan bool, 1)
	httpLifecycleStopSeen = make(chan bool, 1)
	app := New(Options{
		LogHandlerFactory: func(originlog.Config) (originlog.Handler, error) {
			return &silentHandler{}, nil
		},
	})
	app.Setup(&httpLifecycleService{})
	err = app.run(context.Background(), command.StartRequest{
		AppName:            "http-rollback",
		ConfigDir:          directory,
		DiagnosticsAddress: "127.0.0.1:0",
		PprofAddress:       occupied.Addr().String(),
	})
	if !errors.Is(err, errs.ErrDiagnosticsUnavailable) {
		t.Fatalf("run() error = %v", err)
	}
	select {
	case <-httpLifecycleStartSeen:
		t.Fatal("Service.OnStart entered after pprof bind failure")
	default:
	}
	if _, ok := app.DiagnosticsAddress(); ok {
		t.Fatal("Diagnostics leaked after pprof bind failure")
	}
	if _, ok := app.PprofAddress(); ok {
		t.Fatal("pprof reported active after bind failure")
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
