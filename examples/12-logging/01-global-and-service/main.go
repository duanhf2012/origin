// 本示例演示普通进程日志、Service 日志和 Module 日志的作用域区别。
package main

import (
	"context"

	"github.com/duanhf2012/origin/v3/application"
	originlog "github.com/duanhf2012/origin/v3/log"
	"github.com/duanhf2012/origin/v3/service"
)

// app 负责创建唯一日志 Runtime；包级 log.Xxx 只是复用这套 Runtime，不会另起队列。
var app = application.New()

// AuditModule 演示 Module.Logger 与所属 Service.Logger 使用同一作用域。
type AuditModule struct{ service.Module }

// OnStart 在 Service.OnStart 成功后执行，因此这条日志会晚于 Service 的启动日志。
func (module *AuditModule) OnStart(context.Context) error {
	module.Logger().Info(
		"audit module started",
		// Module 不自动增加 module_name；需要区分时使用普通业务字段。
		originlog.String("component", "AuditModule"),
	)
	return nil
}

// LoggingService 是配置中使用的 Service 类型模板。
type LoggingService struct {
	service.Service
	audit AuditModule
}

// OnInit 登记 Module。Module 会自动绑定当前 Service，不需要传递 Service 指针。
func (target *LoggingService) OnInit() error {
	return target.AddModule(&target.audit)
}

// OnStart 对比两种推荐日志入口。
func (target *LoggingService) OnStart(context.Context) error {
	// 包级日志适合不持有 Service 的工具代码，不自动带 node_id/service_name。
	originlog.Info(
		"process logger is ready",
		originlog.String("component", "bootstrap-helper"),
	)

	// Service Logger 自动带当前配置实例的 NodeID 和实际 ServiceName。
	target.Logger().Info(
		"player service is ready",
		originlog.Int64("player_id", 10001),
	)
	return nil
}

// init 登记配置中 services 可引用的 LoggingService 模板。
func init() { app.Setup(&LoggingService{}) }

// main 把配置加载、日志关闭和信号处理交给 Application。
func main() { app.Start() }
