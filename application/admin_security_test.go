package application

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/duanhf2012/origin/v3/admin"
	"github.com/duanhf2012/origin/v3/errs"
	originlog "github.com/duanhf2012/origin/v3/log"
)

// adminGuardFunc 让测试直接表达认证、授权和 Body 读取时序，不替换 HTTP 或 Endpoint 边界。
type adminGuardFunc func(context.Context, *http.Request, admin.Operation) (admin.Principal, error)

// Authorize 实现 admin.Guard。
func (authorize adminGuardFunc) Authorize(
	ctx context.Context,
	request *http.Request,
	operation admin.Operation,
) (admin.Principal, error) {
	return authorize(ctx, request, operation)
}

// trackedAdminBody 记录 Guard 与统一处理器何时第一次读取请求体。
type trackedAdminBody struct {
	reader io.Reader
	reads  int
}

// Read 记录真实读取并转发给底层 Reader。
func (body *trackedAdminBody) Read(target []byte) (int, error) {
	body.reads++
	return body.reader.Read(target)
}

// Close 不拥有外部资源，仅满足 HTTP Body 契约。
func (*trackedAdminBody) Close() error { return nil }

// TestAdminSecurityBindingPolicy 防止 nil Guard 把空 Host、IPv4 wildcard 或 IPv6 wildcard
// 误判为环回；拒绝必须发生在 Listener 发布前。
func TestAdminSecurityBindingPolicy(t *testing.T) {
	for _, address := range []string{":0", "0.0.0.0:0", "[::]:0"} {
		address := address
		t.Run(address, func(t *testing.T) {
			app := newAdminHTTPTestApplication(t)
			if err := app.StartAdminServer(address); !errors.Is(err, errs.ErrAdminUnavailable) {
				t.Fatalf("StartAdminServer(%q) error = %v", address, err)
			}
			if _, ok := app.AdminAddress(); ok {
				t.Fatal("rejected wildcard address was published")
			}
		})
	}

	// 明确的 localhost 是允许的环回主机名，实际解析出的端口仍由 Runtime 发布。
	app := newAdminHTTPTestApplication(t)
	if err := app.StartAdminServer("localhost:0"); err != nil {
		t.Fatalf("StartAdminServer(localhost:0) error = %v", err)
	}
	if _, ok := app.AdminAddress(); !ok {
		t.Fatal("localhost listener address was not published")
	}

	// 非 nil Guard 是允许 wildcard 绑定的唯一条件；通过公开 SetAdminGuard 固定配置语义。
	guarded := New()
	if err := guarded.SetAdminGuard(adminGuardFunc(func(
		context.Context,
		*http.Request,
		admin.Operation,
	) (admin.Principal, error) {
		return admin.Principal{Subject: "gateway"}, nil
	})); err != nil {
		t.Fatalf("SetAdminGuard() error = %v", err)
	}
	guarded.mu.Lock()
	guarded.resourcesReady = true
	guarded.state.Store(uint32(StateRunning))
	guarded.mu.Unlock()
	t.Cleanup(func() {
		if err := guarded.StopAdminServer(context.Background()); err != nil {
			t.Errorf("guarded StopAdminServer() error = %v", err)
		}
	})
	if err := guarded.StartAdminServer("0.0.0.0:0"); err != nil {
		t.Fatalf("guarded wildcard StartAdminServer() error = %v", err)
	}
}

