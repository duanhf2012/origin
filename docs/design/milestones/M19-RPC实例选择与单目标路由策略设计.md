# Origin 第三版 M19 RPC 实例选择与单目标路由策略设计

> 文档状态：已实现并通过验收
>
> 确认日期：2026-07-30
>
> 实现与验收日期：2026-08-01
>
> 前置里程碑：M11、M13、M14、M15、M16、M17、M18

## 1. 里程碑目标

M19 把 M14 的不可变服务发现快照、M13/M15 的 TCP/NATS 单目标 Transport 和 M11 的
强类型生成客户端接成完整的多实例单目标调用闭环。

M19 完成：

1. `rpc.ToService(serviceName)` 从本地与远端的统一可用候选中自动选择一个实例；
2. 默认 RoundRobin，以及显式 RoundRobin、Random、稳定 Key 和自定义 Selector；
3. 可长期保存的生成客户端和一次绑定的便捷入口；
4. 在编码前完成唯一一次目标选择，为本地、TCP、NATS 精确分配请求 Buffer；
5. 选择时排除 Retired 和已知断开 Transport，提交时再次校验竞态；
6. 不复制服务发现候选和标签，不在内置选择成功热路径产生堆分配。

M19 不实现 Broadcast、多目标部分成功、自动重试、权重、健康评分、负载反馈、一致性
哈希、熔断、流量镜像或动态配置策略。这些能力需要独立里程碑和单独错误语义。

后续 M20 已固定：默认自动单目标与 Broadcast 都排除 Retired，`IncludeRetired()` 显式纳入
Running 与 Retired；精确 `OnNode` 继续允许 Retired。该补充不改变 M19 的策略算法、候选
排序或单目标 Prepare。

## 2. 已确认的公共外观

### 2.1 最终业务外观

RPC 契约建议放在能表达业务含义的包中，例如 `playerapi`。`contract` 只曾是设计示例中的
占位包名，不是 Origin 要求的固定包名。

普通业务在 `OnInit` 中绑定一次默认目标并保存为字段：

```go
type GatewayService struct {
    service.Service

    playerRPC playerapi.PlayerRPCClient
}

func (s *GatewayService) OnInit() error {
    s.playerRPC = playerapi.BindPlayerRPC(s)
    return nil
}
```

调用点只表达真正变化的业务参数和可选路由策略：

```go
// 默认 RoundRobin。
player, err := s.playerRPC.AwaitGetPlayer(ctx, playerID)

// 按业务 Key 稳定选择。
player, err := s.playerRPC.
    Route(playerID).
    AwaitGetPlayer(ctx, playerID)

// 精确调用已知归属 Node；沿用已经绑定的 ServiceName。
player, err := s.playerRPC.
    OnNode(playerNodeID).
    AwaitGetPlayer(ctx, playerID)
```

`playerRPC` 是调用方 Service 的字段，类型为生成的轻量 `PlayerRPCClient` 值；它不是玩家
集合、全局对象或连接池。`Route` 与 `OnNode` 都返回新的值客户端，不修改字段本身。

### 2.2 规范构造函数

M11 已有的规范构造函数保持不变：

```go
client := playerapi.NewPlayerRPCClient(
    owner,
    rpc.ToService("PlayerService"),
)

exact := playerapi.NewPlayerRPCClient(
    owner,
    rpc.ToServiceOnNode("player-2", "PlayerService"),
)
```

它仍是构造任意 `rpc.Target` 客户端的唯一通用入口。

### 2.3 默认 ServiceName 与显式覆盖

`origingen` 为每个 RPC 契约生成默认名称绑定和显式名称覆盖两个入口：

```go
func BindPlayerRPC(
    owner service.IService,
) PlayerRPCClient

func BindPlayerRPCTo(
    owner service.IService,
    serviceName string,
) PlayerRPCClient
```

默认 ServiceName 只由契约名确定，不扫描当前实现类型，也不依赖生成命令覆盖了哪些包：

```text
PlayerRPC       -> PlayerService
DBRPC           -> DBService
SceneManagerRPC -> SceneManagerService
```

确定性规则为：契约名以非空 `RPC` 后缀结束时去掉该后缀并追加 `Service`；否则直接追加
`Service`。因此 `BindPlayerRPC(owner)` 等价于：

```go
NewPlayerRPCClient(owner, rpc.ToService("PlayerService"))
```

