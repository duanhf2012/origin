package application

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	stdlog "log"
	"mime"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/duanhf2012/origin/v3/admin"
	"github.com/duanhf2012/origin/v3/errs"
	originlog "github.com/duanhf2012/origin/v3/log"
)

const adminHTTPMaxHeaderBytes = 1 << 20

const adminHTTPMaxActiveRequests = 64

type adminHTTPBoundaryKey struct{}

// adminHTTPAuditState is request-local metadata owned by the outer HTTP
// boundary. Known endpoint handlers may only replace it with validated,
// low-cardinality operation and authenticated principal data.
type adminHTTPAuditState struct {
	startedAt       time.Time
	requestID       string
	principal       admin.Principal
	operation       admin.Operation
	bodyLimitWriter http.ResponseWriter
}

type adminBufferedResponse struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func newAdminBufferedResponse() *adminBufferedResponse {
	return &adminBufferedResponse{header: make(http.Header)}
}

func (response *adminBufferedResponse) Header() http.Header { return response.header }

func (response *adminBufferedResponse) WriteHeader(status int) {
	if response.status == 0 {
		response.status = status
	}
}

func (response *adminBufferedResponse) Write(body []byte) (int, error) {
	if response.status == 0 {
		response.status = http.StatusOK
	}
	return response.body.Write(body)
}

func (response *adminBufferedResponse) resetError(status int) {
	response.header = http.Header{
		"Content-Type":           {"text/plain; charset=utf-8"},
		"X-Content-Type-Options": {"nosniff"},
	}
	response.status = status
	response.body.Reset()
	_, _ = response.body.WriteString(http.StatusText(status) + "\n")
}

func (response *adminBufferedResponse) commit(target http.ResponseWriter) {
	for key, values := range response.header {
		for _, value := range values {
			target.Header().Add(key, value)
		}
	}
	status := response.status
	if status == 0 {
		status = http.StatusOK
	}
	target.WriteHeader(status)
	if response.body.Len() != 0 {
		_, _ = target.Write(response.body.Bytes())
	}
}

// adminHTTPBoundary owns admission, panic recovery, response commit, request ID,
// and exactly one sanitized audit record for every private ServeMux outcome.
func (app *Application) adminHTTPBoundary(next http.Handler) http.Handler {
	return http.HandlerFunc(func(target http.ResponseWriter, request *http.Request) {
		state := &adminHTTPAuditState{
			startedAt:       time.Now(),
			requestID:       rand.Text(),
			bodyLimitWriter: target,
			operation: admin.Operation{
				Method:   safeAdminHTTPMethod(request.Method),
				Endpoint: "unknown",
			},
		}
		response := newAdminBufferedResponse()
		acquired := app.adminHTTP.tryAcquireRequestSlot(adminHTTPMaxActiveRequests)
		if !acquired {
			response.resetError(http.StatusTooManyRequests)
			app.auditAdminRequest(state, response)
			response.commit(target)
			return
		}
		defer app.adminHTTP.releaseRequestSlot()
		defer func() {
			if recover() != nil {
				// Panic values and error chains are deliberately ignored: neither the
				// response nor audit fields may expose authentication or business data.
				response.resetError(http.StatusInternalServerError)
			}
			app.auditAdminRequest(state, response)
			response.commit(target)
		}()

		contextRequest := request.WithContext(
			context.WithValue(request.Context(), adminHTTPBoundaryKey{}, state),
		)
		next.ServeHTTP(response, contextRequest)
	})
}

func safeAdminHTTPMethod(method string) string {
	if method == http.MethodGet || method == http.MethodPost {
		return method
	}
	return ""
}

func (app *Application) auditAdminRequest(
	state *adminHTTPAuditState,
	response *adminBufferedResponse,
) {
	status := response.status
	if status == 0 {
		status = http.StatusOK
	}
	app.logger.Info(
		"admin request audit",
		originlog.String("request_id", state.requestID),
		originlog.String("subject", state.principal.Subject),
		originlog.Any("roles", state.principal.Roles),
		originlog.Any("attributes", state.principal.Attributes),
		originlog.String("method", state.operation.Method),
		originlog.String("endpoint", state.operation.Endpoint),
		originlog.String("target_node_id", state.operation.NodeID),
		originlog.String("target_service_name", state.operation.ServiceName),
		originlog.Int("status", status),
		originlog.Duration("duration", time.Since(state.startedAt)),
		originlog.Int("response_bytes", response.body.Len()),
		originlog.String("outcome", adminAuditOutcome(status)),
	)
}

func cloneAdminPrincipal(principal admin.Principal) admin.Principal {
	clone := admin.Principal{
		Subject: principal.Subject,
		Roles:   append([]string(nil), principal.Roles...),
	}
	if principal.Attributes != nil {
		clone.Attributes = make(map[string]string, len(principal.Attributes))
		for key, value := range principal.Attributes {
			clone.Attributes[key] = value
		}
	}
	return clone
}