// TestAdminSecurityGuardRunsBeforeBody 固定认证和授权发生在任何 Body 读取前，并把两个公开
// 哨兵稳定映射为 401/403；错误文本、凭证和 CORS 信息不得进入响应。
func TestAdminSecurityGuardRunsBeforeBody(t *testing.T) {
	tests := []struct {
		name       string
		guardError error
		wantStatus int
	}{
		{name: "unauthenticated", guardError: admin.ErrUnauthenticated, wantStatus: http.StatusUnauthorized},
		{name: "forbidden", guardError: admin.ErrForbidden, wantStatus: http.StatusForbidden},
		{
			name:       "guard internal error",
			guardError: errors.New("guard-private-reason"),
			wantStatus: http.StatusInternalServerError,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body := &trackedAdminBody{reader: strings.NewReader(`{"secret":"do-not-read"}`)}
			app := New()
			app.adminGuard = adminGuardFunc(func(
				_ context.Context,
				_ *http.Request,
				operation admin.Operation,
			) (admin.Principal, error) {
				if body.reads != 0 {
					t.Fatalf("Guard observed %d Body reads", body.reads)
				}
				if operation.Method != http.MethodPost || operation.Endpoint != "reload" {
					t.Fatalf("Guard operation = %+v", operation)
				}
				return admin.Principal{}, test.guardError
			})
			endpoint := admin.Post("reload", func(context.Context, admin.Request) (admin.Response, error) {
				t.Fatal("unauthorized Handler executed")
				return admin.Response{}, nil
			})
			request := httptest.NewRequest(http.MethodPost, "/admin/v1/reload?token=query-secret", nil)
			request.Body = body
			request.Header.Set("Authorization", "Bearer header-secret")
			request.Header.Set("Cookie", "session=cookie-secret")
			request.Header.Set("Origin", "https://untrusted.example")
			response := httptest.NewRecorder()

			app.serveAdminEndpoint(
				response,
				request,
				admin.Operation{Method: http.MethodPost, Endpoint: "reload"},
				endpoint,
				endpoint.Invoke,
			)

			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d", response.Code, test.wantStatus)
			}
			if body.reads != 0 {
				t.Fatalf("rejected request Body reads = %d", body.reads)
			}
			if response.Header().Get("Access-Control-Allow-Origin") != "" {
				t.Fatal("CORS was enabled by default")
			}
			payload := response.Body.String()
			for _, secret := range []string{
				"header-secret",
				"cookie-secret",
				"query-secret",
				"do-not-read",
				"guard-private-reason",
			} {
				if strings.Contains(payload, secret) {
					t.Fatalf("response leaked %q: %q", secret, payload)
				}
			}
		})
	}
}

// TestAdminSecurityLocalPrincipal 固定无 Guard 环回模式只注入最小 local 身份，不从 Query、
// Cookie 或 Authorization 推导 Principal。
func TestAdminSecurityLocalPrincipal(t *testing.T) {
	endpoint := admin.Get("summary", func(
		_ context.Context,
		request admin.Request,
	) (admin.Response, error) {
		principal := request.Principal()
		if principal.Subject != "local" || principal.Roles != nil || principal.Attributes != nil {
			t.Fatalf("local Principal = %+v", principal)
		}
		return admin.Response{}, nil
	})
	request := httptest.NewRequest(
		http.MethodGet,
		"/admin/v1/summary?token=query-secret",
		nil,
	)
	request.Header.Set("Authorization", "Bearer header-secret")
	request.Header.Set("Cookie", "session=cookie-secret")
	response := httptest.NewRecorder()
	New().serveAdminEndpoint(
		response,
		request,
		admin.Operation{Method: http.MethodGet, Endpoint: "summary"},
		endpoint,
		endpoint.Invoke,
	)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}
}

