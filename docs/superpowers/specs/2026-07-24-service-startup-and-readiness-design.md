# Origin v3 Service 启动与就绪设计

## 1. 文档状态与范围

- 状态：本文范围内的方案已确认
- 确认日期：2026-07-24
- 适用版本：Origin v3

本文定义 Service 启动期间的基础设施顺序、`OnStart` 就绪门、服务发现等待、初始化失败和对外开放规则。

以下问题不在本文展开：

- Application 与 Node 的完整启停顺序生成规则；
- Discovery Provider 的全量、增量和重连协议；
- 幂等 RPC 的自动重试、退避和重试次数；
- Service 正常运行阶段的协作式调度细节；
- 生命周期超时配置的最终字段外观。

相关设计分别见：

- [Application 与 Node 生命周期设计](./2026-07-22-application-node-lifecycle-design.md)
- [服务发现与关注筛选设计](./2026-07-24-service-discovery-and-interest-filter-design.md)
- [RPC 接口与调用语义设计](./2026-07-23-rpc-interface-and-call-semantics-design.md)
- [RPC 实例选择与路由策略设计](./2026-07-24-rpc-instance-selection-and-routing-strategy-design.md)
- [Service 协作式调度设计](./2026-07-23-service-cooperative-scheduling-design.md)

## 2. 设计目标

1. Service 完成必要的数据加载和依赖检查之前，不接受外部业务 RPC。
2. `OnStart` 可以等待尚未出现的远端 Service，而不阻塞服务发现继续更新。
3. 服务发现等待由快照和增量事件驱动，不通过高频 RPC 重试碰运气。
4. Service 是否就绪由 `OnStart` 的返回结果唯一决定，不要求业务额外调用 `MarkReady`。
5. 支持等待任意同名 Service，也支持精确等待 `NodeID + ServiceName`。
6. 启动失败有明确回滚边界，不把初始化不完整的 Service 暴露出去。
7. 启动路径保持简单，不为等待发现创建业务 Runner、Timer 轮询或后台业务 goroutine。

## 3. Node 基础设施先于业务 Service

Node 启动时先建立业务 Service 初始化所需的基础设施：

1. Node 身份和静态配置；
2. TimerEngine 的系统计时能力；
3. TCP、NATS 等 Transport；
4. RPC Client Runtime 和 pending call 管理；
5. Discovery Provider 客户端；
6. 可见服务快照、路由表和发现等待管理器。

完成以上步骤后，Node 才依次启动本地业务 Service。

服务发现由 Node 基础设施自己的 goroutine 持续接收全量和增量更新。业务 Service 的 `OnStart` 即使正在等待，服务发现仍能：

- 建立或恢复 Provider 连接；
- 接收远端 Node 和 Service 更新；
- 更新可见服务快照与可路由状态；
- 完成相应的发现等待。

因此，`OnStart` 不需要通过循环 RPC 或 `Sleep` 等待发现补齐。

## 4. Service 启动状态与就绪门

Service 启动期间处于 `Starting`。框架调用：

```go
OnStart(ctx context.Context) error
```

`OnStart` 返回前：

- Service 不接受入站请求—响应 RPC；
- Notify 和 Broadcast 不向该 Service 投递；
- Service 不进入普通 RPC 候选路由；
- 不启动普通业务 Runner；
- 不处理普通本地事件和业务 Timer；
- 可以使用启动上下文执行 `AwaitService`、`AwaitNodeService` 和 Await 风格的 RPC、Redis、数据库操作。

`OnStart` 返回 `nil` 后，框架在一个有序状态边界完成：

1. 将 Service 切换为 `Running`；
2. 将其标记为可路由；
3. 向 Discovery Provider 发布最新状态；
4. 开放入站 RPC、普通事件、Timer 和业务调度。

`OnStart` 返回成功就是唯一的就绪确认。首版不提供 `MarkReady`、`SetReady` 或 `auto_ready: false`，避免遗漏调用、重复开放以及 Node 部分就绪带来的额外状态。

## 5. 服务发现等待接口

Service 提供两个语义明确的接口，不解析 `NodeID.ServiceName` 组合字符串：

```go
AwaitService(
    ctx context.Context,
    serviceName string,
) error

AwaitNodeService(
    ctx context.Context,
    nodeID string,
    serviceName string,
) error
```

### 5.1 `AwaitService`

`AwaitService` 等待可见服务快照中至少存在一个满足以下条件的实例：

- 实际 `ServiceName` 与参数完全相同；
- 通过当前 Node 的 `allow_discovery` 筛选；
- 当前处于可路由状态。

该接口不固定目标 Node，适合普通高可用服务。后续 RPC 仍由路由器在所有可用实例中选择目标。

### 5.2 `AwaitNodeService`

`AwaitNodeService` 等待由以下复合身份确定的具体实例：

```text
NodeID + ServiceName
```

目标必须同时可见且可路由。该接口适用于分片归属、指定玩家所在 Node 或其他必须定向到具体实例的场景。

