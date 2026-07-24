# Origin v3 RPC 实例选择与路由策略设计

## 1. 文档状态与范围

- 状态：本文范围内的方案已确认
- 确认日期：2026-07-24
- 适用版本：Origin v3

本文定义单目标 RPC 客户端在多个可路由 Service 实例之间的选择规则，包括默认轮询、显式轮询、随机、按 Key 取模以及自定义路由策略。

以下独立问题不在本文展开：

- 幂等 RPC 的自动重试、退避和重试次数；
- 基于实时负载、延迟或权重的动态选择策略；
- Broadcast 的目标计算和投递；
- TCP、NATS 的连接建立、心跳与重连；
- 服务发现 Provider 的一致性规则。

相关设计：

- [单目标 RPC 客户端与路由设计](./2026-07-24-rpc-single-target-client-and-routing-design.md)
- [RPC 接口与调用语义设计](./2026-07-23-rpc-interface-and-call-semantics-design.md)
- [服务发现与关注筛选设计](./2026-07-24-service-discovery-and-interest-filter-design.md)
- [统一错误码设计](./2026-07-24-unified-error-code-design.md)

## 2. 设计目标

1. 常用策略具有直接、易读的强类型客户端外观。
2. 默认策略简单、稳定且不依赖远端负载采集。
3. 允许业务实现自定义策略，但不让自定义接口增加内置策略的热路径开销。
4. 路由策略只从统一筛选后的合法候选实例中选择，不能绕过服务发现和生命周期状态。
5. 客户端只保存轻量路由描述，不拥有 TCP/NATS 连接。
6. Key 类型对业务友好，同时只在绑定路由时归一化一次。
7. 首版优先代码精简、可阅读和可维护；性能结论通过基准验证。

## 3. 候选实例边界

每次 RPC 发起前，Node RPC Runtime 从最新不可变路由快照取得候选实例。候选实例必须同时满足：

- 实际 `ServiceName` 与客户端目标完全相同；
- 实现客户端对应的 RPC 契约和目标方法；
- 通过当前 Node 的 `allow_discovery`；
- 当前状态允许普通 RPC 路由；
- 未进入 `Retired`、`Stopping` 或 `Stopped`；
- 通过后续确定的健康状态检查。

候选可路由不表示瞬时 TCP/NATS 连接一定可用。连接状态与服务发现、Service 路由状态保持分离；目标选择完成后发生的断线或发送失败由 Transport 正常返回，不能伪装成服务失去发现。

实例使用以下身份：

```text
NodeID + ServiceName
```

候选列表在发布不可变快照前按 `NodeID`、`ServiceName` 依次升序排列。业务不能依赖 Discovery Provider 的原始返回顺序。相同可见快照必须产生相同候选顺序。

路由策略只能从该列表中选择，不能重新加入已经被 Runtime 排除的实例。候选列表为空时返回 `CodeRPCNoRoute`。

## 4. 生成客户端外观

对于生成客户端：

```go
playerRPC := contract.NewPlayerRPCClient(
    s,
    "PlayerService",
)
```

首版提供以下路由外观：

```go
// 默认轮询。
playerRPC.AwaitGetPlayer(ctx, playerID)

// 显式轮询。
playerRPC.
    RouteRoundRobin().
    AwaitGetPlayer(ctx, playerID)

// 随机。
playerRPC.
    RouteRandom().
    AwaitGetPlayer(ctx, playerID)

// 按 Key 取模。
playerRPC.
    Route(playerID).
    AwaitGetPlayer(ctx, playerID)

// 自定义策略。
playerRPC.
    RouteBy(zoneSelector).
    AwaitGetPlayer(ctx, playerID)
```

命名使用完整语义，不生成 `RouteRand`、`RouteRR` 或 `RouteModKey`。`Route(key)` 表示在相同候选快照中按业务 Key 稳定选择；具体采用简单取模属于框架实现规则，不暴露在业务方法名中。

`RouteRoundRobin`、`RouteRandom`、`Route` 和 `RouteBy` 都返回同一种生成强类型客户端。它们只改变单目标选择策略，不改变 `AwaitXxx`、`AsyncXxx` 和 `NotifyXxx` 的签名。

这些方法只创建轻量路由描述，不等待服务发现、不执行网络 I/O、不建立连接，也不创建 Future 或 Timer。真正调用生成的 RPC 方法时，Runtime 才读取最新候选快照并执行选择。

