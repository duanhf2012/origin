package application

import (
	"context"
	"encoding/json"
	"net/http"
	"runtime"
	runtimemetrics "runtime/metrics"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/duanhf2012/origin/v3/command"
	"github.com/duanhf2012/origin/v3/diagnostics"
	originlog "github.com/duanhf2012/origin/v3/log"
	"github.com/duanhf2012/origin/v3/node"
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
	summary := app.DiagnosticsSummary()
	if summary.SchemaVersion != 1 || summary.Application.Name != "diagnostics-test" ||
		summary.Application.State != "running" || len(summary.Nodes) != 1 ||
		summary.Nodes[0].NodeID != "gateway-1" || summary.Nodes[0].Services.Total != 1 ||
		!summary.BufferPool.Enabled {
		t.Fatalf("running diagnostics Summary = %+v", summary)
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
	summary := app.DiagnosticsSummary()
	if summary.SchemaVersion != 1 || summary.Application.State != "failed" ||
		summary.Application.AdminServer.State != "stopped" ||
		summary.Application.Pprof.State != "stopped" || summary.Nodes == nil {
		t.Fatalf("nil diagnostics Summary = %+v", summary)
	}
}

// TestDiagnosticsRuntimeFieldsUseRealRuntime 固定 Full 的内存上限已赋值，并验证 Summary 的
// Go 管理内存严格使用 Sys-HeapReleased 且防止理论下溢。
func TestDiagnosticsRuntimeFieldsUseRealRuntime(t *testing.T) {
	app := New()
	full := app.Diagnostics()
	if full.Runtime.MemoryLimitBytes <= 0 {
		t.Fatalf("Full MemoryLimitBytes = %d, want > 0", full.Runtime.MemoryLimitBytes)
	}
	summary := app.DiagnosticsSummary()
	if summary.Runtime.MemoryLimitBytes <= 0 || summary.Runtime.Goroutines <= 0 ||
		summary.Runtime.GOMAXPROCS <= 0 {
		t.Fatalf("RuntimeSummary = %+v", summary.Runtime)
	}

	memory := runtime.MemStats{Sys: 100, HeapReleased: 40}
	got := runtimeSummaryFrom(memory, runtimeMetricValues{})
	if got.GoMemoryUsedBytes != 60 {
		t.Fatalf("GoMemoryUsedBytes = %d, want 60", got.GoMemoryUsedBytes)
	}
	memory.HeapReleased = 101
	if got := runtimeSummaryFrom(memory, runtimeMetricValues{}).GoMemoryUsedBytes; got != 0 {
		t.Fatalf("underflow GoMemoryUsedBytes = %d, want 0", got)
	}
}

// TestRuntimeMetricKindsAndMissingAreZero 防止 runtime/metrics 名称缺失或 KindBad 时调用错误
// Value getter panic；每个缺失指标必须保持稳定零值。
func TestRuntimeMetricKindsAndMissingAreZero(t *testing.T) {
	if got := runtimeMetricValuesFrom(nil); got != (runtimeMetricValues{}) {
		t.Fatalf("missing metrics = %+v", got)
	}
	samples := []runtimemetrics.Sample{
		{Name: runtimeRunnableGoroutinesMetric},
		{Name: runtimeGCCPUSecondsMetric},
		{Name: runtimeMutexWaitSecondsMetric},
		{Name: runtimeMemoryLimitMetric},
	}
	if got := runtimeMetricValuesFrom(samples); got != (runtimeMetricValues{}) {
		t.Fatalf("KindBad metrics = %+v", got)
	}
}

// TestRuntimeMetricNamesHaveGo126Kinds 直接向当前 Go Runtime 请求 brief 固定的四个名称，
// 防止拼写或单位漂移退化成静默 KindBad。
func TestRuntimeMetricNamesHaveGo126Kinds(t *testing.T) {
	samples := []runtimemetrics.Sample{
		{Name: runtimeRunnableGoroutinesMetric},
		{Name: runtimeGCCPUSecondsMetric},
		{Name: runtimeMutexWaitSecondsMetric},
		{Name: runtimeMemoryLimitMetric},
	}
	runtimemetrics.Read(samples)
	wantKinds := []runtimemetrics.ValueKind{
		runtimemetrics.KindUint64,
		runtimemetrics.KindFloat64,
		runtimemetrics.KindFloat64,
		runtimemetrics.KindUint64,
	}
	for index := range samples {
		if got := samples[index].Value.Kind(); got != wantKinds[index] {
			t.Fatalf("metric %q Kind = %v, want %v", samples[index].Name, got, wantKinds[index])
		}
	}
}

type blockingDiagnosticsService struct {
	service.Service
	entered chan struct{}
	release chan struct{}
}

func (target *blockingDiagnosticsService) ExecutionStats() service.ExecutionStats {
	close(target.entered)
	<-target.release
	return service.ExecutionStats{}
}

// TestDiagnosticsSummaryReleasesApplicationLockBeforeLeafCollection 使用阻塞叶子证明 Runtime、
// Pool、Node 采集和后续 JSON 所需 DTO 构造都不持有 Application 锁。
func TestDiagnosticsSummaryReleasesApplicationLockBeforeLeafCollection(t *testing.T) {
	target := &blockingDiagnosticsService{entered: make(chan struct{}), release: make(chan struct{})}
	current, err := node.New(
		node.Config{ID: "lock-boundary", Services: []string{"worker"}},
		[]node.ServiceBinding{{Name: "worker", Template: "blockingDiagnosticsService", Service: target}},
		originlog.NewNop(),
		node.Options{MaxTimersPerNode: 8, TimerLocation: time.UTC},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = current.Rollback(context.Background()) })
	app := New()
	app.mu.Lock()
	app.nodes = []*node.Node{current}
	app.mu.Unlock()
	done := make(chan diagnostics.Summary, 1)
	go func() {
		done <- app.DiagnosticsSummary()
	}()
	select {
	case <-target.entered:
	case <-time.After(time.Second):
		t.Fatal("DiagnosticsSummary did not reach Service leaf")
	}
	lockAcquired := make(chan struct{})
	go func() {
		app.mu.Lock()
		app.mu.Unlock()
		close(lockAcquired)
	}()
	select {
	case <-lockAcquired:
	case <-time.After(time.Second):
		t.Fatal("Application lock remained held during Node leaf collection")
	}
	time.Sleep(20 * time.Millisecond)
	close(target.release)
	select {
	case summary := <-done:
		if summary.CollectCost < diagnostics.Duration(20*time.Millisecond) {
			t.Fatalf("CollectCost = %v, want to include blocked leaf collection", summary.CollectCost)
		}
	case <-time.After(time.Second):
		t.Fatal("DiagnosticsSummary did not finish")
	}
}