// TestAdminSecurityPrincipalOperationAndRequestID 防止 Guard 身份集合与 Handler 共享可变内存，
// 或授权目标脱离 Endpoint 的 GET/POST；每次请求还必须获得独立且非业务派生的 ID。
func TestAdminSecurityPrincipalOperationAndRequestID(t *testing.T) {
	original := admin.Principal{
		Subject:    "operator",
		Roles:      []string{"ops"},
		Attributes: map[string]string{"tenant": "blue"},
	}
	var mu sync.Mutex
	var operations []admin.Operation
	app := New()
	app.adminGuard = adminGuardFunc(func(
		_ context.Context,
		_ *http.Request,
		operation admin.Operation,
	) (admin.Principal, error) {
		mu.Lock()
		operations = append(operations, operation)
		mu.Unlock()
		return original, nil
	})

	var requestIDs []string
	endpoint := admin.Get("summary", func(
		_ context.Context,
		request admin.Request,
	) (admin.Response, error) {
		principal := request.Principal()
		if principal.Subject != "operator" || principal.Roles[0] != "ops" ||
			principal.Attributes["tenant"] != "blue" {
			t.Fatalf("Handler Principal = %+v", principal)
		}
		// 修改 Handler 取得的集合，证明 Guard 返回值和 Request 内部值均未共享。
		principal.Roles[0] = "mutated"
		principal.Attributes["tenant"] = "mutated"
		requestIDs = append(requestIDs, request.ID())
		return admin.Response{}, nil
	})
	for range 2 {
		request := httptest.NewRequest(http.MethodGet, "/admin/v1/summary", nil)
		response := httptest.NewRecorder()
		app.serveAdminEndpoint(
			response,
			request,
			admin.Operation{Method: http.MethodGet, Endpoint: "summary"},
			endpoint,
			endpoint.Invoke,
		)
		if response.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", response.Code)
		}
	}

	if original.Roles[0] != "ops" || original.Attributes["tenant"] != "blue" {
		t.Fatalf("Guard Principal was mutated: %+v", original)
	}
	if len(requestIDs) != 2 || requestIDs[0] == "" || requestIDs[1] == "" ||
		requestIDs[0] == requestIDs[1] {
		t.Fatalf("Request IDs = %q", requestIDs)
	}
	if len(operations) != 2 {
		t.Fatalf("Guard operations = %+v", operations)
	}
	for _, operation := range operations {
		if operation.Method != http.MethodGet || operation.Endpoint != "summary" ||
			operation.NodeID != "" || operation.ServiceName != "" {
			t.Fatalf("Guard operation = %+v", operation)
		}
	}
}

// adminAuditCapture 保存测试 Handler 收到的不可变消息、字段名和字符串/字节字段内容。
type adminAuditCapture struct {
	message  string
	keys     map[string]struct{}
	text     string
	status   int
	endpoint string
}

// adminAuditHandler 捕获真实 Logger Runtime 串行写出的审计记录。
type adminAuditHandler struct {
	mu      sync.Mutex
	records []adminAuditCapture
}

// Enabled 让测试接收全部审计级别。
func (*adminAuditHandler) Enabled(originlog.Level) bool { return true }

// Write 复制字段元数据，并特意保存所有可能携带敏感内容的字符串和字节值。
func (handler *adminAuditHandler) Write(record originlog.Record, fields []originlog.Field) error {
	captured := adminAuditCapture{
		message: record.Message,
		keys:    make(map[string]struct{}, len(fields)),
	}
	var content strings.Builder
	content.WriteString(record.Message)
	for _, field := range fields {
		captured.keys[field.Key()] = struct{}{}
		content.WriteString(field.StringValue())
		content.Write(field.BytesValue())
		switch field.Key() {
		case "status":
			captured.status = int(field.Int64Value())
		case "endpoint":
			captured.endpoint = field.StringValue()
		}
	}
	captured.text = content.String()
	handler.mu.Lock()
	handler.records = append(handler.records, captured)
	handler.mu.Unlock()
	return nil
}

