package application

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/duanhf2012/origin/v3/admin"
	"github.com/duanhf2012/origin/v3/errs"
	"github.com/duanhf2012/origin/v3/node"
)

// newAdminHTTPTestApplication 建立真实运行期状态，并保证每个测试退出前回收 Admin Listener。
func newAdminHTTPTestApplication(t *testing.T) *Application {
	t.Helper()
	app := newHTTPTestApplication(t)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := app.StopAdminServer(ctx); err != nil {
			t.Errorf("StopAdminServer() cleanup error = %v", err)
		}
	})
	return app
}

// TestAdminServerRuntimeLifecycle 防止 :0 实际地址丢失、重复启动新建 Listener、停止后端口
// 泄漏或把管理路由注册进进程 DefaultServeMux。
func TestAdminServerRuntimeLifecycle(t *testing.T) {
	app := newAdminHTTPTestApplication(t)
	if _, ok := app.AdminAddress(); ok {
		t.Fatal("AdminAddress() unexpectedly enabled")
	}

	// 记录进程默认 Mux 的匹配结果；Admin Server 必须始终使用自己的私有 Mux。
	probe, _ := http.NewRequest(http.MethodGet, "/admin/v1/private-mux-probe", nil)
	_, patternBefore := http.DefaultServeMux.Handler(probe)
	if err := app.StartAdminServer("127.0.0.1:0"); err != nil {
		t.Fatalf("StartAdminServer() error = %v", err)
	}
	address, ok := app.AdminAddress()
	if !ok || address == "" || address == "127.0.0.1:0" {
		t.Fatalf("AdminAddress() = %q, %v", address, ok)
	}
	_, patternAfter := http.DefaultServeMux.Handler(probe)
	if patternAfter != patternBefore {
		t.Fatalf("DefaultServeMux pattern changed from %q to %q", patternBefore, patternAfter)
	}

	// 同一请求地址和实际地址均属于同一个 Listener，不得发生第二次绑定。
	if err := app.StartAdminServer("127.0.0.1:0"); err != nil {
		t.Fatalf("same requested address StartAdminServer() error = %v", err)
	}
	if err := app.StartAdminServer(address); err != nil {
		t.Fatalf("actual address StartAdminServer() error = %v", err)
	}
	if got, _ := app.AdminAddress(); got != address {
		t.Fatalf("idempotent address = %q, want %q", got, address)
	}
	if err := app.StartAdminServer("127.0.0.1:1"); !errors.Is(
		err,
		errs.ErrAdminStateConflict,
	) {
		t.Fatalf("different-address error = %v", err)
	}

	// Task 4 尚不安装业务路由；私有空 Mux 对保留路径稳定返回 404。
	response, err := http.Get("http://" + address + "/admin/v1/private-mux-probe")
	if err != nil {
		t.Fatalf("GET private Admin mux error = %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("private Admin mux status = %d, want 404", response.StatusCode)
	}

	// Stop 必须等待 Serve 退出并释放端口；重复 Stop 和随后 Restart 都保持幂等。
	if err := app.StopAdminServer(context.Background()); err != nil {
		t.Fatalf("StopAdminServer() error = %v", err)
	}
	if _, ok := app.AdminAddress(); ok {
		t.Fatal("AdminAddress() remains enabled after Stop")
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatalf("released address cannot be rebound: %v", err)
	}
	_ = listener.Close()
	if err := app.StopAdminServer(context.Background()); err != nil {
		t.Fatalf("idempotent StopAdminServer() error = %v", err)
	}
	if err := app.StartAdminServer("127.0.0.1:0"); err != nil {
		t.Fatalf("restart StartAdminServer() error = %v", err)
	}
}

// TestAdminServerLifecycleErrors 固定 Admin 生命周期使用自己的错误族，不伪装成 Diagnostics。
func TestAdminServerLifecycleErrors(t *testing.T) {
	var nilApplication *Application
	if err := nilApplication.StartAdminServer("127.0.0.1:0"); !errors.Is(
		err,
		errs.ErrInvalidArgument,
	) {
		t.Fatalf("nil Application StartAdminServer() error = %v", err)
	}
	if err := nilApplication.StopAdminServer(context.Background()); !errors.Is(
		err,
		errs.ErrInvalidArgument,
	) {
		t.Fatalf("nil Application StopAdminServer() error = %v", err)
	}
	if address, ok := nilApplication.AdminAddress(); ok || address != "" {
		t.Fatalf("nil Application AdminAddress() = %q, %v", address, ok)
	}
	if err := New().StartAdminServer("127.0.0.1:0"); !errors.Is(
		err,
		errs.ErrAdminStateConflict,
	) {
		t.Fatalf("created Application StartAdminServer() error = %v", err)
	}
	app := newAdminHTTPTestApplication(t)
	if err := app.StartAdminServer(""); !errors.Is(err, errs.ErrInvalidArgument) {
		t.Fatalf("empty address error = %v", err)
	}
	if err := app.StartAdminServer("unique-secret-marker"); !errors.Is(err, errs.ErrInvalidArgument) ||
		strings.Contains(err.Error(), "unique-secret-marker") {
		t.Fatalf("malformed address error leaked input = %v", err)
	}
	if err := app.StopAdminServer(nil); !errors.Is(err, errs.ErrInvalidArgument) {
		t.Fatalf("nil Stop context error = %v", err)
	}

	// 真实占用环回端口，验证 Listen 失败保留 AdminUnavailable 且不会发布半成品地址。
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	if err := app.StartAdminServer(occupied.Addr().String()); !errors.Is(
		err,
		errs.ErrAdminUnavailable,
	) || strings.Contains(err.Error(), occupied.Addr().String()) {
		t.Fatalf("occupied address error = %v", err)
	}
	if _, ok := app.AdminAddress(); ok {
		t.Fatal("failed StartAdminServer() published an address")
	}
}

// TestAdminServerRejectsStartDuringRouteFreeze 防止 Listener 捕获尚未发布的 nil 路由表，
// 并证明 Start 不等待正在执行的 Provider，避免 Provider 自调用产生启动死锁。
func TestAdminServerRejectsStartDuringRouteFreeze(t *testing.T) {
	app := New()
	if err := app.RegisterAdminEndpoint(admin.Get("ready", func(
		context.Context,
		admin.Request,
	) (admin.Response, error) {
		return admin.JSON(http.StatusOK, map[string]bool{"ready": true})
	})); err != nil {
		t.Fatalf("RegisterAdminEndpoint() error = %v", err)
	}
	providerEntered := make(chan struct{}, 1)
	providerRelease := make(chan struct{})
	target := &adminRegistryProviderService{
		providerEntered: providerEntered,
		providerRelease: providerRelease,
	}
	current := newAdminRegistryNode(t, app, "node-freeze", "actual-service", target)
	app.mu.Lock()
	app.nodes = []*node.Node{current}
	app.resourcesReady = true
	app.state.Store(uint32(StateRunning))
	app.mu.Unlock()

	freezeDone := make(chan error, 1)
	go func() { freezeDone <- app.freezeAdminRoutes([]*node.Node{current}) }()
	<-providerEntered
	if err := app.StartAdminServer("127.0.0.1:0"); !errors.Is(err, errs.ErrAdminStateConflict) {
		close(providerRelease)
		<-freezeDone
		t.Fatalf("StartAdminServer(during freeze) error = %v", err)
	}
	if _, ok := app.AdminAddress(); ok {
		close(providerRelease)
		<-freezeDone
		t.Fatal("StartAdminServer published Listener during route freeze")
	}
	close(providerRelease)
	if err := <-freezeDone; err != nil {
		t.Fatalf("freezeAdminRoutes() error = %v", err)
	}
	if err := app.StartAdminServer("127.0.0.1:0"); err != nil {
		t.Fatalf("StartAdminServer(after freeze) error = %v", err)
	}
	t.Cleanup(func() { _ = app.StopAdminServer(context.Background()) })
	address, _ := app.AdminAddress()
	response, err := http.Get("http://" + address + "/admin/v1/application/endpoints/ready")
	if err != nil {
		t.Fatalf("GET frozen Application route error = %v", err)
	}
	body, readErr := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if readErr != nil || response.StatusCode != http.StatusOK || string(body) != `{"ready":true}` {
		t.Fatalf("frozen route status=%d read=%v body=%s", response.StatusCode, readErr, body)
	}
}

// failingAdminBody 稳定制造底层请求体读取错误，不依赖网络故障或 ContentLength。
type failingAdminBody struct{}

// Read 返回固定错误，验证 Runtime 将底层读取失败安全映射为 400。
func (failingAdminBody) Read([]byte) (int, error) { return 0, errors.New("private read failure") }

// Close 没有外部资源。
func (failingAdminBody) Close() error { return nil }

// TestAdminRequestReadFailure 防止底层 Body 读取错误执行 Handler或向响应泄露内部原因。
func TestAdminRequestReadFailure(t *testing.T) {
	invoked := false
	endpoint := admin.Post("reload", func(context.Context, admin.Request) (admin.Response, error) {
		invoked = true
		return admin.Response{}, nil
	})
	request := httptest.NewRequest(http.MethodPost, "/admin/v1/reload", nil)
	request.Body = failingAdminBody{}
	request.ContentLength = -1
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	New().serveAdminEndpoint(
		response,
		request,
		admin.Operation{Method: http.MethodPost, Endpoint: "reload"},
		endpoint,
		endpoint.Invoke,
	)
	if invoked || response.Code != http.StatusBadRequest {
		t.Fatalf("invoked=%v status=%d, want false/400", invoked, response.Code)
	}
	if strings.Contains(response.Body.String(), "private read failure") {
		t.Fatalf("response leaked Body read error: %q", response.Body)
	}
}

// TestAdminServerHTTPConfiguration 锁定写控制面的 Header/Write/Idle 边界，避免复用其他 Server
// 时意外继承更宽松或无界配置。
func TestAdminServerHTTPConfiguration(t *testing.T) {
	app := newAdminHTTPTestApplication(t)
	if err := app.StartAdminServer("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	app.adminHTTP.mu.Lock()
	server := app.adminHTTP.server
	app.adminHTTP.mu.Unlock()
	if server == nil || server.Handler == nil ||
		server.ErrorLog == nil || server.ErrorLog.Writer() != io.Discard ||
		server.ReadHeaderTimeout != 5*time.Second ||
		server.WriteTimeout != 20*time.Second ||
		server.IdleTimeout != 60*time.Second ||
		server.MaxHeaderBytes != 1<<20 {
		t.Fatalf("Admin HTTP Server config = %+v", server)
	}
}

// TestAdminBufferedResponseCapacityBound catches both append-after-overflow and
// bytes.Buffer-style capacity growth beyond the configured hard limit.
func TestAdminBufferedResponseCapacityBound(t *testing.T) {
	response := newAdminBufferedResponse(8)
	if written, err := response.Write([]byte("12345")); err != nil || written != 5 {
		t.Fatalf("first Write = (%d, %v), want (5, nil)", written, err)
	}
	if length, capacity := len(response.body), cap(response.body); length != 5 || capacity > 8 {
		t.Fatalf("after first Write len/cap = %d/%d, want 5/cap<=8", length, capacity)
	}
	if written, err := response.Write([]byte("6789")); err == nil || written != 0 {
		t.Fatalf("overflow Write = (%d, %v), want (0, error)", written, err)
	}
	if length, capacity := len(response.body), cap(response.body); length != 5 || capacity > 8 {
		t.Fatalf("after overflow len/cap = %d/%d, want unchanged 5/cap<=8", length, capacity)
	}
}

// TestAdminBufferedResponseHeaderSnapshot catches live Header aliasing after the
// first explicit or implicit final response, and repeated final status changes.
func TestAdminBufferedResponseHeaderSnapshot(t *testing.T) {
	t.Run("explicit final header", func(t *testing.T) {
		response := newAdminBufferedResponse(int(admin.DefaultMaxResponseBytes))
		response.Header().Set("X-Result", "before")
		response.Header()["X-Multi"] = []string{"one", "two"}
		response.WriteHeader(http.StatusCreated)
		response.Header().Set("X-Result", "after")
		response.Header()["X-Multi"][0] = "mutated"
		response.WriteHeader(http.StatusAccepted)

		target := httptest.NewRecorder()
		response.commit(target)
		if target.Code != http.StatusCreated {
			t.Fatalf("status = %d, want 201", target.Code)
		}
		if got := target.Header().Get("X-Result"); got != "before" {
			t.Fatalf("committed Header = %q, want before", got)
		}
		if got := target.Header().Values("X-Multi"); len(got) != 2 || got[0] != "one" || got[1] != "two" {
			t.Fatalf("committed multi Header = %q, want [one two]", got)
		}
	})

	t.Run("implicit final header", func(t *testing.T) {
		response := newAdminBufferedResponse(int(admin.DefaultMaxResponseBytes))
		response.Header().Set("X-Result", "before")
		if written, err := response.Write([]byte("ok")); err != nil || written != 2 {
			t.Fatalf("Write = (%d, %v), want (2, nil)", written, err)
		}
		response.Header().Set("X-Result", "after")

		target := httptest.NewRecorder()
		response.commit(target)
		if target.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", target.Code)
		}
		if got := target.Header().Get("X-Result"); got != "before" {
			t.Fatalf("committed Header = %q, want before", got)
		}
	})

	t.Run("informational header remains mutable", func(t *testing.T) {
		response := newAdminBufferedResponse(int(admin.DefaultMaxResponseBytes))
		response.Header().Set("X-Result", "informational")
		response.WriteHeader(http.StatusEarlyHints)
		response.Header().Set("X-Result", "final")
		response.WriteHeader(http.StatusCreated)

		target := httptest.NewRecorder()
		response.commit(target)
		if target.Code != http.StatusCreated || target.Header().Get("X-Result") != "final" {
			t.Fatalf("final response = status %d Header %q, want 201/final",
				target.Code, target.Header().Get("X-Result"))
		}
	})
}

// TestAdminServerConcurrentLifecycle 固定并发同地址 Start 只发布一个 Listener，并发 Stop 都等待
// 同一资源退出；该路径由 Race 门禁验证锁顺序和终态可见性。
func TestAdminServerConcurrentLifecycle(t *testing.T) {
	app := newAdminHTTPTestApplication(t)
	startErrors := make(chan error, 8)
	var starts sync.WaitGroup
	starts.Add(8)
	for range 8 {
		go func() {
			defer starts.Done()
			startErrors <- app.StartAdminServer("127.0.0.1:0")
		}()
	}
	starts.Wait()
	close(startErrors)
	for err := range startErrors {
		if err != nil {
			t.Fatalf("concurrent StartAdminServer() error = %v", err)
		}
	}
	address, ok := app.AdminAddress()
	if !ok {
		t.Fatal("concurrent Start did not publish an address")
	}

	stopErrors := make(chan error, 8)
	var stops sync.WaitGroup
	stops.Add(8)
	for range 8 {
		go func() {
			defer stops.Done()
			stopErrors <- app.StopAdminServer(context.Background())
		}()
	}
	stops.Wait()
	close(stopErrors)
	for err := range stopErrors {
		if err != nil {
			t.Fatalf("concurrent StopAdminServer() error = %v", err)
		}
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatalf("concurrent Stop did not release address: %v", err)
	}
	_ = listener.Close()
}

// TestAdminServerStartRuntimeLockOrder deterministically parks Start after it
// owns operationMu but before runtime.mu, then proves a request handler may still
// call app.Node while a concurrent Stop queues behind Start.
func TestAdminServerStartRuntimeLockOrder(t *testing.T) {
	app := newAdminHTTPTestApplication(t)
	app.adminHTTP.mu.Lock()
	runtimeLocked := true
	startDone := make(chan error, 1)
	go func() {
		startDone <- app.StartAdminServer("127.0.0.1:0")
	}()
	stopDone := make(chan error, 1)
	stopStarted := false

	defer func() {
		if runtimeLocked {
			app.adminHTTP.mu.Unlock()
		}
		startErr := <-startDone
		if startErr != nil {
			t.Errorf("StartAdminServer() error = %v", startErr)
		}
		if stopStarted {
			if stopErr := <-stopDone; stopErr != nil {
				t.Errorf("StopAdminServer() error = %v", stopErr)
			}
		}
	}()

	observeDeadline := time.NewTimer(time.Second)
	defer observeDeadline.Stop()
	for {
		if !app.adminHTTP.operationMu.TryLock() {
			break
		}
		app.adminHTTP.operationMu.Unlock()
		select {
		case <-observeDeadline.C:
			t.Fatal("StartAdminServer did not reach the runtime lock barrier")
		default:
			runtime.Gosched()
		}
	}
	go func() {
		stopDone <- app.StopAdminServer(context.Background())
	}()
	stopStarted = true

	nodeReturned := make(chan struct{})
	handlerDone := make(chan struct{})
	handler := app.adminHTTPBoundary(http.HandlerFunc(func(
		w http.ResponseWriter,
		_ *http.Request,
	) {
		_, _ = app.Node("missing")
		w.WriteHeader(http.StatusNoContent)
		close(nodeReturned)
	}))
	go func() {
		handler.ServeHTTP(
			httptest.NewRecorder(),
			httptest.NewRequest(http.MethodGet, "/node", nil),
		)
		close(handlerDone)
	}()
	select {
	case <-nodeReturned:
	case <-time.After(time.Second):
		t.Error("Handler app.Node blocked while StartAdminServer waited on runtime.mu")
	}

	app.adminHTTP.mu.Unlock()
	runtimeLocked = false
	<-handlerDone
}

// TestAdminRequestBoundaries 防止 GET 只看 ContentLength、POST 接受非 JSON、Body 上限失效，
// 或 Handler 未主动 DecodeJSON 时绕过严格单值 JSON 校验。
func TestAdminRequestBoundaries(t *testing.T) {
	tests := []struct {
		name          string
		method        string
		contentType   string
		body          string
		contentLength int64
		maxBodyBytes  int64
		wantStatus    int
		wantInvoked   bool
	}{
		{
			name:          "get chunked body",
			method:        http.MethodGet,
			body:          "{}",
			contentLength: -1,
			wantStatus:    http.StatusBadRequest,
		},
		{
			name:       "post missing content type",
			method:     http.MethodPost,
			body:       "{}",
			wantStatus: http.StatusUnsupportedMediaType,
		},
		{
			name:        "post non json content type",
			method:      http.MethodPost,
			contentType: "text/plain",
			body:        "{}",
			wantStatus:  http.StatusUnsupportedMediaType,
		},
		{
			name:        "post malformed content type",
			method:      http.MethodPost,
			contentType: "application/json; charset",
			body:        "{}",
			wantStatus:  http.StatusUnsupportedMediaType,
		},
		{
			name:         "post body too large",
			method:       http.MethodPost,
			contentType:  "application/json",
			body:         `{"value":1}`,
			maxBodyBytes: 4,
			wantStatus:   http.StatusRequestEntityTooLarge,
		},
		{
			name:        "post malformed json",
			method:      http.MethodPost,
			contentType: "application/json",
			body:        "not-json",
			wantStatus:  http.StatusBadRequest,
		},
		{
			name:        "post second json value",
			method:      http.MethodPost,
			contentType: "application/json",
			body:        `{} {"second":true}`,
			wantStatus:  http.StatusBadRequest,
		},
		{
			name:          "post json media type parameters",
			method:        http.MethodPost,
			contentType:   "application/json; charset=utf-8",
			body:          `{"value":1}`,
			maxBodyBytes:  64,
			wantStatus:    http.StatusNoContent,
			wantInvoked:   true,
			contentLength: int64(len(`{"value":1}`)),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			app := New()
			invoked := false
			handler := func(_ context.Context, request admin.Request) (admin.Response, error) {
				invoked = true
				if test.wantInvoked && string(request.Body()) != test.body {
					t.Fatalf("Handler Body = %q, want %q", request.Body(), test.body)
				}
				return admin.Response{}, nil
			}
			var endpoint admin.Endpoint
			if test.method == http.MethodGet {
				endpoint = admin.Get("summary", handler)
			} else if test.maxBodyBytes > 0 {
				endpoint = admin.Post("reload", handler, admin.WithMaxBodyBytes(test.maxBodyBytes))
			} else {
				endpoint = admin.Post("reload", handler)
			}
			request := httptest.NewRequest(test.method, "/admin/v1/test", strings.NewReader(test.body))
			request.ContentLength = test.contentLength
			if test.contentType != "" {
				request.Header.Set("Content-Type", test.contentType)
			}
			response := httptest.NewRecorder()
			app.serveAdminEndpoint(
				response,
				request,
				admin.Operation{Method: test.method, Endpoint: endpoint.Name()},
				endpoint,
				endpoint.Invoke,
			)

			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d; body=%q", response.Code, test.wantStatus, response.Body)
			}
			if invoked != test.wantInvoked {
				t.Fatalf("Handler invoked = %v, want %v", invoked, test.wantInvoked)
			}
		})
	}
}

