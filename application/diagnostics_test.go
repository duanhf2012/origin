package application

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/duanhf2012/origin/v3/command"
	"github.com/duanhf2012/origin/v3/diagnostics"
	originlog "github.com/duanhf2012/origin/v3/log"
	"github.com/duanhf2012/origin/v3/service"
)

var applicationFacadeSeen chan service.ApplicationRuntime

type applicationFacadeService struct {
	service.Service
}

func (target *applicationFacadeService) OnInit() error {
	applicationFacadeSeen <- target.Application()
	return nil
}

// TestDiagnosticsBeforeStartHasCompleteZeroSemantics 防止未启动 Application 返回 nil 容器或 panic。
func TestDiagnosticsBeforeStartHasCompleteZeroSemantics(t *testing.T) {
	app := New()
	snapshot := app.Diagnostics()
	if snapshot.SchemaVersion != 2 || snapshot.Application.State != "created" {
		t.Fatalf("created diagnostics = %+v", snapshot)
	}
	if snapshot.Nodes == nil || len(snapshot.Nodes) != 0 {
		t.Fatalf("created nodes = %#v, want empty non-nil slice", snapshot.Nodes)
	}
	if snapshot.CollectedAt.IsZero() || snapshot.CollectCost < 0 {
		t.Fatalf("collection metadata = %+v", snapshot)
	}
	if snapshot.Runtime.Goroutines <= 0 || snapshot.Runtime.GOMAXPROCS <= 0 {
		t.Fatalf("runtime diagnostics = %+v", snapshot.Runtime)
	}
	if snapshot.Application.DiagnosticsServer.State != "stopped" ||
		snapshot.Application.Pprof.State != "stopped" {
		t.Fatalf("server diagnostics = %+v", snapshot.Application)
	}
}

// TestDiagnosticsJSONOmitsLogConfiguration 保证诊断快照不暴露日志输出控制状态；
// 该状态由 log.CurrentStatus 提供给确实需要日志管理的调用方。
func TestDiagnosticsJSONOmitsLogConfiguration(t *testing.T) {
	snapshot := New().Diagnostics()
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatalf("Marshal diagnostics error = %v", err)
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &document); err != nil {
		t.Fatalf("Unmarshal diagnostics error = %v", err)
	}
	if _, exists := document["log"]; exists {
		t.Fatalf("diagnostics unexpectedly exposes log status: %s", encoded)
	}
}

// TestDiagnosticsAggregatesRunningApplication 验证 Application 只复制固定 Node 顺序并保留停止后终态。
func TestDiagnosticsAggregatesRunningApplication(t *testing.T) {
	directory := writeApplicationConfig(t, `
buffer_pool:
  track_usage: true
nodes:
  - id: gateway-1
    services:
      - lifecycleTestService
`)
	handler := &silentHandler{}
	app := New(Options{
		LogHandlerFactory: func(originlog.Config) (originlog.Handler, error) {
			return handler, nil
		},
	})
	app.Setup(&lifecycleTestService{})

	runCtx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		result <- app.run(runCtx, command.StartRequest{
			AppName:   "diagnostics-test",
			ConfigDir: directory,
		})
	}()
	waitForState(t, app, StateRunning)

	running := app.Diagnostics()
	if running.Application.Name != "diagnostics-test" ||
		running.Application.State != "running" || running.StartedAt.IsZero() ||
		len(running.Nodes) != 1 || running.Nodes[0].NodeID != "gateway-1" ||
		len(running.Nodes[0].Services) != 1 {
		t.Fatalf("running diagnostics = %+v", running)
	}
	if !running.BufferPool.Enabled {
		t.Fatalf("buffer pool diagnostics = %+v", running.BufferPool)
	}

	cancel()
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("run() error = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Application stop timeout")
	}
	stopped := app.Diagnostics()
	if stopped.Application.State != "stopped" || stopped.Nodes[0].State != "stopped" ||
		stopped.Nodes[0].Services[0].State != "stopped" {
		t.Fatalf("stopped diagnostics = %+v", stopped)
	}
}

// TestNilApplicationDiagnostics 保证 nil Source 仍返回 Schema 2 和 failed 状态。
func TestNilApplicationDiagnostics(t *testing.T) {
	var app *Application
	snapshot := app.Diagnostics()
	if snapshot.SchemaVersion != 2 || snapshot.Application.State != "failed" ||
		snapshot.Nodes == nil {
		t.Fatalf("nil diagnostics = %+v", snapshot)
	}
}

// TestApplicationInjectsRestrictedFacadeIntoRealService 防止 Application.buildNodes 遗漏
// Node Options 注入；反射创建的真实实例必须从 OnInit 起取得同一 Source。
func TestApplicationInjectsRestrictedFacadeIntoRealService(t *testing.T) {
	directory := writeApplicationConfig(t, `
nodes:
  - id: gateway-1
    services:
      - applicationFacadeService
`)
	applicationFacadeSeen = make(chan service.ApplicationRuntime, 1)
	app := New(Options{
		LogHandlerFactory: func(originlog.Config) (originlog.Handler, error) {
			return &silentHandler{}, nil
		},
	})
	app.Setup(&applicationFacadeService{})
	runCtx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		result <- app.run(runCtx, command.StartRequest{
			AppName:   "facade-test",
			ConfigDir: directory,
		})
	}()

	var facade service.ApplicationRuntime
	select {
	case facade = <-applicationFacadeSeen:
	case <-time.After(time.Second):
		t.Fatal("OnInit did not report Application facade")
	}
	if facade == nil || facade.Diagnostics().Application.Name != "facade-test" {
		t.Fatalf("Application facade = %T, diagnostics=%+v", facade, diagnostics.Snapshot{})
	}
	cancel()
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("run() error = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Application stop timeout")
	}
}
