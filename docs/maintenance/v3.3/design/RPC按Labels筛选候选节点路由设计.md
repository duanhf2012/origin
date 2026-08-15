# RPC 按 Labels 筛选候选节点路由设计

> 状态：已实现并完成 Windows 验收
>
> 基线：v3.2.0 发布候选
>
> 目标：v3.3.0
>
> 兼容性：不改变现有 Route、RouteBy、OnNode、Broadcast、RPC Payload 或 TCP/NATS Wire

## 1. 目标与范围

RPC Client 在框架现有 ServiceName、契约、生命周期、Transport 和连接候选链中增加一层
Node Labels 精确过滤，再把过滤后的候选交给现有默认 RoundRobin、显式 RoundRobin、Random、
稳定 Key 或自定义 RouteSelector。多个条件使用 AND；Key、Value 都精确匹配；节点缺少任一
Label 即不匹配；零匹配返回 `ErrRPCNoRoute`。

本次不增加 OR、正则、前缀、表达式、权重、缓存或第二套服务发现模型。

## 2. 公共外观

底层与生成客户端分别增加同名值派生：

```go
func (client rpc.Client) WhereLabels(labels map[string]string) rpc.Client

func (client GameServiceClient) WhereLabels(
    labels map[string]string,
) GameServiceClient
```

推荐调用：

```go
client.
    WhereLabels(map[string]string{
        "scope":        "area",
        "real_area_id": "1",
    }).
    Route(playerKey).
    AwaitXxx(ctx, request)
```

不增加 `rpc.Labels` 命名 Map，避免与服务发现已有 Labels 数据形成第二套公共模型。
`RouteByLabel` 和 `RouteByLabels` 不采用，因为它们只表达过滤条件，无法清晰表达多个匹配实例
之间的最终选择，也会混淆现有 `RouteBy(RouteSelector)`。

## 3. 组合语义

1. `WhereLabels` 与 Route、RouteRoundRobin、RouteRandom、RouteBy、IncludeRetired 和 OnNode
   都按值派生，职责互不覆盖；调用顺序不影响结果。
2. 多次 `WhereLabels` 合并为 AND；同 Key 同 Value 幂等；同 Key 不同 Value 形成不可满足
   条件，调用时返回 `ErrRPCNoRoute`。
3. nil 或空 Map 是无操作，不清除已有条件；调用方需要无过滤范围时继续使用原始基础客户端。
4. `OnNode` 与 Labels 不互斥。`NodeID + ServiceName` 已经最多定位一个实例，Labels 只对该
   精确候选继续执行相同过滤；不匹配时返回 `ErrRPCNoRoute`，绝不扩大到其他 Node。
5. 默认路由继续使用现有 RoundRobin。自定义 Selector 只能看到已经通过 Labels 和框架全部
   可路由条件的候选。

## 4. 候选链与错误

候选扫描阶段为：

```text
ServiceName -> Contract -> Lifecycle -> Labels -> Transport -> Connected
```

Labels 位于生命周期之后、Transport 之前，使标签匹配但仅连接断开的目标继续保留现有 Await
等待恢复语义。最终 RouteCandidates 仍必须通过全部阶段，Labels 不能绕过契约、Running/
Retired、Transport 或连接检查。

- 无同名实例：`ErrRPCNoRoute`；
- 同名但契约不匹配：`ErrRPCContractMismatch`；
- 生命周期或 Labels 无匹配：`ErrRPCNoRoute`；
- Labels 匹配但 Transport 不兼容或断开：保留现有 Transport 错误及 Await 等待行为；
- Selector 拒绝过滤后的候选：`ErrRPCNoRoute`。

## 5. Broadcast 边界

Labels 首期不改变 Broadcast 范围。为了避免 `WhereLabels(...).BroadcastXxx(...)` 静默广播
全部实例，带有效或不可满足 Labels 条件的客户端调用 `PrepareBroadcast` 时快速返回
`ErrInvalidArgument`。没有 Labels 的所有现有 Broadcast 行为保持不变。

标签子集 Broadcast 需要单独设计意图目标数、容量放大、部分失败详情和迁移语义，不在本次
范围内。

## 6. 内存、并发和低延迟

`WhereLabels` 在值派生时把调用方 Map 冻结为最多 32 项的内部有序只读条件，之后不保留
调用方 Map。字符串只复制不可变字符串头，不复制内容。新增条件允许一次有界 Slice 分配；
稳定条件推荐在 Service 初始化时派生并长期保存。

RPC Prepare 热路径直接对候选快照已有 Labels Map 执行每条件一次精确查询：

- 不复制候选 Slice、Labels Map、ServiceName 或字符串内容；
- 不建立过滤结果 Slice；
- 不增加锁、goroutine、包级状态或标签缓存；
- 内置策略和框架自定义 Selector 包装保持 `0 allocs/op`；
- 扫描复杂度为 `O(候选数 * 要求标签数)`，要求标签数受发现模型的 32 项上限约束。

RoundRobin 继续使用现有 `ServiceName + ContractID + ContractFingerprint` Runtime 计数器。
Labels 不进入计数器 Key，避免动态条件组合形成无界状态；不同过滤范围可以共享轮询序号，
与现有动态候选集合变化语义一致。

## 7. 服务发现与生成 ABI

本次直接复用 `routeCandidate.labels`、本地冻结 Labels 和远端不可变发现快照，不修改 Provider
SPI、发现数据模型或目录缓存。

生成客户端增加 `WhereLabels`，正式生成物必须重新生成并通过
`origingen rpc --check ./...`。`GeneratedABIVersion` 保持 3，因为 Prepare/Encode/Submit
协作协议、业务方法签名、Payload 和 Wire 都没有变化；旧生成客户端仍能连接新 Runtime，
新生成代码连接缺少 `Client.WhereLabels` 的旧 Runtime 会直接编译失败。

## 8. 验收

1. 单个、多个、缺失、错误 Value、重复、冲突和空 Labels；
2. 默认、RoundRobin、Random、稳定 Key、自定义 Selector；
3. OnNode、IncludeRetired、契约错误、断开等待和无路由错误优先级；
4. Broadcast 对无 Labels 保持原行为，对带 Labels 快速失败；
5. 生成强类型 API、生成幂等和 ABI 3；
6. 1、2、32 个 Label 与代表性候选规模 Benchmark、allocs/op、Race 和全仓回归。