`NodeID` 是发现目录中的唯一 Node 身份。v3 不为该接口增加语义重复的 `NodeName`。

默认优先使用 `AwaitService`。不需要精确实例时使用 `AwaitNodeService` 会削弱故障转移能力。

### 5.3 名称规则

- `serviceName` 匹配公开的实际 `ServiceName`；
- 模板 Service 必须使用创建后的实际名称，不能使用模板名称代替；
- 方法参数不接受 RPC 契约名、Go 接口名或 RPC 方法名；
- Node 与 Service 参数分开传递，不保留 `.` 作为隐式分隔符。

## 6. 等待实现语义

调用等待接口时：

1. 先查询当前不可变可见服务快照；
2. 已存在匹配的可路由实例时立即返回，不创建等待项；
3. 不存在时，在发现等待管理器中注册一次等待；
4. 后续快照更新或实例状态变化时重新匹配相关等待；
5. 匹配成功、Context 取消或 Deadline 到期时完成一次并移除等待项。

快照检查与等待注册必须使用同一个有序边界，或使用快照版本进行二次校验，避免实例恰好在两步之间出现而造成丢失唤醒。

发现等待是一次性 Future，不是 Timer 轮询。服务发现更新只检查可能受本次变化影响的等待键，不在每次更新中扫描无关业务对象。

如果本地 `allow_discovery` 配置从 Service 名称维度已经确定不可能匹配目标，调用立即返回对应的服务发现配置错误。需要依赖尚未出现的远端标签或实例信息才能判断时，等待由启动 Context 的 Deadline 结束。

两个等待接口遵守已经确认的统一 Await Deadline 规则：

```text
调用方显式 Deadline > Service 默认值 > Node 默认值 > Origin 内置 15s
```

`OnStart` 传入的启动 Context 已有 Deadline 时直接继承该 Deadline，不再叠加另一层 `15s`。

## 7. `OnStart` 示例

```go
func (s *PlayerService) OnStart(ctx context.Context) error {
    if err := s.AwaitService(ctx, "DBService"); err != nil {
        return err
    }

    data, err := s.dbRPC.AwaitLoadGlobalData(ctx)
    if err != nil {
        return err
    }

    s.globalData = data
    return nil
}
```

等待指定实例：

```go
func (s *SceneService) OnStart(ctx context.Context) error {
    return s.AwaitNodeService(
        ctx,
        "world-1",
        "WorldService",
    )
}
```

`AwaitNodeService` 只负责确认指定实例当前可路由，不把后续普通 RPC 隐式绑定到该 Node。需要调用同一实例时，调用方使用对应的生成 Node 客户端：

```go
client := contract.NewWorldRPCNodeClient(
    s,
    "world-1",
    "WorldService",
)
```

完整外观见 [单目标 RPC 客户端与路由设计](./2026-07-24-rpc-single-target-client-and-routing-design.md)。

实例可能在等待成功后、定向 RPC 发起前丢失，因此 RPC 仍必须正常处理无可用路由、断线、取消和超时。

## 8. 重试与失败处理

服务发现等待和 RPC 重试是两个不同问题：

- 尚无可路由实例时，使用 `AwaitService` 或 `AwaitNodeService` 等待发现事件；
- 已经发现实例后，RPC 按正常调用语义返回成功或错误；
- 框架不把无可用实例隐藏成无限 RPC 重试；
- 只有明确可安全重试的幂等 RPC 才能使用有界重试；
- 重试必须继承 `OnStart` 的 Context，不得突破其 Deadline；
- 非幂等 RPC 默认不自动重试。

`OnStart` 出现以下任一结果时启动失败：

- 返回非 `nil` 错误；
- 启动 Context 被取消；
- 启动 Context 的 Deadline 到期；
- 发生 panic。

启动失败后：

1. 失败 Service 不进入 `Running`，也不发布为可路由；
2. Node 停止启动后续 Service；
3. Node 在基础设施仍可用时，按已启动 Service 的反序执行回滚；
4. Node 执行失败 Service 的启动失败清理路径，释放框架已经登记的资源；
5. Node 向 Application 返回启动错误和回滚错误。

Context 取消和超时使用统一错误码设计中的 `CodeCanceled` 与 `CodeDeadlineExceeded`。业务返回错误保留其原始 Origin Code，Node/Application 负责聚合，不通过错误文本判断原因。

业务在 `OnStart` 返回前自行创建且尚未交给框架管理的临时资源，必须通过 `defer` 或本地错误路径释放，不能依赖尚未进入 `Running` 的普通 `OnStop` 流程。

## 9. 启动顺序与交叉依赖

Application 已确认按有效 `start_order` 顺序启动 Node，并等待前一个 Node 进入 `Ready` 后才启动下一个 Node。因此：

