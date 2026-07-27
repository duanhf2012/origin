// lifecycle 展示 M7 最小 Application、Node 和 Service 生命周期外观。
package main

import (
	"context"

	"github.com/duanhf2012/origin/v3/application"
	"github.com/duanhf2012/origin/v3/service"
)

// app 是当前示例可执行程序明确持有的唯一 Application，不是框架全局注册表。
var app = application.New()

// GatewayService 展示只需要默认生命周期的最小 Service。
type GatewayService struct {
	service.Service
}

// PlayerService 展示按需覆盖启动和停止回调。
type PlayerService struct {
	service.Service
}

func (target *PlayerService) OnStart(context.Context) error {
	target.Logger().Info("player service started")
	return nil
}

func (target *PlayerService) OnStop(context.Context) error {
	target.Logger().Info("player service stopped")
	return nil
}

func init() {
	// Setup 只登记零值 Go 类型；每个配置实例都会得到独立的新对象。
	app.Setup(
		&GatewayService{},
		&PlayerService{},
	)
}

func main() {
	// Start 内部接入 M4 命令、配置加载、PID 运行权和优雅停止。
	app.Start()
}
