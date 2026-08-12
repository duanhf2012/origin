# Origin Blueprint Module 核心设计

> 状态：已完成讨论，等待最终 Review
> 基线：Origin v3.0，目标版本：v3.2
> 引擎基线：`github.com/duanhf2012/OriginBlueprint/engine/go/blueprint`
> 版本门禁：当前已验证提交为 `14f0d1a`；实施前由维护者发布并固定 `v0.1.6`
> 兼容性：不兼容 Origin v2 Blueprint API、文件格式或运行时行为

## 1. 文档定位

本文是 `sysmodule/blueprintmodule` 实现、测试、Example 和教程的单一核心设计。Module 的目标是把
OriginBlueprint Go 引擎适配到 Origin Service 生命周期与串行工作协程，并提供不容易泄漏的实例外观。

本模块不重新实现 Registry、解析器、编译器、VM、Execution、Yield/Resume 或热加载算法，也不新增
Delay、RPC、Timer、MongoDB、Redis、Kafka 等蓝图节点。业务节点由项目按自身协议实现；包装层只提供
注册入口、常用类型别名和正确的 Origin 调度环境。

核心成功标准：

1. `Run`、`Start` 的首次节点执行位于所属 Service 工作协程；
2. 外部 RPC、Timer 等 goroutine 只调用 `Resume/ResumeTo`，恢复后的节点重新进入同一 Service 工作协程；
3. 热加载的文件读取、解析和编译不占用 Service 工作协程；
4. 已开始或已挂起的 Execution 固定使用旧编译快照，下一次执行才使用新图；
5. 长期实例具有明确所有者、幂等释放与 Module 停止兜底，不再让业务直接操作裸 `graphID`；
6. 外观保持轻量，底层引擎已经具备的能力不重复包装。

## 2. 基线审查与范围结论

### 2.1 Origin v2 对照

v2 的 `BlueprintModule` 直接保存 `graphID -> IGraph`，业务通过裸 ID 调用 `Do` 和 `TriggerEvent`。底层与
Module 重复管理图实例，释放责任依赖调用者记忆，也没有明确首次执行、异步恢复和热加载所在协程。

| v2 能力 | v3.2 结论 | 原因 |
| --- | --- | --- |
| `CreateGraph` 返回 `int64` | 改为 `Create` 返回 `*Instance` | 让所有权、关闭状态和诊断信息集中在一个句柄 |
| `Do(graphID, ...)` | 改为 `Instance.Run` | 不允许绕过实例状态和归属检查 |
| `TriggerEvent(graphID, ...)` | 不单独保留 | 本质仍是从入口执行，统一使用 `Run/Start` |
| Module 与底层各自保存图 Map | 删除 Module 的第二份图实现 | 实例索引只用于生命周期包装，执行真相属于新引擎 |
| 找不到图时返回 0 | `Create` 返回明确错误 | 不让无效图名延迟到业务执行阶段 |
| 调用者手工释放 ID | `Instance.Close` + `Module.OnStop` 兜底 | 减少忘记释放与重复释放风险 |
| v2 文件格式与节点外观 | 不兼容 | 新版编辑器与 Go VM 是全新设计，不保留历史包袱 |

### 2.2 OriginBlueprint 新引擎审查

新引擎已经提供：目录递归加载、节点定义 Registry、图与函数编译、只读 `CompiledGraph`、VM、结构化
流程、循环、函数、Execution、Context 取消、Yield/Resume、Trace、结构化诊断和并发安全热加载。

已确认的执行快照机制：

- 每次 `Start` 在引擎锁内取得当时图池中的 `CompiledGraph`；
- `Graph`、VM、PC、变量、流程栈、循环栈、函数调用栈和 Yield token 均属于该次 Execution；
- 热加载在新 Registry 和新图池中完成解析与编译，成功后只短锁替换 `graphs` Map；
- 已开始或挂起的 Execution 继续引用旧 `CompiledGraph`，不会迁移变量或执行位置；
- 同一长期 Instance 下一次 `Start` 会重新读取图池，因此自动使用热加载后的新图；
- 加载失败不发布半成品，旧图池继续工作。

当前提交补齐验证资源后，已通过：

```text
go test ./... -count=1
go test -race ./... -count=1
```

