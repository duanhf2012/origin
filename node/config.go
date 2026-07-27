package node

import (
	"time"

	"github.com/duanhf2012/origin/v3/service"
)

// Options 是 Application 构建 Node 时传入的冻结运行选项。
//
// 它不属于 YAML/JSON 配置模型；默认值统一由 Application 处理。
type Options struct {
	// MaxTimersPerNode 是当前 Node 全部 Service 共享的业务 Timer 总额度。
	MaxTimersPerNode int
	// TimerLocation 是当前 Node 全部 Cron Timer 使用的统一只读时区。
	TimerLocation *time.Location
}

// Config 是一个 Node 在配置文件中的最小静态定义。
type Config struct {
	// ID 是 Node 在当前配置和集群中的稳定身份。
	ID string
	// Private 表示该 Node 后续不发布到服务发现。
	Private bool
	// Scheduler 定义当前 Node 下每个 Service 独立使用的调度容量和默认 Await 超时。
	//
	// 完全零值表示使用 service.DefaultSchedulerConfig；部分非零配置必须自身完整有效。
	Scheduler service.SchedulerConfig
	// Services 按声明顺序保存当前 Node 实际启用的 Service。
	Services []string
}

// ServiceBinding 是 Application 完成类型实例化后交给 Node 的一次性装配数据。
//
// 该类型属于框架装配 API，业务通常只通过配置和 app.Setup 间接产生它。
type ServiceBinding struct {
	// Name 是实例在当前 Node 内的实际 ServiceName。
	Name string
	// Template 是创建该实例所用的 Go 类型模板名。
	Template string
	// Private 表示该实例后续不发布到服务发现。
	Private bool
	// Service 是只属于当前 Node 的全新业务实例。
	Service interfaceService
}
