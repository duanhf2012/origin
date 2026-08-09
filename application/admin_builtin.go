package application

import (
	"context"
	"net/http"

	"github.com/duanhf2012/origin/v3/admin"
	"github.com/duanhf2012/origin/v3/node"
	"github.com/duanhf2012/origin/v3/service"
)

const adminApplicationEndpointPattern = "/admin/v1/application/endpoints/{endpoint}"

const adminServiceEndpointPattern = "/admin/v1/nodes/{node}/services/{service}/endpoints/{endpoint}"

// adminControlServiceKey 用真实 NodeID 和 ServiceName 定位内置 Service 生命周期目标。
type adminControlServiceKey struct {
	nodeID      string
	serviceName string
}

// adminControlTargets 是 Server 启动冷路径生成、请求期只读的生命周期目标索引。
type adminControlTargets struct {
	nodes    map[string]*node.Node
	services map[adminControlServiceKey]service.IService
}

// newAdminServeMux 为当前 Admin Server 构造独占路由树。
//
// routes 是 StartAdminServer 在 Application 锁内取得的冻结指针；Handler 只读其中的 Map，
// 不在请求期扫描 Node/Service、调用 Provider、执行反射或复制路由。
func (app *Application) newAdminServeMux(
	routes *adminRouteTable,
	nodes []*node.Node,
) *http.ServeMux {
	mux := http.NewServeMux()
	controls := buildAdminControlTargets(nodes)
	app.registerAdminControlRoutes(mux, controls)

	// GET/POST 使用两个显式 Method Pattern。相同 EndpointName 可以分别绑定查询和修改，
	// 未登记的 Method/Name 组合只返回固定 404，不回退到另一种 Method。
	mux.HandleFunc(
		http.MethodGet+" "+adminApplicationEndpointPattern,
		app.applicationAdminEndpointHandler(routes, http.MethodGet),
	)
	mux.HandleFunc(
		http.MethodPost+" "+adminApplicationEndpointPattern,
		app.applicationAdminEndpointHandler(routes, http.MethodPost),
	)
	mux.HandleFunc(
		http.MethodGet+" "+adminServiceEndpointPattern,
		app.serviceAdminEndpointHandler(routes, http.MethodGet),
	)
	mux.HandleFunc(
		http.MethodPost+" "+adminServiceEndpointPattern,
		app.serviceAdminEndpointHandler(routes, http.MethodPost),
	)
	return mux
}

// buildAdminControlTargets 在 Listener 启动前一次索引真实 Node/Service；请求期不再扫描。
func buildAdminControlTargets(nodes []*node.Node) adminControlTargets {
	targets := adminControlTargets{
		nodes:    make(map[string]*node.Node, len(nodes)),
		services: make(map[adminControlServiceKey]service.IService),
	}
	for _, current := range nodes {
		if current == nil {
			continue
		}
		nodeID := current.ID()
		targets.nodes[nodeID] = current
		for _, target := range current.Services() {
			if target == nil {
				continue
			}
			targets.services[adminControlServiceKey{
				nodeID:      nodeID,
				serviceName: target.Name(),
			}] = target
		}
	}
	return targets
}

// registerAdminControlRoutes 安装六条固定 POST Pattern；其他 Method 由 ServeMux 自动生成
// 405 和 Allow，避免业务 Handler 自己维护另一份方法表。
func (app *Application) registerAdminControlRoutes(
	mux *http.ServeMux,
	targets adminControlTargets,
) {
	retire := admin.Post("retire", emptyAdminControlHandler)
	resume := admin.Post("resume", emptyAdminControlHandler)

	mux.HandleFunc(
		http.MethodPost+" /admin/v1/application/retire",
		app.applicationAdminControlHandler(retire, app.Retire),
	)
	mux.HandleFunc(
		http.MethodPost+" /admin/v1/application/resume",
		app.applicationAdminControlHandler(resume, app.Resume),
	)
	mux.HandleFunc(
		http.MethodPost+" /admin/v1/nodes/{node}/retire",
		app.nodeAdminControlHandler(targets, retire, (*node.Node).Retire),
	)
	mux.HandleFunc(
		http.MethodPost+" /admin/v1/nodes/{node}/resume",
		app.nodeAdminControlHandler(targets, resume, (*node.Node).Resume),
	)
	mux.HandleFunc(
		http.MethodPost+" /admin/v1/nodes/{node}/services/{service}/retire",
		app.serviceAdminControlHandler(targets, retire, service.IService.Retire),
	)
	mux.HandleFunc(
		http.MethodPost+" /admin/v1/nodes/{node}/services/{service}/resume",
		app.serviceAdminControlHandler(targets, resume, service.IService.Resume),
	)
}

// emptyAdminControlHandler 只让内置 Endpoint 复用 POST 输入和成功状态元数据；真实控制动作
// 由已经精确绑定目标的 invoke 闭包执行。
func emptyAdminControlHandler(context.Context, admin.Request) (admin.Response, error) {
	return admin.Empty(http.StatusNoContent), nil
}