运行时实际 ServiceName 由配置决定。模板 `SceneService` 可以实例化为
`scene-1:SceneService`；调用这种改名实例时使用：

```go
sceneRPC := sceneapi.BindSceneRPCTo(s, "scene-1")
```

`BindXxxRPCTo` 等价于 `NewXxxRPCClient(owner, rpc.ToService(serviceName))`。不使用可变参数，
避免多余名称可以编译却只能延迟到首次 RPC 才报错；也不按 ContractID 在运行时猜测任意
实现，避免把不同 ServiceName 的业务边界合并。

推荐在 Service `OnInit` 冷路径绑定并长期复用：

```go
type GatewayService struct {
    service.Service

    playerRPC      playerapi.PlayerRPCClient
    zonePlayerRPC  playerapi.PlayerRPCClient
    zoneSelector   *ZoneSelector
}

func (s *GatewayService) OnInit() error {
    s.playerRPC = playerapi.BindPlayerRPC(s)
    s.zonePlayerRPC = s.playerRPC.RouteBy(s.zoneSelector)
    return nil
}
```

普通调用不再重复构造 Target 或设置策略：

```go
player, err := s.playerRPC.AwaitGetPlayer(ctx, playerID)
```

按每次业务 Key 选择时只派生一个值客户端：

```go
player, err := s.playerRPC.
    Route(playerID).
    AwaitGetPlayer(ctx, playerID)
```

生成客户端不持有专属连接、goroutine、快照、取消函数或待完成请求，可以作为 Service
字段长期保存，也可以安全按值复制。业务不能把客户端跨 Service 共用，因为客户端保存
owner，并据此确定 Await、Async 回调和停止归属。

### 2.4 路由与精确节点派生

每个生成客户端提供：

```go
func (c PlayerRPCClient) OnNode(nodeID string) PlayerRPCClient
func (c PlayerRPCClient) RouteRoundRobin() PlayerRPCClient
func (c PlayerRPCClient) RouteRandom() PlayerRPCClient
func (c PlayerRPCClient) Route(key any) PlayerRPCClient
func (c PlayerRPCClient) RouteBy(selector rpc.RouteSelector) PlayerRPCClient
```

所有方法都使用值接收者并返回同一强类型客户端，不修改原客户端。`OnNode` 保留当前
ServiceName 与契约，只把目标收窄为指定 Node；它等价于使用同一 ServiceName 构造
`ToServiceOnNode`，不执行发现查询、拨号或等待。内置策略保存紧凑枚举和归一化后的
`uint64`，不保存闭包、原始 Key、候选切片或运行时连接。

`ToService` 未显式选择策略时默认使用 RoundRobin。`RouteRoundRobin` 主要用于显式表达
意图以及从其他策略派生回默认策略。

## 3. 目标范围与候选资格

### 3.1 `ToService`

`ToService(serviceName)` 的候选范围由当前调用方 Node 统一组成：

- 当前 Node 内同名本地 Service，包括没有发布到服务发现的私有 Service；
- 当前不可变服务发现快照中的同名远端 Service。

本地实例不享受优先级，也不作为远端失败时的隐式回退。它按 NodeID 插入相同的稳定顺序，
与远端实例一起参与所选策略。

如果业务必须调用本地实例，应使用当前 NodeID 的 `OnNode` 或 `ToServiceOnNode`；如果必须
调用特定远端实例，同样使用精确目标。M19 不引入“优先本地”这种隐藏策略。

### 3.2 自动候选过滤

一个实例只有同时满足以下条件，才能进入 `ToService` 自动候选：

1. ServiceName 完全相同；
2. ContractID 和完整 ContractFingerprint 与生成客户端一致；
3. 本地 Service 已 Ready，或远端实例已经通过 `allow_discovery` 进入当前快照；
4. Service 当前为 Running；
5. 没有进入 Failed、Stopping、Stopped 或 Lost；
6. 实例声明的 Transport 与调用方 Node 的 RPC Transport 一致；
7. 选择瞬间 Transport 已知连通。

完整 ContractFingerprint 已经覆盖契约的全部方法集合，因此不再为“实现当前方法”增加
一项重复扫描。第 7 条的具体含义：

