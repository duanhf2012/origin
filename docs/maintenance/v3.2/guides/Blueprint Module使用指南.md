# Blueprint Module 使用指南

Blueprint Module 把新版 OriginBlueprint Go 引擎接入 Origin Service 生命周期。普通请求用一次性
`Module.Run`；战斗、副本、AI 或任务会话用长期 `Instance`；外部 RPC、Timer 回包用
`YieldHandle.Resume` 恢复。蓝图业务节点始终回到所属 Service 工作协程执行。

完整可运行代码见 [战斗蓝图工作流](../../../../examples/18-blueprint/01-battle-workflow/README.md)。

## 1. 十分钟接入

业务 Module 匿名组合 `blueprintmodule.Module`，从 Service 配置读取目录，并注册节点工厂：

```go
type BattleBlueprintModule struct {
    blueprintmodule.Module
    players map[int64]*Player
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
        func() blueprintmodule.IExecNode { return &ApplyDamageNode{module: m} },
    )
}
```

若业务 Module 覆盖生命周期，必须显式转调嵌入 Module：

```go
func (m *BattleBlueprintModule) OnStart(ctx context.Context) error {
    return m.Module.OnStart(ctx)
}

func (m *BattleBlueprintModule) OnStop(ctx context.Context) error {
    return m.Module.OnStop(ctx)
}
```

## 2. 配置

```yaml
services:
  BattleService:
    blueprint:
      node_dir: "./blueprints/nodes"
      graph_dir: "./blueprints/graphs"
```

| 字段 | 必填 | 默认值 | 说明与生产建议 |
| --- | --- | --- | --- |
| `node_dir` | 是 | 无 | 节点定义 JSON 根目录；可包含子目录。建议随应用只读发布并做版本控制。 |
| `graph_dir` | 是 | 无 | `.vgf`、`.obp`、`.obpf` 根目录；热加载会检查整个目录。建议先在发布流水线验证蓝图。 |

`Setup` 会去除首尾空白并把相对路径冻结为绝对路径，但不读取文件。第一次完整读取、解析和编译发生在
`OnStart`；任一文件失败都会阻止 Service 启动。首版不自动监听目录，也不支持多目录覆盖顺序。

## 3. 节点定义与业务节点

节点定义描述端口，自定义 Go 节点实现 `GetName` 和 `Exec`：

```go
type ApplyDamageNode struct {
    blueprintmodule.BaseExecNode
    module *BattleBlueprintModule
}

func (*ApplyDamageNode) GetName() string { return "ApplyDamage" }

func (n *ApplyDamageNode) Exec() (int, error) {
    playerID, ok := n.GetInPortInt(1)
    if !ok {
        return -1, errors.New("missing player_id")
    }
    n.module.players[int64(playerID)].HP -= 10
    return 0, nil
}
```

节点工厂在首次加载、热加载和执行时都可能调用，并且加载/热加载可能不在 Service 工作协程。工厂每次
必须返回新节点，只做构造和依赖注入，不能读取或修改 `players` 等串行业务数据。`Exec` 才是访问业务
状态的位置；它由蓝图 Dispatcher 放在所属 Service 工作协程中执行。

## 4. 选择一次性或长期 Instance

| 外观 | 适合场景 | 所有权与等待 |
| --- | --- | --- |
| `Module.Run(ctx, graphName, entranceID, args...)` | 普通请求、一次计算 | 自动创建、等待终态并关闭临时 Instance |
| `Module.Create(graphName, options...)` | 战斗、副本、AI、任务会话 | 返回长期 `*Instance`，业务所有者必须 `Close` |
| `Instance.Run(ctx, entranceID, args...)` | 长期图上的同步业务流程 | 挂起时通过 Origin `Await` 释放 Service 执行权 |
| `Instance.Start(ctx, entranceID, args...)` | 不能同步占用当前业务流程 | 返回 `*Execution`；用 `OnComplete`、`Done/Result` 或 `Cancel` 收口 |

`graphName` 是文件加载后的图名；`entranceID` 是编译图入口编号；`args` 按入口端口顺序传递，并使用引擎
定义的端口类型。整数端口是 `int64` 语义。找不到图时 `Create` 返回 `ErrGraphNotFound`，不会返回无效 ID。

长期实例示例：

