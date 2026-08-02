// 本示例用两组日志锁定同一 Node 内 Service 的正序启动和反序停止。
package main

import (
	"context"

	"github.com/duanhf2012/origin/v3/application"
	"github.com/duanhf2012/origin/v3/service"
)

// app 是当前示例唯一的 Application。
var app = application.New()

// FirstService 在 YAML services 列表中位于前面，因此先启动、后停止。
type FirstService struct{ service.Service }

// SecondService 位于后面，因此后启动、先停止。
type SecondService struct{ service.Service }

func (target *FirstService) OnStart(context.Context) error {
	target.Logger().Info("first starts before second")
	return nil
}

func (target *FirstService) OnStop(context.Context) error {
	target.Logger().Info("first stops last")
	return nil
}

func (target *SecondService) OnStart(context.Context) error {
	target.Logger().Info("second starts after first")
	return nil
}

func (target *SecondService) OnStop(context.Context) error {
	target.Logger().Info("second stops first")
	return nil
}

// init 登记两个可由 YAML 引用的 Service 类型。
func init() { app.Setup(&FirstService{}, &SecondService{}) }

// main 启动 Application；实际顺序由配置而不是 Go 声明顺序决定。
func main() { app.Start() }