// TestAdminDiagnosticsDetailRouting 固定默认 Summary、唯一 full、非法和重复 detail，以及
// ServeMux 自动生成的 POST 405/Allow。所有成功响应必须是预编码 JSON。
func TestAdminDiagnosticsDetailRouting(t *testing.T) {
	app := New()
	if err := app.freezeAdminRoutes(nil); err != nil {
		t.Fatal(err)
	}
	baseURL := startAdminRouteTestServer(t, app)
	tests := []struct {
		name       string
		method     string
		query      string
		wantStatus int
		wantSchema float64
		wantAllow  string
	}{
		{name: "summary", method: http.MethodGet, wantStatus: http.StatusOK, wantSchema: 1},
		{name: "full", method: http.MethodGet, query: "?detail=full", wantStatus: http.StatusOK, wantSchema: 2},
		{name: "invalid", method: http.MethodGet, query: "?detail=x", wantStatus: http.StatusBadRequest},
		{name: "empty", method: http.MethodGet, query: "?detail=", wantStatus: http.StatusBadRequest},
		{name: "repeated", method: http.MethodGet, query: "?detail=full&detail=full", wantStatus: http.StatusBadRequest},
		{name: "unknown key", method: http.MethodGet, query: "?token=query-secret", wantStatus: http.StatusBadRequest},
		{name: "full with extra key", method: http.MethodGet, query: "?detail=full&token=query-secret", wantStatus: http.StatusBadRequest},
		{name: "post", method: http.MethodPost, wantStatus: http.StatusMethodNotAllowed, wantAllow: http.MethodGet},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request, err := http.NewRequest(test.method, baseURL+"/admin/v1/diagnostics"+test.query, nil)
			if err != nil {
				t.Fatal(err)
			}
			response, err := http.DefaultClient.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			body := readAdminRouteResponse(t, response)
			if response.StatusCode != test.wantStatus || response.Header.Get("Allow") != test.wantAllow {
				t.Fatalf("status=%d Allow=%q Body=%q", response.StatusCode, response.Header.Get("Allow"), body)
			}
			if test.wantSchema == 0 {
				if test.wantStatus == http.StatusBadRequest &&
					body != http.StatusText(http.StatusBadRequest)+"\n" {
					t.Fatalf("400 Body = %q, want stable StatusText", body)
				}
				return
			}
			if response.Header.Get("Content-Type") != "application/json" {
				t.Fatalf("Content-Type = %q", response.Header.Get("Content-Type"))
			}
			var document map[string]any
			if err := json.Unmarshal([]byte(body), &document); err != nil {
				t.Fatalf("JSON body = %q: %v", body, err)
			}
			if document["schema_version"] != test.wantSchema {
				t.Fatalf("schema_version = %v, want %v", document["schema_version"], test.wantSchema)
			}
			if nodes, ok := document["nodes"].([]any); !ok || len(nodes) != 0 {
				t.Fatalf("nodes = %#v, want []", document["nodes"])
			}
		})
	}
}

