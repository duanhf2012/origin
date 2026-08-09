package application

import (
	"fmt"
	"reflect"

	"github.com/duanhf2012/origin/v3/admin"
	"github.com/duanhf2012/origin/v3/errs"
	"github.com/duanhf2012/origin/v3/node"
	"github.com/duanhf2012/origin/v3/service"
)

// adminState 保存当前 Application 独占的管理面注册、冻结路由与 HTTP 资源。
//
// 该状态嵌入 Application，由 Application.mu 串行化冷路径写入；不使用包级注册表。
type adminState struct {
	adminEndpoints []admin.Endpoint
	adminGuard     admin.Guard
	adminRoutes    *adminRouteTable
	adminHTTP      httpRuntime
	// adminFreezeDone 在首次冻结开始时创建、终态发布后关闭；成功与失败都只收集一次。
	adminFreezeDone chan struct{}
	adminFreezeErr  error
}

// adminEndpointKey 用 HTTP Method 和 Endpoint 名称唯一定位 Application 自定义路由。
type adminEndpointKey struct {
	method   string
	endpoint string
}

// serviceAdminRouteKey 用真实运行身份和 Endpoint 身份唯一定位 Service 路由。
type serviceAdminRouteKey struct {
	nodeID      string
	serviceName string
	method      string
	endpoint    string
}

// boundServiceAdminEndpoint 把冻结的描述符绑定到创建它的真实 Service 实例。
type boundServiceAdminEndpoint struct {
	target   service.IService
	endpoint admin.Endpoint
}

// adminRouteTable 是一次构建后只读的 Application/Service 管理路由快照。
type adminRouteTable struct {
	application map[adminEndpointKey]admin.Endpoint
	services    map[serviceAdminRouteKey]boundServiceAdminEndpoint
}

// RegisterAdminEndpoint 在首次执行命令前登记 Application 自定义管理端点。
func (app *Application) RegisterAdminEndpoint(endpoint admin.Endpoint) error {
	if app == nil {
		return errs.NewMessage(errs.CodeInvalidArgument, "Application 不能为空")
	}
	if err := endpoint.Validate(); err != nil {
		return err
	}

	app.mu.Lock()
	defer app.mu.Unlock()
	if app.commandRun || app.State() != StateCreated || app.adminFreezeDone != nil {
		return errs.NewMessage(
			errs.CodeInvalidArgument,
			"Admin Endpoint 只能在 Application 创建后、执行命令前注册",
		)
	}
	for _, registered := range app.adminEndpoints {
		if registered.Method() == endpoint.Method() && registered.Name() == endpoint.Name() {
			return errs.NewMessage(
				errs.CodeInvalidArgument,
				fmt.Sprintf("Application Admin Endpoint %s %q 重复", endpoint.Method(), endpoint.Name()),
			)
		}
	}

	// Endpoint 是不可变值描述符；按值追加让 Application 拥有独立 Slice 与元素副本。
	app.adminEndpoints = append(app.adminEndpoints, endpoint)
	return nil
}

// SetAdminGuard 在首次执行命令前设置当前 Application 唯一的管理授权策略。
func (app *Application) SetAdminGuard(guard admin.Guard) error {
	if app == nil || isNilAdminGuard(guard) {
		return errs.NewMessage(errs.CodeInvalidArgument, "Application 和 Admin Guard 不能为空")
	}

	app.mu.Lock()
	defer app.mu.Unlock()
	if app.commandRun || app.State() != StateCreated || app.adminFreezeDone != nil {
		return errs.NewMessage(
			errs.CodeInvalidArgument,
			"Admin Guard 只能在 Application 创建后、执行命令前设置",
		)
	}
	if app.adminGuard != nil {
		return errs.NewMessage(errs.CodeInvalidArgument, "Admin Guard 只能设置一次")
	}
	app.adminGuard = guard
	return nil
}