// TestAdminRequestQueryParsing 固定统一 Endpoint 边界使用 url.ParseQuery 的完整成功或失败
// 语义：Guard 先执行，malformed query 返回固定 400 且 partial values 不得进入 Handler；合法
// 加号、百分号和多值仍按标准库结果交付。
func TestAdminRequestQueryParsing(t *testing.T) {
	malformed := []string{
		"detail=full;unknown=x",
		"detail=full&unknown=x;bad",
		"bad=%zz",
		"bad=%",
		"good=value&bad=%GG",
	}
	for _, rawQuery := range malformed {
		t.Run("malformed "+rawQuery, func(t *testing.T) {
			app := New()
			var guardCalls atomic.Int64
			var handlerCalls atomic.Int64
			app.adminGuard = adminGuardFunc(func(
				context.Context,
				*http.Request,
				admin.Operation,
			) (admin.Principal, error) {
				guardCalls.Add(1)
				return admin.Principal{Subject: "operator"}, nil
			})
			endpoint := admin.Get("query", func(
				context.Context,
				admin.Request,
			) (admin.Response, error) {
				handlerCalls.Add(1)
				return admin.Empty(http.StatusNoContent), nil
			})
			request := httptest.NewRequest(http.MethodGet, "/admin/v1/query", nil)
			request.URL.RawQuery = rawQuery
			response := httptest.NewRecorder()
			app.serveAdminEndpoint(
				response,
				request,
				admin.Operation{},
				endpoint,
				endpoint.Invoke,
			)
			if response.Code != http.StatusBadRequest ||
				response.Body.String() != http.StatusText(http.StatusBadRequest)+"\n" ||
				guardCalls.Load() != 1 || handlerCalls.Load() != 0 {
				t.Fatalf("rawQuery=%q status=%d Body=%q guard=%d handler=%d",
					rawQuery, response.Code, response.Body.String(),
					guardCalls.Load(), handlerCalls.Load())
			}
		})
	}

	legal := []struct {
		name        string
		rawQuery    string
		wantEncoded string
	}{
		{name: "empty"},
		{name: "plus as space", rawQuery: "term=a+b", wantEncoded: "term=a+b"},
		{name: "escaped plus", rawQuery: "term=%2B", wantEncoded: "term=%2B"},
		{name: "multi value", rawQuery: "tag=one&tag=two", wantEncoded: "tag=one&tag=two"},
	}
	for _, test := range legal {
		t.Run(test.name, func(t *testing.T) {
			app := New()
			var gotEncoded string
			endpoint := admin.Get("query", func(
				_ context.Context,
				request admin.Request,
			) (admin.Response, error) {
				gotEncoded = request.Query().Encode()
				return admin.Empty(http.StatusNoContent), nil
			})
			request := httptest.NewRequest(http.MethodGet, "/admin/v1/query", nil)
			request.URL.RawQuery = test.rawQuery
			response := httptest.NewRecorder()
			app.serveAdminEndpoint(
				response,
				request,
				admin.Operation{},
				endpoint,
				endpoint.Invoke,
			)
			if response.Code != http.StatusNoContent || gotEncoded != test.wantEncoded {
				t.Fatalf("rawQuery=%q status=%d query=%q, want %q",
					test.rawQuery, response.Code, gotEncoded, test.wantEncoded)
			}
		})
	}
}