- 本地实例：本地 Runtime 与目标 Dispatcher 可提交；
- TCP：目标 Node 当前存在与发现 SessionID 匹配、已经完成握手的活动会话；
- NATS：当前 Node 的共享 NATS Connection 为 Connected；
- 没有配置相应 Transport、正在拨号、握手、Disconnected、Reconnecting、Recovering、
  Draining 或 Closed：不进入自动候选。

大多数断线代表真实故障，因此 M19 不把“已经发现但当前断开”的实例继续交给自动路由。
连接恢复后，不需要新的业务配置；下一次调用读取当前连接状态即可重新纳入候选。

调用类型保持 M17 已确认的等待边界：

- `AwaitXxx` 如果存在同名、契约匹配、Running、Transport 兼容的实例，只是当前全部
  Disconnected，可以在调用 Context 内无轮询等待首个 Connected 候选；
- 等待期间没有选择任何断开实例；连接事件唤醒后重新读取最新快照，只有真正 Connected
  的实例才进入唯一一次选择；
- `AsyncXxx` 与 `NotifyXxx` 不建立隐藏等待，当前没有 Connected 候选时立即返回
  `CodeTransportUnavailable`；
- 没有同名实例、契约不匹配或只有 Retired 时不等待连接，按对应路由错误立即返回；
- 选择成功后的断线属于提交竞态，不再等待、不改选。

发现快照与连接状态无法形成跨系统事务。选择完成后仍可能立即断线，所以提交阶段必须复核
选中 Session/Connection；复核失败立即返回 Transport 错误，不重新选择。

### 3.3 Retired 的精确边界

M14～M16 已确认 Retired 仍然：

- 保留在服务发现查询和事件中；
- 可以接收普通入站 RPC；
- 可以被 `ToServiceOnNode` 精确调用；
- 可以主动返回业务 `CodeServiceRetired`；
- 不等同于 Stopping 或 Stopped。

M19 只改变自动选择行为：`ToService` 默认排除 Retired。这样业务把实例切换为 Retired
即可停止获得新的自动流量，同时仍能通过精确目标完成管理、迁移、排空、恢复或诊断调用。

如果自定义 Selector 需要选择 Retired，业务应先使用发现查询取得 NodeID，再显式构造
`ToServiceOnNode`；M19 不提供绕过自动摘流的 Selector 开关。

### 3.4 稳定身份与顺序

候选的公开稳定身份是：

```text
NodeID + ServiceName
```

同一组候选按 NodeID 升序排列。ServiceName 在一个候选组内相同，本地实例也按自身 NodeID
插入该顺序。

内部准备结果还固定所选 SessionID 和 Transport generation，用于阻止“选择旧会话、发送到
同 NodeID 新进程”的竞态。SessionID、地址、连接对象和 generation 不进入业务 API。

## 4. 选择前准备与请求提交

### 4.1 `Prepare -> Encode -> Submit`

现有生成代码先分配并编码，再由 Runtime 解析目标。该顺序不能同时满足自动路由和精确
headroom：自动目标在编码前还不知道是本地、TCP 还是 NATS。

M19 把每个生成方法调整为：

```text
Prepare(kind, method)
  -> 从一个不可变发现快照读取候选
  -> 读取当前 Transport 连通状态
  -> Await 且仅缺连接时，在 Context 内等待连接事件后重读
  -> 只选择一次
  -> 返回带精确目标的 rpc.Client 值副本
  -> AllocateRequest(size, kind)
  -> Encode
  -> Await / Async / Notify
  -> Submit 时复核所选身份和连接
```

生成代码使用三个含义明确、只服务生成 ABI 的底层入口：

```go
func (c Client) PrepareAwait(
    ctx context.Context,
    methodID MethodID,
) (Client, error)

func (c Client) PrepareAsync(
    ctx context.Context,
    methodID MethodID,
) (Client, error)

func (c Client) PrepareNotify(
    ctx context.Context,
    methodID MethodID,
) (Client, error)
```

三个入口共用一个 Runtime 内部实现，不复制候选扫描与策略代码。只有 `PrepareAwait` 可以
进入连接等待慢路径；`PrepareAsync` 和 `PrepareNotify` 始终快速返回。它们返回值客户端，
不修改可长期保存的基础客户端。准备结果只包含本次调用所需的小型值字段；生成代码不会把
它保存到请求完成之后，也不创建公开 Future 对象。

选择失败、非法 Key、Selector 失败、契约不匹配或没有候选时，在请求 Buffer 分配和参数编码
之前返回。TCP/NATS 只为最终选中的 Transport 预留准确 headroom，本地调用不承担网络头部。

