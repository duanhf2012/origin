package application

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	runtimepprof "runtime/pprof"
	"runtime/trace"
	"strings"
	"testing"
	"time"

	"github.com/duanhf2012/origin/v3/errs"
)

// TestPprofRuntimeLifecycleAndPrivateMux 防止 pprof 依赖 DefaultServeMux 或关闭时遗留端口。
func TestPprofRuntimeLifecycleAndPrivateMux(t *testing.T) {
	app := newHTTPTestApplication(t)
	defaultRequest := mustRequest(t, http.MethodGet, "http://example/debug/pprof/")
	_, defaultPatternBefore := http.DefaultServeMux.Handler(defaultRequest)
	if err := app.StartPprof("127.0.0.1:0"); err != nil {
		t.Fatalf("StartPprof() error = %v", err)
	}
	address, ok := app.PprofAddress()
	if !ok || address == "" || address == "127.0.0.1:0" {
		t.Fatalf("PprofAddress() = %q, %v", address, ok)
	}
	if err := app.StartPprof(address); err != nil {
		t.Fatalf("same-address StartPprof() error = %v", err)
	}
	if err := app.StartPprof("127.0.0.1:1"); !errors.Is(
		err,
		errs.ErrDiagnosticsStateConflict,
	) {
		t.Fatalf("different-address StartPprof() error = %v", err)
	}

	response, err := http.Get("http://" + address + "/debug/pprof/goroutine?debug=1")
	if err != nil {
		t.Fatalf("GET goroutine profile error = %v", err)
	}
	body, readErr := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if readErr != nil || response.StatusCode != http.StatusOK ||
		!bytes.Contains(body, []byte("goroutine profile")) {
		t.Fatalf("goroutine status=%d read=%v body=%q", response.StatusCode, readErr, body)
	}

	_, defaultPatternAfter := http.DefaultServeMux.Handler(defaultRequest)
	if defaultPatternAfter != defaultPatternBefore {
		t.Fatalf(
			"DefaultServeMux pattern changed from %q to %q",
			defaultPatternBefore,
			defaultPatternAfter,
		)
	}

	if err := app.StopPprof(context.Background()); err != nil {
		t.Fatalf("StopPprof() error = %v", err)
	}
	if _, ok := app.PprofAddress(); ok {
		t.Fatal("PprofAddress() remains enabled")
	}
	if err := app.StopPprof(context.Background()); err != nil {
		t.Fatalf("idempotent StopPprof() error = %v", err)
	}
	if err := app.StartPprof("127.0.0.1:0"); err != nil {
		t.Fatalf("restart StartPprof() error = %v", err)
	}
}

// TestPprofAndAdminAreIndependent 防止复用同一个 Server 导致任一 Stop 关闭另一端点。
func TestPprofAndAdminAreIndependent(t *testing.T) {
	app := newHTTPTestApplication(t)
	if err := app.freezeAdminRoutes(nil); err != nil {
		t.Fatal(err)
	}
	if err := app.StartAdminServer("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	if err := app.StartPprof("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	adminAddress, _ := app.AdminAddress()
	pprofAddress, _ := app.PprofAddress()
	full := app.Diagnostics()
	if full.SchemaVersion != 2 || full.Application.AdminServer.State != "serving" ||
		full.Application.DiagnosticsServer.State != "stopped" {
		t.Fatalf("Full Application compatibility snapshot = %+v", full.Application)
	}
	if adminAddress == pprofAddress {
		t.Fatalf("servers share address %q", adminAddress)
	}
	if err := app.StopPprof(context.Background()); err != nil {
		t.Fatal(err)
	}
	response, err := http.Get("http://" + adminAddress + "/admin/v1/diagnostics")
	if err != nil {
		t.Fatalf("Admin diagnostics after StopPprof error = %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("Admin diagnostics after StopPprof status = %d", response.StatusCode)
	}
}

// TestPprofValidation 固定空地址、nil Context 和未初始化生命周期的错误码。
func TestPprofValidation(t *testing.T) {
	app := newHTTPTestApplication(t)
	if err := app.StartPprof(""); !errors.Is(err, errs.ErrInvalidArgument) {
		t.Fatalf("empty StartPprof() error = %v", err)
	}
	if err := app.StopPprof(nil); !errors.Is(err, errs.ErrInvalidArgument) {
		t.Fatalf("nil StopPprof() error = %v", err)
	}
	if err := New().StartPprof("127.0.0.1:0"); !errors.Is(
		err,
		errs.ErrDiagnosticsStateConflict,
	) {
		t.Fatalf("created StartPprof() error = %v", err)
	}
}

// TestPprofDurationValidation 防止超大 seconds 在转换为 time.Duration 时回绕为负数，
// 导致原本应长期采集的 CPU Profile 或 Trace 立即返回一个误导性的成功结果。
func TestPprofDurationValidation(t *testing.T) {
	const overflowingSeconds = "9223372036854775807"
	mux := newPprofMux()
	for _, route := range []string{"profile", "trace"} {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(
			http.MethodGet,
			"http://origin.test/debug/pprof/"+route+"?seconds="+overflowingSeconds,
			nil,
		)
		mux.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("%s overflowing seconds status = %d, want 400", route, response.Code)
		}
	}

	maximumSeconds := int64(time.Duration(1<<63-1) / time.Second)
	request := httptest.NewRequest(http.MethodGet, "http://origin.test/debug/pprof/profile", nil)
	request.URL.RawQuery = "seconds=" + fmt.Sprint(maximumSeconds)
	duration, err := parsePositiveDuration(request, "seconds", time.Second)
	if err != nil || duration != time.Duration(maximumSeconds)*time.Second {
		t.Fatalf("maximum duration = %s, %v", duration, err)
	}
}

// TestPprofMuxProfileAndSymbolBoundaries 固定索引使用的每个 Runtime Profile 都真实注册，
// symbol 遵守 pprof 握手格式，并对超限 POST 明确失败而不是返回静默截断的部分结果。
func TestPprofMuxProfileAndSymbolBoundaries(t *testing.T) {
	mux := newPprofMux()
	for _, profile := range runtimepprof.Profiles() {
		request := httptest.NewRequest(
			http.MethodGet,
			"http://origin.test/debug/pprof/"+profile.Name(),
			nil,
		)
		_, pattern := mux.Handler(request)
		if pattern == "" {
			t.Errorf("Runtime profile %q is listed but not registered", profile.Name())
		}
	}

	symbol := httptest.NewRecorder()
	mux.ServeHTTP(
		symbol,
		httptest.NewRequest(http.MethodGet, "http://origin.test/debug/pprof/symbol", nil),
	)
	if symbol.Code != http.StatusOK || !strings.HasPrefix(symbol.Body.String(), "num_symbols: 1\n") {
		t.Fatalf("symbol handshake status=%d body=%q", symbol.Code, symbol.Body.String())
	}

	overflow := httptest.NewRecorder()
	mux.ServeHTTP(
		overflow,
		httptest.NewRequest(
			http.MethodPost,
			"http://origin.test/debug/pprof/symbol",
			strings.NewReader(strings.Repeat("1", pprofSymbolMaxBodyBytes+1)),
		),
	)
	if overflow.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("symbol overflow status = %d, want 413", overflow.Code)
	}
}