// isNilAdminGuard 同时识别 nil 接口和装箱后底层值为 nil 的 Guard。
//
// Guard 是启动冷注册路径，这里使用一次反射换取完整的 interface nil
// 语义。IsNil 只能对 nilable kind 调用，因此必须先显式分类，不使用 panic 探测。
func isNilAdminGuard(guard admin.Guard) bool {
	if guard == nil {
		return true
	}
	value := reflect.ValueOf(guard)
	switch value.Kind() {
	case reflect.Chan,
		reflect.Func,
		reflect.Interface,
		reflect.Map,
		reflect.Pointer,
		reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

// freezeAdminRoutes 在任何 Service OnInit 前一次收集真实实例的 Endpoint 并发布只读路由表。
func (app *Application) freezeAdminRoutes(nodes []*node.Node) error {
	if app == nil {
		return errs.NewMessage(errs.CodeInvalidArgument, "Application 不能为空")
	}

	// 在锁内唯一确定收集者；后续调用只等待同一终态，不重新执行 Provider。
	app.mu.Lock()
	if done := app.adminFreezeDone; done != nil {
		app.mu.Unlock()
		<-done
		app.mu.Lock()
		err := app.adminFreezeErr
		app.mu.Unlock()
		return err
	}
	done := make(chan struct{})
	app.adminFreezeDone = done
	applicationEndpoints := append([]admin.Endpoint(nil), app.adminEndpoints...)
	app.mu.Unlock()

	// Provider 是业务实现，收集时不持有 Application.mu，避免静态声明阻塞诊断读取等冷路径。
	routes, err := buildAdminRouteTable(applicationEndpoints, nodes)

	// 无论成功或失败都发布唯一终态；关闭 Channel 后所有等待者读到同一结果。
	app.mu.Lock()
	if err == nil {
		app.adminRoutes = routes
	}
	app.adminFreezeErr = err
	close(done)
	app.mu.Unlock()
	return err
}

// buildAdminRouteTable 在不持有 Application 锁时构建完整临时表，失败直接丢弃半成品。
func buildAdminRouteTable(
	applicationEndpoints []admin.Endpoint,
	nodes []*node.Node,
) (*adminRouteTable, error) {
	routes := &adminRouteTable{
		application: make(map[adminEndpointKey]admin.Endpoint, len(applicationEndpoints)),
		services:    make(map[serviceAdminRouteKey]boundServiceAdminEndpoint),
	}

	// Application 端点同样通过值副本进入新 Map，不让注册 Slice 成为运行期路由表。
	for _, endpoint := range applicationEndpoints {
		key := adminEndpointKey{method: endpoint.Method(), endpoint: endpoint.Name()}
		if _, duplicate := routes.application[key]; duplicate {
			return nil, invalidConfigf(
				"Application Admin Endpoint %s %q 重复",
				endpoint.Method(),
				endpoint.Name(),
			)
		}
		routes.application[key] = endpoint
	}

	// Node.Services 按配置顺序返回真实绑定实例；每个 Provider 仅在此处调用一次。
	for _, current := range nodes {
		if current == nil {
			return nil, invalidConfigf("Admin 路由不能包含空 Node")
		}
		for _, target := range current.Services() {
			provider, ok := target.(admin.Provider)
			if !ok {
				continue
			}
			endpoints, err := collectAdminEndpoints(current.ID(), target.Name(), provider)
			if err != nil {
				return nil, err
			}
			for _, endpoint := range endpoints {
				if err := endpoint.Validate(); err != nil {
					return nil, invalidConfigf(
						"Node %q Service %q Admin Endpoint 无效: %v",
						current.ID(),
						target.Name(),
						err,
					)
				}
				key := serviceAdminRouteKey{
					nodeID:      current.ID(),
					serviceName: target.Name(),
					method:      endpoint.Method(),
					endpoint:    endpoint.Name(),
				}
				if _, duplicate := routes.services[key]; duplicate {
					return nil, invalidConfigf(
						"Node %q Service %q Admin Endpoint %s %q 重复",
						key.nodeID,
						key.serviceName,
						key.method,
						key.endpoint,
					)
				}
				routes.services[key] = boundServiceAdminEndpoint{
					target:   target,
					endpoint: endpoint,
				}
			}
		}
	}

	return routes, nil
}

// collectAdminEndpoints 把业务 Provider panic 恢复为含运行身份的配置错误。
func collectAdminEndpoints(
	nodeID string,
	serviceName string,
	provider admin.Provider,
) (endpoints []admin.Endpoint, err error) {
	defer func() {
		if recover() != nil {
			// Provider panic 值可能由业务生成，错误只保留定位所需的运行身份。
			endpoints = nil
			err = invalidConfigf(
				"Node %q Service %q Admin Provider panic",
				nodeID,
				serviceName,
			)
		}
	}()
	// append 建立 Application 拥有的 Slice，业务随后改写原 Slice 不会替换已收集的 Endpoint 值。
	return append([]admin.Endpoint(nil), provider.AdminEndpoints()...), nil
}
