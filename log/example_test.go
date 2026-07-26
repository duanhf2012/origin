package log_test

import (
	"context"

	originlog "github.com/duanhf2012/origin/v3/log"
	"github.com/duanhf2012/origin/v3/log/zaplog"
)

func Example() {
	// 使用默认配置创建完整日志 Runtime。
	config := originlog.DefaultConfig()
	runtime, err := zaplog.New(config)
	if err != nil {
		// Example 只展示外观，生产入口应记录或返回启动错误。
		return
	}

	// 预绑定稳定 Service 字段后按普通方式写日志。
	logger := runtime.Logger().With(originlog.String("service", "PlayerService"))
	logger.Info("service started")
	// 退出前排空队列并释放 Handler。
	_ = runtime.Close(context.Background())
}
