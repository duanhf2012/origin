package ginmodule

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/duanhf2012/origin/v3/errs"
	"github.com/gin-gonic/gin"
	"golang.org/x/net/http/httpguts"
)

var hopByHopResponseHeaders = map[string]struct{}{
	"Connection":          {},
	"Keep-Alive":          {},
	"Proxy-Authenticate":  {},
	"Proxy-Authorization": {},
	"Te":                  {},
	"Trailer":             {},
	"Transfer-Encoding":   {},
	"Upgrade":             {},
	"Content-Length":      {},
}

type safeRequestSnapshot struct {
	request  *http.Request
	body     []byte
	params   gin.Params
	keys     map[any]any
	clientIP string
	fullPath string
}

type safeInvocationResult struct {
	response Response
	err      error
}

// SafeHandle 注册最终业务回调固定、可选 Middleware 在 Service 工作协程执行的路由。
func (module *Module) SafeHandle(
	method string,
	path string,
	handler SafeHandlerFunc,
	middleware ...SafeMiddlewareFunc,
) gin.IRoutes {
	return module.requireEngine().Handle(method, path, module.safeAdapter(handler, middleware))
}

// SafeGET 注册在 Service 工作协程执行的 GET Handler。
func (module *Module) SafeGET(
	path string,
	handler SafeHandlerFunc,
	middleware ...SafeMiddlewareFunc,
) gin.IRoutes {
	return module.SafeHandle(http.MethodGet, path, handler, middleware...)
}

// SafePOST 注册在 Service 工作协程执行的 POST Handler。
func (module *Module) SafePOST(
	path string,
	handler SafeHandlerFunc,
	middleware ...SafeMiddlewareFunc,
) gin.IRoutes {
	return module.SafeHandle(http.MethodPost, path, handler, middleware...)
}

// SafePUT 注册在 Service 工作协程执行的 PUT Handler。
func (module *Module) SafePUT(
	path string,
	handler SafeHandlerFunc,
	middleware ...SafeMiddlewareFunc,
) gin.IRoutes {
	return module.SafeHandle(http.MethodPut, path, handler, middleware...)
}

// SafePATCH 注册在 Service 工作协程执行的 PATCH Handler。
func (module *Module) SafePATCH(
	path string,
	handler SafeHandlerFunc,
	middleware ...SafeMiddlewareFunc,
) gin.IRoutes {
	return module.SafeHandle(http.MethodPatch, path, handler, middleware...)
}

// SafeDELETE 注册在 Service 工作协程执行的 DELETE Handler。
func (module *Module) SafeDELETE(
	path string,
	handler SafeHandlerFunc,
	middleware ...SafeMiddlewareFunc,
) gin.IRoutes {
	return module.SafeHandle(http.MethodDelete, path, handler, middleware...)
}

func (module *Module) safeAdapter(
	handler SafeHandlerFunc,
	middleware []SafeMiddlewareFunc,
) gin.HandlerFunc {
	validateSafeHandlers(handler, middleware)
	chain := append([]SafeMiddlewareFunc(nil), middleware...)
	return func(ginContext *gin.Context) {
		snapshot, err := snapshotRequest(ginContext)
		if err != nil {
			if isRequestBodyTooLarge(err) {
				ginContext.AbortWithStatus(http.StatusRequestEntityTooLarge)
				return
			}
			module.commitMappedError(ginContext, err)
			return
		}

		resultChannel := make(chan safeInvocationResult, 1)
		dispatchErr := module.DispatchAsync(func(taskContext context.Context) {
			mergedContext, finish := mergeSafeContext(taskContext, snapshot.request.Context())
			defer finish()
			defer func() {
				if recovered := recover(); recovered != nil {
					module.counters.panics.Add(1)
					deliverSafeResult(resultChannel, safeInvocationResult{
						err: fmt.Errorf("ginmodule Safe Handler panic: %v", recovered),
					})
					panic(recovered)
				}
			}()
			if cause := context.Cause(mergedContext); cause != nil {
				deliverSafeResult(resultChannel, safeInvocationResult{err: cause})
				return
			}

			safeContext := newSafeContext(mergedContext, snapshot, handler, chain)
			safeContext.run()
			if cause := context.Cause(mergedContext); cause != nil {
				deliverSafeResult(resultChannel, safeInvocationResult{err: cause})
				return
			}
			response, responseErr := module.freezeSafeResponse(safeContext)
			deliverSafeResult(resultChannel, safeInvocationResult{
				response: response,
				err:      responseErr,
			})
		})
		if dispatchErr != nil {
			module.counters.rejected.Add(1)
			module.commitMappedError(ginContext, dispatchErr)
			return
		}

		requestContext := snapshot.request.Context()
		select {
		case result := <-resultChannel:
			module.commitSafeResult(ginContext, result)
		case <-requestContext.Done():
			// 已完成结果优先，避免 Deadline 与结果同时就绪时随机丢弃有效响应。
			select {
			case result := <-resultChannel:
				module.commitSafeResult(ginContext, result)
			default:
				if errors.Is(context.Cause(requestContext), context.DeadlineExceeded) {
					module.commitMappedError(ginContext, context.DeadlineExceeded)
				}
			}
		}
	}
}