- 依赖方 Node 必须排在被依赖方 Node 之后；
- 如果 Node A 的 `OnStart` 等待尚未启动的 Node B，而配置又要求 A Ready 后才启动 B，A 最终会超时并导致启动失败；
- 两个 Service 在 `OnStart` 中互相等待对方可路由会形成启动环；
- 服务发现补齐和 RPC 重试不能解决启动环。

v3 不为启动阶段引入依赖图和自动解环。项目通过明确的 Node 启动顺序以及单向初始化依赖避免启动环。确实存在双向运行依赖时，至少一方必须先完成不依赖对方的启动，在进入 `Running` 后再建立非启动关键路径的联系。

## 10. 调度规则

`OnStart` 由唯一的生命周期 goroutine 执行。它的 Await 行为与 `OnStop` 的 finalizer 等待路径相似：

- 等待期间占用该生命周期 goroutine；
- 不占用一个正在执行用户业务事件的 Runner；
- 不创建替代业务 Runner；
- Future 由 RPC Runtime、Discovery Provider 或外部适配器的 goroutine 完成；
- Future 完成后唤醒同一个生命周期 goroutine，从 Await 后继续顺序执行。

这样可以在启动逻辑中使用顺序代码，同时保证 Service 状态尚未开放给并发业务任务。

业务不得在 `OnStart` 中：

- 使用 Async 回调作为启动完成条件；
- 注册业务 Timer 轮询依赖；
- 创建无法被启动 Context 管理的后台 goroutine；
- 提前开放入站 RPC；
- 通过 Notify 或 Broadcast 代替需要确认完成的初始化调用。

## 11. 可观测性

至少记录：

- 每个 Service 的 `Starting` 开始、成功和失败时间；
- `OnStart` 总耗时；
- 当前等待的 ServiceName、可选 NodeID 和等待耗时；
- 服务发现快照版本以及完成等待的实例身份；
- 初始化 RPC 的目标、耗时和 Origin Code；
- 启动 Context 取消、超时和 panic；
- Node 启动回滚顺序与回滚错误。

日志和 Trace 可以携带 NodeID、ServiceName 等诊断字段，但 RPC 线协议错误仍遵守统一错误码的轻量规则。

## 12. 测试要求

后续实现至少验证：

1. Discovery Provider 和 RPC Client Runtime 在 `OnStart` 前可用；
2. `OnStart` 返回前，Service 不接受入站 RPC 且不进入候选路由；
3. `OnStart` 返回成功后，Service 原子进入 `Running` 和可路由状态；
4. 当前快照已有实例时，`AwaitService` 立即完成；
5. 实例稍后被发现时，等待可以被增量更新唤醒；
6. 快照查询与等待注册竞争时不会丢失唤醒；
7. `AwaitService` 可以由任意同名可路由实例完成；
8. `AwaitNodeService` 只能由指定 `NodeID + ServiceName` 完成；
9. `Starting`、`Retired` 或不可路由实例不能完成等待；
10. Context 取消或超时会移除等待项并返回统一错误；
11. 显式 Deadline 和默认 `15s` 兜底遵守统一 Await 优先级；
12. 模板 Service 只能通过实际 ServiceName 匹配；
13. `OnStart` 失败时不会发布失败 Service，并会触发 Node 回滚；
14. `OnStart` 等待期间服务发现仍能更新快照；
15. `OnStart` 不创建普通业务 Runner、Timer 轮询或 Async 回调；
16. 启动顺序错误和交叉等待最终由启动 Context 有界失败；
17. `AwaitNodeService` 不会把后续普通 RPC 隐式绑定到目标 Node；
18. 目标在等待成功后、RPC 发起前丢失时，RPC 正常返回路由错误；
19. TCP 与 NATS 使用相同的等待匹配和就绪语义。

## 13. 已确认结论

- Node 先启动 Transport、RPC Runtime 和服务发现等基础设施，再执行业务 Service 的 `OnStart`；
- Service 在 `OnStart` 返回成功前保持 `Starting` 且不对外可路由；
- `OnStart` 返回成功就是唯一就绪确认，首版不提供手动 `MarkReady`；
- 服务发现等待由快照和增量事件驱动，不使用 RPC 或 Timer 轮询；
- 提供 `AwaitService(ctx, serviceName)` 等待任意同名可路由实例；
- 提供 `AwaitNodeService(ctx, nodeID, serviceName)` 等待指定实例；
- Node 与 Service 参数分开，不解析 `NodeID.ServiceName` 组合字符串；
- 精确身份继续使用已确认的 `NodeID + ServiceName`，不增加 `NodeName`；
- 发现等待遵守统一 Await Deadline 规则，最终兜底为内置 `15s`；
- `AwaitNodeService` 不自动绑定后续 RPC 的路由目标；
- 无实例等待与 RPC 重试保持分离，非幂等 RPC 不自动重试；
- `OnStart` 失败时 Service 不开放，Node 按启动失败规则回滚；
- 显式启动顺序必须保证被依赖 Node 先 Ready，启动交叉依赖不自动解环。
