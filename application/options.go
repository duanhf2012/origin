package application

import (
	"time"
	_ "time/tzdata"

	originlog "github.com/duanhf2012/origin/v3/log"
)

const (
	// DefaultMaxTimersPerNode 是每个 Node 共享给全部 Service 的默认业务 Timer 硬上限。
	//
	// 该值只参与运行时额度判断，不会触发任何按三百万容量的预分配。
	DefaultMaxTimersPerNode = 3_000_000
)

// LogHandlerFactory 根据最终日志配置创建可替换的输出 Handler。
//
// 工程未设置该函数时使用内置 Zap 实现；自定义实现仍由 Origin Runtime
// 统一提供异步队列、调用者定位、Flush 和 Close 生命周期。
type LogHandlerFactory func(config originlog.Config) (originlog.Handler, error)

// TimerOptions 定义 Application 中所有 Node 共享的业务 Timer 策略。
type TimerOptions struct {
	// MaxTimersPerNode 是每个 Node 全部 Service 合计的活跃业务 Timer 上限。
	//
	// 零值使用 DefaultMaxTimersPerNode；负值无效。
	MaxTimersPerNode int
	// Location 是全部 Node 的 Cron 统一时区；nil 使用创建 Application 时的 time.Local。
	Location *time.Location
}

// Options 定义 Application 创建后不再变化的框架选项。
type Options struct {
	// StartTimeout 限制全部 Node 启动阶段；零表示不设置框架超时。
	StartTimeout time.Duration
	// StopTimeout 限制框架发起的完整停止阶段；零表示不设置框架超时。
	StopTimeout time.Duration
	// LogHandlerFactory 允许项目替换日志输出后端；nil 使用内置 Zap。
	LogHandlerFactory LogHandlerFactory
	// Timer 定义每个 Node 的业务 Timer 额度和 Cron 时区。
	Timer TimerOptions
}
