package application

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"testing"

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