等待连接是“选择前等待可发送条件”，不是选择失败后的重试。实现复用 Service Await 与
RPC Runtime 的连接状态通知，不使用轮询、Go 默认 Timer、每目标等待 goroutine 或重连
发送缓冲。已经 Connected 的成功热路径不创建等待对象。

### 4.2 一次选择

一次调用最多执行一次路由策略。以下情况都不自动重选或重试：

- 准备后候选进入 Retired；
- 发现快照替换；
- TCP Session 替换或断开；
- NATS 在提交前断开；
- 发送队列过载；
- 请求已发送但响应前断线；
- Notify 提交失败。

这避免非幂等业务执行两次，也让稳定 Key 和自定义 Selector 的结果具有清晰含义。

RoundRobin 在成功选出候选时推进计数，即使随后提交失败也不回滚。并发回滚既昂贵，也不能
证明失败请求没有被目标端接收。

## 5. 内置策略

### 5.1 RoundRobin

RoundRobin 状态属于 Node 的 RPC Runtime，而不是某个临时客户端。分组键为：

```text
ServiceName + ContractID + ContractFingerprint
```

Runtime 对首次出现且确实存在合法候选的组惰性建立原子计数器。无效的任意 ServiceName
不会创建状态，因此业务错误输入不能无限扩张计数表。

选择规则：

```text
index = atomicCounter.Add(1)-1 mod candidateCount
```

计数溢出按无符号整数自然回绕。不同 Node 各自维护独立状态；临时构造客户端不会重新从
第一个实例开始。

### 5.2 Random

Random 使用每 Node RPC Runtime 的低竞争伪随机状态，只在当前候选数量内选择。它不使用
密码学随机源、进程全局带锁随机数，也不增加 seed 配置。

### 5.3 稳定 Key

`Route(key)` 支持：

- `string`；
- `[]byte`；
- `int`、`int8`、`int16`、`int32`、`int64`；
- `uint`、`uint8`、`uint16`、`uint32`、`uint64`。

`uintptr` 不支持，因为其位宽依赖目标平台，不能作为跨 Windows/Linux/macOS 的稳定路由
Key。自定义命名整数类型在调用点显式转换为对应基础类型，例如
`Route(uint64(playerID))`。Go 的 `any` 类型分支不能直接匹配任意命名类型；为了支持该
语法引入反射会与零分配、低延迟和实现简洁目标冲突，因此 M19 不做隐式底层类型识别。

字符串和字节使用 FNV-1a 64 位哈希。整数按数值确定性归一化到 `uint64`，不依赖本机
Map hash、指针地址或随机 seed。选择时：

```text
index = normalizedKey mod candidateCount
```

`Route` 调用完成类型识别和归一化，客户端只保存结果。非法类型不 panic，派生客户端记录
`CodeRPCInvalidRouteKey`，后续方法在 Prepare 阶段立即返回。

简单取模不保证候选变化时最小迁移。有稳定所有权要求的业务使用 `OnNode`、
`ToServiceOnNode` 或未来单独设计的一致性哈希，不把简单取模误当作所有权协议。

连接断开会把实例移出候选，因此 Key 可能暂时映射到其他 Connected 实例；连接恢复后又按
恢复后的稳定候选重新计算。这是可用性路由，不是粘性会话或分片所有权承诺。

## 6. 自定义 Selector

### 6.1 最小接口

```go
type RouteSelector interface {
    Select(RouteCandidates) (index int, ok bool)
}
```

不提供字符串全局注册表、每次调用 Options、任意目标返回值或 Provider 路由插件。Selector
只能从 Runtime 已筛好的候选中返回下标，不能绕过契约、Running、发现和连接过滤。

### 6.2 只读候选

```go
type RouteCandidates struct {
    // 字段不导出。
}

func (c RouteCandidates) Len() int
func (c RouteCandidates) NodeID(index int) string
func (c RouteCandidates) ServiceName(index int) string
func (c RouteCandidates) State(index int) discovery.State
func (c RouteCandidates) Label(index int, name string) (string, bool)
```

M19 默认 `ToService` 自动候选排除 Retired，因此其 State 恒为 Running；M20 的
`IncludeRetired()` 派生以及精确 `ToServiceOnNode` 的单候选可以是 Running 或 Retired。
保留 State 访问既兑现统一只读视图，也让业务在已显式扩展范围时区分状态。Selector 仍
不能自行把范围外的 Retired 重新加入候选。

