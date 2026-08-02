// 本示例展示 Application、Node 查询与统一 Diagnostics 快照外观。
package main

import (
	"context"
	"fmt"

	"github.com/duanhf2012/origin/v3/application"
	"github.com/duanhf2012/origin/v3/service"
)

// app 是当前示例唯一的 Application。
var app = application.New()

// DiagnosticsService 在启动后读取只读快照，不接触内部运行时对象。
type DiagnosticsService struct{ service.Service }

// OnStart 同时演示 App.State、Nodes、Node 和 Diagnostics。
func (target *DiagnosticsService) OnStart(context.Context) error {
	// Diagnostics 每次都返回当前时间点的新快照，不要长期缓存当作实时状态。
	snapshot := app.Diagnostics()
	// Nodes 返回只读用途的 Node 列表副本，Node 可按 ID 再精确查询。
	nodes := app.Nodes()
	currentNode, found := app.Node(target.NodeID())
	if !found {
		return fmt.Errorf("current node %q not found", target.NodeID())
	}
	nodeSnapshot := currentNode.Diagnostics()
	target.Logger().Info(fmt.Sprintf(
		"diagnostics: app_state=%v snapshot_state=%s nodes=%d node=%s services=%d goroutines=%d",
		app.State(),
		snapshot.Application.State,
		len(nodes),
		nodeSnapshot.NodeID,
		len(nodeSnapshot.Services),
		snapshot.Runtime.Goroutines,
	))
	return nil
}

// init 登记诊断示例 Service。
func init() { app.Setup(&DiagnosticsService{}) }

// main 启动 Application。
func main() { app.Start() }
