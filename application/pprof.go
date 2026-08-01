package application

import (
	"context"
	"fmt"
	"html"
	"io"
	"net/http"
	"os"
	"runtime"
	runtimepprof "runtime/pprof"
	"runtime/trace"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/duanhf2012/origin/v3/errs"
	originlog "github.com/duanhf2012/origin/v3/log"
)

// StartPprof 在 Application 私有 Listener 上显式安装 Go Runtime Profile 路由。
//
// 实现直接使用 runtime/pprof 和 runtime/trace，避免 net/http/pprof 的 init 把路由注册到
// http.DefaultServeMux。CPU Profile 和 Trace 仍遵守 Go Runtime 的进程级互斥约束。
func (app *Application) StartPprof(address string) error {
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

	server := &http.Server{
		Handler:           newPprofMux(),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
	err := app.pprofHTTP.start(address, server)
	app.mu.Unlock()
	if err == nil && !isLoopbackAddress(address) {
		logger.Warn(
			"pprof server is listening on a non-loopback address without built-in TLS or authentication",
			originlog.String("address", address),
		)
	}
	return err
}

// StopPprof 停止接受新的 Profile 请求，并在 ctx 内等待当前采集退出。
func (app *Application) StopPprof(ctx context.Context) error {
	if app == nil || ctx == nil {
		return errs.ErrInvalidArgument
	}
	return app.pprofHTTP.stop(ctx)
}

// PprofAddress 返回当前 pprof Listener 的实际地址。
func (app *Application) PprofAddress() (string, bool) {
	if app == nil {
		return "", false
	}
	return app.pprofHTTP.addressSnapshot()
}

func newPprofMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/debug/pprof/", handlePprofIndex)
	mux.HandleFunc("/debug/pprof/cmdline", handlePprofCmdline)
	mux.HandleFunc("/debug/pprof/profile", handlePprofCPU)
	mux.HandleFunc("/debug/pprof/symbol", handlePprofSymbol)
	mux.HandleFunc("/debug/pprof/trace", handlePprofTrace)
	for _, name := range []string{
		"allocs",
		"block",
		"goroutine",
		"heap",
		"mutex",
		"threadcreate",
	} {
		profileName := name
		mux.HandleFunc("/debug/pprof/"+profileName, func(
			response http.ResponseWriter,
			request *http.Request,
		) {
			handleNamedProfile(response, request, profileName)
		})
	}
	return mux
}

func handlePprofIndex(response http.ResponseWriter, request *http.Request) {
	if !requireMethod(response, request, http.MethodGet) {
		return
	}
	if request.URL.Path != "/debug/pprof/" {
		http.NotFound(response, request)
		return
	}
	profiles := runtimepprof.Profiles()
	sort.Slice(profiles, func(left, right int) bool {
		return profiles[left].Name() < profiles[right].Name()
	})
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = io.WriteString(response, "<!doctype html><html><body><h1>Origin pprof</h1><ul>")
	for _, profile := range profiles {
		name := html.EscapeString(profile.Name())
		_, _ = fmt.Fprintf(
			response,
			"<li><a href=\"/debug/pprof/%s?debug=1\">%s</a> (%d)</li>",
			name,
			name,
			profile.Count(),
		)
	}
	_, _ = io.WriteString(response, "</ul></body></html>")
}

func handlePprofCmdline(response http.ResponseWriter, request *http.Request) {
	if !requireMethod(response, request, http.MethodGet) {
		return
	}
	response.Header().Set("Content-Type", "application/octet-stream")
	_, _ = io.WriteString(response, strings.Join(os.Args, "\x00"))
}