// adminHTTPRuntimeErrors 返回 Admin Server 独立的 Listener 生命周期错误族。
func adminHTTPRuntimeErrors() httpRuntimeErrors {
	return httpRuntimeErrors{
		unavailableCode: errs.CodeAdminUnavailable,
		stateConflict:   errs.ErrAdminStateConflict,
	}
}

// StartAdminServer 在 Application 私有 Listener 和 ServeMux 上启动管理 HTTP Server。
func (app *Application) StartAdminServer(address string) error {
	if app == nil || strings.TrimSpace(address) == "" {
		return errs.ErrInvalidArgument
	}
	address = strings.TrimSpace(address)
	if _, _, err := net.SplitHostPort(address); err != nil {
		return errs.Wrap(errs.CodeInvalidArgument, err)
	}

	// Snapshot lifecycle and Guard state only. Listener/runtime operations may
	// block and must never run while app.mu excludes request handlers such as Node.
	app.mu.Lock()
	state := app.State()
	if !app.resourcesReady || app.resourcesClosing ||
		(state != StateStarting && state != StateRunning) {
		app.mu.Unlock()
		return errs.ErrAdminStateConflict
	}
	guardConfigured := app.adminGuard != nil
	app.mu.Unlock()
	if !guardConfigured && !isLoopbackAddress(address) {
		// 未配置 Guard 时在 Listen 前拒绝 wildcard 和非环回主机，避免短暂暴露写控制面。
		return errs.ErrAdminUnavailable
	}

	privateMux := http.NewServeMux()
	server := &http.Server{
		Handler:           app.adminHTTPBoundary(privateMux),
		ErrorLog:          stdlog.New(io.Discard, "", 0),
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      20 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    adminHTTPMaxHeaderBytes,
	}
	return app.adminHTTP.startWithErrors(address, server, adminHTTPRuntimeErrors())
}

// StopAdminServer 停止接受新的管理请求，并在 ctx 内等待已有请求和 Serve goroutine 退出。
func (app *Application) StopAdminServer(ctx context.Context) error {
	if app == nil || ctx == nil {
		return errs.ErrInvalidArgument
	}
	return app.adminHTTP.stopWithErrors(ctx, adminHTTPRuntimeErrors())
}

// AdminAddress 返回当前 Admin Listener 的实际地址；`:0` 已展开为操作系统分配端口。
func (app *Application) AdminAddress() (string, bool) {
	if app == nil {
		return "", false
	}
	return app.adminHTTP.addressSnapshot()
}

// serveAdminEndpoint 统一建立请求身份、不可变输入和成功响应；后续边界在同一入口继续收紧。
func (app *Application) serveAdminEndpoint(
	w http.ResponseWriter,
	r *http.Request,
	operation admin.Operation,
	endpoint admin.Endpoint,
	invoke func(context.Context, admin.Request) (admin.Response, error),
) {
	state, bounded := r.Context().Value(adminHTTPBoundaryKey{}).(*adminHTTPAuditState)
	if !bounded {
		// Direct package-level boundary tests and future private routes receive the
		// exact same outer admission/recovery/audit semantics as the live Server.
		app.adminHTTPBoundary(http.HandlerFunc(func(inner http.ResponseWriter, request *http.Request) {
			app.serveAdminEndpoint(inner, request, operation, endpoint, invoke)
		})).ServeHTTP(w, r)
		return
	}
	// Method/Endpoint come from the frozen Endpoint definition, never the request
	// path or an unsupported action vocabulary. Target identifiers are supplied by
	// the frozen route match in Task 5.
	operation.Method = endpoint.Method()
	operation.Endpoint = endpoint.Name()
	state.operation = operation
	requestID := state.requestID
	principal := admin.Principal{}
	if app.adminGuard != nil {
		authorized, err := app.adminGuard.Authorize(r.Context(), r, operation)
		if err != nil {
			status := http.StatusInternalServerError
			switch {
			case errors.Is(err, admin.ErrUnauthenticated):
				status = http.StatusUnauthorized
			case errors.Is(err, admin.ErrForbidden):
				status = http.StatusForbidden
				// ErrForbidden may accompany an authenticated Principal. The Guard
				// contract permits that identity to be audited, but ownership remains
				// with the Guard, so copy every collection before returning from here.
				principal = cloneAdminPrincipal(authorized)
				state.principal = principal
			}
			finishAdminError(w, status, nil)
			return
		}
		principal = cloneAdminPrincipal(authorized)
	} else {
		principal = admin.Principal{Subject: "local"}
	}
	state.principal = principal

	// Guard 已完成后再检查方法和读取 Body，避免未授权请求消耗解析资源或触发读取副作用。
	if r.Method != endpoint.Method() {
		finishAdminError(
			w,
			http.StatusMethodNotAllowed,
			http.Header{"Allow": {endpoint.Method()}},
		)
		return
	}
	// Preserve net/http's private requestTooLarge notification by giving
	// MaxBytesReader the underlying Server writer, not the buffering wrapper.
	body, requestStatus := readAdminRequestBody(state.bodyLimitWriter, r, endpoint)
	if requestStatus != 0 {
		finishAdminError(w, requestStatus, nil)
		return
	}

	// NewRequest 在调用业务前复制 Guard 身份和 HTTP 集合，解除网络对象的可变所有权。
	request := admin.NewRequest(requestID, principal, r.URL.Query(), r.Header, body)
	invokeContext, cancel := context.WithTimeout(r.Context(), endpoint.Timeout())
	defer cancel()
	if err := invokeContext.Err(); err != nil {
		finishAdminError(w, adminInvokeErrorStatus(err), nil)
		return
	}
	response, err := invoke(invokeContext, request)
	// Endpoint timeout is cooperative: the boundary never starts a fire-and-forget
	// goroutine, and the request slot remains held until invoke actually returns.
	// Once it returns, the Context terminal state takes precedence over business errors.
	if contextErr := invokeContext.Err(); contextErr != nil {
		err = contextErr
	}
	if err != nil {
		finishAdminError(w, adminInvokeErrorStatus(err), nil)
		return
	}
	status := response.Status()
	if status == 0 {
		status = endpoint.SuccessStatus()
	}
	responseHeader := response.Header()
	responseBody := response.Body()
	if status < http.StatusOK || status >= http.StatusMultipleChoices ||
		int64(len(responseBody)) > endpoint.MaxResponseBytes() {
		// 在任何业务 Header 或状态写入前完成全部响应校验，错误只返回固定安全文本。
		finishAdminError(w, http.StatusInternalServerError, nil)
		return
	}
	finishAdminResponse(w, status, responseHeader, responseBody)
}