`RouteCandidates` 不复制候选切片或标签 Map，不允许修改，也不能在 `Select` 返回后保存。
越界访问返回零值和 `false`，不 panic。

### 6.3 执行约束

Selector 必须同步、快速、无阻塞并可安全并发调用。它不得：

- 调用 RPC、Await、数据库、Redis、文件或网络；
- 修改或长期保存候选视图；
- 启动业务 goroutine；
- 依赖调用顺序形成没有同步保护的可变状态。

`ok=false` 返回 `CodeRPCNoRoute`。Selector 为 nil、返回越界下标或 panic 时返回
`CodeRPCRouteSelectorFailed` 并记录诊断。panic 恢复只进入自定义策略冷分支；RoundRobin、
Random 和 Key 路由不经过接口分派或 panic 恢复。

自定义 Selector 应在 Service 启动时创建并绑定，避免每次 RPC 构造闭包：

```go
s.playerRPC = playerapi.
    BindPlayerRPC(s).
    RouteBy(s.zoneSelector)
```

## 7. 精确目标

`OnNode(nodeID)` 和 `ToServiceOnNode(nodeID, serviceName)` 的目标范围最多一个实例，永远
不会因为路由策略扩大到其他 Node。`OnNode` 只是复用客户端已经绑定的 ServiceName；
模板改名客户端先用 `BindXxxRPCTo` 绑定实际名称，再使用相同的 `OnNode`。

- RoundRobin、Random 和 Key 对单候选自然选择该实例；
- 自定义 Selector 收到零个或一个候选，可以接受或拒绝；
- 精确目标允许 Running 或 Retired；
- 精确远端目标仍要求发现可见、契约匹配、Transport 兼容；
- 精确目标当前断开时不作为 Selector 候选，也绝不改选其他 Node。

精确调用保留 M13～M17 的连接恢复语义。客户端构造不拨号、不等待；`AwaitXxx` 在目标
已经发现、契约匹配而只缺连接时，可以在调用 Context 内等待该精确目标 Connected。
`AsyncXxx`、`NotifyXxx` 仍快速失败，且任何模式都不切换到其他 Node。

## 8. Runtime 与 Discovery 内部接缝

M19 不修改 M17 已公开的第三方 Provider SPI。Origin、etcd、未来 Consul 都只负责提交完整
权威快照，不理解 RoundRobin、Key、Selector 或 TCP/NATS 连接。

RPC Runtime 与当前 Node 的 Discovery Runtime 增加一个内部只读候选视图接缝：

- 按 ServiceName 读取已经稳定排序的不可变快照；
- 暴露契约、SessionID、Transport、标签和发现状态的只读访问；
- 不复制候选切片、标签或地址；
- 不把 `internal/discovery` 类型泄漏到公开 `rpc` API；
- 继续保留精确 `ResolveRemote`，供 `ToServiceOnNode` 和提交复核使用。

该接缝返回一次原子读取取得的不可变快照视图；一次 Prepare 的全部远端扫描始终读取同一
快照，不能对每个候选重新读取当前目录。视图按索引返回标量和不可变标签 Map 引用，不在
热路径复制 Slice、Instance 或标签。第三方 Provider SPI 不接触该内部视图。

RPC Runtime 负责合并本地候选、过滤契约与 Running 状态、检查 Transport 连通性和执行
策略。Discovery Runtime 不保存轮询计数、随机状态或业务 Selector。

远端候选的 `Label` 读取不可变发现快照；本地候选读取当前 Node 冻结后的标签视图。两者
都只读且不为一次选择复制 Map。

TCP 选择热路径使用每个 remote target 的原子活动 Session 视图；写侧仍可保留互斥锁完成
会话替换和关闭。NATS 使用当前 active generation 与 Connection 原子状态。不得为了检查
候选而拨号、等待重连或逐候选创建临时对象。

## 9. 错误语义

准备和提交阶段按以下语义返回已有错误：

