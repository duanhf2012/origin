# Origin 第三版 M20 多节点 Broadcast 与部分失败设计

> 文档状态：已按确认方案落档，待最终书面复核
>
> 确认日期：2026-08-01
>
> 前置里程碑：M11、M13、M14、M15、M16、M17、M18、M19

## 1. 里程碑目标

M20 把 M11 已生成并在当前 Node 本地范围工作的 `BroadcastXxx`，扩展为基于 M14/M17/M18
不可变发现快照、M13 TCP、M15 NATS 和 M19 路由视图的完整多 Node 通知广播。

M20 完成：

1. `BroadcastXxx` 向客户端 Target 基础范围内的全部目标投递一次通知；
2. 编码前固定一次完整广播计划，连接或快照变化不能改变本次目标身份；
3. 参数只执行一次静态编码，再为各目标建立独立 Buffer 所有权；
4. 本地、TCP 和 NATS 使用一致的目标、Context、提交与部分失败语义；
5. 默认自动范围排除 Retired，并提供单一 `IncludeRetired()` 值派生显式包含；
6. 部分成功和多目标全部失败返回可按 Code 判断、可读取逐目标原因的聚合错误；
7. 对目标数和一次广播的总放大容量设置固定边界；
8. 成功路径不创建逐目标公开结果、goroutine 或逐目标 Go 堆对象。

M20 不实现多响应收集、逐 Node 远端 ACK、自动重试、标签/区服广播 API、广播组、消息
持久化、离线补发、顺序广播、流式 RPC、压缩或新的 Wire 类型。这些能力不能复用通知广播
的“只确认本地提交”语义，需要真实业务需求后独立设计。

## 2. 最终业务外观

### 2.1 标准广播

生成方法签名保持不变：

```go
err := s.playerRPC.BroadcastPlayerOnline(
    ctx,
    playerID,
)
```

`BroadcastPlayerOnline` 返回 `nil` 只表示本次计划中的每个目标都在本地提交阶段被对应
Transport 接受，不表示远端业务已经开始、完成或成功。

带返回值的 RPC 方法仍生成 Broadcast，但主动放弃全部返回值和远端业务错误：

```go
err := s.playerRPC.BroadcastGetPlayer(
    ctx,
    playerID,
)
```

需要确认单个目标执行结果时使用 `AwaitXxx` 或 `AsyncXxx`；需要收集多个目标响应时由后续
独立系统提供，不能改变 `BroadcastXxx` 的通知语义。

### 2.2 显式包含 Retired

所有自动范围默认只接受 Running。业务明确需要让退休实例继续参与自动单目标选择或广播
时，从长期保存的基础客户端派生一个值：

```go
maintenanceRPC := s.playerRPC.IncludeRetired()

err := maintenanceRPC.BroadcastPlayerOnline(
    ctx,
    playerID,
)
```

同一个派生也适用于单目标调用：

```go
player, err := s.playerRPC.
    IncludeRetired().
    AwaitGetPlayer(ctx, playerID)
```

`IncludeRetired()` 的固定规则：

- 只把 Retired 加入原有 Running 自动范围，不提供“仅 Retired”筛选；
- 返回新的轻量客户端值，不修改基础客户端；
- `BindXxxRPC`、`BindXxxRPCTo` 默认不包含 Retired；
- `RouteRoundRobin`、`RouteRandom`、`Route`、`RouteBy` 和 `OnNode` 保留该标志；
- 重复调用幂等；
- `OnNode` 已经是显式精确目标，本来就允许 Retired，因此该标志对精确范围没有额外效果；
- 未来本地 `Retire/Resume` 落地后直接复用同一规则，不新增第二套接口。

生成客户端增加：

```go
func (client PlayerRPCClient) IncludeRetired() PlayerRPCClient
```

底层只增加一个对应值方法：

```go
func (client rpc.Client) IncludeRetired() rpc.Client
```

不增加 Provider 选项、全局开关、Service 配置或 Dispatcher 判断。

### 2.3 精确 Node

`OnNode` 只广播到已绑定 ServiceName 在指定 Node 上的一个实例：

```go
err := s.playerRPC.
    OnNode("player-2").
    BroadcastPlayerOnline(ctx, playerID)
```