热加载、挂起恢复、变量结构跨版本隔离和并发执行已有引擎测试覆盖。本模块需要补充的是 Origin Service
调度、生命周期和公共外观集成测试，不复制引擎内部测试。

### 2.3 方案比较

| 方案 | 结论 | 主要取舍 |
| --- | --- | --- |
| 原样迁移 v2 裸 ID 外观 | 不采用 | 简单但容易泄漏、传错 ID，无法表达所有权和关闭状态 |
| 全量代理新引擎所有 API | 不采用 | 形成重复 API，升级时维护成本高，也容易让调用者绕过协程约束 |
| 轻量 Origin facade + `*Instance` | 采用 | 只包装生命周期、调度、热加载、诊断和高频执行，其他能力留在引擎 |

## 3. 包与依赖

包路径固定为：

```text
sysmodule/blueprintmodule
```

正式实施固定依赖：

```go
github.com/duanhf2012/OriginBlueprint v0.1.6
```

不提交本地 `replace`，避免 Windows、Ubuntu 和 CI 使用不同源码。若 `v0.1.6` 与已验证提交
`14f0d1a` 不一致，必须重新执行引擎审查与完整测试。

## 4. 配置、构造与生命周期

### 4.1 配置

```go
type Config struct {
    // NodeDir 是节点定义 JSON 根目录；引擎按自身规则递归加载。
    NodeDir string `yaml:"node_dir"`

    // GraphDir 是 .vgf、.obp、.obpf 蓝图根目录；引擎按自身规则递归加载。
    GraphDir string `yaml:"graph_dir"`
}
```

两个字段都必填。`Setup` 清理空白、转换为绝对路径并冻结配置；目录存在性、文件读取、节点绑定和图编译
统一在 `OnStart` 验证。首版只支持一组目录，不引入多目录覆盖顺序、自动文件监听或周期热加载。

### 4.2 构造外观

```go
func New(config Config, options ...Option) (*Module, error)
func (m *Module) Setup(config Config, options ...Option) error
func (m *Module) RegisterNodes(factories ...NodeFactory) error
```

`New` 适合独立 Module；`Setup` 适合业务 Module 匿名嵌入。配置只能成功一次。节点工厂允许在首次
`OnStart` 前登记，启动后冻结；空工厂、返回 nil 和自定义名称冲突在注册时返回明确错误，内置名称冲突、
定义名称无法绑定等依赖文件内容的问题在 `OnStart` 返回明确错误。

业务组合示例：

```go
type BattleBlueprintModule struct {
    blueprintmodule.Module
    battle *BattleService
}

func (m *BattleBlueprintModule) OnInit() error {
    var config blueprintmodule.Config
    if err := m.GetServiceConfigStrict("blueprint", &config); err != nil {
        return err
    }
    if err := m.Setup(config); err != nil {
        return err
    }
    return m.RegisterNodes(
        func() blueprintmodule.IExecNode { return &GetBattleHPNode{battle: m.battle} },
        func() blueprintmodule.IExecNode { return &ApplyDamageNode{battle: m.battle} },
    )
}
```

节点工厂会在首次加载和热加载 goroutine 中被调用，用于取得名称和建立 NodeDefinition；蓝图执行时也会
再次调用工厂，为该次节点执行创建独立对象。因此工厂每次都必须返回新节点，只能注入并发安全的引用，
不能读取或修改 Service 业务数据。业务数据只能在节点 `Exec()` 中访问。

### 4.3 生命周期

- `Setup`：完成配置归一化、校验和冻结；嵌入型业务 Module 在自己的 `OnInit` 中调用
  `Setup/RegisterNodes`，不需要再调用被嵌入 Module 的 `OnInit`；
- `OnInit`：只供通过 `New` 独立加入 Service 的 Module 验证已配置状态；
- `OnStart`：安装 Service Dispatcher，注册节点工厂，同步完成首次目录加载和全量编译；失败阻止启动；
- `OnStop`：关闭准入、标记并注销所有 Instance、关闭引擎并取消仍在执行或挂起的 Execution；重复停止安全。

首次加载位于 Service 启动阶段，服务尚未接收业务流量，必须完整成功后才发布 Running。运行期重新加载
使用第 8 节的协作等待路径，不占用 Service 工作协程。

