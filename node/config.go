package node

import (
	"time"

	"github.com/duanhf2012/origin/v3/internal/bufferpool"
	internaldiscovery "github.com/duanhf2012/origin/v3/internal/discovery"
	"github.com/duanhf2012/origin/v3/rpc"
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
	// BufferPool 是 Application 进程级共享的网络与 RPC 字节缓冲区池。
	//
	// 测试或独立使用 node.New 时可以省略，Node 会创建关闭统计的私有 Pool；正式
	// Application 始终传入同一个共享实例。
	BufferPool *bufferpool.Pool
	// DiscoverySource 是 Application 在 M14 为实际启动 Node 创建的内部过渡完整快照源。
	//
	// 正式项目不直接设置该字段；省略时 Node 仍拥有空的本地目录，便于独立单元测试。
	DiscoverySource *internaldiscovery.Source
	// RuntimeFailure 接收当前 Node 的 TCP Listener 或 NATS Connection 永久终态。
	//
	// 正式 Application 用它取消唯一生命周期 Context；回调必须快速返回，不能直接执行 Stop。
	RuntimeFailure func(nodeID string, cause error)
}

// Config 是一个 Node 在配置文件中的最小静态定义。
type Config struct {
	// ID 是 Node 在当前配置和集群中的稳定身份。
	ID string
	// Private 表示该 Node 后续不发布到服务发现。
	Private bool
	// Labels 是当前 Node 对外发布、供其他 Node 关注规则精确匹配的业务标签。
	Labels map[string]string
	// DiscoveryFilter 是配置加载阶段已经校验并预编译的远端服务关注规则。
	DiscoveryFilter internaldiscovery.Filter
	// Scheduler 定义当前 Node 下每个 Service 独立使用的调度容量和默认 Await 超时。
	//
	// 完全零值表示使用 service.DefaultSchedulerConfig；部分非零配置必须自身完整有效。
	Scheduler service.SchedulerConfig
	// RPC 为 nil 时当前 Node 只支持本地调用；非 nil 时按 Transport 启用 TCP 或 NATS。
	RPC *rpc.Config
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
