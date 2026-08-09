// 本示例只在进程内读取 Application.Diagnostics，不启动任何管理 HTTP Listener。
package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/duanhf2012/origin/v3/application"
	"github.com/duanhf2012/origin/v3/service"
)

// app 是快照根对象，也是当前示例全部 Node 的唯一所有者。
var app = application.New()

// SnapshotService 在启动后读取一次完整、只读、按值拥有的诊断快照。
type SnapshotService struct{ service.Service }

// OnStart 展示 Snapshot 根、Application、Runtime、Node 与 Service 的层级关系。
func (target *SnapshotService) OnStart(context.Context) error {
	runtime := target.Application()
	if runtime == nil {
		return fmt.Errorf("application runtime is unavailable")
	}

	// 每次 Diagnostics 都重新聚合当前状态，返回的是一次性的 Full Snapshot 值。
	// 返回值不持有可变 Node/Service 对象，调用方可以独立编码或传递；但旧快照不会
	// 随运行状态自动更新，需要最新数据时必须重新调用 Diagnostics。
	snapshot := runtime.Diagnostics()
	// Snapshot 已经是进程内的完整值模型；这里的 JSON 编码只发生在示例的 OnStart，
	// 不会启动 Admin Listener，也不会把快照自动发布到网络。
	encoded, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return fmt.Errorf("encode diagnostics snapshot: %w", err)
	}
	fmt.Println(string(encoded))
	target.Logger().Info(fmt.Sprintf(
		"snapshot schema=%d app=%s admin=%s pprof=%s nodes=%d services_in_current_node=%d",
		snapshot.SchemaVersion,
		snapshot.Application.State,
		snapshot.Application.AdminServer.State,
		snapshot.Application.Pprof.State,
		len(snapshot.Nodes),
		len(snapshot.Nodes[0].Services),
	))
	return nil
}

// init 安装示例 Service。
func init() { app.Setup(&SnapshotService{}) }

// main 启动 Application；脚本不传 --admin 或 --pprof，因此两个 Listener 都保持关闭。
func main() { app.Start() }
