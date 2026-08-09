// 本示例展示 Application 自定义 Endpoint，以及内置 Application/Node/Service 控制路由。
package main

import (
	"context"
	"fmt"
	"net/http"
	"sync/atomic"

	"github.com/duanhf2012/origin/v3/admin"
	"github.com/duanhf2012/origin/v3/application"
	"github.com/duanhf2012/origin/v3/errs"
	"github.com/duanhf2012/origin/v3/service"
)

// app 是当前示例唯一的 Application。
var app = application.New()

// controlState 是 Application Endpoint 的并发安全共享状态；这类 Handler 不进入 Service 槽。
type controlState struct {
	routingRevision atomic.Uint64
}

// applicationStatus 是 GET Endpoint 的稳定 JSON 结果。
type applicationStatus struct {
	RoutingRevision uint64 `json:"routing_revision"`
}

// reloadRoutingRequest 是 POST Endpoint 唯一接受的请求结构。
type reloadRoutingRequest struct {
	RoutingRevision uint64 `json:"routing_revision"`
}

// newControlState 建立当前 Application 独占的初始配置版本。
func newControlState() *controlState {
	state := &controlState{}
	state.routingRevision.Store(1)
	return state
}

// applicationEndpoints 返回冷启动阶段注册的 Application 级 GET/POST Endpoint。
func applicationEndpoints(state *controlState) []admin.Endpoint {
	return []admin.Endpoint{
		admin.Get("routing-status", func(
			_ context.Context,
			_ admin.Request,
		) (admin.Response, error) {
			return admin.JSON(http.StatusOK, applicationStatus{
				RoutingRevision: state.routingRevision.Load(),
			})
		}),
		admin.Post("reload-routing", func(
			_ context.Context,
			request admin.Request,
		) (admin.Response, error) {
			var input reloadRoutingRequest
			if err := request.DecodeJSON(&input); err != nil {
				return admin.Response{}, err
			}
			if input.RoutingRevision == 0 {
				return admin.Response{}, errs.NewMessage(
					errs.CodeInvalidArgument,
					"routing_revision 必须大于零",
				)
			}
			state.routingRevision.Store(input.RoutingRevision)
			return admin.Empty(http.StatusNoContent), nil
		}),
	}
}

// ControlService 只是内置 Service retire/resume 路由的明确控制目标。
type ControlService struct{ service.Service }

// OnStart 输出实际 Admin 地址；--admin Listener 在任何 Service OnInit/OnStart 前已绑定。
func (target *ControlService) OnStart(context.Context) error {
	runtime := target.Application()
	if runtime == nil {
		return fmt.Errorf("application runtime is unavailable")
	}
	address, running := runtime.AdminAddress()
	if !running {
		return fmt.Errorf("--admin did not start the listener")
	}
	target.Logger().Info(fmt.Sprintf("admin control: http://%s/admin/v1/", address))
	return nil
}

// init 在命令执行前完成类型安装和 Application Endpoint 注册。
func init() {
	app.Setup(&ControlService{})
	state := newControlState()
	for _, endpoint := range applicationEndpoints(state) {
		if err := app.RegisterAdminEndpoint(endpoint); err != nil {
			// 固定源码中的重复或非法 Endpoint 是不可继续的程序装配错误。
			panic(fmt.Sprintf("register application admin endpoint: %v", err))
		}
	}
}

// main 启动 Application。
func main() { app.Start() }
