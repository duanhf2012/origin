// 本示例演示不重启 Application 就调整 Console/File 日志状态。
package main

import (
	"context"

	"github.com/duanhf2012/origin/v3/application"
	originlog "github.com/duanhf2012/origin/v3/log"
	"github.com/duanhf2012/origin/v3/service"
)

// app 安装当前进程默认 Logger 和 Controller。
var app = application.New()

// ControlService 模拟收到管理指令后临时打开 Debug、暂停某个输出端。
type ControlService struct{ service.Service }

// OnStart 按固定顺序执行控制动作；示例使用 sync 模式，输出结果容易逐条核对。
func (target *ControlService) OnStart(context.Context) error {
	// 启动配置是 info；临时把控制台调到 debug，用于定位线上问题。
	if err := originlog.SetConsoleLevel(originlog.DebugLevel); err != nil {
		return err
	}
	originlog.Debug("temporary console debug is visible")

	// Reset 恢复配置文件中的 info，而不是写死某个级别。
	if err := originlog.ResetConsoleLevel(); err != nil {
		return err
	}

	// 临时把文件提高到 warn；下一条 Info 仍显示在控制台，但不会进入文件。
	if err := originlog.SetFileLevel(originlog.WarnLevel); err != nil {
		return err
	}
	target.Logger().Info("file is temporarily filtering info")
	if err := originlog.ResetFileLevel(); err != nil {
		return err
	}

	// 暂停控制台不关闭 stdout/stderr，也不会影响文件输出。
	if err := originlog.SetConsoleEnabled(false); err != nil {
		return err
	}
	target.Logger().Warn("this record is written only to the file")
	if err := originlog.SetConsoleEnabled(true); err != nil {
		return err
	}

	// 文件同样可以独立暂停和恢复；暂停不会删除或重新打开活动文件。
	if err := originlog.SetFileEnabled(false); err != nil {
		return err
	}
	target.Logger().Warn("this record is written only to the console")
	if err := originlog.SetFileEnabled(true); err != nil {
		return err
	}

	// 状态快照可以提供给已有的、经过鉴权的管理命令或监控接口。
	status, err := originlog.CurrentStatus()
	if err != nil {
		return err
	}
	target.Logger().Info(
		"logging controls restored",
		originlog.String("console_level", status.Console.Level.String()),
		originlog.Bool("console_enabled", status.Console.Enabled),
		originlog.String("file_level", status.File.Level.String()),
		originlog.Bool("file_enabled", status.File.Enabled),
	)
	return nil
}

// init 登记配置中引用的 ControlService 模板。
func init() { app.Setup(&ControlService{}) }

// main 启动 Application。
func main() { app.Start() }
