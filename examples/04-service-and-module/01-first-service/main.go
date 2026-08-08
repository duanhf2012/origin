// 本示例展示新增业务 Service 所需的最小代码结构。
package main

import (
	"context"

	"github.com/duanhf2012/origin/v3/application"
	"github.com/duanhf2012/origin/v3/service"
)

// app 是当前示例唯一的 Application。
var app = application.New()

// InventoryService 匿名嵌入 service.Service，从而获得调度、日志和生命周期能力。
type InventoryService struct{ service.Service }

// OnStart 在库存服务准备好处理业务时记录日志。
func (target *InventoryService) OnStart(context.Context) error {
	target.Logger().Info("inventory service is ready")
	return nil
}

// init 登记类型后，YAML 才能用 InventoryService 作为实际 ServiceName。
func init() { app.Setup(&InventoryService{}) }

// main 启动 Application。
func main() { app.Start() }