// applicationAdminControlHandler 把 Application 生命周期方法直接交给统一 Endpoint 边界。
func (app *Application) applicationAdminControlHandler(
	endpoint admin.Endpoint,
	action func(context.Context) error,
) http.HandlerFunc {
	return func(w http.ResponseWriter, request *http.Request) {
		app.serveAdminControl(w, request, admin.Operation{}, endpoint, action)
	}
}

// nodeAdminControlHandler 只接受启动快照中精确存在的 NodeID。
func (app *Application) nodeAdminControlHandler(
	targets adminControlTargets,
	endpoint admin.Endpoint,
	action func(*node.Node, context.Context) error,
) http.HandlerFunc {
	return func(w http.ResponseWriter, request *http.Request) {
		target, exists := targets.nodes[request.PathValue("node")]
		if !exists {
			finishAdminError(w, http.StatusNotFound, nil)
			return
		}
		app.serveAdminControl(
			w,
			request,
			admin.Operation{NodeID: target.ID()},
			endpoint,
			func(ctx context.Context) error { return action(target, ctx) },
		)
	}
}

// serviceAdminControlHandler 只接受启动快照中精确存在的 NodeID/ServiceName 组合。
func (app *Application) serviceAdminControlHandler(
	targets adminControlTargets,
	endpoint admin.Endpoint,
	action func(service.IService, context.Context) error,
) http.HandlerFunc {
	return func(w http.ResponseWriter, request *http.Request) {
		key := adminControlServiceKey{
			nodeID:      request.PathValue("node"),
			serviceName: request.PathValue("service"),
		}
		target, exists := targets.services[key]
		if !exists {
			finishAdminError(w, http.StatusNotFound, nil)
			return
		}
		app.serveAdminControl(
			w,
			request,
			admin.Operation{NodeID: key.nodeID, ServiceName: target.Name()},
			endpoint,
			func(ctx context.Context) error { return action(target, ctx) },
		)
	}
}

// serveAdminControl 复用统一认证、Body、Deadline、响应和 outer boundary，并只把生命周期
// 最终 error 交给既有安全映射；本层不判断状态，也不伪造失败回滚。
func (app *Application) serveAdminControl(
	w http.ResponseWriter,
	request *http.Request,
	operation admin.Operation,
	endpoint admin.Endpoint,
	action func(context.Context) error,
) {
	app.serveAdminEndpoint(
		w,
		request,
		operation,
		endpoint,
		func(ctx context.Context, _ admin.Request) (admin.Response, error) {
			if err := action(ctx); err != nil {
				return admin.Response{}, err
			}
			return admin.Empty(http.StatusNoContent), nil
		},
	)
}

// applicationAdminEndpointHandler 只把冻结表中精确存在的 Application 描述符交给统一边界。
func (app *Application) applicationAdminEndpointHandler(
	routes *adminRouteTable,
	method string,
) http.HandlerFunc {
	return func(w http.ResponseWriter, request *http.Request) {
		// PathValue 由私有 ServeMux 的单段 wildcard 提供；动态名称在确认命中前不会进入
		// Operation、错误 Body 或审计字段。
		if routes == nil {
			finishAdminError(w, http.StatusNotFound, nil)
			return
		}
		key := adminEndpointKey{
			method:   method,
			endpoint: request.PathValue("endpoint"),
		}
		endpoint, exists := routes.application[key]
		if !exists {
			finishAdminError(w, http.StatusNotFound, nil)
			return
		}

		// Application Handler 不需要额外调度跳转；Endpoint.Invoke 仍负责唯一业务 panic 边界。
		app.serveAdminEndpoint(
			w,
			request,
			admin.Operation{},
			endpoint,
			endpoint.Invoke,
		)
	}
}

// serviceAdminEndpointHandler 把冻结描述符投递到其绑定的真实 Service 串行执行槽。
func (app *Application) serviceAdminEndpointHandler(
	routes *adminRouteTable,
	method string,
) http.HandlerFunc {
	return func(w http.ResponseWriter, request *http.Request) {
		// NodeID、ServiceName 和 EndpointName 必须一起精确命中同一冻结键；不存在的动态
		// 身份统一视为 404，不扫描运行对象寻找近似目标。
		if routes == nil {
			finishAdminError(w, http.StatusNotFound, nil)
			return
		}
		key := serviceAdminRouteKey{
			nodeID:      request.PathValue("node"),
			serviceName: request.PathValue("service"),
			method:      method,
			endpoint:    request.PathValue("endpoint"),
		}
		bound, exists := routes.services[key]
		if !exists {
			finishAdminError(w, http.StatusNotFound, nil)
			return
		}

		// InvokeService 是 Service Admin 调用唯一的调度桥；此处不复制其排队、取消或
		// 串行化语义。outer boundary 继续唯一拥有请求配额、panic 恢复和审计。
		app.serveAdminEndpoint(
			w,
			request,
			admin.Operation{
				NodeID:      key.nodeID,
				ServiceName: key.serviceName,
			},
			bound.endpoint,
			func(ctx context.Context, request admin.Request) (admin.Response, error) {
				return admin.InvokeService(ctx, bound.target, bound.endpoint, request)
			},
		)
	}
}