业务 Module 若覆盖 `OnStart/OnStop`，必须显式调用嵌入的 Blueprint Module 对应生命周期方法；教程须给出
完整示例，避免 Go 方法提升被覆盖后遗漏资源启动或关闭。

新引擎的 `Init` 仍保留可选的 `IBlueprintModule.TriggerEvent(graphID, ...)` 兼容桥，但该方法没有 Context，
无法安全表达 Origin 的协作等待，也会重新暴露裸 ID。包装层初始化引擎时不安装这条桥；自定义节点通过工厂
注入所需的小接口。节点不得依赖 `BaseExecNode.GetBlueprintModule()` 访问 Service。

## 5. Service 工作协程模型

### 5.1 硬性使用约定

所有通过公共外观调用的 `Run`、`Start` 必须来自所属 Service 工作协程，并传入该业务任务的 Context。
按已确认结论，首版不增加额外的运行时 goroutine 身份校验；该约束通过 GoDoc、教程、Example 和 Race
集成测试明确。

包装层使用自己的 Dispatcher：

```go
type serviceDispatcher struct {
    module *Module
}

func (d *serviceDispatcher) SubmitInitial(task func()) error {
    task() // Start 调用方当前就是所属 Service 工作协程。
    return nil
}

func (d *serviceDispatcher) Submit(task func()) error {
    return d.module.DispatchAsync(func(context.Context) { task() })
}
```

不使用引擎默认 Worker Dispatcher。也不直接使用 `NewActorExecutionDispatcher`，因为它的 enqueue 外观无法
把 Origin 有界队列的拒绝错误返回给引擎。

### 5.2 首次执行

`Start` 通过 `SubmitInitial` 在当前 Service 工作协程立即执行。它会连续运行，直到出现三种结果之一：

1. 蓝图同步完成；
2. 节点调用 `Yield` 并返回 `ErrExecutionSuspended`；
3. 节点或 VM 执行失败。

因此同步蓝图在 `Start` 返回前已经完成；异步蓝图在首次 Yield 后以 Suspended 状态返回。

### 5.3 异步恢复

RPC、Timer、网络和其他外部 callback 可能位于任意 goroutine，只允许保存普通值与 `YieldHandle`，并调用
`Resume/ResumeTo`。引擎随后调用 Dispatcher 的 `Submit`，把后续 VM 执行投递回所属 Service 工作协程。

外部 callback 禁止直接访问 Service 业务数据，也禁止访问原节点的 `BaseExecNode` 或端口。`Resume` 返回 nil
只代表恢复任务已被 Service 队列接受；最终结果仍通过 Execution 取得。

## 6. Instance 外观与所有权

### 6.1 为什么不用裸 ID

底层引擎仍使用 `graphID`，但公共执行 API 不接收裸 ID。`Instance` 私有保存 ID、图名、业务诊断 Key、
所属 Module 与关闭状态：

```go
type Instance struct {
    noCopy noCopy
    // 私有字段
}
```

`Instance` 禁止按值复制，所有方法使用指针接收者。多个业务字段可以保存同一个 `*Instance` 指针；复制
指针不会创建新图，也不会增加释放次数。业务必须指定唯一所有者负责 `Close`，其他引用只借用。

### 6.2 创建与诊断

```go
func (m *Module) Create(graphName string, options ...InstanceOption) (*Instance, error)

func WithKey(key string) InstanceOption

func (i *Instance) ID() int64
func (i *Instance) Name() string
func (i *Instance) Key() string
func (i *Instance) Close() error
```

`ID` 只用于日志和诊断，不提供 `Module.Do(graphID, ...)`。`Key` 只作为业务关联信息，不建立按 Key 查询的
全局注册中心。`Close` 幂等：禁止新执行，释放底层实例并取消该实例尚未完成的 Execution，再从 Module
实例索引注销。

Module 保存所有活动 Instance，用于停止兜底。忘记主动关闭的 Instance 会在 `OnStop` 被回收并计入诊断，
但运行期不会基于 TTL 猜测业务生命周期，也不使用 Finalizer 代替显式释放。

## 7. Run、Start 与 Execution

### 7.1 一次性 Run

```go
func (m *Module) Run(
    ctx context.Context,
    graphName string,
    entranceID int64,
    args ...any,
) (PortArray, error)
```

