package ginmodule

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// RouterGroup 是只通过当前 Gin Module 暴露路由能力的普通请求协程分组。
type RouterGroup struct {
	module *Module
	group  *gin.RouterGroup
}

// Use 安装在 HTTP 请求 goroutine 执行的分组 Middleware。
func (module *Module) Use(middleware ...gin.HandlerFunc) gin.IRoutes {
	return module.requireEngine().Use(middleware...)
}

// Group 创建继承当前路径和请求协程 Middleware 的普通路由分组。
func (module *Module) Group(path string, middleware ...gin.HandlerFunc) *RouterGroup {
	return &RouterGroup{module: module, group: module.requireEngine().Group(path, middleware...)}
}

// Handle 注册普通请求协程 Handler；可选 Middleware 按声明顺序先于 Handler 执行。
func (module *Module) Handle(
	method string,
	path string,
	handler gin.HandlerFunc,
	middleware ...gin.HandlerFunc,
) gin.IRoutes {
	return module.requireEngine().Handle(method, path, routeHandlers(handler, middleware)...)
}

// GET 注册普通 GET Handler。
func (module *Module) GET(path string, handler gin.HandlerFunc, middleware ...gin.HandlerFunc) gin.IRoutes {
	return module.Handle(http.MethodGet, path, handler, middleware...)
}

// POST 注册普通 POST Handler。
func (module *Module) POST(path string, handler gin.HandlerFunc, middleware ...gin.HandlerFunc) gin.IRoutes {
	return module.Handle(http.MethodPost, path, handler, middleware...)
}

// PUT 注册普通 PUT Handler。
func (module *Module) PUT(path string, handler gin.HandlerFunc, middleware ...gin.HandlerFunc) gin.IRoutes {
	return module.Handle(http.MethodPut, path, handler, middleware...)
}

// PATCH 注册普通 PATCH Handler。
func (module *Module) PATCH(path string, handler gin.HandlerFunc, middleware ...gin.HandlerFunc) gin.IRoutes {
	return module.Handle(http.MethodPatch, path, handler, middleware...)
}

// DELETE 注册普通 DELETE Handler。
func (module *Module) DELETE(path string, handler gin.HandlerFunc, middleware ...gin.HandlerFunc) gin.IRoutes {
	return module.Handle(http.MethodDelete, path, handler, middleware...)
}

// HEAD 注册普通 HEAD Handler。
func (module *Module) HEAD(path string, handler gin.HandlerFunc, middleware ...gin.HandlerFunc) gin.IRoutes {
	return module.Handle(http.MethodHead, path, handler, middleware...)
}

// OPTIONS 注册普通 OPTIONS Handler。
func (module *Module) OPTIONS(path string, handler gin.HandlerFunc, middleware ...gin.HandlerFunc) gin.IRoutes {
	return module.Handle(http.MethodOptions, path, handler, middleware...)
}

// NoRoute 注册普通请求协程中的 404 Handler。
func (module *Module) NoRoute(handler gin.HandlerFunc, middleware ...gin.HandlerFunc) {
	module.requireEngine().NoRoute(routeHandlers(handler, middleware)...)
}

// NoMethod 注册普通请求协程中的 405 Handler。
func (module *Module) NoMethod(handler gin.HandlerFunc, middleware ...gin.HandlerFunc) {
	module.requireEngine().NoMethod(routeHandlers(handler, middleware)...)
}

// Use 安装在 HTTP 请求 goroutine 执行的分组 Middleware。
func (group *RouterGroup) Use(middleware ...gin.HandlerFunc) {
	group.requireGroup().Use(middleware...)
}

// Group 创建嵌套普通路由分组。
func (group *RouterGroup) Group(path string, middleware ...gin.HandlerFunc) *RouterGroup {
	return &RouterGroup{
		module: group.module,
		group:  group.requireGroup().Group(path, middleware...),
	}
}

// Handle 注册普通请求协程 Handler。
func (group *RouterGroup) Handle(
	method string,
	path string,
	handler gin.HandlerFunc,
	middleware ...gin.HandlerFunc,
) gin.IRoutes {
	return group.requireGroup().Handle(method, path, routeHandlers(handler, middleware)...)
}

// GET 注册普通 GET Handler。
func (group *RouterGroup) GET(path string, handler gin.HandlerFunc, middleware ...gin.HandlerFunc) gin.IRoutes {
	return group.Handle(http.MethodGet, path, handler, middleware...)
}

// POST 注册普通 POST Handler。
func (group *RouterGroup) POST(path string, handler gin.HandlerFunc, middleware ...gin.HandlerFunc) gin.IRoutes {
	return group.Handle(http.MethodPost, path, handler, middleware...)
}

// PUT 注册普通 PUT Handler。
func (group *RouterGroup) PUT(path string, handler gin.HandlerFunc, middleware ...gin.HandlerFunc) gin.IRoutes {
	return group.Handle(http.MethodPut, path, handler, middleware...)
}

// PATCH 注册普通 PATCH Handler。
func (group *RouterGroup) PATCH(path string, handler gin.HandlerFunc, middleware ...gin.HandlerFunc) gin.IRoutes {
	return group.Handle(http.MethodPatch, path, handler, middleware...)
}

// DELETE 注册普通 DELETE Handler。
func (group *RouterGroup) DELETE(path string, handler gin.HandlerFunc, middleware ...gin.HandlerFunc) gin.IRoutes {
	return group.Handle(http.MethodDelete, path, handler, middleware...)
}

// HEAD 注册普通 HEAD Handler。
func (group *RouterGroup) HEAD(path string, handler gin.HandlerFunc, middleware ...gin.HandlerFunc) gin.IRoutes {
	return group.Handle(http.MethodHead, path, handler, middleware...)
}

// OPTIONS 注册普通 OPTIONS Handler。
func (group *RouterGroup) OPTIONS(path string, handler gin.HandlerFunc, middleware ...gin.HandlerFunc) gin.IRoutes {
	return group.Handle(http.MethodOptions, path, handler, middleware...)
}

func routeHandlers(handler gin.HandlerFunc, middleware []gin.HandlerFunc) []gin.HandlerFunc {
	if handler == nil {
		panic("ginmodule: Handler 不能为空")
	}
	handlers := make([]gin.HandlerFunc, 0, len(middleware)+1)
	for _, current := range middleware {
		if current == nil {
			panic("ginmodule: Middleware 不能为空")
		}
		handlers = append(handlers, current)
	}
	return append(handlers, handler)
}

func (group *RouterGroup) requireGroup() *gin.RouterGroup {
	if group == nil || group.module == nil || group.group == nil {
		panic("ginmodule: nil RouterGroup")
	}
	return group.group
}