该范围最多一个目标，与 `NotifyXxx` 使用相同错误和提交语义。精确目标允许 Running 或
Retired，但仍必须可见、契约匹配且 Transport 可用。

## 3. 目标集合

### 3.1 自动范围

标准 `ToService("PlayerService")` 从同一次不可变视图取得目标，依次应用：

1. 本地端点和远端发现实例的 ServiceName 必须匹配；
2. 必须支持生成期固定的 ContractID、完整 Fingerprint 和 MethodID；
3. 远端实例必须位于当前关注规则形成的可见快照；
4. 默认只接受 Running；调用 `IncludeRetired()` 后接受 Running 和 Retired；
5. Lost、Unknown、Starting、Failed、Stopping 和 Stopped 不进入目标集合；
6. 同一 `NodeID + ServiceName` 最多一个目标，并按 NodeID 稳定排序；
7. 当前 Node 的同名本地私有 Service 可以参与；远端私有 Service 不在发现快照中，不能
   被广播；
8. 单目标路由策略不缩小广播范围。

Retired 被默认排除不算投递失败；这是调用方选择的目标范围。显式 `IncludeRetired()` 后，
Retired 与 Running 使用相同连接、提交和错误规则，服务端仍不因 Retired 自动拒绝入站。

### 3.2 已知断开实例

Broadcast 与 M19 单目标自动选择的目标不同：单目标选择可以在多个实例中挑一个健康目标，
而 Broadcast 的承诺是覆盖全部符合范围的实例。因此，契约和生命周期合法但当前已知断开的
实例仍属于本次广播意图：

- 不等待它恢复；
- 不改选其他实例代替它；
- 不静默从结果中删除；
- 立即记录 `ErrTransportUnavailable`；
- 其他目标仍继续投递；
- 只要至少一个其他目标成功，就返回部分失败聚合错误。

连接在 Prepare 后恢复只影响下一次调用。连接在 Prepare 后断开，则提交阶段把已经固定的
目标记录为失败，也不重新扫描或重试。

### 3.3 契约和方法

同名但 ContractID、Fingerprint 或 MethodID 不匹配的实例不是合法广播目标。没有任何契约
匹配实例时返回 `ErrRPCContractMismatch`；存在合法实例时，只向合法实例投递，不让错误
部署扩大为向未知方法发送消息。

## 4. 编码前广播计划

生成代码把原来的 `Encode -> Broadcast` 改为：

```text
PrepareBroadcast -> Encode Once -> Submit Fan-out
```

公开业务方法不变。生成代码内部先调用：

```go
preparedClient, err := client.client.PrepareBroadcast(ctx, methodID)
```

成功后把 `preparedClient` 交给原有静态编码函数，最后调用其 `Broadcast`。rpcgen ABI 从 2
提升到 3，旧生成物由 `origingen rpc --check` 明确诊断并要求重新生成。

一次 `PrepareBroadcast` 固定：

- 一份 RemoteSnapshot；
- 本地 endpoint 和当时的本地生命周期；
- TCP 64 分片 target/session 视图；
- NATS connection/generation 视图；
- ServiceName、ContractID、Fingerprint、MethodID 和 Retired 范围标志；
- 稳定目标数量、可发送数量和已知不可用目标；
- Context 开始状态。

Prepare 先检查 Context，再分类目标。没有合法目标时返回无路由或契约错误；只有一个合法
目标且已知不可用时直接返回该 Transport 错误；多个合法目标全部已知不可用时直接返回
2011 聚合错误。以上路径均不执行 Sizer、编码或 Buffer 申请。只要至少一个目标可发送，
Prepare 就保留其他已知失败并进入单次编码，随后继续尝试全部可发送目标。

计划不复制发现候选、Label 或每个成功目标。多目标时允许建立一个固定大小的内部
`broadcastPlan`；它只保存不可变视图和扫描状态，提交时对同一视图无分配重扫。只有单个
合法目标且没有其他失败目标时复用 M19 `preparedTarget`，保持 Notify 等价快路径。

计划没有 goroutine、Timer、连接或需要显式归还的池对象。编码失败后由普通 Go 生命周期
回收，不向生成代码暴露 `ReleasePlan` 一类易误用接口。是否把固定 plan 优化为零分配必须
由实现后的逃逸分析和 Benchmark 决定，不能为了预设结论引入危险复用。