`Module.Run` 自动创建临时 Instance，调用 `Instance.Run`，并在终态后释放。它适合奖励计算、条件判断、
掉落规则等一次性执行。首版不提供 `Module.Start`；异步启动需要显式 Instance，确保所有者和关闭责任清楚。

### 7.2 长期 Instance

```go
func (i *Instance) Run(
    ctx context.Context,
    entranceID int64,
    args ...any,
) (PortArray, error)

func (i *Instance) Start(
    ctx context.Context,
    entranceID int64,
    args ...any,
) (*Execution, error)
```

长期 Instance 适合一场战斗、一个副本、AI 控制器或剧情会话，可以从多个入口启动多次独立 Execution。
Instance 不保存跨 Run 普通变量；长期状态仍属于 Service 业务对象、数据库或缓存。

热加载不会替换 Instance 身份：已经开始的 Execution 使用旧快照，同一 Instance 的下一次 `Run/Start`
自动使用新图。

### 7.3 Run 的协作等待

`Run` 先调用 `Start`。同步完成时直接返回 Result；只有 Execution 挂起时才调用 Origin `Await`：

```go
execution, err := instance.Start(ctx, entranceID, args...)
if err != nil {
    return nil, err
}
if execution.IsDone() {
    return execution.Result()
}

err = instance.module.Await(ctx, func(waitCtx context.Context) error {
    select {
    case <-execution.Done():
        return nil
    case <-waitCtx.Done():
        execution.Cancel()
        return waitCtx.Err()
    }
})
if err != nil {
    execution.Cancel()
    return nil, err
}
return execution.Result()
```

`Await` 的等待函数只等待 `Done`，绝不调用蓝图执行代码。等待期间原业务任务释放 Service 执行权，恢复
Dispatcher 才能取得执行槽继续蓝图；`Await` 返回时原调用栈重新拥有 Service 工作协程。

### 7.4 Execution 包装

```go
type Completion func(ctx context.Context, returns PortArray, err error)

func (e *Execution) ID() uint64
func (e *Execution) Done() <-chan struct{}
func (e *Execution) State() ExecutionState
func (e *Execution) IsDone() bool
func (e *Execution) Result() (PortArray, error)
func (e *Execution) Cancel() bool
func (e *Execution) OnComplete(callback Completion) error
```

`Execution` 不暴露可替换 Dispatcher、Graph 或 VM 的入口。`Done/State/Result/Cancel` 是并发安全的只读或
取消操作；只有 `OnComplete` 的业务 callback 保证在所属 Service 工作协程执行。

`OnComplete` 使用 `Start` 时捕获的生命周期 Context 和 Origin 已有的完成任务机制，预留一次 Service 回调
任务，再协作等待底层 `Done`。Context 到期时先取消 Execution 并等待其进入终态，随后 callback 才在
Service 工作协程收到最终结果。callback 收到的是该完成任务的新 Service Context，不是已经结束的 Start
任务 Context；即使 Execution 在注册时已经完成，callback 也通过预留任务执行，不在 `OnComplete` 内联调用。

推荐在调用 `Start` 的同一个 Service 任务中立即登记 `OnComplete`，尽早在 Origin 有界 FIFO 中预留回调
容量；首版不把“同一任务”做成额外运行时强制，其他 goroutine 登记仍会走同一安全预留路径。每个 Execution
最多注册一个包装层 Completion。注册失败不会静默丢回调，也不隐式取消执行；调用者收到错误后决定改用
`Run`、读取 `Done/Result` 或显式取消。

## 8. 热加载

```go
type ReloadResult struct {
    GraphCount int
    Applied    bool
}

func (m *Module) Reload(ctx context.Context) (ReloadResult, error)
```

规则：

- 必须从所属 Service 工作协程调用；
- 同一 Module 同时只允许一次 Reload；第二次立即返回 `ErrReloadInProgress`，不排队、不合并；
- `Reload` 在 `Await` 的等待函数中调用引擎并发安全的 `HotReload`；目录 I/O、解析和编译不持有 Service
  执行权；
- 引擎在全部成功后短锁替换图池，失败保留旧图；
- 调用返回时已经重新取得 Service 执行权；
- 已开始或挂起的 Execution 不迁移，新 Execution 使用新图；
- 不提供自动目录监听、周期扫描或额外 `ReloadAsync`。

