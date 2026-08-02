// 本示例是 Origin Application、Node 和 Service 生命周期的最小入口。
package main

import (
	"context"

	"github.com/duanhf2012/origin/v3/application"
	"github.com/duanhf2012/origin/v3/service"
)

// app 是当前进程唯一的 Application 实例。
var app = application.New()

// HelloService 是最小业务 Service：匿名嵌入 service.Service，只覆盖需要的回调。
type HelloService struct {
	service.Service
}

func (target *HelloService) OnInit() error {
	// OnInit 适合完成不依赖外部服务的对象初始化和静态注册。
	target.Logger().Info("hello service initialized")
	return nil
}

func (target *HelloService) OnStart(context.Context) error {
	// OnStart 返回 nil 后，Service 才会进入可处理业务的 Running 状态。
	target.Logger().Info("hello, Origin v3")
	return nil
}

func (target *HelloService) OnStop(context.Context) error {
	// OnStop 在 Ctrl+C 或 stop 命令触发的优雅停止中执行。
	target.Logger().Info("hello service stopped")
	return nil
}

func init() {
	// Setup 登记零值模板；框架不会把这个指针直接当作运行实例复用。
	app.Setup(&HelloService{})
}

func main() {
	// Start 统一解析 start/stop 命令并管理 PID 与退出信号。
	app.Start()
}