如果连续绑定多个策略，最后一次绑定生效：

```go
routed := playerRPC.
    Route(playerID).
    RouteRandom()
```

上例最终使用随机策略。业务应避免无意义的连续覆盖。

## 5. 默认与显式轮询

普通客户端未显式绑定策略时默认使用轮询，等价于：

```go
playerRPC.RouteRoundRobin()
```

概念规则：

```text
index = nextCounter % len(candidates)
```

轮询状态由 Node RPC Runtime 按路由组统一维护，不能保存在临时客户端中。路由组至少包含 RPC 契约和实际 `ServiceName`，并绑定当前 Node 的可见路由范围。

这样即使业务反复创建轻量客户端，也不会每次从候选列表第一个实例重新开始。多个调用方 Node 各自维护轮询状态，不追求跨 Node 的全局严格轮询。

实现使用短小的原子计数或等价低开销机制，不为每次选择加互斥锁。快照变化后继续使用计数值对新候选数量取模，不需要迁移旧计数状态。

## 6. 随机路由

显式调用：

```go
randomRPC := playerRPC.RouteRandom()
```

每次 RPC 从当时的候选列表中随机选择一个实例。随机策略用于业务明确接受短期分布不均或希望避免严格轮询序列的场景。

随机数只用于负载分散，不用于安全用途。Runtime 使用无全局锁或低竞争的快速伪随机状态，不调用密码学随机源，也不为每次选择创建随机数生成器。

## 7. 按 Key 取模

### 7.1 使用外观

```go
playerRoute := playerRPC.Route(playerID)

player, err := playerRoute.AwaitGetPlayer(
    ctx,
    playerID,
)

err = playerRoute.AwaitSavePlayer(
    ctx,
    player,
)
```

`Route` 调用时立即把 Key 归一化成稳定的 `uint64`。生成客户端只保存归一化结果，不保存原始 Key：

```go
type rpcClientBase struct {
    routeMode routeMode
    routeHash uint64
}
```

发起 RPC 时只执行：

```text
index = routeHash % len(candidates)
```

### 7.2 支持的 Key 类型

`Route(key any)` 首版支持：

- `string`；
- `[]byte`；
- `int`、`int8`、`int16`、`int32`、`int64`；
- `uint`、`uint8`、`uint16`、`uint32`、`uint64`；
- 底层类型为以上类型的自定义命名类型。

首版不支持：

- `float32`、`float64`；
- `bool`；
- `uintptr`；
- 指针；
- struct；
- map；
- `[]byte` 之外的 slice；
- `nil`。

不支持的类型不触发 panic。返回的轻量客户端保存 `CodeRPCInvalidRouteKey`，后续 `AwaitXxx` 和 Notify 直接返回该错误，`AsyncXxx` 通过正常异步回调返回该错误。

### 7.3 归一化规则

精确基础类型优先通过类型分支处理；自定义命名类型只在 `Route` 调用时识别一次底层类型。后续 RPC 热路径不再执行类型判断或反射。

归一化规则：

- 非负整数按数值转换为 `uint64`；
- 有符号负整数按固定的二进制补码转换为 `uint64`；
- `string` 按原始 UTF-8 字节执行 FNV-1a 64 位哈希；
- `[]byte` 按原始字节执行同一 FNV-1a 64 位哈希；
- 空字符串和空 `[]byte` 是合法 Key，并得到固定哈希；
- `Route` 完成哈希后不保存 `[]byte`，调用方后续修改原切片不会改变已绑定客户端；
- 内容相同的 `string` 与 `[]byte` 得到相同哈希；
- 数字字符串与数值不是同一种 Key，例如 `"10001"` 不要求与 `int64(10001)` 路由相同。

FNV-1a 64 位算法和字节处理方式是首版稳定路由规则。发布后不能在同一兼容版本中静默更换，否则会造成 Key 批量迁移。

### 7.4 实例变化

简单取模不保证实例变化时的最小迁移。候选数量或稳定排序发生变化后，大量 Key 可能重新映射。

这是首版为保持实现简单而明确接受的限制。适用场景包括：

- 可从共享存储重新加载状态的分片服务；
- 重新映射不会产生所有权冲突的无状态服务；
- 业务能够处理实例增减带来的 Key 迁移。

