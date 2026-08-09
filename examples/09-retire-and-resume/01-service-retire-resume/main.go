// 本示例展示外部命令控制 Application 退休/恢复，以及 Service 状态事件监听。
package main

import (
	"context"

	"github.com/duanhf2012/origin/v3/application"
	"github.com/duanhf2012/origin/v3/service"
)

// app 是当前示例唯一的 Application。
var app = application.New()

// MaintenanceService 监听由 Application 控制命令触发的真实状态变化。
type MaintenanceService struct{ service.Service }

func (target *MaintenanceService) OnInit() error {
	return target.SubscribeEvent(
		service.ServiceStateChangedEventID,
		func(_ context.Context, raw service.Event) error {
			changed := raw.(service.ServiceStateChanged)
			target.Logger().Info(
				"service state changed: " +
					changed.Previous.String() + " -> " + changed.Current.String(),
			)
			return nil
		},
	)
}

// init 登记维护状态示例 Service。
func init() { app.Setup(&MaintenanceService{}) }

// main 启动 Application。
func main() { app.Start() }