// TestPprofHandlers 验证私有 pprof Mux 的标准读取、方法限制、运行时互斥与取消分支。
// 底层 ResponseWriter/Runtime Profile 的写失败没有稳定公共注入点，不为覆盖率增加生产钩子。
func TestPprofHandlers(t *testing.T) {
	mux := newPprofMux()

	for _, test := range []struct {
		name        string
		method      string
		target      string
		wantStatus  int
		wantContent string
	}{
		{name: "index", method: http.MethodGet, target: "/debug/pprof/", wantStatus: http.StatusOK, wantContent: "Origin pprof"},
		{name: "index wrong method", method: http.MethodPost, target: "/debug/pprof/", wantStatus: http.StatusMethodNotAllowed},
		{name: "unknown index child", method: http.MethodGet, target: "/debug/pprof/missing", wantStatus: http.StatusNotFound},
		{name: "cmdline", method: http.MethodGet, target: "/debug/pprof/cmdline", wantStatus: http.StatusOK},
		{name: "cmdline wrong method", method: http.MethodPost, target: "/debug/pprof/cmdline", wantStatus: http.StatusMethodNotAllowed},
		{name: "named binary", method: http.MethodGet, target: "/debug/pprof/goroutine", wantStatus: http.StatusOK},
		{name: "named text", method: http.MethodGet, target: "/debug/pprof/goroutine?debug=1", wantStatus: http.StatusOK, wantContent: "goroutine profile"},
		{name: "named invalid debug", method: http.MethodGet, target: "/debug/pprof/goroutine?debug=-1", wantStatus: http.StatusBadRequest},
		{name: "named wrong method", method: http.MethodPost, target: "/debug/pprof/goroutine", wantStatus: http.StatusMethodNotAllowed},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			mux.ServeHTTP(response, httptest.NewRequest(test.method, "http://origin.test"+test.target, nil))
			if response.Code != test.wantStatus ||
				test.wantContent != "" && !strings.Contains(response.Body.String(), test.wantContent) {
				t.Fatalf("status=%d body=%q, want status=%d content=%q",
					response.Code, response.Body.String(), test.wantStatus, test.wantContent)
			}
		})
	}

	missing := httptest.NewRecorder()
	handleNamedProfile(
		missing,
		httptest.NewRequest(http.MethodGet, "http://origin.test/debug/pprof/missing", nil),
		"missing",
	)
	if missing.Code != http.StatusNotFound {
		t.Fatalf("missing named profile status = %d, want 404", missing.Code)
	}

	t.Run("CPU canceled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		response := httptest.NewRecorder()
		handlePprofCPU(
			response,
			httptest.NewRequest(http.MethodGet, "http://origin.test/debug/pprof/profile?seconds=1", nil).WithContext(ctx),
		)
		if response.Code != http.StatusOK || response.Body.Len() == 0 {
			t.Fatalf("canceled CPU profile status=%d bytes=%d", response.Code, response.Body.Len())
		}
	})

	t.Run("CPU already active", func(t *testing.T) {
		if err := runtimepprof.StartCPUProfile(io.Discard); err != nil {
			t.Fatal(err)
		}
		defer runtimepprof.StopCPUProfile()
		response := httptest.NewRecorder()
		handlePprofCPU(
			response,
			httptest.NewRequest(http.MethodGet, "http://origin.test/debug/pprof/profile?seconds=1", nil),
		)
		if response.Code != http.StatusInternalServerError {
			t.Fatalf("concurrent CPU profile status = %d, want 500", response.Code)
		}
	})

	t.Run("trace canceled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		response := httptest.NewRecorder()
		handlePprofTrace(
			response,
			httptest.NewRequest(http.MethodGet, "http://origin.test/debug/pprof/trace?seconds=1", nil).WithContext(ctx),
		)
		if response.Code != http.StatusOK || response.Body.Len() == 0 {
			t.Fatalf("canceled trace status=%d bytes=%d", response.Code, response.Body.Len())
		}
	})

	t.Run("trace already active", func(t *testing.T) {
		if err := trace.Start(io.Discard); err != nil {
			t.Fatal(err)
		}
		defer trace.Stop()
		response := httptest.NewRecorder()
		handlePprofTrace(
			response,
			httptest.NewRequest(http.MethodGet, "http://origin.test/debug/pprof/trace?seconds=1", nil),
		)
		if response.Code != http.StatusInternalServerError {
			t.Fatalf("concurrent trace status = %d, want 500", response.Code)
		}
	})

	for _, route := range []string{"profile", "trace"} {
		response := httptest.NewRecorder()
		mux.ServeHTTP(
			response,
			httptest.NewRequest(http.MethodPost, "http://origin.test/debug/pprof/"+route, nil),
		)
		if response.Code != http.StatusMethodNotAllowed || response.Header().Get("Allow") != http.MethodGet {
			t.Fatalf("POST %s status=%d Allow=%q", route, response.Code, response.Header().Get("Allow"))
		}
	}

	programCounter := reflect.ValueOf(handlePprofSymbol).Pointer()
	symbol := httptest.NewRecorder()
	mux.ServeHTTP(
		symbol,
		httptest.NewRequest(
			http.MethodPost,
			"http://origin.test/debug/pprof/symbol",
			strings.NewReader(fmt.Sprintf("%d+1", programCounter)),
		),
	)
	if symbol.Code != http.StatusOK || !strings.Contains(symbol.Body.String(), "handlePprofSymbol") {
		t.Fatalf("symbol lookup status=%d body=%q", symbol.Code, symbol.Body.String())
	}

	readFailure := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "http://origin.test/debug/pprof/symbol", nil)
	request.Body = failingAdminBody{}
	mux.ServeHTTP(readFailure, request)
	if readFailure.Code != http.StatusBadRequest {
		t.Fatalf("symbol read failure status = %d, want 400", readFailure.Code)
	}

	wrongSymbolMethod := httptest.NewRecorder()
	mux.ServeHTTP(
		wrongSymbolMethod,
		httptest.NewRequest(http.MethodPut, "http://origin.test/debug/pprof/symbol", nil),
	)
	if wrongSymbolMethod.Code != http.StatusMethodNotAllowed {
		t.Fatalf("PUT symbol status = %d, want 405", wrongSymbolMethod.Code)
	}
}

