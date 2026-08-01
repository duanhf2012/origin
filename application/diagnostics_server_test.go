package application

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/duanhf2012/origin/v3/diagnostics"
	"github.com/duanhf2012/origin/v3/errs"
)

// newHTTPTestApplication 只建立 HTTP 控制器依赖的真实生命周期状态；Server、Listener、
// 请求和关闭都使用标准库真实实现，不替换网络边界。
func newHTTPTestApplication(t *testing.T) *Application {
	t.Helper()
	app := New()
	app.mu.Lock()
	app.appName = "http-test"
	app.startedAt = time.Now()
	app.resourcesReady = true
	app.state.Store(uint32(StateRunning))
	app.mu.Unlock()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := app.StopPprof(ctx); err != nil {
			t.Errorf("StopPprof() cleanup error = %v", err)
		}
		if err := app.StopDiagnosticsServer(ctx); err != nil {
			t.Errorf("StopDiagnosticsServer() cleanup error = %v", err)
		}
	})
	return app
}

// TestDiagnosticsServerRuntimeLifecycle 防止 :0 地址丢失、重复 Start 新建 Listener，或 Stop 后
// 端口仍被占用。HTTP 响应必须来自当前 Application.Diagnostics。
func TestDiagnosticsServerRuntimeLifecycle(t *testing.T) {
	app := newHTTPTestApplication(t)
	if _, ok := app.DiagnosticsAddress(); ok {
		t.Fatal("DiagnosticsAddress() unexpectedly enabled")
	}
	if err := app.StartDiagnosticsServer("127.0.0.1:0"); err != nil {
		t.Fatalf("StartDiagnosticsServer() error = %v", err)
	}
	address, ok := app.DiagnosticsAddress()
	if !ok || address == "" || address == "127.0.0.1:0" {
		t.Fatalf("DiagnosticsAddress() = %q, %v", address, ok)
	}
	if err := app.StartDiagnosticsServer(address); err != nil {
		t.Fatalf("same-address StartDiagnosticsServer() error = %v", err)
	}
	if got, _ := app.DiagnosticsAddress(); got != address {
		t.Fatalf("idempotent address = %q, want %q", got, address)
	}
	if err := app.StartDiagnosticsServer("127.0.0.1:0"); err != nil {
		t.Fatalf("same requested :0 StartDiagnosticsServer() error = %v", err)
	}
	if err := app.StartDiagnosticsServer("127.0.0.1:1"); !errors.Is(
		err,
		errs.ErrDiagnosticsStateConflict,
	) {
		t.Fatalf("different-address error = %v", err)
	}

	response, err := http.Get("http://" + address + "/debug/origin/diagnostics")
	if err != nil {
		t.Fatalf("GET diagnostics error = %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK ||
		response.Header.Get("Content-Type") != "application/json" {
		t.Fatalf("GET status=%d content-type=%q", response.StatusCode, response.Header.Get("Content-Type"))
	}
	var snapshot diagnostics.Snapshot
	if err := json.NewDecoder(response.Body).Decode(&snapshot); err != nil {
		t.Fatalf("decode diagnostics error = %v", err)
	}
	if snapshot.SchemaVersion != 1 || snapshot.Application.Name != "http-test" ||
		snapshot.Application.DiagnosticsServer.State != "serving" ||
		snapshot.Application.DiagnosticsServer.Address != address {
		t.Fatalf("HTTP snapshot = %+v", snapshot)
	}

	if err := app.StopDiagnosticsServer(context.Background()); err != nil {
		t.Fatalf("StopDiagnosticsServer() error = %v", err)
	}
	if _, ok := app.DiagnosticsAddress(); ok {
		t.Fatal("DiagnosticsAddress() remains enabled after Stop")
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatalf("released address cannot be rebound: %v", err)
	}
	_ = listener.Close()
	if err := app.StopDiagnosticsServer(context.Background()); err != nil {
		t.Fatalf("idempotent StopDiagnosticsServer() error = %v", err)
	}
	if err := app.StartDiagnosticsServer("127.0.0.1:0"); err != nil {
		t.Fatalf("restart StartDiagnosticsServer() error = %v", err)
	}
}

// TestDiagnosticsServerReadOnlyRoutes 防止引入控制端点、非 GET 方法或 DefaultServeMux 注册。
func TestDiagnosticsServerReadOnlyRoutes(t *testing.T) {
	app := newHTTPTestApplication(t)
	if err := app.StartDiagnosticsServer("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	address, _ := app.DiagnosticsAddress()

	request, _ := http.NewRequest(
		http.MethodPost,
		"http://"+address+"/debug/origin/diagnostics",
		nil,
	)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("POST status = %d", response.StatusCode)
	}

	response, err = http.Get("http://" + address + "/debug/origin/control")
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("control route status = %d", response.StatusCode)
	}

	recorder := &statusRecorder{header: make(http.Header)}
	http.DefaultServeMux.ServeHTTP(
		recorder,
		mustRequest(t, http.MethodGet, "http://example/debug/origin/diagnostics"),
	)
	if recorder.status != http.StatusNotFound {
		t.Fatalf("DefaultServeMux status = %d", recorder.status)
	}
}

// TestDiagnosticsServerValidationAndBindFailure 固定参数、生命周期和端口冲突的稳定错误码。
func TestDiagnosticsServerValidationAndBindFailure(t *testing.T) {
	created := New()
	if err := created.StartDiagnosticsServer("127.0.0.1:0"); !errors.Is(
		err,
		errs.ErrDiagnosticsStateConflict,
	) {
		t.Fatalf("created Start error = %v", err)
	}
	app := newHTTPTestApplication(t)
	if err := app.StartDiagnosticsServer(""); !errors.Is(err, errs.ErrInvalidArgument) {
		t.Fatalf("empty address error = %v", err)
	}
	if err := app.StartDiagnosticsServer("not-an-address"); !errors.Is(
		err,
		errs.ErrInvalidArgument,
	) {
		t.Fatalf("invalid address error = %v", err)
	}
	if err := app.StopDiagnosticsServer(nil); !errors.Is(err, errs.ErrInvalidArgument) {
		t.Fatalf("nil Stop context error = %v", err)
	}

	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	if err := app.StartDiagnosticsServer(occupied.Addr().String()); !errors.Is(
		err,
		errs.ErrDiagnosticsUnavailable,
	) {
		t.Fatalf("occupied address error = %v", err)
	}
}

// TestDiagnosticsServerConcurrentStartStop 固定同地址并发 Start 和重复 Stop 的线性化语义。
// 所有调用共享一个 Listener；全部 Stop 返回后端口必须已经释放。
func TestDiagnosticsServerConcurrentStartStop(t *testing.T) {
	app := newHTTPTestApplication(t)
	const callers = 16
	startErrors := make(chan error, callers)
	var startGroup sync.WaitGroup
	for range callers {
		startGroup.Add(1)
		go func() {
			defer startGroup.Done()
			startErrors <- app.StartDiagnosticsServer("127.0.0.1:0")
		}()
	}
	startGroup.Wait()
	close(startErrors)
	for err := range startErrors {
		if err != nil {
			t.Fatalf("concurrent StartDiagnosticsServer() error = %v", err)
		}
	}
	address, ok := app.DiagnosticsAddress()
	if !ok {
		t.Fatal("concurrent Start did not publish an address")
	}

	stopErrors := make(chan error, callers)
	var stopGroup sync.WaitGroup
	for range callers {
		stopGroup.Add(1)
		go func() {
			defer stopGroup.Done()
			stopErrors <- app.StopDiagnosticsServer(context.Background())
		}()
	}
	stopGroup.Wait()
	close(stopErrors)
	for err := range stopErrors {
		if err != nil {
			t.Fatalf("concurrent StopDiagnosticsServer() error = %v", err)
		}
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatalf("concurrent Stop did not release %q: %v", address, err)
	}
	_ = listener.Close()
}

// TestHTTPRuntimeCanceledStopForcesClose 使用一个真实阻塞请求验证 Shutdown 的 Context
// 耗尽后仍会强制关闭连接、等待 Serve 退出并释放端口。
func TestHTTPRuntimeCanceledStopForcesClose(t *testing.T) {
	entered := make(chan struct{})
	server := &http.Server{Handler: http.HandlerFunc(func(
		_ http.ResponseWriter,
		request *http.Request,
	) {
		close(entered)
		<-request.Context().Done()
	})}
	var runtime httpRuntime
	if err := runtime.start("127.0.0.1:0", server); err != nil {
		t.Fatalf("httpRuntime.start() error = %v", err)
	}
	address, ok := runtime.addressSnapshot()
	if !ok {
		t.Fatal("httpRuntime address was not published")
	}
	requestDone := make(chan error, 1)
	go func() {
		response, err := http.Get("http://" + address)
		if response != nil {
			_ = response.Body.Close()
		}
		requestDone <- err
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("blocking HTTP request did not enter handler")
	}

	stopCtx, cancelStop := context.WithCancel(context.Background())
	cancelStop()
	if err := runtime.stop(stopCtx); !errors.Is(err, errs.ErrCanceled) {
		t.Fatalf("canceled httpRuntime.stop() error = %v", err)
	}
	select {
	case <-requestDone:
	case <-time.After(time.Second):
		t.Fatal("forced Close did not release blocking request")
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatalf("forced Close did not release %q: %v", address, err)
	}
	_ = listener.Close()
}

// TestDiagnosticsServerUnexpectedServeExit 固定 Listener 异常退出只把诊断资源标为失败，
// Application 仍可采集快照；随后 Stop 清理失败态并允许重新启动。
func TestDiagnosticsServerUnexpectedServeExit(t *testing.T) {
	app := newHTTPTestApplication(t)
	if err := app.StartDiagnosticsServer("127.0.0.1:0"); err != nil {
		t.Fatalf("StartDiagnosticsServer() error = %v", err)
	}
	address, _ := app.DiagnosticsAddress()
	app.diagnosticsHTTP.mu.Lock()
	listener := app.diagnosticsHTTP.listener
	done := app.diagnosticsHTTP.done
	app.diagnosticsHTTP.mu.Unlock()
	if err := listener.Close(); err != nil {
		t.Fatalf("close diagnostics Listener error = %v", err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Serve did not exit after Listener failure")
	}
	snapshot := app.Diagnostics()
	if snapshot.Application.DiagnosticsServer.State != "failed" ||
		snapshot.Application.DiagnosticsServer.ErrorCode != errs.CodeDiagnosticsUnavailable ||
		snapshot.Application.State != "running" {
		t.Fatalf("unexpected Serve diagnostics = %+v", snapshot.Application)
	}
	if err := app.StopDiagnosticsServer(context.Background()); err != nil {
		t.Fatalf("StopDiagnosticsServer(failed) error = %v", err)
	}
	if err := app.StartDiagnosticsServer("127.0.0.1:0"); err != nil {
		t.Fatalf("restart after Serve failure error = %v", err)
	}
	if address == "" {
		t.Fatal("failed Listener did not expose its original address")
	}
}

// statusRecorder 是不依赖 httptest 默认行为的最小 ResponseWriter，用于检查全局 mux 未注册。
type statusRecorder struct {
	header http.Header
	status int
}

func (recorder *statusRecorder) Header() http.Header { return recorder.header }
func (recorder *statusRecorder) Write(payload []byte) (int, error) {
	if recorder.status == 0 {
		recorder.status = http.StatusOK
	}
	return len(payload), nil
}
func (recorder *statusRecorder) WriteHeader(status int) { recorder.status = status }

func mustRequest(t *testing.T, method string, target string) *http.Request {
	t.Helper()
	request, err := http.NewRequest(method, target, nil)
	if err != nil {
		t.Fatal(err)
	}
	return request
}
