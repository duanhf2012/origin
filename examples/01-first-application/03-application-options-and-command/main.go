// 本示例展示 Application 的框架级 Options 与自定义离线命令。
package main

import (
	"context"
	"fmt"
	"time"

	"github.com/duanhf2012/origin/v3/application"
	"github.com/duanhf2012/origin/v3/command"
	"github.com/duanhf2012/origin/v3/service"
)

// app 在程序装配阶段创建一次；Options 只影响框架边界，不替代 YAML 业务配置。
var app = application.New(application.Options{
	// 0 表示不设置 Application 级总 Deadline；示例显式设置，便于展示两个生命周期边界。
	StartTimeout: 30 * time.Second,
	StopTimeout:  30 * time.Second,
	Timer: application.TimerOptions{
		// 每个 Node 内所有 Service 合计最多创建 128 个活跃业务 Timer。
		MaxTimersPerNode: 128,
		// 此 Application 的 Cron 统一采用 UTC；没有 Cron 时不会产生额外成本。
		Location: time.UTC,
	},
})

// OptionService 用于证明同一个可执行程序仍可经 start 进入正常生命周期。
type OptionService struct{ service.Service }

// OnStart 只在执行 start 时调用；执行自定义命令时不会创建本 Service。
func (target *OptionService) OnStart(context.Context) error {
	target.Logger().Info("application started with explicit options")
	return nil
}

func init() {
	// Setup、RegisterCommand 都必须在 app.Start 前完成。
	app.Setup(&OptionService{})
	if err := app.RegisterCommand(command.Command{
		// 命令名必须为小写 kebab-case，且不能与内置命令重名。
		Name:    "print-options",
		Summary: "打印离线命令收到的参数",
		Usage:   "application-options print-options [name]",
		Run: func(ctx command.Context, args []string) error {
			// 使用注入的输出流，避免直接绑定 os.Stdout，便于测试和嵌入式运行。
			_, err := fmt.Fprintf(ctx.Stdout, "custom command args=%v\\n", args)
			return err
		},
	}); err != nil {
		// 注册失败属于程序装配错误：命令尚未开始运行，立即终止最清晰。
		panic(err)
	}
}

// main 统一交给 Application 分发 start、stop、help、version 和已注册的自定义命令。
func main() { app.Start() }