// adminInvokeErrorStatus 把请求取消、Endpoint Deadline 和其他内部失败映射为安全 HTTP 状态。
func adminInvokeErrorStatus(err error) int {
	status := http.StatusInternalServerError
	switch errs.CodeOf(err) {
	case errs.CodeCanceled:
		status = http.StatusRequestTimeout
	case errs.CodeDeadlineExceeded:
		status = http.StatusGatewayTimeout
	}
	return status
}

// finishAdminError 构建只含稳定状态文本的错误响应，并保留调用方提供的 Allow 等安全 Header。
func finishAdminError(
	w http.ResponseWriter,
	status int,
	extraHeader http.Header,
) {
	header := http.Header{
		"Content-Type":           {"text/plain; charset=utf-8"},
		"X-Content-Type-Options": {"nosniff"},
	}
	for key, values := range extraHeader {
		header[key] = append([]string(nil), values...)
	}
	finishAdminResponse(w, status, header, []byte(http.StatusText(status)+"\n"))
}

// finishAdminResponse writes only a fully validated response into the outer
// buffer; the boundary owns the eventual audit and network commit.
func finishAdminResponse(
	w http.ResponseWriter,
	status int,
	header http.Header,
	body []byte,
) {
	for key, values := range header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	w.WriteHeader(status)
	if len(body) != 0 {
		_, _ = w.Write(body)
	}
}

// adminAuditOutcome 把 HTTP 终态压缩为稳定低基数审计结果，不携带业务错误文本。
func adminAuditOutcome(status int) string {
	switch status {
	case http.StatusUnauthorized:
		return "unauthenticated"
	case http.StatusForbidden:
		return "forbidden"
	case http.StatusRequestTimeout:
		return "canceled"
	case http.StatusTooManyRequests:
		return "overloaded"
	case http.StatusGatewayTimeout:
		return "deadline"
	default:
		if status >= http.StatusOK && status < http.StatusMultipleChoices {
			return "succeeded"
		}
		if status >= http.StatusInternalServerError {
			return "failed"
		}
		return "rejected"
	}
}

// readAdminRequestBody 在 Guard 之后执行方法相关的 MIME、字节上限和严格 JSON 校验。
//
// 返回 status=0 表示成功；错误只暴露稳定 HTTP 状态，不返回解析器或 Body 内容。
func readAdminRequestBody(
	w http.ResponseWriter,
	r *http.Request,
	endpoint admin.Endpoint,
) ([]byte, int) {
	if endpoint.Method() == http.MethodGet {
		// 上限为零的 MaxBytesReader 会真实探测 chunked/未知长度 Body，不依赖 ContentLength。
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 0))
		if err != nil || len(body) != 0 {
			return nil, http.StatusBadRequest
		}
		return nil, 0
	}

	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || !strings.EqualFold(mediaType, "application/json") {
		return nil, http.StatusUnsupportedMediaType
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, endpoint.MaxBodyBytes()))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return nil, http.StatusRequestEntityTooLarge
		}
		return nil, http.StatusBadRequest
	}
	if !isSingleJSONValue(body) {
		return nil, http.StatusBadRequest
	}
	return body, 0
}

// isSingleJSONValue 只验证完整 Body 恰好包含一个 JSON 值，不要求 Handler 主动 DecodeJSON。
func isSingleJSONValue(body []byte) bool {
	decoder := json.NewDecoder(bytes.NewReader(body))
	var value json.RawMessage
	if err := decoder.Decode(&value); err != nil {
		return false
	}
	var extra json.RawMessage
	return decoder.Decode(&extra) == io.EOF
}