// TestAdminSecurityOuterBoundary proves that every ServeMux outcome, including
// unknown routes, passes through the per-Application admission and audit boundary.
func TestAdminSecurityOuterBoundary(t *testing.T) {
	handler := &adminAuditHandler{}
	logRuntime, err := originlog.NewRuntime(originlog.Config{Mode: originlog.SyncMode}, handler)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = logRuntime.Close(context.Background()) })

	app := New()
	app.logger = logRuntime.Logger()
	entered := make(chan struct{}, adminHTTPMaxActiveRequests)
	release := make(chan struct{})
	mux := http.NewServeMux()
	mux.HandleFunc("/block", func(w http.ResponseWriter, _ *http.Request) {
		entered <- struct{}{}
		<-release
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/tree/", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	boundary := app.adminHTTPBoundary(mux)

	var wait sync.WaitGroup
	for range adminHTTPMaxActiveRequests {
		wait.Add(1)
		go func() {
			defer wait.Done()
			boundary.ServeHTTP(
				httptest.NewRecorder(),
				httptest.NewRequest(http.MethodGet, "/block", nil),
			)
		}()
	}
	for range adminHTTPMaxActiveRequests {
		<-entered
	}

	overloaded := httptest.NewRecorder()
	boundary.ServeHTTP(
		overloaded,
		httptest.NewRequest(http.MethodGet, "/dynamic-secret?token=query-secret", nil),
	)
	if overloaded.Code != http.StatusTooManyRequests {
		t.Fatalf("overloaded unknown route status = %d, want 429", overloaded.Code)
	}
	records := handler.snapshot()
	if len(records) != 1 || records[0].status != http.StatusTooManyRequests ||
		records[0].endpoint != "unknown" {
		t.Fatalf("overload audit records = %+v, want one stable unknown/429 record", records)
	}

	close(release)
	wait.Wait()
	if active := len(app.adminHTTP.requestSlots); active != 0 {
		t.Fatalf("active request slots after release = %d, want 0", active)
	}
	notFound := httptest.NewRecorder()
	boundary.ServeHTTP(
		notFound,
		httptest.NewRequest(http.MethodGet, "/another-dynamic-secret?token=query-secret", nil),
	)
	if notFound.Code != http.StatusNotFound {
		t.Fatalf("unknown route status = %d, want 404", notFound.Code)
	}
	records = handler.snapshot()
	if len(records) != adminHTTPMaxActiveRequests+2 {
		t.Fatalf("audit records = %d, want %d", len(records), adminHTTPMaxActiveRequests+2)
	}
	last := records[len(records)-1]
	if last.status != http.StatusNotFound || last.endpoint != "unknown" {
		t.Fatalf("unknown route audit = %+v, want stable unknown/404", last)
	}
	redirect := httptest.NewRecorder()
	boundary.ServeHTTP(
		redirect,
		httptest.NewRequest(http.MethodGet, "/tree?token=query-secret", nil),
	)
	if redirect.Code < http.StatusMultipleChoices || redirect.Code >= http.StatusBadRequest {
		t.Fatalf("ServeMux redirect status = %d, want 3xx", redirect.Code)
	}
	records = handler.snapshot()
	if len(records) != adminHTTPMaxActiveRequests+3 {
		t.Fatalf("audit records after redirect = %d, want %d", len(records), adminHTTPMaxActiveRequests+3)
	}
	last = records[len(records)-1]
	if last.status != redirect.Code || last.endpoint != "unknown" {
		t.Fatalf("redirect audit = %+v, want stable unknown/%d", last, redirect.Code)
	}
	for _, record := range records {
		for _, secret := range []string{"dynamic-secret", "another-dynamic-secret", "query-secret"} {
			if strings.Contains(record.text, secret) {
				t.Fatalf("audit leaked dynamic route data %q: %q", secret, record.text)
			}
		}
	}
}

// TestAdminSecurityPanicRecovery fixes the outermost recovery contract for both
// authentication and custom invocation code, without exposing panic values.
func TestAdminSecurityPanicRecovery(t *testing.T) {
	handler := &adminAuditHandler{}
	logRuntime, err := originlog.NewRuntime(originlog.Config{Mode: originlog.SyncMode}, handler)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = logRuntime.Close(context.Background()) })

	for _, test := range []struct {
		name   string
		guard  admin.Guard
		invoke func(context.Context, admin.Request) (admin.Response, error)
		secret string
	}{
		{
			name: "guard",
			guard: adminGuardFunc(func(context.Context, *http.Request, admin.Operation) (admin.Principal, error) {
				panic("guard-panic-secret")
			}),
			invoke: func(context.Context, admin.Request) (admin.Response, error) {
				t.Fatal("invoke ran after Guard panic")
				return admin.Response{}, nil
			},
			secret: "guard-panic-secret",
		},
		{
			name: "custom invoke",
			guard: adminGuardFunc(func(context.Context, *http.Request, admin.Operation) (admin.Principal, error) {
				return admin.Principal{Subject: "operator"}, nil
			}),
			invoke: func(context.Context, admin.Request) (admin.Response, error) {
				panic("invoke-panic-secret")
			},
			secret: "invoke-panic-secret",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			app := New()
			app.logger = logRuntime.Logger()
			app.adminGuard = test.guard
			endpoint := admin.Get("panic", func(context.Context, admin.Request) (admin.Response, error) {
				return admin.Response{}, nil
			})
			mux := http.NewServeMux()
			mux.HandleFunc("/panic", func(w http.ResponseWriter, request *http.Request) {
				app.serveAdminEndpoint(
					w,
					request,
					admin.Operation{Method: http.MethodGet, Endpoint: "panic"},
					endpoint,
					test.invoke,
				)
			})
			response := httptest.NewRecorder()
			app.adminHTTPBoundary(mux).ServeHTTP(
				response,
				httptest.NewRequest(http.MethodGet, "/panic", nil),
			)
			if response.Code != http.StatusInternalServerError {
				t.Fatalf("status = %d, want 500", response.Code)
			}
			if strings.Contains(response.Body.String(), test.secret) {
				t.Fatalf("response leaked panic value: %q", response.Body.String())
			}
			if active := len(app.adminHTTP.requestSlots); active != 0 {
				t.Fatalf("active request slots after panic = %d, want 0", active)
			}
		})
	}

	records := handler.snapshot()
	if len(records) != 2 {
		t.Fatalf("audit records = %d, want exactly 2", len(records))
	}
	for _, record := range records {
		if record.status != http.StatusInternalServerError || record.endpoint != "panic" {
			t.Fatalf("panic audit = %+v, want panic/500", record)
		}
		for _, secret := range []string{"guard-panic-secret", "invoke-panic-secret"} {
			if strings.Contains(record.text, secret) {
				t.Fatalf("panic audit leaked %q: %q", secret, record.text)
			}
		}
	}
}