// TestAdminRouteMethodBoundary 固定未知路径为 404，已知路径的错误方法为 405 且 Allow 只列出
// Endpoint 的 GET/POST，不把授权操作扩展为另一套 Action 分类。
func TestAdminRouteMethodBoundary(t *testing.T) {
	app := New()
	endpoint := admin.Get("summary", func(context.Context, admin.Request) (admin.Response, error) {
		return admin.Response{}, nil
	})
	mux := http.NewServeMux()
	mux.HandleFunc("/admin/v1/summary", func(w http.ResponseWriter, r *http.Request) {
		app.serveAdminEndpoint(
			w,
			r,
			admin.Operation{Method: http.MethodGet, Endpoint: "summary"},
			endpoint,
			endpoint.Invoke,
		)
	})

	unknown := httptest.NewRecorder()
	mux.ServeHTTP(unknown, httptest.NewRequest(http.MethodGet, "/admin/v1/unknown", nil))
	if unknown.Code != http.StatusNotFound {
		t.Fatalf("unknown status = %d, want 404", unknown.Code)
	}

	wrongMethod := httptest.NewRecorder()
	mux.ServeHTTP(wrongMethod, httptest.NewRequest(http.MethodPost, "/admin/v1/summary", nil))
	if wrongMethod.Code != http.StatusMethodNotAllowed ||
		wrongMethod.Header().Get("Allow") != http.MethodGet {
		t.Fatalf("wrong method status=%d Allow=%q", wrongMethod.Code, wrongMethod.Header().Get("Allow"))
	}
}