## 5. 单次编码与 Buffer 所有权

### 5.1 编码一次

静态 Sizer 和 Writer 只执行一次，形成一个规范业务 payload。RPC Runtime 不为每个目标
重新运行字段遍历、Codec、Protobuf Marshal 或业务自定义编码器。

在取得编码大小后、申请请求 Buffer 前，Runtime 先校验本次广播总放大容量。超过容量时零
投递并返回 `ErrTransportOverloaded`。

### 5.2 每目标独占所有权

现有本地任务、TCP 发送队列和 NATS Publish 都要求明确的消息所有权。M20 不把同一个可释放
Buffer 同时交给多个所有者，也不引入引用计数、分段写或 `unsafe`：

1. 规范 payload 只编码一次；
2. Prepare 按稳定顺序确定最后一个可发送目标为原始 Buffer 消费者；编码器按该目标的
   精确 headroom 申请并写入原始 Buffer；
3. 除该保留目标外，其余可发送目标从 BufferPool 取得带精确 headroom 的 Buffer，并复制
   规范 payload；
4. 按稳定顺序提交这些副本；
5. 最后一个可发送目标消费原始 Buffer；
6. 任何未转移所有权的 Buffer 在当前错误路径立即释放；
7. 每个目标任务或 Transport 按既有 Notify 规则独立释放自己接受的 Buffer；
8. 已知不可用目标不申请 Buffer。

因此 M20 保证“只编码一次”，不承诺“跨多个目标零字节复制”。引用计数 payload 或
`writev` 只有在真实 Profile 证明复制是主要瓶颈且能保持所有权清晰时才允许后续立项。

## 6. 投递顺序、并发与 Context

### 6.1 顺序和并发

发送端按 NodeID 稳定顺序在调用方当前执行栈中执行有界、非阻塞准入：

- 不为每个目标创建 goroutine；
- 不增加广播工作池或全局队列；
- 一个目标失败后继续其他目标；
- 不承诺各 Node 的网络到达顺序或业务执行顺序；
- 不让慢业务阻塞发送端，因为成功边界仍是本地队列或 Transport 接受。

### 6.2 Context

Context 只约束发现、编码和本地提交阶段：

1. Prepare 前已经取消：零目标投递，返回对应 Context 错误；
2. 编码或首次提交前取消：零目标投递并释放请求 Buffer；
3. 扇出中途取消：停止后续投递，已接受目标不可撤回；
4. 尚未尝试的剩余目标以同一取消或超时原因进入失败详情；
5. 已接受目标的远端业务不继承调用方取消，沿用 Notify 的 `WithoutCancel` 控制边界。

单个目标的过载、断线或协议错误不等于整个 Context 取消，必须继续尝试其他目标。

### 6.3 禁止重试

M20 不重试、不重发、不重新选择，也不把失败目标交给其他实例代替。Broadcast 可能调用
非幂等方法，自动重试会产生无法判断的重复通知。

## 7. Transport 一致语义

M20 不增加 Broadcast Wire、Magic、版本、NATS Subject 或 Subscription：

- 本地目标：现有 `CallNotify` Dispatcher 和 Service Ready FIFO 接受即成功；
- TCP：每个目标 Node 复用已固定的 outboundSession，发送现有 Notify 帧；发送队列接受即
  成功；
- NATS：逐 Node 向现有稳定请求 Subject 发布 Notify，NATS 客户端接受 Publish 即成功；
- 所有 Transport 都由目标 Node 根据 ServiceName、ContractID/Fingerprint 和 MethodID 精确
  分发；
- NATS 不建立广播 Subject，不用 Queue Group 猜测服务实例集合；
- M17 Provider SPI、M14 Directory 和业务发现 API 均不增加 Broadcast 方法。

TCP/NATS 成功不表示对端已经收到，进程可能在本地接受后、远端处理前退出。这是 Notify
语义的固有边界。需要逐 Node 收讫确认时应设计带响应的独立协议，不在 M20 偷加 ACK。

## 8. 路由派生关系

生成客户端值派生固定如下：

