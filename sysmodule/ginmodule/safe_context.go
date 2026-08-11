package ginmodule

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/duanhf2012/origin/v3/errs"
	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
)

// SafeHandlerFunc 是在所属 Service 工作协程执行的最终 HTTP 业务回调。
type SafeHandlerFunc func(*SafeContext)

// SafeMiddlewareFunc 是在所属 Service 工作协程执行的可选前后处理器。
//
// Middleware 成功时必须调用 SafeContext.Next 才会继续下一层；失败时应写入响应并 Abort。
type SafeMiddlewareFunc func(*SafeContext)

// SafeContext 保存请求快照、Service Task Context 和尚未提交的私有响应。
//
// SafeContext 只在当前 Safe Handler 链返回前有效，不得交给其他 goroutine 或在返回后保留。
type SafeContext struct {
	ctx        context.Context
	request    *http.Request
	body       []byte
	params     gin.Params
	keys       map[any]any
	clientIP   string
	fullPath   string
	handler    SafeHandlerFunc
	middleware []SafeMiddlewareFunc
	index      int
	aborted    bool
	inHandler  bool

	statusCode  int
	header      http.Header
	response    []byte
	rendered    bool
	responseErr error
}

// Context 返回保留 Service 执行令牌并继承 HTTP 取消、Deadline 和 Value 的 Context。
func (ctx *SafeContext) Context() context.Context {
	return ctx.ctx
}

// Request 返回当前 Safe 回调独占的请求克隆；其中 Body 已受配置上限约束。
func (ctx *SafeContext) Request() *http.Request {
	return ctx.request
}

// Param 返回路由参数。
func (ctx *SafeContext) Param(key string) string {
	return ctx.params.ByName(key)
}

// Query 返回首个 Query 参数值；不存在时返回空字符串。
func (ctx *SafeContext) Query(key string) string {
	value, _ := ctx.GetQuery(key)
	return value
}

// GetQuery 返回首个 Query 参数值及其是否存在。
func (ctx *SafeContext) GetQuery(key string) (string, bool) {
	values, exists := ctx.request.URL.Query()[key]
	if !exists || len(values) == 0 {
		return "", exists
	}
	return values[0], true
}

// GetHeader 返回请求 Header 的首个值。
func (ctx *SafeContext) GetHeader(key string) string {
	return ctx.request.Header.Get(key)
}

// ClientIP 返回请求 goroutine 在可信代理规则下解析并冻结的客户端 IP。
func (ctx *SafeContext) ClientIP() string {
	return ctx.clientIP
}

// FullPath 返回 Gin 匹配后的完整路由模板。
func (ctx *SafeContext) FullPath() string {
	return ctx.fullPath
}

// Get 返回请求 Middleware 写入并在投递前浅复制的值。
func (ctx *SafeContext) Get(key string) (any, bool) {
	value, exists := ctx.keys[key]
	return value, exists
}

// MustGet 返回请求 Middleware 写入的值；不存在时 panic，并由 Service Task 边界转换为安全 500。
func (ctx *SafeContext) MustGet(key string) any {
	value, exists := ctx.Get(key)
	if !exists {
		panic("ginmodule: SafeContext key 不存在: " + key)
	}
	return value
}

// GetRawData 返回请求 Body 的独立副本，不转移 SafeContext 内部缓冲区所有权。
func (ctx *SafeContext) GetRawData() ([]byte, error) {
	return bytes.Clone(ctx.body), nil
}

// ShouldBindJSON 使用 Gin JSON Binding 和 Validator 绑定已经冻结的请求 Body。
func (ctx *SafeContext) ShouldBindJSON(value any) error {
	if value == nil {
		return errs.ErrInvalidArgument
	}
	request := ctx.request.Clone(ctx.ctx)
	request.Body = http.NoBody
	if len(ctx.body) > 0 {
		request.Body = io.NopCloser(bytes.NewReader(ctx.body))
	}
	request.ContentLength = int64(len(ctx.body))
	return binding.JSON.Bind(request, value)
}

// Header 设置尚未提交的响应 Header；完成最终渲染后再设置会成为契约错误。
func (ctx *SafeContext) Header(key, value string) {
	if ctx.responseErr != nil {
		return
	}
	if ctx.rendered {
		ctx.responseErr = errs.NewMessage(errs.CodeInternal, "ginmodule: 响应渲染后不能修改 Header")
		return
	}
	ctx.header.Set(key, value)
}

// Status 设置无 Body 响应或后续渲染使用的状态码。
func (ctx *SafeContext) Status(code int) {
	if ctx.responseErr != nil {
		return
	}
	if ctx.rendered {
		ctx.responseErr = errs.NewMessage(errs.CodeInternal, "ginmodule: 响应渲染后不能修改状态码")
		return
	}
	ctx.statusCode = code
}

// JSON 把 value 编码到私有响应缓冲区。
func (ctx *SafeContext) JSON(code int, value any) {
	body, err := json.Marshal(value)
	if err != nil {
		ctx.setResponseError(err)
		return
	}
	ctx.render(code, "application/json; charset=utf-8", body)
}

// String 把格式化文本编码到私有响应缓冲区。
func (ctx *SafeContext) String(code int, format string, values ...any) {
	ctx.render(code, "text/plain; charset=utf-8", []byte(fmt.Sprintf(format, values...)))
}

// Data 把调用方数据复制到私有响应缓冲区。
func (ctx *SafeContext) Data(code int, contentType string, data []byte) {
	ctx.render(code, contentType, data)
}

// Next 进入下一层 Safe Middleware 或最终 Handler，并在其返回后恢复当前 Middleware。
func (ctx *SafeContext) Next() {
	if ctx.aborted || ctx.responseErr != nil {
		return
	}
	if ctx.inHandler {
		ctx.responseErr = errs.NewMessage(errs.CodeInternal, "ginmodule: 最终 Safe Handler 不能调用 Next")
		return
	}
	ctx.index++
	if ctx.index < len(ctx.middleware) {
		ctx.middleware[ctx.index](ctx)
		return
	}
	if ctx.index == len(ctx.middleware) {
		ctx.inHandler = true
		ctx.handler(ctx)
		ctx.inHandler = false
	}
}

// Abort 阻止进入下一层 Safe Middleware 或最终 Handler。
func (ctx *SafeContext) Abort() {
	ctx.aborted = true
}

// AbortWithStatusJSON 写入 JSON 响应并阻止后续 Safe 链执行。
func (ctx *SafeContext) AbortWithStatusJSON(code int, value any) {
	ctx.JSON(code, value)
	ctx.Abort()
}

// IsAborted 报告 Safe 链是否已经被中止。
func (ctx *SafeContext) IsAborted() bool {
	return ctx.aborted
}

func (ctx *SafeContext) run() {
	if len(ctx.middleware) == 0 {
		ctx.inHandler = true
		ctx.handler(ctx)
		ctx.inHandler = false
		return
	}
	ctx.index = -1
	ctx.Next()
}

func (ctx *SafeContext) render(code int, contentType string, body []byte) {
	if ctx.responseErr != nil {
		return
	}
	if ctx.rendered {
		ctx.responseErr = errs.NewMessage(errs.CodeInternal, "ginmodule: Safe 响应只能最终渲染一次")
		return
	}
	ctx.statusCode = code
	if contentType != "" {
		ctx.header.Set("Content-Type", contentType)
	}
	ctx.response = bytes.Clone(body)
	ctx.rendered = true
}

func (ctx *SafeContext) setResponseError(err error) {
	if ctx.responseErr == nil {
		ctx.responseErr = err
		ctx.aborted = true
	}
}