// TestAdminResponseBoundary 防止超限/非法/错误响应在完成检查前写入业务 Header、状态或内容。
func TestAdminResponseBoundary(t *testing.T) {
	tests := []struct {
		name       string
		endpoint   admin.Endpoint
		wantStatus int
		wantBody   string
	}{
		{
			name: "response too large",
			endpoint: admin.Get("large", func(context.Context, admin.Request) (admin.Response, error) {
				return admin.JSON(http.StatusOK, map[string]string{"secret": "response-secret"})
			}, admin.WithMaxResponseBytes(3)),
			wantStatus: http.StatusInternalServerError,
		},
		{
			name: "invalid explicit status",
			endpoint: admin.Get("invalid", func(context.Context, admin.Request) (admin.Response, error) {
				return admin.Empty(http.StatusTeapot), nil
			}),
			wantStatus: http.StatusInternalServerError,
		},
		{
			name: "handler error is redacted",
			endpoint: admin.Get("failed", func(context.Context, admin.Request) (admin.Response, error) {
				return admin.Response{}, errors.New("response-secret")
			}),
			wantStatus: http.StatusInternalServerError,
		},
		{
			name: "valid explicit response",
			endpoint: admin.Get("valid", func(context.Context, admin.Request) (admin.Response, error) {
				return admin.JSON(http.StatusAccepted, map[string]bool{"ok": true})
			}),
			wantStatus: http.StatusAccepted,
			wantBody:   `{"ok":true}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			New().serveAdminEndpoint(
				response,
				httptest.NewRequest(http.MethodGet, "/admin/v1/"+test.endpoint.Name(), nil),
				admin.Operation{Method: http.MethodGet, Endpoint: test.endpoint.Name()},
				test.endpoint,
				test.endpoint.Invoke,
			)
			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d", response.Code, test.wantStatus)
			}
			if test.wantBody != "" && response.Body.String() != test.wantBody {
				t.Fatalf("Body = %q, want %q", response.Body, test.wantBody)
			}
			if test.wantStatus == http.StatusInternalServerError {
				if strings.Contains(response.Body.String(), "response-secret") {
					t.Fatalf("error response leaked business content: %q", response.Body)
				}
				if response.Header().Get("Content-Type") == "application/json" {
					t.Fatal("business Content-Type was written before response validation")
				}
			}
		})
	}
}

// TestAdminLimitConcurrentRequests 用 Channel 屏障占满 64 个活动请求，固定第 65 个请求快速
// 返回 429；另一个 Application 不得共享额度，释放后原 Application 也必须重新准入。
func TestAdminLimitConcurrentRequests(t *testing.T) {
	app := New()
	entered := make(chan struct{}, 64)
	release := make(chan struct{})
	var calls atomic.Int32
	endpoint := admin.Get("blocking", func(context.Context, admin.Request) (admin.Response, error) {
		current := calls.Add(1)
		if current <= 64 {
			entered <- struct{}{}
			<-release
		}
		return admin.Response{}, nil
	})

	var requests sync.WaitGroup
	requests.Add(64)
	for range 64 {
		go func() {
			defer requests.Done()
			app.serveAdminEndpoint(
				httptest.NewRecorder(),
				httptest.NewRequest(http.MethodGet, "/admin/v1/blocking", nil),
				admin.Operation{Method: http.MethodGet, Endpoint: "blocking"},
				endpoint,
				endpoint.Invoke,
			)
		}()
	}
	for range 64 {
		<-entered
	}

	// Handler 对第 65 次调用不会阻塞，使未实现限流时稳定返回错误状态而不是让测试挂起。
	overloaded := httptest.NewRecorder()
	app.serveAdminEndpoint(
		overloaded,
		httptest.NewRequest(http.MethodGet, "/admin/v1/blocking", nil),
		admin.Operation{Method: http.MethodGet, Endpoint: "blocking"},
		endpoint,
		endpoint.Invoke,
	)
	if overloaded.Code != http.StatusTooManyRequests {
		t.Fatalf("65th status = %d, want 429", overloaded.Code)
	}
	if calls.Load() != 64 {
		t.Fatalf("Handler calls at capacity = %d, want 64", calls.Load())
	}

	// 第二个 Application 必须拥有独立额度，不能被第一个实例的 64 个请求拖累。
	otherEndpoint := admin.Get("ready", func(context.Context, admin.Request) (admin.Response, error) {
		return admin.Response{}, nil
	})
	other := httptest.NewRecorder()
	New().serveAdminEndpoint(
		other,
		httptest.NewRequest(http.MethodGet, "/admin/v1/ready", nil),
		admin.Operation{Method: http.MethodGet, Endpoint: "ready"},
		otherEndpoint,
		otherEndpoint.Invoke,
	)
	if other.Code != http.StatusOK {
		t.Fatalf("independent Application status = %d, want 200", other.Code)
	}

	close(release)
	requests.Wait()
	afterRelease := httptest.NewRecorder()
	app.serveAdminEndpoint(
		afterRelease,
		httptest.NewRequest(http.MethodGet, "/admin/v1/blocking", nil),
		admin.Operation{Method: http.MethodGet, Endpoint: "blocking"},
		endpoint,
		endpoint.Invoke,
	)
	if afterRelease.Code != http.StatusOK {
		t.Fatalf("after release status = %d, want 200", afterRelease.Code)
	}
}

// TestAdminTimeoutRetainsAdmissionUntilInvokeReturns documents cooperative
// timeout semantics: cancellation is signaled through Context, while the slot is
// held until the Handler acknowledges it and actually returns.
func TestAdminTimeoutRetainsAdmissionUntilInvokeReturns(t *testing.T) {
	app := New()
	entered := make(chan struct{}, adminHTTPMaxActiveRequests)
	canceled := make(chan struct{}, adminHTTPMaxActiveRequests)
	release := make(chan struct{})
	endpoint := admin.Get(
		"cooperative-timeout",
		func(ctx context.Context, _ admin.Request) (admin.Response, error) {
			entered <- struct{}{}
			<-ctx.Done()
			canceled <- struct{}{}
			<-release
			return admin.Response{}, ctx.Err()
		},
		admin.WithTimeout(10*time.Millisecond),
	)

	responses := make([]*httptest.ResponseRecorder, adminHTTPMaxActiveRequests)
	var requests sync.WaitGroup
	for index := range adminHTTPMaxActiveRequests {
		responses[index] = httptest.NewRecorder()
		requests.Add(1)
		go func(response *httptest.ResponseRecorder) {
			defer requests.Done()
			app.serveAdminEndpoint(
				response,
				httptest.NewRequest(http.MethodGet, "/cooperative-timeout", nil),
				admin.Operation{Method: http.MethodGet, Endpoint: "cooperative-timeout"},
				endpoint,
				endpoint.Invoke,
			)
		}(responses[index])
	}
	for range adminHTTPMaxActiveRequests {
		<-entered
	}
	for range adminHTTPMaxActiveRequests {
		<-canceled
	}

	overloaded := httptest.NewRecorder()
	app.serveAdminEndpoint(
		overloaded,
		httptest.NewRequest(http.MethodGet, "/cooperative-timeout", nil),
		admin.Operation{Method: http.MethodGet, Endpoint: "cooperative-timeout"},
		endpoint,
		endpoint.Invoke,
	)
	if overloaded.Code != http.StatusTooManyRequests {
		t.Fatalf("request after 64 canceled-but-running Handlers = %d, want 429", overloaded.Code)
	}

	close(release)
	requests.Wait()
	for index, response := range responses {
		if response.Code != http.StatusGatewayTimeout {
			t.Errorf("response %d status = %d, want 504", index, response.Code)
		}
	}
	if active := len(app.adminHTTP.requestSlots); active != 0 {
		t.Fatalf("active request slots after cooperative returns = %d, want 0", active)
	}
}

// TestAdminTimeoutAndCancellation 固定调用前取消不执行 Handler，执行中取消传播到 Handler，
// Endpoint Deadline 由同一 Context 触发且分别映射为 408/504。
func TestAdminTimeoutAndCancellation(t *testing.T) {
	t.Run("canceled before invoke", func(t *testing.T) {
		app := New()
		invoked := false
		endpoint := admin.Get("cancel", func(context.Context, admin.Request) (admin.Response, error) {
			invoked = true
			return admin.Response{}, nil
		})
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		request := httptest.NewRequest(http.MethodGet, "/admin/v1/cancel", nil).WithContext(ctx)
		response := httptest.NewRecorder()
		app.serveAdminEndpoint(
			response,
			request,
			admin.Operation{Method: http.MethodGet, Endpoint: "cancel"},
			endpoint,
			endpoint.Invoke,
		)
		if invoked || response.Code != http.StatusRequestTimeout {
			t.Fatalf("invoked=%v status=%d, want false/408", invoked, response.Code)
		}
	})

	t.Run("canceled during invoke", func(t *testing.T) {
		app := New()
		entered := make(chan struct{})
		endpoint := admin.Get("cancel", func(ctx context.Context, _ admin.Request) (admin.Response, error) {
			close(entered)
			<-ctx.Done()
			return admin.Response{}, ctx.Err()
		})
		ctx, cancel := context.WithCancel(context.Background())
		request := httptest.NewRequest(http.MethodGet, "/admin/v1/cancel", nil).WithContext(ctx)
		response := httptest.NewRecorder()
		returned := make(chan struct{})
		go func() {
			app.serveAdminEndpoint(
				response,
				request,
				admin.Operation{Method: http.MethodGet, Endpoint: "cancel"},
				endpoint,
				endpoint.Invoke,
			)
			close(returned)
		}()
		<-entered
		cancel()
		<-returned
		if response.Code != http.StatusRequestTimeout {
			t.Fatalf("status = %d, want 408", response.Code)
		}
	})

	t.Run("endpoint deadline", func(t *testing.T) {
		app := New()
		entered := make(chan struct{})
		endpoint := admin.Get(
			"timeout",
			func(ctx context.Context, _ admin.Request) (admin.Response, error) {
				close(entered)
				select {
				case <-ctx.Done():
					return admin.Response{}, ctx.Err()
				case <-time.After(100 * time.Millisecond):
					// 未实现 Endpoint Timeout 时也让 RED 稳定结束，避免测试套件悬挂。
					return admin.Response{}, errors.New("endpoint timeout context was not canceled")
				}
			},
			admin.WithTimeout(20*time.Millisecond),
		)
		response := httptest.NewRecorder()
		app.serveAdminEndpoint(
			response,
			httptest.NewRequest(http.MethodGet, "/admin/v1/timeout", nil),
			admin.Operation{Method: http.MethodGet, Endpoint: "timeout"},
			endpoint,
			endpoint.Invoke,
		)
		<-entered
		if response.Code != http.StatusGatewayTimeout {
			t.Fatalf("status = %d, want 504", response.Code)
		}
	})

	for _, test := range []struct {
		name      string
		invokeErr error
	}{
		{name: "deadline overrides business error", invokeErr: errors.New("late business failure")},
		{name: "deadline overrides returned cancellation", invokeErr: context.Canceled},
	} {
		t.Run(test.name, func(t *testing.T) {
			app := New()
			endpoint := admin.Get(
				"timeout-precedence",
				func(ctx context.Context, _ admin.Request) (admin.Response, error) {
					<-ctx.Done()
					return admin.Response{}, test.invokeErr
				},
				admin.WithTimeout(10*time.Millisecond),
			)
			response := httptest.NewRecorder()
			app.serveAdminEndpoint(
				response,
				httptest.NewRequest(http.MethodGet, "/admin/v1/timeout-precedence", nil),
				admin.Operation{Method: http.MethodGet, Endpoint: "timeout-precedence"},
				endpoint,
				endpoint.Invoke,
			)
			if response.Code != http.StatusGatewayTimeout {
				t.Fatalf("status = %d, want 504", response.Code)
			}
		})
	}
}