| 派生 | 对 Broadcast 的影响 |
|---|---|
| `BindXxxRPC` | 使用契约默认 ServiceName，默认排除 Retired |
| `BindXxxRPCTo` | 使用显式 ServiceName，默认排除 Retired |
| `IncludeRetired` | 自动范围增加 Retired |
| `OnNode` | 范围缩小为精确 Node，最多一个目标 |
| `RouteRoundRobin` | 不缩小 Broadcast |
| `RouteRandom` | 不缩小 Broadcast |
| `Route(key)` | 不缩小 Broadcast |
| `RouteBy(selector)` | 不执行 Selector，不缩小 Broadcast |

业务需要标签、区服或任意子集时，继续使用发现快照筛选 NodeID，再逐个通过 `OnNode` 调用
`NotifyXxx`。M20 不增加第二个通用筛选 DSL，也不让 RouteSelector 同时承担单选和多选两种
不兼容职责。

## 9. 错误模型

### 9.1 基础分类

- 没有同名合法范围：`ErrRPCNoRoute`；
- 同名但没有契约匹配实例：`ErrRPCContractMismatch`；
- 单个目标断开或 Transport 不兼容：保留具体 Transport 错误；
- 单个目标队列过载：保留 `ErrTransportOverloaded` 或 Service 过载错误；
- 编码失败：`ErrRPCEncodeFailed`；
- 总放大容量超过上限：`ErrTransportOverloaded`；
- Context 取消或超时：使用既有 Context Code；
- 多目标部分成功：`CodeRPCBroadcastPartialFailed=2010`；
- 多目标全部失败：新增 `CodeRPCBroadcastFailed=2011`。

零目标不构造 BroadcastError。只有一个目标时保持与 Notify 相同的原始错误，避免精确广播
无故改变既有错误判断。

### 9.2 聚合错误

公开最小只读外观：

```go
type BroadcastFailure struct {
    NodeID string
    Err    error
}

type BroadcastError struct {
    // 字段保持私有。
}

func (err *BroadcastError) Total() int
func (err *BroadcastError) Succeeded() int
func (err *BroadcastError) FailureCount() int
func (err *BroadcastError) Failure(index int) (BroadcastFailure, bool)
func (err *BroadcastError) Code() errs.Code
```

一次 Broadcast 只绑定一个 ServiceName，因此逐目标重复保存 ServiceName 没有信息增益；
聚合日志记录一次公共 ServiceName，`BroadcastFailure` 只保留 NodeID 和非 nil 原因。

`BroadcastError` 实现 `error`、`errors.Is` 和单一稳定 Code：

- `0 < Succeeded() < Total()`：2010；
- `Succeeded() == 0 && Total() > 1`：2011。

错误文本只包含目标、成功和失败数量，不展开全部 NodeID 或底层错误。逐目标详情通过
`Failure(index)` 读取并按 NodeID 稳定排序。成功路径不创建该对象；失败路径允许按失败数
分配详情，因为返回后的业务可能保存错误，不能复用内部临时 Slice。

`errors.Is` 除识别聚合哨兵外，也能识别任一失败原因；`errs.CodeOf` 固定返回 2010 或 2011，
不因第一个失败 Node 的排序变化而漂移。

Context 已取消、路由失败、契约失败、Sizer/编码失败或容量超限等“全调用错误”如果发生在
首次逐目标尝试前，直接返回原始稳定错误并保证零投递，不构造 BroadcastError。唯一例外
是 Prepare 已经得到多个合法意图目标且它们全部各自已知不可用，此时结果本身就是逐目标
失败并直接返回 2011。全调用预检成功后，计划中保留的逐目标不可用原因与实际提交失败
一起参与统计；多个意图目标按最终成功数返回 2010/2011。因此 Context 中途取消可以成为
逐目标失败原因，但不会覆盖聚合错误的稳定 Code。

## 10. 容量与配置

### 10.1 目标数

一次 Broadcast 最多 8192 个意图目标，与 M17/M18 Node 容量和 M19 TCP 目标上限一致。
达到 8193 时在编码和投递前返回 `ErrTransportOverloaded`，绝不静默截断，也不只发送前
8192 个。

### 10.2 总放大容量

RPC 冻结配置增加：

```yaml
rpc:
  max_payload_size: 4M
  max_broadcast_size: 64M
```

公共配置语义：