| 场景 | 错误 |
|---|---|
| Target、Context 或生成客户端无效 | `CodeInvalidArgument` |
| 路由 Key 类型不支持 | `CodeRPCInvalidRouteKey` |
| Selector 为 nil、越界或 panic | `CodeRPCRouteSelectorFailed` |
| 没有同名本地或远端实例 | `CodeRPCNoRoute` |
| 有同名实例，但没有匹配契约 | `CodeRPCContractMismatch` |
| 有匹配契约实例，但全部 Retired | `CodeRPCNoRoute` |
| 有匹配 Running 实例，但没有兼容 Transport | `CodeTransportUnavailable` |
| 兼容实例当前全部断开 | Await 在 Context 内等待；Async/Notify 返回 `CodeTransportUnavailable` |
| Selector 主动拒绝全部候选 | `CodeRPCNoRoute` |
| 选中后发现身份消失或 Session 已替换 | `CodeRPCNoRoute` |
| 选中后连接断开、队列不可用或发送失败 | 对应 Transport/Overloaded 错误 |

错误分类需要在候选扫描中记录“同名、契约匹配、Running、Transport 兼容、Connected”五个
阶段的存在位，不通过复制候选或第二次扫描推断。

## 10. 生成 ABI

`Prepare -> Encode -> Submit` 改变生成代码与 `rpc.Client` 的协作协议，因此 M19 把
`rpc.GeneratedABIVersion` 和生成器双向编译期常量从 `1` 提升到 `2`。

`origingen -check` 必须明确报告旧生成物需要重新生成，不维护两套隐藏调用流程。业务 RPC
接口、生成方法名称、参数、返回值以及 M11 已冻结的 payload 编码布局不变；M19 不改变
TCP/NATS Wire。

## 11. 性能与内存门禁

M19 的性能要求是验收门禁：

1. `BindXxxRPC`、`BindXxxRPCTo`、`OnNode`、`RouteRoundRobin`、`RouteRandom` 和合法
   基础类型 `Route` 返回值不产生堆分配；
2. RoundRobin、Random、整数 Key、字符串 Key 的准备成功热路径达到 `0 allocs/op`；
3. 本地、TCP、NATS 候选选择不复制候选切片、标签 Map、ServiceName 或地址；
4. `rpc.Client.Prepare` 返回小型值副本，不返回每次调用新建的指针对象；
5. 内置策略不使用闭包、反射、`fmt`、临时 `[]byte` 或 Selector 接口；
6. `Route(any)` 对全部支持的精确基础类型执行编译器逃逸分析与 Benchmark；如果 `any`
   装箱导致稳定堆分配，实施必须增加无分配快速入口或调整内部签名，不能接受该回归；
7. 自定义 Selector 只允许业务自身造成的分配，框架包装与候选视图不额外分配；
8. 100、1000 和 8192 Node 候选下分别记录延迟、分配和锁竞争；
9. TCP 连接状态读取不逐候选争用连接生命周期互斥锁；
10. 路由状态只为成功出现过合法候选的组惰性创建，错误 ServiceName 不增长 Runtime Map。
11. Await 缺连接慢路径不轮询、不创建每目标 goroutine，取消后等待项和引用全部释放。
12. TCP 目标表与 Listener 连接上限从历史 4096 同步为 Provider 的 8192 Node 上限；本地
    候选不占远端连接额度。

性能优化不得改变一次选择、无重试、Session 固定和错误语义。

## 12. 测试与验收

M19 至少覆盖：

1. `ToService` 默认 RoundRobin；
2. `BindXxxRPC` 使用生成的默认 ServiceName，`BindXxxRPCTo` 与显式 `ToService` 规范
   构造完全等价；
3. 长期保存客户端和临时构造客户端共享 Runtime 轮询状态；
4. 本地公开、私有及远端实例进入统一稳定顺序；
5. 自动路由排除 Retired，精确调用 Retired 仍可提交；
6. Retired 恢复 Running 后重新进入下一次调用的候选；
7. TCP 未连接、握手中、断开和重连后候选变化，Await 可取消等待首个 Connected；
8. NATS Disconnected/Reconnecting 时不进入候选，Await 等待全局恢复，Async/Notify
   快速失败；
9. 选择与提交之间断线只返回一次错误，不改选；
10. 选择与提交之间 SessionID 替换不把请求发给新进程；
11. 相同 Key 和相同候选快照稳定选择相同实例；
12. 候选增减后 Key 对新数量重新取模；
13. 所有受支持的基础整数、string、`[]byte`，以及命名整数显式转换后的结果；
14. 非法 Key 不 panic 且不分配请求 Buffer；
15. Selector 读取 NodeID、ServiceName、State 和 Label；
16. Selector `ok=false`、nil、越界和 panic；
17. `OnNode` 与 `ToServiceOnNode` 应用任意策略后不扩大范围；
18. 无同名、契约不匹配、Retired、Transport 不兼容，以及 Async/Notify 全部断开的错误
    区分；