// TestPprofStopInterruptsActiveProfile 验证运行中的长 CPU Profile 不会让运行时关闭失效。
// Shutdown 期限耗尽后必须强制关闭连接，使 handler 观察请求取消并释放 Listener。
func TestPprofStopInterruptsActiveProfile(t *testing.T) {
	app := newHTTPTestApplication(t)
	if err := app.StartPprof("127.0.0.1:0"); err != nil {
		t.Fatalf("StartPprof() error = %v", err)
	}
	address, _ := app.PprofAddress()
	connection, err := net.DialTimeout("tcp", address, time.Second)
	if err != nil {
		t.Fatalf("dial pprof error = %v", err)
	}
	defer connection.Close()
	if _, err := io.WriteString(
		connection,
		"GET /debug/pprof/profile?seconds=30 HTTP/1.1\r\nHost: "+address+"\r\n\r\n",
	); err != nil {
		t.Fatalf("write pprof request error = %v", err)
	}
	// CPU Profile 启动不立即写响应体；给 handler 一个有界窗口进入采集，再触发关闭。
	time.Sleep(100 * time.Millisecond)
	stopCtx, cancelStop := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancelStop()
	if err := app.StopPprof(stopCtx); !errors.Is(err, errs.ErrDeadlineExceeded) {
		t.Fatalf("StopPprof(active profile) error = %v", err)
	}
	if _, ok := app.PprofAddress(); ok {
		t.Fatal("PprofAddress remains enabled after forced Stop")
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatalf("forced pprof Stop did not release %q: %v", address, err)
	}
	_ = listener.Close()
}
