package application

import (
	"time"

	originlog "github.com/duanhf2012/origin/v3/log"
)

// LogHandlerFactory 根据最终日志配置创建可替换的输出 Handler。
//
// 工程未设置该函数时使用内置 Zap 实现；自定义实现仍由 Origin Runtime
// 统一提供异步队列、调用者定位、Flush 和 Close 生命周期。
type LogHandlerFactory func(config originlog.Config) (originlog.Handler, error)

// Options 定义 Application 创建后不再变化的框架选项。
type Options struct {
	// StartTimeout 限制全部 Node 启动阶段；零表示不设置框架超时。
	StartTimeout time.Duration
	// StopTimeout 限制框架发起的完整停止阶段；零表示不设置框架超时。
	StopTimeout time.Duration
	// LogHandlerFactory 允许项目替换日志输出后端；nil 使用内置 Zap。
	LogHandlerFactory LogHandlerFactory
}