当前引擎 `HotReload` 是一次不可中断的事务。`ctx` 在开始前取消时不进入加载；一旦加载开始，Module 会等待
该事务结束再恢复业务调用栈。它不会占用 Service 工作协程，但底层文件读取和编译不能被 Context 中途打断。
发布时引擎只持有一次短锁；普通 Service 任务不受影响，恰好同时调用 `Run/Start` 的任务可能短暂等待该锁。

`Applied` 明确本次新图池是否已经发布。若事务成功但 `ctx` 在等待或恢复排队期间到期，返回
`ReloadResult{Applied: true}` 和 Deadline 错误；若编译失败则 `Applied` 为 false，旧图继续运行。教程必须要求
同时检查 Result 与 error，并建议管理接口使用覆盖最坏编译和恢复排队时间的 Deadline。

## 9. 完整公共外观

首版面向普通业务的完整外观固定为：

```go
type Config struct {
    NodeDir  string `yaml:"node_dir"`
    GraphDir string `yaml:"graph_dir"`
}

func New(config Config, options ...Option) (*Module, error)
func (m *Module) Setup(config Config, options ...Option) error
func (m *Module) RegisterNodes(factories ...NodeFactory) error

func (m *Module) Run(
    ctx context.Context,
    graphName string,
    entranceID int64,
    args ...any,
) (PortArray, error)
func (m *Module) Create(graphName string, options ...InstanceOption) (*Instance, error)
func (m *Module) Reload(ctx context.Context) (ReloadResult, error)
func (m *Module) SetTraceEnabled(enabled bool) error
func (m *Module) Stats() Stats

func WithTraceLogger(logger BlueprintTraceLogger) Option
func WithDiagnosticSink(sink BlueprintDiagnosticSink) Option
func WithKey(key string) InstanceOption

func (i *Instance) Run(
    ctx context.Context,
    entranceID int64,
    args ...any,
) (PortArray, error)
func (i *Instance) Start(
    ctx context.Context,
    entranceID int64,
    args ...any,
) (*Execution, error)
func (i *Instance) ID() int64
func (i *Instance) Name() string
func (i *Instance) Key() string
func (i *Instance) Close() error

func (e *Execution) ID() uint64
func (e *Execution) Done() <-chan struct{}
func (e *Execution) State() ExecutionState
func (e *Execution) IsDone() bool
func (e *Execution) Result() (PortArray, error)
func (e *Execution) Cancel() bool
func (e *Execution) OnComplete(callback Completion) error
```

不增加同义方法、v2 命名、裸 ID 执行入口或逐项引擎代理。调用约束、生命周期和错误语义以前述章节为准。

## 10. 类型别名与自由层边界

业务实现普通与异步节点时通常只需导入 `blueprintmodule`。首批提供高频别名和错误：

```go
type NodeFactory = func() IExecNode
type IExecNode = blueprint.IExecNode
type BaseExecNode = blueprint.BaseExecNode
type YieldHandle = blueprint.YieldHandle

type PortArray = blueprint.PortArray
type PortInt = blueprint.PortInt
type PortFloat = blueprint.PortFloat
type PortString = blueprint.PortString
type PortBool = blueprint.PortBool
type ArrayData = blueprint.ArrayData

type ExecutionState = blueprint.ExecutionState
type BlueprintError = blueprint.BlueprintError
type BlueprintTraceLogger = blueprint.BlueprintTraceLogger
type BlueprintDiagnosticSink = blueprint.BlueprintDiagnosticSink

var (
    ErrExecutionSuspended      = blueprint.ErrExecutionSuspended
    ErrExecutionPending        = blueprint.ErrExecutionPending
    ErrExecutionCanceled       = blueprint.ErrExecutionCanceled
    ErrExecutionCompleted      = blueprint.ErrExecutionCompleted
    ErrExecutionBudgetExceeded = blueprint.ErrExecutionBudgetExceeded
    ErrEntranceNotFound        = blueprint.ErrEntranceNotFound
    ErrGraphReleased           = blueprint.ErrGraphReleased
    ErrYieldResumed            = blueprint.ErrYieldResumed
    ErrYieldInvalid            = blueprint.ErrYieldInvalid
)
```

