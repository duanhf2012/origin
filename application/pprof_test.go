package application

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
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

// TestPprofAndDiagnosticsAreIndependent 防止复用同一个 Server 导致任一 Stop 关闭另一端点。
func TestPprofAndDiagnosticsAreIndependent(t *testing.T) {
	app := newHTTPTestApplication(t)
	if err := app.StartDiagnosticsServer("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	if err := app.StartPprof("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	diagnosticsAddress, _ := app.DiagnosticsAddress()
	pprofAddress, _ := app.PprofAddress()
	if diagnosticsAddress == pprofAddress {
		t.Fatalf("servers share address %q", diagnosticsAddress)
	}
	if err := app.StopPprof(context.Background()); err != nil {
		t.Fatal(err)
	}
	response, err := http.Get("http://" + diagnosticsAddress + diagnosticsPath)
	if err != nil {
		t.Fatalf("Diagnostics after StopPprof error = %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("Diagnostics after StopPprof status = %d", response.StatusCode)
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
