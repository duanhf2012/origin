// 本示例展示一个 Application 内多个 Node 与多个 Service 实例的关系。
package main

import (
	"context"

	"github.com/duanhf2012/origin/v3/application"
	originlog "github.com/duanhf2012/origin/v3/log"
	"github.com/duanhf2012/origin/v3/service"
)

// app 管理两个 Node 的统一启动和反序停止。
var app = application.New()

// GatewayService 只覆盖自己需要的启动回调。
type GatewayService struct{ service.Service }

// OnStart 表示网关已经完成业务启动。
func (target *GatewayService) OnStart(context.Context) error {
	target.Logger().Info("gateway is ready")
	return nil
}

// PlayerService 可由配置在不同 Node 上创建相互独立的运行实例。
type PlayerService struct{ service.Service }

// OnInit 使用 NodeID 证明每个实例已经绑定到自己的所属 Node。
func (target *PlayerService) OnInit() error {
	target.Logger().Info("player initialized", originlog.String("node_id", target.NodeID()))
	return nil
}

// OnStart 在全部初始化成功后进入启动阶段。
func (target *PlayerService) OnStart(context.Context) error {
	target.Logger().Info("player is ready")
	return nil
}

// OnStop 由 Application 按 Node 和 Service 的反序生命周期调用。
func (target *PlayerService) OnStop(context.Context) error {
	target.Logger().Info("player stopped")
	return nil
}

// init 只登记 Go 类型模板，实际启用哪些 Service 由 YAML 决定。
func init() { app.Setup(&GatewayService{}, &PlayerService{}) }

// main 启动命令行 Application。
func main() { app.Start() }