func snapshotRequest(ctx *gin.Context) (safeRequestSnapshot, error) {
	if ctx == nil || ctx.Request == nil {
		return safeRequestSnapshot{}, errs.ErrInvalidArgument
	}
	var body []byte
	if ctx.Request.Body != nil && ctx.Request.Body != http.NoBody {
		var err error
		body, err = io.ReadAll(ctx.Request.Body)
		if err != nil {
			return safeRequestSnapshot{}, err
		}
	}
	request := ctx.Request.Clone(ctx.Request.Context())
	request.Body = http.NoBody
	request.GetBody = nil
	if len(body) > 0 {
		request.Body = io.NopCloser(bytes.NewReader(body))
		request.GetBody = func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(body)), nil
		}
	}
	request.ContentLength = int64(len(body))

	copied := ctx.Copy()
	return safeRequestSnapshot{
		request:  request,
		body:     body,
		params:   copied.Params,
		keys:     copied.Keys,
		clientIP: ctx.ClientIP(),
		fullPath: ctx.FullPath(),
	}, nil
}

func newSafeContext(
	taskContext context.Context,
	snapshot safeRequestSnapshot,
	handler SafeHandlerFunc,
	middleware []SafeMiddlewareFunc,
) *SafeContext {
	request := snapshot.request.Clone(taskContext)
	request.Body = http.NoBody
	if len(snapshot.body) > 0 {
		request.Body = io.NopCloser(bytes.NewReader(snapshot.body))
	}
	request.ContentLength = int64(len(snapshot.body))
	return &SafeContext{
		ctx:        taskContext,
		request:    request,
		body:       snapshot.body,
		params:     snapshot.params,
		keys:       snapshot.keys,
		clientIP:   snapshot.clientIP,
		fullPath:   snapshot.fullPath,
		handler:    handler,
		middleware: middleware,
		index:      -1,
		statusCode: http.StatusOK,
		header:     make(http.Header),
	}
}

func (module *Module) freezeSafeResponse(ctx *SafeContext) (Response, error) {
	if ctx == nil {
		return Response{}, errs.ErrInternal
	}
	if ctx.responseErr != nil {
		return Response{}, ctx.responseErr
	}
	if ctx.statusCode < 200 || ctx.statusCode > 599 {
		return Response{}, errs.NewMessage(errs.CodeInternal, "ginmodule: Safe 响应状态码无效")
	}
	if int64(len(ctx.response)) > module.options.MaxSafeResponseBodySize {
		return Response{}, errs.NewMessage(errs.CodeInternal, "ginmodule: Safe 响应 Body 超过上限")
	}
	if err := validateResponseHeader(ctx.header, module.options.MaxHeaderBytes); err != nil {
		return Response{}, err
	}
	return Response{
		StatusCode: ctx.statusCode,
		Header:     ctx.header.Clone(),
		Body:       bytes.Clone(ctx.response),
	}, nil
}

func validateResponseHeader(header http.Header, maximum int) error {
	total := 0
	for name, values := range header {
		canonical := http.CanonicalHeaderKey(name)
		if canonical == "" || !httpguts.ValidHeaderFieldName(canonical) {
			return errs.NewMessage(errs.CodeInternal, "ginmodule: Safe 响应 Header 名称无效")
		}
		if _, forbidden := hopByHopResponseHeaders[canonical]; forbidden {
			return errs.NewMessage(errs.CodeInternal, "ginmodule: Safe 响应包含禁止的 Header")
		}
		total += len(canonical) + 4
		for _, value := range values {
			if !httpguts.ValidHeaderFieldValue(value) {
				return errs.NewMessage(errs.CodeInternal, "ginmodule: Safe 响应 Header 值无效")
			}
			total += len(value) + 2
			if total > maximum {
				return errs.NewMessage(errs.CodeInternal, "ginmodule: Safe 响应 Header 超过上限")
			}
		}
	}
	return nil
}

