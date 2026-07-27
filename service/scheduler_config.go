package service

import (
	"fmt"
	"time"

	"github.com/duanhf2012/origin/v3/errs"
)

const (
	// DefaultMaxTasks 是单个 Service 已接受但尚未完整返回的根任务默认上限。
	DefaultMaxTasks = 20000
	// DefaultMaxAwaitTasks 是单个 Service 已释放执行权但尚未恢复的 Await 默认上限。
	DefaultMaxAwaitTasks = 10000
	// MaxSchedulerTasks 是单个 Service 可配置的根任务绝对硬上限。
	MaxSchedulerTasks = 65536
	// DefaultAwaitTimeout 是没有调用方、Service 或 Node 显式值时的最终内置超时。
	DefaultAwaitTimeout = 15 * time.Second
)

// SchedulerConfig 定义一个 ServiceScheduler 启动后冻结的容量和 Await 默认值。
type SchedulerConfig struct {
	// MaxTasks 限制 Ready、Running 和 Awaiting 根任务的总数。
	MaxTasks int
	// MaxAwaitTasks 限制已经释放执行权且尚未恢复返回的任务数。
	MaxAwaitTasks int
	// DefaultAwaitTimeout 是当前 Node 为 Service 提供的默认 Await 超时。
	DefaultAwaitTimeout time.Duration
}

// DefaultSchedulerConfig 返回适用于普通游戏逻辑 Service 的默认配置。
func DefaultSchedulerConfig() SchedulerConfig {
	return SchedulerConfig{
		MaxTasks:            DefaultMaxTasks,
		MaxAwaitTasks:       DefaultMaxAwaitTasks,
		DefaultAwaitTimeout: DefaultAwaitTimeout,
	}
}

// Validate 校验 SchedulerConfig 能否形成明确、有界的运行策略。
func (config SchedulerConfig) Validate() error {
	return validateSchedulerConfig(config)
}

// validateSchedulerConfig 校验已经完成默认值填充的调度配置。
func validateSchedulerConfig(config SchedulerConfig) error {
	// 三项配置均必须显式形成有效有界策略，零值不能表示无限。
	if config.MaxTasks <= 0 || config.MaxTasks > MaxSchedulerTasks {
		return errs.NewMessage(
			errs.CodeInvalidConfig,
			fmt.Sprintf("scheduler.max_tasks 必须在 1 到 %d 之间", MaxSchedulerTasks),
		)
	}
	if config.MaxAwaitTasks <= 0 || config.MaxAwaitTasks > config.MaxTasks {
		return errs.NewMessage(
			errs.CodeInvalidConfig,
			"scheduler.max_await_tasks 必须大于 0 且不能超过 max_tasks",
		)
	}
	if config.DefaultAwaitTimeout <= 0 {
		return errs.NewMessage(
			errs.CodeInvalidConfig,
			"scheduler.default_await_timeout 必须大于 0",
		)
	}
	return nil
}

// normalizedSchedulerConfig 只把完全零值视为“省略 scheduler”，部分配置继续严格报错。
func normalizedSchedulerConfig(config SchedulerConfig) (SchedulerConfig, error) {
	if config == (SchedulerConfig{}) {
		config = DefaultSchedulerConfig()
	}
	if err := validateSchedulerConfig(config); err != nil {
		return SchedulerConfig{}, err
	}
	return config, nil
}