持有在线玩家、场景、战斗等进程内唯一状态时，不能把 `Route(key)` 当作所有权目录。业务必须保存实际 `NodeID + ServiceName`，并使用 `NewXxxRPCNodeClient` 精确调用；迁移状态需要单独的业务协议。

## 8. 自定义路由

### 8.1 接口

首版提供高级扩展点：

```go
type RouteSelector interface {
    Select(RouteCandidates) (index int, ok bool)
}
```

生成客户端使用：

```go
zoneRPC := playerRPC.RouteBy(
    &ZoneSelector{
        Region: "cn-east",
    },
)
```

示例：

```go
type ZoneSelector struct {
    Region string
}

func (s *ZoneSelector) Select(
    candidates origin.RouteCandidates,
) (int, bool) {
    for i := 0; i < candidates.Len(); i++ {
        region, ok := candidates.Label(i, "region")
        if ok && region == s.Region {
            return i, true
        }
    }

    return 0, false
}
```

### 8.2 只读候选视图

`RouteCandidates` 是引用当前不可变快照的只读值，不复制候选切片：

```go
type RouteCandidates struct {
    // 内部字段不公开。
}

func (c RouteCandidates) Len() int
func (c RouteCandidates) NodeID(index int) string
func (c RouteCandidates) ServiceName(index int) string
func (c RouteCandidates) Label(index int, name string) (string, bool)
```

Selector 只能返回候选下标，不能构造任意目标身份。这样自定义策略不能绕过 Runtime 已经完成的契约、发现和生命周期筛选。

### 8.3 执行约束

自定义 Selector 必须：

- 同步、快速且无阻塞；
- 不调用 RPC、Redis、数据库、文件系统或 `Await`；
- 不修改或长期保存 `RouteCandidates`；
- 不在选择过程中启动业务 goroutine；
- 可以安全并发调用；
- 返回有效候选下标，或使用 `ok=false` 表示没有合适实例。

`ok=false` 返回 `CodeRPCNoRoute`。Selector 为 `nil`、返回越界下标或发生 panic 时，Runtime 记录诊断信息并返回 `CodeRPCRouteSelectorFailed`。panic 恢复只存在于自定义策略路径，不进入内置轮询、随机或 Key 路由热路径。

业务应在 Service 初始化时创建并保存自定义路由客户端，避免在每次 RPC 前创建闭包或重复分配 Selector：

```go
func (s *GatewayService) OnStart(
    ctx context.Context,
) error {
    base := contract.NewPlayerRPCClient(
        s,
        "PlayerService",
    )

    s.localPlayerRPC = base.RouteBy(
        &ZoneSelector{
            Region: s.Region(),
        },
    )

    return nil
}
```

首版不提供按字符串名称全局注册策略，也不直接接受任意函数闭包。这样避免全局注册顺序、字符串查找、闭包捕获和不明确的生命周期。

## 9. 与 Node 定向客户端的关系

路由策略只在客户端已经绑定的目标范围中选择。

普通客户端的目标范围是所有符合 `ServiceName` 和 RPC 契约的可路由实例。Node 定向客户端的目标范围最多只有指定的 `NodeID + ServiceName` 一个实例：

```go
nodeRPC := contract.NewPlayerRPCNodeClient(
    s,
    "player-2",
    "PlayerService",
)
```

因此，对 Node 定向客户端调用 `RouteRoundRobin`、`RouteRandom` 或 `Route(key)` 不会扩大目标范围，仍只能选择 `player-2 + PlayerService`。`RouteBy` 只会收到零个或一个候选实例。

底层 TCP 重连、NATS 重订阅或快照对象替换都不能改变 Node 定向客户端的逻辑目标。

## 10. 选择、发送与重试

一次 RPC 只根据调用开始时的最新快照选择一次目标。选择成功不表示目标一定能处理请求；选择后仍可能发生退休、停止、断线或发送失败。

首版路由策略本身不执行：

- 第二次选址；
- 自动切换其他实例；
- 自动重试；
- Notify 重发。

这样避免非幂等 RPC 被重复执行。未来只有明确标记为幂等的 RPC 才能使用独立、有界的重试设计，且必须继承原调用的 Context 和 Deadline。

## 11. TCP、NATS 与本地调用

轮询、随机、Key 取模和自定义 Selector 的候选语义与 Transport 无关：