func (module *Module) commitSafeResult(ctx *gin.Context, result safeInvocationResult) {
	if result.err != nil {
		if errors.Is(result.err, context.Canceled) {
			return
		}
		module.commitMappedError(ctx, result.err)
		return
	}
	commitResponse(ctx, result.response)
}

func (module *Module) commitMappedError(ctx *gin.Context, err error) {
	response := module.options.SafeErrorMapper(err)
	if validationErr := validateFrozenResponse(response, module.options); validationErr != nil {
		response = defaultSafeErrorMapper(errs.ErrInternal)
	}
	commitResponse(ctx, response)
}

func validateFrozenResponse(response Response, options ServerOptions) error {
	if response.StatusCode < 200 || response.StatusCode > 599 {
		return errs.ErrInternal
	}
	if int64(len(response.Body)) > options.MaxSafeResponseBodySize {
		return errs.ErrInternal
	}
	return validateResponseHeader(response.Header, options.MaxHeaderBytes)
}

func commitResponse(ctx *gin.Context, response Response) {
	if ctx == nil || ctx.Writer.Written() {
		return
	}
	for name, values := range response.Header {
		for _, value := range values {
			ctx.Writer.Header().Add(name, value)
		}
	}
	ctx.Status(response.StatusCode)
	if len(response.Body) > 0 && ctx.Request.Method != http.MethodHead {
		_, _ = ctx.Writer.Write(response.Body)
	}
}

func defaultSafeErrorMapper(err error) Response {
	status := http.StatusInternalServerError
	message := "internal server error"
	switch {
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, errs.ErrDeadlineExceeded):
		status = http.StatusGatewayTimeout
		message = "request timeout"
	case errors.Is(err, errs.ErrServiceNotReady), errors.Is(err, errs.ErrServiceStopping),
		errors.Is(err, errs.ErrServiceStopped), errors.Is(err, errs.ErrServiceQueueFull),
		errors.Is(err, errs.ErrServiceFailed):
		status = http.StatusServiceUnavailable
		message = "service unavailable"
	}
	return Response{
		StatusCode: status,
		Header: http.Header{
			"Content-Type": []string{"application/json; charset=utf-8"},
		},
		Body: []byte(`{"error":"` + message + `"}`),
	}
}

func validateSafeHandlers(handler SafeHandlerFunc, middleware []SafeMiddlewareFunc) {
	if handler == nil {
		panic("ginmodule: Safe Handler 不能为空")
	}
	for _, current := range middleware {
		if current == nil {
			panic("ginmodule: Safe Middleware 不能为空")
		}
	}
}

func deliverSafeResult(channel chan<- safeInvocationResult, result safeInvocationResult) {
	select {
	case channel <- result:
	default:
	}
}

func isRequestBodyTooLarge(err error) bool {
	var maximumError *http.MaxBytesError
	return errors.As(err, &maximumError)
}

type taskRequestValueContext struct {
	context.Context
	request context.Context
}

func (ctx *taskRequestValueContext) Value(key any) any {
	if value := ctx.Context.Value(key); value != nil {
		return value
	}
	return ctx.request.Value(key)
}

func mergeSafeContext(
	taskContext context.Context,
	requestContext context.Context,
) (context.Context, func()) {
	values := &taskRequestValueContext{Context: taskContext, request: requestContext}
	merged, cancelCause := context.WithCancelCause(values)
	stopRequest := context.AfterFunc(requestContext, func() {
		cancelCause(context.Cause(requestContext))
	})
	var cancelDeadline context.CancelFunc = func() {}
	if deadline, exists := requestContext.Deadline(); exists {
		merged, cancelDeadline = context.WithDeadlineCause(
			merged,
			deadline,
			context.DeadlineExceeded,
		)
	}
	return merged, func() {
		stopRequest()
		cancelDeadline()
		cancelCause(context.Canceled)
	}
}