实施时按新引擎实际导出名称校对，不建立拼写兼容别名。只导出实现业务节点和读取结果必需的类型；不别名
`Blueprint`、Registry、CompiledGraph、Graph 或 Dispatcher。确需编译器等高级能力的工具代码直接导入
OriginBlueprint，且不属于 Module 管理的生产执行路径。

## 11. Trace、诊断与统计

### 11.1 Option

```go
func WithTraceLogger(logger BlueprintTraceLogger) Option
func WithDiagnosticSink(sink BlueprintDiagnosticSink) Option
```

Trace 默认关闭。安装 TraceLogger 不自动开启端口值复制；通过下列接口在明确诊断窗口切换：

```go
func (m *Module) SetTraceEnabled(enabled bool) error
```

Trace 可能包含业务数据并增加端口复制开销，生产不得默认常开。执行失败始终通过 `Run/Result` 返回；未提供
自定义 DiagnosticSink 时，Module 使用 Origin 结构化 Logger 记录异步终态失败，包含 graph、instance、
entrance、execution、node、PC 和根因，但不输出完整端口值。

TraceLogger 和 DiagnosticSink 都可能由引擎执行、恢复拒绝或取消路径调用，不能访问未加保护的 Service 业务
字段；它们只用于并发安全的日志和监控上报。需要修改业务状态时使用 `Run` 返回值或 `OnComplete`。

### 11.2 精确轻量统计

```go
type Stats struct {
    ActiveInstances   int
    CreatedTotal      uint64
    ClosedTotal       uint64
    StartedTotal      uint64
    ReloadedTotal     uint64
    ReloadFailedTotal uint64
}

func (m *Module) Stats() Stats
```

首版不统计 ActiveExecutions、Completed/Failed/CanceledTotal。新引擎没有公开无分配完成钩子；不能为了统计
给每个挂起 Execution 创建观察 goroutine 或在 Module 保留已经终态的句柄。执行状态由 `Execution` 直接查询，
高基数图/节点指标由可选诊断或业务监控负责。若新引擎未来提供轻量完成通知，再单独评估扩展。

## 12. 错误与状态

包装层至少提供：

```go
var (
    ErrInvalidArgument  error
    ErrInvalidConfig    error
    ErrNotSetup         error
    ErrAlreadySetup     error
    ErrNotRunning       error
    ErrInstanceClosed   error
    ErrReloadInProgress error
)
```

引擎执行错误保持 `errors.Is/As` 链，不能改成纯字符串。解析、编译和执行错误中的源路径、图名、实例 ID、
入口 ID、Execution ID、节点 ID 和 PC 必须保留。配置和日志不得输出文件内容、端口值或可能包含密钥的
业务参数。

## 13. 并发、关闭与所有权不变量

1. Module 配置与节点工厂在启动前冻结；运行期不得修改。
2. `CompiledGraph` 由引擎只读共享，Module 不缓存另一份执行状态。
3. Instance 可以共享指针但禁止复制值；`Close` 严格幂等。
4. 同一 Instance 可以存在多个挂起 Execution；它们的 VM 与变量完全隔离。
5. 业务节点按 Service 工作协程串行执行，不需要为 Service 业务字段额外加锁。
6. 外部 callback 不访问业务字段，只调用一次性 `Resume/ResumeTo` 并处理返回错误。
7. `Close`、Context 取消、Module 停止与 Resume 允许并发；以引擎返回的终态为准。
8. Reload 不修改活动 Execution；发布点前启动的执行用旧图，发布点后的执行用新图。
9. Module 停止先拒绝 Create/Run/Start/Reload，再关闭实例和引擎；停止完成后不接受恢复任务。
10. 不使用 Finalizer、对象池、隐藏 Worker Pool 或自动重试掩盖错误。
11. 推荐 `OnComplete` 在 Start 所在 Service 任务中立即注册；完成回调本身在新的 Service 任务中执行。

## 14. 测试与验收

### 14.1 单元测试