type countingDiagnosticsService struct {
	service.Service
	executionReads atomic.Int64
}

func (target *countingDiagnosticsService) ExecutionStats() service.ExecutionStats {
	target.executionReads.Add(1)
	return service.ExecutionStats{}
}

// TestAdminDiagnosticsSamplesExactlyOnceForValidQuery 使用真实 Node 叶子调用计数固定采样边界：
// 两种合法查询各采集一次，所有非法 query 在采样前返回 400。
func TestAdminDiagnosticsSamplesExactlyOnceForValidQuery(t *testing.T) {
	audit := &adminAuditHandler{}
	logRuntime, err := originlog.NewRuntime(originlog.Config{Mode: originlog.SyncMode}, audit)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = logRuntime.Close(context.Background()) })
	target := &countingDiagnosticsService{}
	current, err := node.New(
		node.Config{ID: "sample-count", Services: []string{"worker"}},
		[]node.ServiceBinding{{Name: "worker", Template: "countingDiagnosticsService", Service: target}},
		originlog.NewNop(),
		node.Options{MaxTimersPerNode: 8, TimerLocation: time.UTC},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = current.Rollback(context.Background()) })
	app := New()
	app.logger = logRuntime.Logger()
	app.nodes = []*node.Node{current}
	if err := app.freezeAdminRoutes(nil); err != nil {
		t.Fatal(err)
	}
	baseURL := startAdminRouteTestServer(t, app)

	request := func(query string, wantStatus int, wantReads int64, forbidden string) {
		t.Helper()
		beforeAudit := len(audit.snapshot())
		response, requestErr := http.Get(baseURL + "/admin/v1/diagnostics" + query)
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		body := readAdminRouteResponse(t, response)
		if response.StatusCode != wantStatus || target.executionReads.Load() != wantReads {
			t.Fatalf("GET %q status=%d reads=%d Body=%q, want status=%d reads=%d",
				query, response.StatusCode, target.executionReads.Load(), body, wantStatus, wantReads)
		}
		if wantStatus == http.StatusBadRequest && body != http.StatusText(http.StatusBadRequest)+"\n" {
			t.Fatalf("GET %q Body = %q, want stable 400", query, body)
		}
		records := audit.snapshot()
		if len(records) != beforeAudit+1 || records[len(records)-1].status != wantStatus {
			t.Fatalf("GET %q audit delta=%d tail=%+v", query, len(records)-beforeAudit, records)
		}
		if forbidden != "" && strings.Contains(records[len(records)-1].text, forbidden) {
			t.Fatalf("GET %q audit leaked raw query fragment %q: %q",
				query, forbidden, records[len(records)-1].text)
		}
		if forbidden != "" && strings.Contains(
			records[len(records)-1].text,
			strings.TrimPrefix(query, "?"),
		) {
			t.Fatalf("GET %q audit leaked complete raw query: %q",
				query, records[len(records)-1].text)
		}
	}
	request("?detail=full;unknown=semicolon-summary-secret", http.StatusBadRequest, 0, "semicolon-summary-secret")
	request("?detail=full&unknown=semicolon-full-secret;bad", http.StatusBadRequest, 0, "semicolon-full-secret")
	request("?unknown=query-secret", http.StatusBadRequest, 0, "query-secret")
	request("?detail=full&unknown=query-secret", http.StatusBadRequest, 0, "query-secret")
	request("?detail=full&detail=full", http.StatusBadRequest, 0, "")
	request("", http.StatusOK, 1, "")
	request("?detail=full", http.StatusOK, 2, "")
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