- TCP 根据选中的目标 Node 使用 Node 级连接管理器；
- NATS 根据选中的目标 Node 和 Service 使用 Node 级共享连接发布；
- 本地目标仍经过相同候选筛选和路由策略；
- 客户端不建立或持有专属连接。

Transport 只能负责把已经确定的单目标消息送往目标，不能擅自重新选择另一个 Service 实例。

## 12. 性能约束

- 默认轮询使用 Runtime 级低竞争计数，不为每次调用创建客户端状态；
- 随机策略不使用密码学随机源和全局互斥随机数生成器；
- `Route(key)` 只在绑定时识别类型并归一化，后续调用只做整数取模；
- 精确基础类型 Key 走快速类型分支；
- 自定义命名类型的底层类型识别只发生在 `Route` 调用；
- `RouteCandidates` 不复制候选切片或标签表；
- 内置策略不经过 `RouteSelector` 接口分派或 panic 恢复；
- 所有策略读取不可变路由快照，不与服务发现更新持有长时间互斥锁；
- 路由客户端构造、内置选择成功路径以零堆分配为实现目标；
- 任何为了降低分配而增加明显复杂度的优化，都必须先用基准证明必要并与开发者确认。

即使 `Route(any)` 的绑定成本通常远低于网络 RPC，仍必须分别基准验证“每次调用前临时 Route”和“保存已 Route 客户端”两种用法。文档推荐保存已 Route 客户端，但不能依赖该建议掩盖异常分配。

## 13. 测试要求

后续实现至少验证：

1. 普通客户端未绑定策略时使用轮询；
2. 显式 `RouteRoundRobin` 与默认轮询语义一致；
3. 临时创建普通客户端不会使轮询反复从第一个候选开始；
4. 多个调用方 Node 各自维护轮询状态；
5. `RouteRandom` 只选择合法候选；
6. 候选列表按 `NodeID + ServiceName` 稳定排序；
7. 相同快照和相同 Key 选择相同实例；
8. 整数 Key 使用数值归一化；
9. `string` 与相同内容的 `[]byte` 得到相同哈希；
10. 数字字符串与数值允许得到不同哈希；
11. 支持全部已声明整数类型及其自定义命名类型；
12. 不支持的 Key 类型不 panic，并返回 `CodeRPCInvalidRouteKey`；
13. `Route` 后的客户端不保存原始 Key；
14. Key 归一化不进入后续 RPC 热路径；
15. 候选数量变化后按新数量重新取模；
16. 退休、停止、未就绪和不健康实例不会进入 Selector；
17. 自定义 Selector 只能选择候选下标；
18. 自定义 Selector 返回 `ok=false` 时返回 `CodeRPCNoRoute`；
19. nil Selector、越界下标和 panic 返回 `CodeRPCRouteSelectorFailed`；
20. 内置策略不进入自定义 Selector 的接口和 panic 恢复路径；
21. Node 定向客户端应用任意策略后仍不扩大目标范围；
22. 一次调用选择失败或发送失败时不自动重选和重试；
23. TCP、NATS 和本地 Transport 使用相同选择结果；
24. 轮询、随机、整数 Key、字符串 Key 和自定义 Selector 的延迟与分配基准；
25. 临时 `Route` 与保存已 Route 客户端的对比基准；
26. 100 个候选实例下各内置策略的延迟基准。

## 14. 已确认结论

- 普通客户端默认使用轮询；
- 提供 `RouteRoundRobin()` 显式轮询；
- 提供 `RouteRandom()` 随机选择；
- 提供 `Route(key)` 按稳定 Key 哈希结果对候选数量取模；
- 提供 `RouteBy(RouteSelector)` 自定义选择；
- 使用完整语义名称，不使用 `RouteRand`、`RouteRR` 或 `RouteModKey`；
- Key 支持字符串、字节、常用有符号和无符号整数及其自定义命名类型；
- `Route` 调用时归一化 Key，客户端只保存 `uint64`；
- 字符串和字节使用稳定的 FNV-1a 64 位哈希；
- 候选实例按 `NodeID + ServiceName` 稳定排序；
- 简单取模不保证实例变化时的最小迁移；
- 有状态实例所有权使用 Node 定向客户端，不依赖简单取模；
- 自定义 Selector 只读取统一筛选后的候选视图；
- 内置策略不经过自定义接口；
- 路由选择不自动重选或重试；
- 所有策略通过 Node RPC Runtime 使用内部统一管理的连接。