```go
instance, err := module.Create("battle", blueprintmodule.WithKey("battle:1001"))
if err != nil {
    return err
}
// instance 由当前战斗拥有；结束或 Module.OnStop 时释放。
defer instance.Close()
returns, err := instance.Run(ctx, 1, int64(1001))
```

`WithKey` 只增加日志诊断信息，不负责唯一性、查找或持久化。不要按值复制 `Instance`，也不要建立第二份
`graphID` 注册表；可以让多个字段引用同一指针，但只指定一个业务所有者负责关闭。`Close` 可重复调用，
Module 停止也会兜底关闭仍存活的 Instance。

## 5. Run、Start 与 Await

`Run` 和 `Start` 都必须从所属 Service 工作协程调用并传入当前任务 `ctx`。首次节点会在当前调用栈执行，
直到蓝图完成、失败、取消或第一个节点 `Yield`：

- 同步完成时，`Run` 直接返回，不进入 Await；
- 遇到 `Yield` 时，`Run` 用 `Module.Await` 等待，Service 可以继续处理其他任务；
- `Start` 在第一个 `Yield` 处立即返回 Suspended Execution，不等待最终结果；
- Resume 后的节点通过 Service 有界 FIFO 重新进入同一个 Service，不在外部回调 goroutine 执行。

非阻塞启动示例：

```go
execution, err := instance.Start(ctx, 1, playerID)
if err != nil {
    return err
}
if err := execution.OnComplete(func(taskCtx context.Context, returns blueprintmodule.PortArray, err error) {
    // 新的 Service task：这里可以安全修改玩家状态。
}); err != nil {
    execution.Cancel() // 完成任务未登记成功，由调用者决定继续观察还是取消。
    return err
}
```

每个 Execution 最多登记一个 `OnComplete`。登记会预留一个有界 Service 根任务；队列满时立即返回错误，
不会偷偷创建 goroutine，也不会自动取消 Execution。`Done`、`State`、`Result`、`Cancel` 可从任意 goroutine
用于观察和控制；业务状态修改仍应放在 `Run` 返回后或 `OnComplete` 中。

## 6. 自定义异步节点

```go
func (n *LoadProfileNode) Exec() (int, error) {
    handle, err := n.Yield(0)
    if err != nil {
        return -1, err
    }
    if err := n.client.LoadAsync(func(profile *Profile, rpcErr error) {
        if rpcErr != nil {
            // 按项目约定将失败转成恢复分支，或取消外层 Execution。
            return
        }
        _ = handle.Resume(profile.Level)
    }); err != nil {
        return -1, err
    }
    return -1, blueprintmodule.ErrExecutionSuspended
}
```

`Yield(nextPort)` 的 `nextPort` 是恢复后继续执行的 exec 输出端口。`Resume(outputs...)` 使用 Yield 时选择的
端口；`ResumeTo(nextPort, outputs...)` 可选择其他合法 exec 分支。`outputs` 按节点数据输出端口顺序写入。
句柄只能成功恢复一次；迟到、重复、已取消或已释放的恢复都会返回明确错误。

若 `Resume/ResumeTo` 因 Service 队列满而提交失败，本次不会消费句柄，业务可以在自身有界重试策略内再次
恢复，或取消对应 Execution。不要无限重试。`OnComplete` 会预留一个根任务，Resume 还需要一个任务容量；
若项目覆盖 Scheduler 配置，使用异步蓝图的 Service 必须让 `max_tasks` 至少为 2，并为正常并发留出容量。

外部回调只应调用 `Resume/ResumeTo`，不能访问玩家、房间或战斗状态。真实 RPC Client、Timer 或订阅器由
业务 Module 在 `OnStart/OnStop` 成对创建和关闭；不要在节点内启动无人管理的 goroutine。

## 7. 显式热加载

```go
result, err := module.Reload(ctx)
if err != nil {
    module.Logger().Error(fmt.Sprintf("reload applied=%t: %v", result.Applied, err))
    return err
}
```

`Reload` 必须从所属 Service 工作协程调用。目录读取、解析和全量编译在 `Await` 等待函数中进行，不占用
Service 工作协程；发布只替换一次编译图池。同一 Module 同时只允许一个事务，第二个调用立即返回
`ErrReloadInProgress`。