// TestAdminSecurityForbiddenPrincipalSnapshot proves that an authenticated
// forbidden caller remains attributable without sharing Guard-owned collections.
func TestAdminSecurityForbiddenPrincipalSnapshot(t *testing.T) {
	handler := &adminAuditHandler{}
	logRuntime, err := originlog.NewRuntime(originlog.Config{Mode: originlog.SyncMode}, handler)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = logRuntime.Close(context.Background()) })

	original := admin.Principal{
		Subject:    "operator",
		Roles:      []string{"ops"},
		Attributes: map[string]string{"tenant": "blue"},
	}
	app := New()
	app.logger = logRuntime.Logger()
	app.adminGuard = adminGuardFunc(func(
		context.Context,
		*http.Request,
		admin.Operation,
	) (admin.Principal, error) {
		return original, admin.ErrForbidden
	})
	endpoint := admin.Get("forbidden", func(context.Context, admin.Request) (admin.Response, error) {
		t.Fatal("forbidden endpoint invoked")
		return admin.Response{}, nil
	})
	mux := http.NewServeMux()
	mux.HandleFunc("/forbidden", func(w http.ResponseWriter, request *http.Request) {
		app.serveAdminEndpoint(
			w,
			request,
			admin.Operation{Method: http.MethodGet, Endpoint: "forbidden"},
			endpoint,
			endpoint.Invoke,
		)
		// The known endpoint has returned, but the outer boundary has not audited
		// yet. Mutating Guard-owned collections here deterministically detects aliasing.
		original.Roles[0] = "mutated-role"
		original.Attributes["tenant"] = "mutated-tenant"
	})
	response := httptest.NewRecorder()
	app.adminHTTPBoundary(mux).ServeHTTP(
		response,
		httptest.NewRequest(http.MethodGet, "/forbidden", nil),
	)
	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", response.Code)
	}
	records := handler.snapshot()
	if len(records) != 1 {
		t.Fatalf("audit records = %d, want 1", len(records))
	}
	if !strings.Contains(records[0].text, `"ops"`) ||
		!strings.Contains(records[0].text, `"tenant":"blue"`) {
		t.Fatalf("forbidden audit lost Principal snapshot: %q", records[0].text)
	}
	for _, mutation := range []string{"mutated-role", "mutated-tenant"} {
		if strings.Contains(records[0].text, mutation) {
			t.Fatalf("forbidden audit observed later mutation %q: %q", mutation, records[0].text)
		}
	}
}

// Sync/Close 没有外部资源；Runtime 仍会真实执行自己的 Flush/Close 生命周期。
func (*adminAuditHandler) Sync() error  { return nil }
func (*adminAuditHandler) Close() error { return nil }

// snapshot 返回审计记录的独立 Slice，供请求结束后断言。
func (handler *adminAuditHandler) snapshot() []adminAuditCapture {
	handler.mu.Lock()
	defer handler.mu.Unlock()
	return append([]adminAuditCapture(nil), handler.records...)
}