- `max_payload_size` 继续限制单个业务 payload；
- `max_broadcast_size` 限制 `payload_size × 意图目标数`；
- 默认 `64M`；
- 最大允许配置为 `1G`；
- 使用 Origin `ByteSize` 严格字符串，不接受裸整数；
- 使用 `int64` 计算并先检查乘法溢出；
- 超限时任何目标都未提交；
- 不按当前断开数量降低计算结果，避免故障状态意外改变容量准入；
- 本地单目标同样经过单 payload 上限，但不会因为没有远端配置而缺少默认值。

该上限约束一次调用可以放大的业务数据，不等于精确瞬时 RSS；Transport 自身的逐连接发送
队列仍按已有消息数上限独立背压。

## 11. 可观测性

至少提供内部汇总字段：

- ServiceName、MethodID；
- 意图目标数、可发送数、成功数、失败数；
- 本地、TCP、NATS 目标数量；
- Retired 是否显式包含；
- 编码 payload 大小和总放大大小；
- 总 Prepare、编码、复制和提交耗时；
- 各稳定错误码数量。

全成功不逐目标记录日志。部分或全部失败记录一条汇总日志，逐目标详情由返回错误和受控
诊断采样读取，避免 8192 目标同时失败形成日志风暴。日志不得包含 payload、认证信息或
业务参数。

## 12. 性能与内存门禁

M20 热路径固定：

- 目标发现和连接视图各只原子读取一次；
- O(N) 无分配扫描同一不可变视图；
- schema/Codec 只执行一次；
- 不使用反射、`[]any`、逐目标闭包、Timer 或 goroutine；
- 单目标成功快路径复用 M19 prepared target，目标计划不产生额外堆分配；
- 多目标成功路径最多一个固定 plan Go 堆对象，不按目标数分配 Go 对象；
- 每目标消息 Buffer 来自现有 BufferPool，所有权独立；
- 失败详情只在失败路径分配；
- Route 派生和 `IncludeRetired()` 保持 `0 allocs/op`。

实施前后必须保存：

- 1、100、1000、8192 目标的 Prepare 和 fan-out Benchmark；
- 32B、1KB、64KB 和 4M payload 的编码/复制/容量边界；
- `ns/op`、`B/op`、`allocs/op`、吞吐量和 P50/P95/P99；
- 单目标与 M19 Notify 基线对比；
- 多目标全成功、首个失败、随机失败、全部失败和 Context 中断；
- Race、GC 压力、队列积压和 64M/1G 边界；
- 编译器逃逸分析和代表性 CPU/Heap Profile。

4M payload 在默认 64M 限制下最多覆盖 16 个意图目标；更大 fan-out 必须由项目显式提高
上限。不得为了让容量测试通过而绕过或分批隐藏一次广播的总放大。

## 13. 兼容性

M20 保持：

- 业务 RPC 接口和 `BroadcastXxx` 方法签名不变；
- payload 数据布局和 Fingerprint 规则不变；
- TCP `ORP1`、NATS `ORN1` Wire 不变；
- NATS Subject/Subscription 数量不变；
- M17 Provider SPI、M14 Directory 公开 API 和第三方 Consul 替换边界不变；
- Retired 入站准入、精确调用、Timer 和停止语义不变；
- 当前 Node 本地 Broadcast 的真实 Dispatcher 行为不变。

变化只有：

- 标准 Broadcast 从本地最多一个目标扩展为多 Node；
- 自动范围默认排除 Retired；
- 新增显式 `IncludeRetired()` 值派生；
- 多目标失败增加只读聚合详情和 2011 错误码；
- rpcgen ABI 从 2 提升为 3；
- RPC 配置增加 `max_broadcast_size`。

## 14. 测试与验收

### 14.1 公共外观与生成

1. `BindXxxRPC`、`BindXxxRPCTo` 和 `OnNode` 的 Broadcast；
2. `IncludeRetired()` 值语义、幂等和所有派生顺序；
3. 默认排除远端 Retired，显式包含后参与单目标和 Broadcast；
4. 当前本地尚无 Retired API 的边界记录；未来接入时补同一组本地测试；
5. Route 四种策略不缩小 Broadcast；
6. `PrepareBroadcast` 严格位于编码前；
7. ABI 2 旧生成物诊断和 ABI 3 生成幂等。

### 14.2 目标与竞态