- 全量编译失败：不发布，旧图继续提供服务；
- 编译成功：后续 `Run/Start` 使用新图；
- 已运行或已 Yield 的 Execution：始终持有旧编译快照，恢复后不会混用新节点结构；
- `Applied=true` 且 `err` 非空：图池已经发布，但 Service 恢复排队阶段超过 `ctx` 截止时间，不能重试假设
  “尚未发布”。

生产建议由受鉴权的管理入口触发，先完成文件原子替换，再调用 `Reload`，并记录图版本、`GraphCount`、
操作者和结果。首版有意不提供文件监听和自动重试，避免半写文件与重试风暴。

## 8. Trace、诊断与统计

`WithTraceLogger` 和 `WithDiagnosticSink` 在 `Setup/New` 时各设置一次。Trace Logger 实现
`TraceBlueprintNode(BlueprintTraceEvent)`；事件的端口快照元素类型为 `BlueprintTracePortValue`。两类回调都可能并发发生，只能写
并发安全日志或指标，不能修改 Service 串行业务状态。未提供 Diagnostic Sink 时，Module 使用 Origin
Logger 记录阶段、文件、图、入口、Execution、节点、PC 和根因，不记录端口值。

Trace 默认关闭：

```go
if err := module.SetTraceEnabled(true); err != nil { /* 处理错误 */ }
defer module.SetTraceEnabled(false)
```

Trace 会复制端口值，既有性能成本也可能包含业务隐私，只在明确、短时诊断窗口开启。`Stats()` 返回活动
Instance，以及创建、关闭、启动、热加载成功/失败累计值；它不是玩家、图或节点级高基数指标。

## 9. 函数与参数在哪个协程执行

| 函数或回调 | 执行位置 | 可否访问 Service 串行状态 |
| --- | --- | --- |
| `Setup`、`RegisterNodes` | Module `OnInit` 调用栈 | 仅做配置；不要读业务运行态 |
| `NodeFactory` | OnInit 后加载/热加载 goroutine，或节点创建路径 | 否，只构造新节点 |
| `Module.Run`、`Instance.Run/Start` 首段 | 调用方 Service 工作协程 | 是 |
| 自定义节点 `Exec` | 所属 Service 工作协程 | 是 |
| RPC/Timer 底层回调 | 对应客户端或计时器 goroutine | 否，只 Resume |
| `YieldHandle.Resume/ResumeTo` 调用本身 | 任意 goroutine | 否 |
| Resume 后的节点 | 所属 Service 工作协程的新任务 | 是 |
| `Execution.OnComplete` 的等待函数 | Origin Await worker | 否，只等 Done/Result |
| `Completion` 回调 | 所属 Service 工作协程的新任务 | 是 |
| `Reload` 文件读取与编译 | Origin Await worker | 否 |
| `Reload` 调用前后 | 调用方 Service 工作协程 | 是 |
| Trace/Diagnostic 回调 | 可能并发的执行或取消路径 | 否 |

## 10. 常见错误

- `ErrNotSetup`：遗漏 `Setup`，或独立 Module 未通过 `New` 构造；
- `ErrNotRunning`：在 `OnStart` 成功前、停止中或停止后调用运行接口；
- `ErrBlueprintClosed`：Module 停止关闭引擎，活动或挂起 Execution 被收口；
- `ErrGraphNotFound` / `ErrEntranceNotFound`：图名或入口与编译文件不一致；
- `ErrExecutionPending`：Execution 尚未终态就调用 `Result`；
- `ErrInstanceClosed`：关闭后继续开始新执行；
- `ErrReloadInProgress`：已有热加载事务，当前请求应直接返回冲突而不是排队；
- `ErrYieldResumed`：同一个回调被重复触发；
- Context Deadline：`Run` 会取消对应 Execution；`Reload` 还需同时检查 `Applied`。

排查顺序：先看根错误链和默认结构化诊断，再核对配置绝对路径、图名/入口、节点定义名称、端口类型以及
节点工厂是否每次返回新对象。不要用自动重试掩盖编译错误或业务节点错误。

## 11. 范围与性能原则

包装层不兼容 v2 裸 ID，不暴露生产 `Engine()`，也不包装底层 Registry、Compiler、VM 或全部节点 API。
它不创建隐藏 Worker Pool、第二层消息队列、对象池、自动热加载或执行重试。当前 Benchmark 应先作为项目
基线；只有实际 Profile 证明包装分配或短锁成为瓶颈，才考虑局部优化，避免为了理论性能增加所有权风险。