// TestAdminSecurityAuditRedaction 固定成功 GET、拒绝 POST 与 panic 都记录统一审计字段，同时
// Query、凭证 Header、Cookie、Body、业务错误和响应内容绝不能进入消息或字段值。
func TestAdminSecurityAuditRedaction(t *testing.T) {
	handler := &adminAuditHandler{}
	logRuntime, err := originlog.NewRuntime(
		originlog.Config{Mode: originlog.SyncMode},
		handler,
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := logRuntime.Close(context.Background()); err != nil {
			t.Errorf("close audit logger: %v", err)
		}
	})

	app := New()
	app.logger = logRuntime.Logger()
	app.adminGuard = adminGuardFunc(func(
		_ context.Context,
		_ *http.Request,
		operation admin.Operation,
	) (admin.Principal, error) {
		if operation.Endpoint == "blocked" {
			return admin.Principal{}, admin.ErrForbidden
		}
		return admin.Principal{Subject: "operator"}, nil
	})

	summary := admin.Get("summary", func(context.Context, admin.Request) (admin.Response, error) {
		return admin.JSON(http.StatusOK, map[string]string{"value": "response-secret"})
	})
	summaryRequest := httptest.NewRequest(
		http.MethodGet,
		"/admin/v1/summary?token=query-secret",
		nil,
	)
	summaryRequest.Header.Set("Authorization", "Bearer authorization-secret")
	summaryRequest.Header.Set("Cookie", "session=cookie-secret")
	app.serveAdminEndpoint(
		httptest.NewRecorder(),
		summaryRequest,
		admin.Operation{Method: http.MethodGet, Endpoint: "summary"},
		summary,
		summary.Invoke,
	)

	blocked := admin.Post("blocked", func(context.Context, admin.Request) (admin.Response, error) {
		t.Fatal("forbidden POST Handler executed")
		return admin.Response{}, nil
	})
	blockedRequest := httptest.NewRequest(
		http.MethodPost,
		"/admin/v1/blocked?token=query-secret",
		strings.NewReader(`{"secret":"body-secret"}`),
	)
	blockedRequest.Header.Set("Content-Type", "application/json")
	blockedRequest.Header.Set("Authorization", "Bearer authorization-secret")
	blockedRequest.Header.Set("Cookie", "session=cookie-secret")
	app.serveAdminEndpoint(
		httptest.NewRecorder(),
		blockedRequest,
		admin.Operation{Method: http.MethodPost, Endpoint: "blocked"},
		blocked,
		blocked.Invoke,
	)

	panicking := admin.Get("panic", func(context.Context, admin.Request) (admin.Response, error) {
		panic("panic-secret")
	})
	app.serveAdminEndpoint(
		httptest.NewRecorder(),
		httptest.NewRequest(http.MethodGet, "/admin/v1/panic", nil),
		admin.Operation{Method: http.MethodGet, Endpoint: "panic"},
		panicking,
		panicking.Invoke,
	)

	records := handler.snapshot()
	if len(records) != 3 {
		t.Fatalf("audit records = %d, want 3", len(records))
	}
	required := []string{
		"request_id",
		"subject",
		"method",
		"endpoint",
		"target_node_id",
		"target_service_name",
		"status",
		"duration",
		"response_bytes",
		"outcome",
	}
	for index, record := range records {
		for _, key := range required {
			if _, ok := record.keys[key]; !ok {
				t.Errorf("record %d missing audit field %q: %+v", index, key, record.keys)
			}
		}
		for _, forbidden := range []string{
			"query-secret",
			"authorization-secret",
			"cookie-secret",
			"body-secret",
			"response-secret",
			"panic-secret",
		} {
			if strings.Contains(record.text, forbidden) {
				t.Errorf("record %d leaked %q: %q", index, forbidden, record.text)
			}
		}
		for _, forbiddenKey := range []string{
			"query",
			"header",
			"authorization",
			"cookie",
			"body",
			"response",
			"error",
		} {
			if _, ok := record.keys[forbiddenKey]; ok {
				t.Errorf("record %d contains forbidden field %q", index, forbiddenKey)
			}
		}
	}
}
