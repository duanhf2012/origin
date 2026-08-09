package application

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
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

	// 生命周期锁同时固定 Guard 与资源关闭状态，防止安全校验后绑定条件发生变化。
	app.mu.Lock()
	state := app.State()
	if !app.resourcesReady || app.resourcesClosing ||
		(state != StateStarting && state != StateRunning) {
		app.mu.Unlock()
		return errs.ErrAdminStateConflict
	}
	if app.adminGuard == nil && !isLoopbackAddress(address) {
		// 未配置 Guard 时在 Listen 前拒绝 wildcard 和非环回主机，避免短暂暴露写控制面。
		app.mu.Unlock()
		return errs.ErrAdminUnavailable
	}

	server := &http.Server{
		Handler:           http.NewServeMux(),
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      20 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    adminHTTPMaxHeaderBytes,
	}
	err := app.adminHTTP.startWithErrors(address, server, adminHTTPRuntimeErrors())
	app.mu.Unlock()
	return err
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
	startedAt := time.Now()
	// 固定容量 Channel 只属于当前 Application 的 adminHTTP；额度耗尽时不建立等待队列。
	if !app.adminHTTP.tryAcquireRequestSlot(adminHTTPMaxActiveRequests) {
		app.finishAdminError(
			w,
			startedAt,
			rand.Text(),
			admin.Principal{},
			operation,
			http.StatusTooManyRequests,
			nil,
		)
		return
	}
	defer app.adminHTTP.releaseRequestSlot()

	// RequestID 只使用系统随机源，不混入 URL、身份、Body 或其他业务数据。
	requestID := rand.Text()
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
			}
			app.finishAdminError(w, startedAt, requestID, principal, operation, status, nil)
			return
		}
		principal = authorized
	} else {
		principal = admin.Principal{Subject: "local"}
	}

	// Guard 已完成后再检查方法和读取 Body，避免未授权请求消耗解析资源或触发读取副作用。
	if r.Method != endpoint.Method() {
		app.finishAdminError(
			w,
			startedAt,
			requestID,
			principal,
			operation,
			http.StatusMethodNotAllowed,
			http.Header{"Allow": {endpoint.Method()}},
		)
		return
	}
	body, requestStatus := readAdminRequestBody(w, r, endpoint)
	if requestStatus != 0 {
		app.finishAdminError(
			w,
			startedAt,
			requestID,
			principal,
			operation,
			requestStatus,
			nil,
		)
		return
	}

	// NewRequest 在调用业务前复制 Guard 身份和 HTTP 集合，解除网络对象的可变所有权。
	request := admin.NewRequest(requestID, principal, r.URL.Query(), r.Header, body)
	invokeContext, cancel := context.WithTimeout(r.Context(), endpoint.Timeout())
	defer cancel()
	if err := invokeContext.Err(); err != nil {
		app.finishAdminError(
			w,
			startedAt,
			requestID,
			principal,
			operation,
			adminInvokeErrorStatus(err),
			nil,
		)
		return
	}
	response, err := invoke(invokeContext, request)
	if err == nil {
		// Handler 可能在 Context 结束后返回 nil；一旦边界超时，结果仍按取消/Deadline 处理。
		err = invokeContext.Err()
	}
	if err != nil {
		app.finishAdminError(
			w,
			startedAt,
			requestID,
			principal,
			operation,
			adminInvokeErrorStatus(err),
			nil,
		)
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
		app.finishAdminError(
			w,
			startedAt,
			requestID,
			principal,
			operation,
			http.StatusInternalServerError,
			nil,
		)
		return
	}
	app.finishAdminRequest(
		w,
		startedAt,
		requestID,
		principal,
		operation,
		status,
		responseHeader,
		responseBody,
	)
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
func (app *Application) finishAdminError(
	w http.ResponseWriter,
	startedAt time.Time,
	requestID string,
	principal admin.Principal,
	operation admin.Operation,
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
	app.finishAdminRequest(
		w,
		startedAt,
		requestID,
		principal,
		operation,
		status,
		header,
		[]byte(http.StatusText(status)+"\n"),
	)
}

// finishAdminRequest 先记录脱敏审计，再一次性提交已经完成全部校验的 Header、状态和 Body。
func (app *Application) finishAdminRequest(
	w http.ResponseWriter,
	startedAt time.Time,
	requestID string,
	principal admin.Principal,
	operation admin.Operation,
	status int,
	header http.Header,
	body []byte,
) {
	app.logger.Info(
		"admin request audit",
		originlog.String("request_id", requestID),
		originlog.String("subject", principal.Subject),
		originlog.String("method", operation.Method),
		originlog.String("endpoint", operation.Endpoint),
		// 根 Logger 会过滤保留归属键 node_id/service_name；target_* 明确表示被管理目标并保留空值。
		originlog.String("target_node_id", operation.NodeID),
		originlog.String("target_service_name", operation.ServiceName),
		originlog.Int("status", status),
		originlog.Duration("duration", time.Since(startedAt)),
		originlog.Int("response_bytes", len(body)),
		originlog.String("outcome", adminAuditOutcome(status)),
	)
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
