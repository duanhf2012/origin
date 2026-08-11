package ginmodule

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// SafeRouterGroup 是 Middleware 与最终 Handler 都在 Service 工作协程执行的路由分组。
type SafeRouterGroup struct {
	module     *Module
	group      *gin.RouterGroup
	middleware []SafeMiddlewareFunc
}

// SafeGroup 创建根 Safe 路由分组；可选 Middleware 在 Service 工作协程执行。
func (module *Module) SafeGroup(
	path string,
	middleware ...SafeMiddlewareFunc,
) *SafeRouterGroup {
	validateSafeMiddleware(middleware)
	return &SafeRouterGroup{
		module:     module,
		group:      module.requireEngine().Group(path),
		middleware: append([]SafeMiddlewareFunc(nil), middleware...),
	}
}

// SafeGroup 在普通请求分组下创建 Safe 路由分组。
//
// 父 RouterGroup 的 Gin Middleware 仍在请求 goroutine 执行，本方法接收的 Middleware 在 Service
// 工作协程执行。
func (group *RouterGroup) SafeGroup(
	path string,
	middleware ...SafeMiddlewareFunc,
) *SafeRouterGroup {
	validateSafeMiddleware(middleware)
	return &SafeRouterGroup{
		module:     group.module,
		group:      group.requireGroup().Group(path),
		middleware: append([]SafeMiddlewareFunc(nil), middleware...),
	}
}

// Group 创建继承 Service 工作协程 Middleware 的嵌套 Safe 分组。
func (group *SafeRouterGroup) Group(
	path string,
	middleware ...SafeMiddlewareFunc,
) *SafeRouterGroup {
	validateSafeMiddleware(middleware)
	current := group.requireGroup()
	combined := make([]SafeMiddlewareFunc, 0, len(group.middleware)+len(middleware))
	combined = append(combined, group.middleware...)
	combined = append(combined, middleware...)
	return &SafeRouterGroup{
		module:     group.module,
		group:      current.Group(path),
		middleware: combined,
	}
}

// Handle 注册 Safe 分组路由；分组 Middleware 先于单路由 Middleware 执行。
func (group *SafeRouterGroup) Handle(
	method string,
	path string,
	handler SafeHandlerFunc,
	middleware ...SafeMiddlewareFunc,
) gin.IRoutes {
	validateSafeHandlers(handler, middleware)
	combined := make([]SafeMiddlewareFunc, 0, len(group.middleware)+len(middleware))
	combined = append(combined, group.middleware...)
	combined = append(combined, middleware...)
	return group.requireGroup().Handle(method, path, group.module.safeAdapter(handler, combined))
}

// GET 注册在 Service 工作协程执行的 GET Handler。
func (group *SafeRouterGroup) GET(
	path string,
	handler SafeHandlerFunc,
	middleware ...SafeMiddlewareFunc,
) gin.IRoutes {
	return group.Handle(http.MethodGet, path, handler, middleware...)
}

// POST 注册在 Service 工作协程执行的 POST Handler。
func (group *SafeRouterGroup) POST(
	path string,
	handler SafeHandlerFunc,
	middleware ...SafeMiddlewareFunc,
) gin.IRoutes {
	return group.Handle(http.MethodPost, path, handler, middleware...)
}

// PUT 注册在 Service 工作协程执行的 PUT Handler。
func (group *SafeRouterGroup) PUT(
	path string,
	handler SafeHandlerFunc,
	middleware ...SafeMiddlewareFunc,
) gin.IRoutes {
	return group.Handle(http.MethodPut, path, handler, middleware...)
}

// PATCH 注册在 Service 工作协程执行的 PATCH Handler。
func (group *SafeRouterGroup) PATCH(
	path string,
	handler SafeHandlerFunc,
	middleware ...SafeMiddlewareFunc,
) gin.IRoutes {
	return group.Handle(http.MethodPatch, path, handler, middleware...)
}

// DELETE 注册在 Service 工作协程执行的 DELETE Handler。
func (group *SafeRouterGroup) DELETE(
	path string,
	handler SafeHandlerFunc,
	middleware ...SafeMiddlewareFunc,
) gin.IRoutes {
	return group.Handle(http.MethodDelete, path, handler, middleware...)
}

// HEAD 注册在 Service 工作协程执行的 HEAD Handler。
func (group *SafeRouterGroup) HEAD(
	path string,
	handler SafeHandlerFunc,
	middleware ...SafeMiddlewareFunc,
) gin.IRoutes {
	return group.Handle(http.MethodHead, path, handler, middleware...)
}

// OPTIONS 注册在 Service 工作协程执行的 OPTIONS Handler。
func (group *SafeRouterGroup) OPTIONS(
	path string,
	handler SafeHandlerFunc,
	middleware ...SafeMiddlewareFunc,
) gin.IRoutes {
	return group.Handle(http.MethodOptions, path, handler, middleware...)
}

func (group *SafeRouterGroup) requireGroup() *gin.RouterGroup {
	if group == nil || group.module == nil || group.group == nil {
		panic("ginmodule: nil SafeRouterGroup")
	}
	return group.group
}

func validateSafeMiddleware(middleware []SafeMiddlewareFunc) {
	for _, current := range middleware {
		if current == nil {
			panic("ginmodule: Safe Middleware 不能为空")
		}
	}
}
