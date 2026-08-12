package blueprintmodule_test

import (
	"context"

	"github.com/duanhf2012/origin/v3/sysmodule/blueprintmodule"
)

// ExampleModule_Run 展示普通请求的一次性执行外观。Run 必须从所属 Service 工作协程调用；它会在终态
// 自动释放临时 Instance。
func ExampleModule_Run() {
	var module *blueprintmodule.Module
	run := func(ctx context.Context) (blueprintmodule.PortArray, error) {
		return module.Run(ctx, "battle", 1, int64(1001))
	}
	_ = run
}

// ExampleInstance_Start 展示长期 Instance 的非阻塞执行。OnComplete 回调在所属 Service 工作协程执行，
// 因而适合更新串行业务数据；Instance 仍由创建它的业务所有者负责关闭。
func ExampleInstance_Start() {
	var instance *blueprintmodule.Instance
	start := func(ctx context.Context) error {
		execution, err := instance.Start(ctx, 1, int64(1001))
		if err != nil {
			return err
		}
		return execution.OnComplete(func(_ context.Context, returns blueprintmodule.PortArray, err error) {
			// 在这里处理最终结果或错误。
		})
	}
	_ = start
}

// ExampleModule_Reload 展示显式热加载。即使 err 非空，也要检查 Applied：加载可能已经发布，只是在
// Service 恢复排队阶段超过了调用 Context 的截止时间。
func ExampleModule_Reload() {
	var module *blueprintmodule.Module
	reload := func(ctx context.Context) error {
		result, err := module.Reload(ctx)
		if result.Applied {
			// 后续 Run/Start 将使用新图；活动 Execution 仍使用旧快照。
		}
		return err
	}
	_ = reload
}