func handleNamedProfile(
	response http.ResponseWriter,
	request *http.Request,
	name string,
) {
	if !requireMethod(response, request, http.MethodGet) {
		return
	}
	profile := runtimepprof.Lookup(name)
	if profile == nil {
		http.NotFound(response, request)
		return
	}
	debugLevel, err := parseNonNegativeInt(request, "debug", 0)
	if err != nil {
		http.Error(response, err.Error(), http.StatusBadRequest)
		return
	}
	if debugLevel == 0 {
		response.Header().Set("Content-Type", "application/octet-stream")
		response.Header().Set(
			"Content-Disposition",
			fmt.Sprintf("attachment; filename=\"%s\"", name),
		)
	} else {
		response.Header().Set("Content-Type", "text/plain; charset=utf-8")
	}
	if err := profile.WriteTo(response, debugLevel); err != nil {
		return
	}
}

func handlePprofCPU(response http.ResponseWriter, request *http.Request) {
	if !requireMethod(response, request, http.MethodGet) {
		return
	}
	seconds, err := parsePositiveInt(request, "seconds", 30)
	if err != nil {
		http.Error(response, err.Error(), http.StatusBadRequest)
		return
	}
	response.Header().Set("Content-Type", "application/octet-stream")
	response.Header().Set("Content-Disposition", "attachment; filename=\"profile\"")
	if err := runtimepprof.StartCPUProfile(response); err != nil {
		http.Error(response, err.Error(), http.StatusInternalServerError)
		return
	}
	timer := time.NewTimer(time.Duration(seconds) * time.Second)
	select {
	case <-timer.C:
	case <-request.Context().Done():
		if !timer.Stop() {
			<-timer.C
		}
	}
	runtimepprof.StopCPUProfile()
}

func handlePprofTrace(response http.ResponseWriter, request *http.Request) {
	if !requireMethod(response, request, http.MethodGet) {
		return
	}
	seconds, err := parsePositiveInt(request, "seconds", 1)
	if err != nil {
		http.Error(response, err.Error(), http.StatusBadRequest)
		return
	}
	response.Header().Set("Content-Type", "application/octet-stream")
	response.Header().Set("Content-Disposition", "attachment; filename=\"trace\"")
	if err := trace.Start(response); err != nil {
		http.Error(response, err.Error(), http.StatusInternalServerError)
		return
	}
	timer := time.NewTimer(time.Duration(seconds) * time.Second)
	select {
	case <-timer.C:
	case <-request.Context().Done():
		if !timer.Stop() {
			<-timer.C
		}
	}
	trace.Stop()
}

func handlePprofSymbol(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet && request.Method != http.MethodPost {
		response.Header().Set("Allow", http.MethodGet+", "+http.MethodPost)
		http.Error(response, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}
	var source string
	if request.Method == http.MethodPost {
		payload, err := io.ReadAll(io.LimitReader(request.Body, 1<<20))
		if err != nil {
			http.Error(response, err.Error(), http.StatusBadRequest)
			return
		}
		source = string(payload)
	} else {
		source = request.URL.RawQuery
	}
	response.Header().Set("Content-Type", "text/plain; charset=utf-8")
	for _, token := range strings.FieldsFunc(source, func(value rune) bool {
		return value == '+' || value == ' ' || value == '\n' || value == '\r' || value == '\t'
	}) {
		address, err := strconv.ParseUint(token, 0, 64)
		if err != nil {
			continue
		}
		name := "unknown"
		if function := runtime.FuncForPC(uintptr(address)); function != nil {
			name = function.Name()
		}
		_, _ = fmt.Fprintf(response, "%s %s\n", token, name)
	}
}

func requireMethod(
	response http.ResponseWriter,
	request *http.Request,
	method string,
) bool {
	if request.Method == method {
		return true
	}
	response.Header().Set("Allow", method)
	http.Error(response, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
	return false
}

func parsePositiveInt(request *http.Request, name string, fallback int) (int, error) {
	value := request.URL.Query().Get(name)
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer", name)
	}
	return parsed, nil
}

func parseNonNegativeInt(request *http.Request, name string, fallback int) (int, error) {
	value := request.URL.Query().Get(name)
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 0 {
		return 0, fmt.Errorf("%s must be a non-negative integer", name)
	}
	return parsed, nil
}