- Config 归一化、空目录、重复 Setup、生命周期状态与错误链；
- 节点工厂空值、返回 nil、启动后注册拒绝和工厂后台调用约束；
- Create、WithKey、禁止值复制检查、重复 Close、关闭后执行与 Module 停止兜底；
- Module.Run 自动释放、Instance.Run 同步快速路径、Start 同步完成与挂起状态；
- Execution Done/State/Result/Cancel、OnComplete 单次登记、同步终态异步回调及预留失败；
- Reload 单飞、失败保留旧图、成功发布但调用 Deadline 到期、统计和 Trace 开关。

### 14.2 Origin Service 集成测试

- 同步节点在 Start 返回前完成，且不触发 Await；
- 挂起 Run 调用 Await 释放执行权，其他 Service 任务可继续执行；
- 从外部 goroutine Resume 后，后续节点和 OnComplete 均回到同一 Service 串行工作协程；
- OnComplete 等待超时会取消 Execution、等待终态，再以新 Service Context 严格回调一次；
- 多个挂起 Execution 并发恢复时业务状态无竞态、执行不重入；
- Instance.Close、Context 取消和 Service Stop 能取消挂起执行；迟到 Resume 返回明确错误；
- 热加载期间 Service 仍能处理普通任务；
- 旧 Execution 挂起后 Reload，恢复得到旧版本结果；同一 Instance 下一次 Run 得到新版本结果；
- 非法图热加载失败后旧版本继续执行；并发 Reload 返回 `ErrReloadInProgress`。

### 14.3 Example 与教程

提供一个完整 Example，按同一个游戏业务 Module 展示：

1. 严格配置和节点注册；
2. 一次性 `Module.Run`；
3. 战斗长期 Instance 的多入口执行与 `Close`；
4. 自定义同步业务节点访问 Service 数据；
5. 自定义异步 RPC 节点的 Yield/ResumeTo；
6. `Start + OnComplete`；
7. 显式 Reload 与旧执行快照语义；
8. Trace 仅在诊断窗口开启；
9. 每个函数、节点工厂、Exec、底层 callback、恢复节点和完成回调所在协程。

教程以使用者视角给出配置表、必填项、默认行为、完整注释、所有权、错误处理、热加载发布步骤和禁止
用法。README 教程表新增 Blueprint Module 入口，不改变既有教程章节结构。

### 14.4 双平台门禁

Windows 与 Ubuntu 均执行：

```text
go test ./sysmodule/blueprintmodule -count=1
go test -race ./sysmodule/blueprintmodule -count=1
go test ./... -count=1
go test -race ./... -count=1
go vet ./...
go build ./...
```

核心调度、实例生命周期、热加载旧/新快照、停止取消和异步恢复分支尽量达到 100% 覆盖；不得用无业务
价值的测试追求全包数字。性能测试聚焦同步 Run 额外开销、Instance 创建/关闭、并发 Execution 和热加载
发布短暂停顿，不因缺少证据增加对象池或第二层队列。

## 15. 明确不做

- 不兼容 v2 API、裸 graphID 外观和旧文件格式承诺；
- 不实现或包装 Delay、Timer、RPC、数据库、缓存和消息队列节点；
- 不提供 `Module.Start`、`Module.Do(graphID, ...)`、按 Key 查询实例或进程级实例注册中心；
- 不暴露可替换生产 Dispatcher 的 `Engine()`；
- 不自动监听目录、不周期热加载、不合并并发 Reload；
- 不保存跨 Run 蓝图变量，不把 Instance 当业务实体；
- 不复制引擎 Registry、Compiler、VM、Trace 或诊断实现；
- 不增加隐藏 goroutine、对象池、Worker Pool、执行重试或节点级高基数统计。

## 16. 实施顺序

1. 维护者发布 `OriginBlueprint v0.1.6`，Origin 固定依赖并复跑上游测试；
2. 建立 Config、Option、类型别名、错误和生命周期骨架；
3. TDD 实现 Service Dispatcher 与同步/挂起执行路径；
4. TDD 实现 Instance、Execution、Run、Start、OnComplete 和停止兜底；
5. TDD 实现单飞 Reload 与旧/新执行快照集成测试；
6. 补齐 Trace、诊断、轻量统计和全部 GoDoc；
7. 完成游戏场景 Example、教程和 README 入口；
8. Windows 与 Ubuntu 完整 Test/Race/Vet/Build、覆盖率和性能验收；
9. 独立代码 Review 后修复 Critical/Important，再提交 v3 分支。