19. Prepare 前后快照高频替换与连接高频抖动的 Race 测试；
20. 本地、真实双 Node TCP、真实三 Node NATS 端到端调用；
21. 默认名称派生、模板改名的 `BindXxxRPCTo`、`OnNode` 保留已绑定 ServiceName，以及
    `origingen -check` 对旧 ABI 和新生成物的诊断；
22. RoundRobin、Random、Key、自定义 Selector 和 Prepare 的 Benchmark；
23. 编译器 `-gcflags=-m` 逃逸基线；
24. Windows/Linux 原生测试、Race、Vet 和 macOS 交叉构建；
25. Await 等待连接不选择断开实例、不轮询、不泄漏，连接恢复后只选择一次。

## 13. 最终 Review 结论

1. M19 只完成单目标路由，Broadcast 后置；
2. 路由方法位于生成强类型客户端，不扩张 `rpc.Target` 策略 API；
3. 增加无名称参数的 `BindXxxRPC(owner)` 默认入口和显式覆盖
   `BindXxxRPCTo(owner, serviceName)`；
4. 生成客户端推荐以 `playerRPC` 这类字段名长期复用，`OnNode` 与策略派生保持值语义；
5. `ToService` 合并本地与远端候选，本地不优先；
6. 本地私有 Service 保留为当前 Node 候选；
7. 自动候选只接受 Running，排除 Retired；
8. 精确目标仍允许 Retired，服务端不自动拒绝；
9. 已知断开的 TCP/NATS 实例不进入自动候选；
10. 连接恢复后实例自动重新进入后续候选；
11. Await 只在选择前等待可发送连接，Async/Notify 快速失败；
12. 选择发生在编码前，一次调用只选择一次；
13. 选中身份固定到 NodeID、ServiceName、SessionID 和 Transport generation；
14. 默认 RoundRobin，另有 Random、稳定 Key 和最小自定义 Selector；
15. 候选按 NodeID 稳定排序；
16. 不自动重选、重试或重发 Notify；
17. M17 Provider SPI 不因 M19 改动；
18. 提升生成 ABI，但不改变业务方法签名、payload 或 TCP/NATS Wire；
19. 内置成功热路径、值客户端派生和候选读取以零堆分配作为硬门禁；
20. 命名整数显式转换，避免为了语法糖引入反射；`uintptr` 不作为跨平台稳定 Key；
21. TCP 目标和 Listener 上限与 8192 Node 固定容量一致。

## 14. 实现与验收结果

M19 已按本文外观完成实现。最终收尾额外修正了一个连接恢复竞态：自定义 Selector
读取候选后，如果排序更靠前的 Node 恢复连接，后续无分配扫描不能把同一下标解释为另一
个实例。Runtime 因此把 Discovery 快照、本地生命周期、TCP 会话分片视图和 NATS
connection generation 一并固定到本次 Prepare；提交阶段只复核已选身份，不改选。

TCP 活动会话索引使用 64 个不可变分片。单个连接建立或断开只复制所在分片，不在 8192
Node 规模下为每个连接事件复制完整目标表。RPC 成功 Prepare、候选读取、生成客户端绑定
和所有路由值派生保持 `0 allocs/op`。

2026-08-01 最终验收通过：

- 全仓普通测试与全仓 Race；
- `go vet ./...` 与 `origingen rpc --check ./...`；
- Windows 原生执行、Linux amd64 和 macOS amd64 交叉构建；
- 本地、真实多 Node TCP、真实三 Node NATS 集成场景；
- 100、1000、8192 候选规模，以及所有内置策略和生成绑定 Benchmark；
- 逃逸分析、占位扫描、`git diff --check` 和工作区范围复核。

当前 `service.State` 尚未提供本地 `Retired` 状态，也没有公开 `Retire/Resume` API；这部分
仍属于后续生命周期能力。M19 的 Retired 自动摘流与精确调用规则已经对服务发现可表达的
远端状态生效，未来本地退休只需把状态接入同一候选边界，不修改 M19 客户端 API。
