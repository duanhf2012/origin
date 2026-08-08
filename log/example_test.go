package log_test

import (
	"context"

	originlog "github.com/duanhf2012/origin/v3/log"
	"github.com/duanhf2012/origin/v3/log/zaplog"
)

func Example() {
	// 库级示例显式创建 Runtime；普通 Application 会在启动时自动完成这一步。
	config := originlog.DefaultConfig()
	runtime, err := zaplog.New(config)
	if err != nil {
		// Example 只展示外观，生产入口应记录或返回启动错误。
		return
	}

	// Application 会自动安装默认 Logger；这里手工安装只是为了展示包级便捷外观。
	originlog.SetDefault(runtime.Logger())
	originlog.Info("application logger is ready")

	// 退出前排空队列并释放 Handler。
	_ = runtime.Close(context.Background())
}