1. 本地公开/私有和远端可见目标合并且按 NodeID 排序；
2. 同名不同契约、不同 ServiceName、Lost 和默认 Retired 被排除；
3. 已知断开实例形成失败而不静默消失；
4. Prepare 后快照增删、Retired/Running 变化、TCP session 和 NATS generation 替换；
5. Selector 不为 Broadcast 执行；
6. 目标变化不换人、不重试、不重复投递；
7. 8192 接受、8193 整体拒绝。

### 14.3 错误和所有权

1. 零目标、契约不匹配、单目标断开和过载；
2. 多目标全部已知断开在 Prepare 返回 2011，且 Sizer、编码器和 BufferPool 均未调用；
3. 部分成功 2010、多目标全部失败 2011、单目标保留原错误；
4. Failure 顺序、非 nil 原因、越界访问、`errors.Is/As`、`Code()` 和 `errs.CodeOf`；
5. Context 在 Prepare、编码前和扇出中途取消；
6. 编码失败和 64M/1G 容量边界保证零投递；
7. 每个成功/失败分支的 Buffer 唯一释放和无双重释放；
8. 目标业务 error、返回值和 panic 不回传。

### 14.4 真实集成

1. 当前 Node 本地服务；
2. 三 Node 以上真实 TCP，覆盖断线、队列过载和恢复后的下一次调用；
3. 三 Node 以上真实 NATS，确认逐 Node Publish 且 Subscription 数量不增长；
4. Origin 与 etcd Provider 各形成相同目标结果；
5. 自定义最小 Provider/Consul 映射测试证明 SPI 无变化；
6. Windows、Linux、macOS，普通测试、全仓 Race、Vet、生成检查和跨平台构建。

## 15. 里程碑边界与编号封顶

全局建设里程碑固定封顶到 M22，不允许继续创建 M23：

| 编号 | 主题 | 边界 |
|---|---|---|
| M20 | 多节点 Broadcast 与部分失败 | 本文范围，不收集响应 |
| M21 | 业务运行时扩展收口 | Module 静态树、Service 配置、本地事件、公开 Retire/Resume；使用独立工作包和计划，不再拆新 M 编号 |
| M22 | Origin v3.0 稳定发布 | 兼容审计、文档、观测、性能和发布门禁；不增加新业务子系统 |

M22 后：

- TCP Module、HTTP、WebSocket、KCP、Redis、数据库、MongoDB、Kafka 等使用组件工作包；
- 外部 gRPC、压缩、流式 RPC 等可选能力使用独立提案；
- 组件可以有自己的版本和实施计划，但不得继续占用全局 `Mxx`；
- 新需求先判断属于核心缺陷、M21 收口、M22 发布门禁还是发布后的组件，不通过增加 M23
  回避范围管理。

## 16. 最终确认结论

1. M20 只完成多 Node 通知 Broadcast 和部分失败闭环；
2. 业务 `BroadcastXxx` 签名不变；
3. 默认自动单目标和 Broadcast 都排除 Retired；
4. 增加唯一 `IncludeRetired()` 值派生，显式包含 Running 与 Retired；
5. 精确 `OnNode` 继续允许 Retired；
6. 已知断开实例属于广播意图并产生失败，不静默忽略；
7. 编码前固定不可变计划，连接变化只影响下次调用；
8. Route 策略不缩小 Broadcast，标签子集继续使用发现快照加精确 Notify；
9. 参数只编码一次，每目标 Buffer 所有权独立；
10. 不新增 Wire、Subject、Subscription 或 ACK；
11. 稳定 NodeID 顺序非阻塞投递，不创建逐目标 goroutine；
12. 单个失败后继续，Context 取消后停止剩余，已接受目标不可撤回；
13. 不重试、不改选、不重发；
14. 单目标保留原错误，部分成功使用 2010，多目标全失败新增 2011；
15. BroadcastError 只提供集中、只读的计数和逐目标失败访问；
16. 一次最多 8192 目标，不截断；
17. `max_broadcast_size` 默认 64M、最大 1G，超限时零投递；
18. M17 Provider SPI 和未来 Consul 替换边界不变；
19. 成功仅表示本地提交接受，不表示远端执行；
20. rpcgen ABI 提升到 3；
21. 全局里程碑硬封顶 M22，禁止 M23。
