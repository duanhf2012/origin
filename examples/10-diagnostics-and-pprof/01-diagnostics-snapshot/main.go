// 本示例展示 Application、Node 查询与统一 Diagnostics 快照外观。
package main

import (
	"context"
	"fmt"

	"github.com/duanhf2012/origin/v3/application"
	originlog "github.com/duanhf2012/origin/v3/log"
	"github.com/duanhf2012/origin/v3/service"
)

// app 是当前示例唯一的 Application。
var app = application.New()

// DiagnosticsService 在启动后读取只读快照，不接触内部运行时对象。
type DiagnosticsService struct{ service.Service }

// OnStart 同时演示 Service.Application 与进程级 Diagnostics。
func (target *DiagnosticsService) OnStart(context.Context) error {
	// Service.Application 返回受限且并发安全的进程外观，不开放 Stop、Setup 等高风险能力。
	runtime := target.Application()
	if runtime == nil {
		return fmt.Errorf("application runtime is unavailable")
	}
	// Diagnostics 每次都返回当前时间点的新快照，不要长期缓存当作实时状态。
	snapshot := runtime.Diagnostics()
	// Nodes 返回只读用途的 Node 列表副本，Node 可按 ID 再精确查询。
	nodes := app.Nodes()
	currentNode, found := app.Node(target.NodeID())
	if !found {
		return fmt.Errorf("current node %q not found", target.NodeID())
	}
	// Node.Service 与 Services 只查询本 Node 的本地实例，不经过远端发现或 RPC 路由。
	localService, localFound := currentNode.Service(target.Name())
	serviceStatus, statusFound := currentNode.ServiceStatus(target.Name())
	transport := currentNode.TransportStatus()
	discovery := currentNode.DiscoveryStatus()
	health := currentNode.HealthStatus()
	nodeSnapshot := currentNode.Diagnostics()
	// 进程级管理与观测事件使用包级日志，不需要暴露 Application.Logger。
	originlog.Info("application diagnostics snapshot collected")
	target.Logger().Info(fmt.Sprintf(
		"diagnostics: app_state=%v snapshot_state=%s nodes=%d node=%s private=%t services=%d local_found=%t status_found=%t service_state=%s ready=%t degraded=%t transport=%v discovery=%v goroutines=%d",
		app.State(),
		snapshot.Application.State,
		len(nodes),
		currentNode.ID(),
		currentNode.Private(),
		len(nodeSnapshot.Services),
		localFound && localService != nil,
		statusFound,
		serviceStatus.State,
		health.Readiness,
		health.Degraded,
		transport.State,
		discovery.State,
		snapshot.Runtime.Goroutines,
	))
	return nil
}

// init 登记诊断示例 Service。
func init() { app.Setup(&DiagnosticsService{}) }

// main 启动 Application。
func main() { app.Start() }
