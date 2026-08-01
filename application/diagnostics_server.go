package application

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/duanhf2012/origin/v3/errs"
	originlog "github.com/duanhf2012/origin/v3/log"
)

const diagnosticsPath = "/debug/origin/diagnostics"

// StartDiagnosticsServer 在独立私有 Listener 上提供当前 Application 诊断 JSON。
func (app *Application) StartDiagnosticsServer(address string) error {
	if app == nil || strings.TrimSpace(address) == "" {
		return errs.ErrInvalidArgument
	}
	address = strings.TrimSpace(address)
	app.mu.Lock()
	state := app.State()
	allowed := app.resourcesReady && !app.resourcesClosing &&
		(state == StateStarting || state == StateRunning)
	logger := app.logger
	if !allowed {
		app.mu.Unlock()
		return errs.ErrDiagnosticsStateConflict
	}

	mux := http.NewServeMux()
	mux.HandleFunc(diagnosticsPath, app.handleDiagnostics)
	server := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
	err := app.diagnosticsHTTP.start(address, server)
	app.mu.Unlock()
	if err == nil && !isLoopbackAddress(address) {
		logger.Warn(
			"diagnostics server is listening on a non-loopback address without built-in TLS or authentication",
			originlog.String("address", address),
		)
	}
	return err
}

// StopDiagnosticsServer 停止接收新诊断请求，并在 ctx 内等待已有请求排空。
func (app *Application) StopDiagnosticsServer(ctx context.Context) error {
	if app == nil || ctx == nil {
		return errs.ErrInvalidArgument
	}
	return app.diagnosticsHTTP.stop(ctx)
}

// DiagnosticsAddress 返回当前正在监听的实际地址；`:0` 已展开为操作系统分配端口。
func (app *Application) DiagnosticsAddress() (string, bool) {
	if app == nil {
		return "", false
	}
	return app.diagnosticsHTTP.addressSnapshot()
}

func (app *Application) handleDiagnostics(
	response http.ResponseWriter,
	request *http.Request,
) {
	if request.Method != http.MethodGet {
		response.Header().Set("Allow", http.MethodGet)
		http.Error(response, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(response).Encode(app.Diagnostics())
}

func isLoopbackAddress(address string) bool {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
