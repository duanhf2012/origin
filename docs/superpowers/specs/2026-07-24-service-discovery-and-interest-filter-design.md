# Origin v3 服务发现与关注筛选设计

## 1. 文档状态与范围

- 状态：本文范围内的方案已确认
- 确认日期：2026-07-24
- 适用版本：Origin v3

本文记录 Origin v3 服务发现可见快照、`allow_discovery` 关注筛选、发现与失去发现事件方面已经确认的设计。

以下独立问题不属于本文范围：

- 静态配置、Origin Discovery、etcd 等 Discovery Provider 的具体接口与一致性实现；
- Service 名称、实例 ID、动态实例和模板 Service 的完整命名规则；
- 单目标 RPC 的负载均衡和定向路由算法；
- TCP 连接池、连接关闭延迟、心跳与重连；
- Node 退休、优雅摘流和健康状态模型；
- 发现监听器的最终公开注册、取消注册 API 外观。

这些问题将在后续逐项讨论，不与本文已经确认的关注筛选和事件语义混合。

## 2. 背景

Origin v2 已经具备以下能力：

- 从静态配置、Origin Discovery 或 etcd 获得 Node 和 Service；
- 通过 Node 下的 `AllowDiscovery` 筛选允许发现的服务；
- TCP 模式下为发现的 Node 创建 RPC Client；
- 通过 `OnDiscoveryService` 和 `OnUnDiscoveryService` 通知业务 Service；
- 把发现事件投递到所属 Service 的事件队列中执行。

v2 的能力需要保留，但实现存在以下问题：

- Node 信息刷新时重复把当前全部 Service 报告为新发现；
- Node 只删除部分 Service 时，可能没有对应的失去发现事件；
- 重复的 etcd `Put` 或完整快照可能产生重复通知；
- Service 后注册监听器时可能错过已经存在的发现结果；
- 一个 Service 只能保存一个发现监听器；
- 服务发现、路由表、连接创建和事件广播集中在 Cluster 中；
- 正则表达式在运行时反复执行，非法表达式错误被忽略；
- 发现事件可能早于 RPC Client 和路由状态更新；
- 服务发现状态容易与 TCP/NATS 连接状态混淆。

v3 保留 v2 的使用习惯，但使用独立快照、差异计算和 Service 调度投递重新实现。

## 3. 设计目标

1. Node 通过配置明确声明允许发现的远端 Service。
2. TCP 模式只为至少包含一个关注 Service 的目标 Node 产生连接需求。
3. NATS 模式使用相同的可见服务快照和路由语义。
4. 支持监听具体 Service 实例的发现与失去发现事件。
5. 新监听器不会错过注册前已经发现的 Service。
6. 重复快照不产生重复事件，部分变化只报告变化部分。
7. 关注规则容易阅读、校验和排查，不在热路径执行正则表达式。
8. 服务发现事件始终在监听方 Service 的调度上下文中运行。

## 4. 核心概念

### 4.1 原始发现目录

Discovery Provider 向当前 Node 提供原始 Node 与 Service 信息。原始目录表示 Provider 当前知道的全部候选对象，不直接暴露给业务 Service，也不直接作为 RPC 路由表。

一个远端 Node 记录至少包含：

- `NodeID`；
- TCP 地址或 Transport 所需端点；
- Node 标签；
- 当前公开的 Service 实例；
- 每个 Service 由生成代码注册的 RPC 契约和方法元数据。

RPC 契约和方法元数据由 Go RPC 接口及 `origin-gen` 自动产生，不要求开发者在配置中重复声明 RPC 函数。

### 4.2 可见服务快照

当前 Node 使用自己的 `allow_discovery` 规则筛选原始目录，得到只读的可见服务快照。

可见服务快照是以下功能的共同事实来源：

- Service 查询；
- RPC 候选路由构建；
- Broadcast 目标 Node 计算；
- TCP 连接需求计算；
- `Discovered` 和 `Lost` 事件差异计算。

业务代码不能修改快照。快照更新完成后才允许投递相应事件。

### 4.3 具体 Service 实例

发现事件观察具体 Service 实例，而不只观察某个 RPC 契约是否存在。

每个可见实例具有稳定的复合身份：

```text
NodeID + ServiceID
```

事件中的 Service 描述至少包含稳定的 `ServiceID` 和面向配置与诊断的 `ServiceName`。Service ID 的默认生成、显式配置和动态实例规则由独立的 Service 注册设计确定。

多个 Service 即使实现相同 RPC 契约，也属于不同实例。Broadcast 仍遵守已经确认的规则：语义上投递给全部匹配 Service，网络上对同一个目标 Node 合并为一次消息。

## 5. Node 配置外观

v3 延续 v2“在 Node 下直接配置本地 Service 和允许发现的 Service”的结构。

示例：

```yaml
nodes:
  - id: gateway-1

    labels:
      region: cn-east
      environment: production

    services:
      - GatewayService

    allow_discovery:
      - services:
          - PlayerService
          - ChatService

        node_labels:
          region:
            - cn-east
            - cn-north
          environment: production
```

其中：

- `services` 表示当前 Node 本地运行的 Service；
- `allow_discovery.services` 表示当前 Node 允许发现的远端 Service；
- `labels` 是当前 Node 对外发布的标签；
- `allow_discovery.node_labels` 用于匹配目标 Node 发布的标签；
- `node_labels` 不会自动引用当前 Node 的同名标签，配置值始终明确表示目标值。

`services` 和 `allow_discovery.services` 中的字符串均匹配公开的 `ServiceName`，不匹配 `ServiceID`，也不直接匹配 Go 实现类型。动态实例如何确定 `ServiceName` 和 `ServiceID`，由独立的 Service 身份设计确定。

配置不使用 `contracts` 或逐个 RPC 函数列表。一个 Service 实现哪些 RPC 接口和方法，由生成代码在注册阶段确定并自动发布。

## 6. 关注规则

### 6.1 显式配置

`allow_discovery` 是 Node 的显式配置。框架不扫描业务调用代码，也不根据运行时创建了哪些 RPC Client 自动改变关注关系。

显式配置带来以下结果：

- 启动时即可校验关注规则；
- TCP 连接关系稳定且可预测；
- 可以解释某个 Node 为什么被发现和连接；
- 没有执行到的条件分支不会改变网络拓扑；
- 只监听服务状态但不发起 RPC 的业务同样能够声明关注关系。

### 6.2 未配置与显式空列表

为兼容 v2，同时允许明确关闭远端业务服务发现，采用以下规则：

- Node 完全没有配置 `allow_discovery` 字段：发现 Provider 范围内的全部公开 Service；
- Node 显式配置 `allow_discovery: []`：不发现任何远端业务 Service；
- Node 配置一个或多个规则：只发现至少匹配其中一条规则的远端业务 Service。

示例：

```yaml
nodes:
  - id: compatible-node
    services:
      - CompatibleService
```

`compatible-node` 没有 `allow_discovery` 字段，因此保持 v2 的默认行为，发现 Provider 范围内全部公开 Service。

```yaml
nodes:
  - id: isolated-node
    services:
      - IsolatedService
    allow_discovery: []
```

`isolated-node` 不发现任何远端业务 Service，也不会因业务服务匹配而产生 TCP Node 连接需求。本地 Service 注册、查询和本地调用不受影响。

`allow_discovery` 只控制业务 Service 的可见性和由此产生的业务 RPC 连接需求，不得切断 Discovery Provider 自身所需的控制面连接，例如 etcd Client、NATS Client 或 Origin Discovery Master 连接。

YAML 中写出字段但不给值会解析为 `null`，容易与“未配置”和“显式空列表”混淆，因此以下配置属于错误：

```yaml
allow_discovery:
```

需要发现全部时省略该字段；需要关闭远端业务服务发现时明确写成 `allow_discovery: []`。

### 6.3 匹配组合

一条非空规则允许只配置部分筛选字段。缺少的字段表示“不限制该维度”：

- 只配置 `services`：匹配任意 Node 上名称符合条件的 Service；
- 只配置 `node_labels`：匹配标签符合条件的 Node 上全部公开 Service；
- 同时配置 `services` 和 `node_labels`：Service 名称和目标 Node 标签必须同时匹配。

示例：

```yaml
allow_discovery:
  - services:
      - PlayerService
```

该规则允许发现 Provider 范围内任意 Node 上的 `PlayerService`。

```yaml
allow_discovery:
  - node_labels:
      region: cn-east
```

该规则允许发现 `region` 为 `cn-east` 的目标 Node 上全部公开 Service。

空规则没有明确筛选意图，容易因漏写字段意外形成全连接，因此以下配置属于错误：

```yaml
allow_discovery:
  - {}
```

需要发现全部公开 Service 时，应直接省略整个 `allow_discovery` 字段。

匹配语义统一规定为：

- `services` 中的多个值使用 `OR`；
- 同一个 Node 标签的多个允许值使用 `OR`；
- 不同 Node 标签之间使用 `AND`；
- 多条 `allow_discovery` 规则之间使用 `OR`。

前述配置表达：

```text
Service 是 PlayerService 或 ChatService
并且
目标 Node 的 region 是 cn-east 或 cn-north
并且
目标 Node 的 environment 是 production
```

### 6.4 Node 标签

Node 标签用于表达区域、可用区、环境、集群等通用部署属性，例如：

```yaml
labels:
  region: cn-east
  zone: zone-1
  environment: production
  cluster: game-1
```

标签没有 `region` 等内置业务含义。`node_labels.region: cn-east` 只表示目标 Node 的 `region` 必须等于 `cn-east`，不表示自动匹配当前 Node 所在区域。

第一版使用区分大小写的精确匹配，不支持正则表达式或通配符。目标 Node 缺少规则要求的标签时，该规则不匹配。

### 6.5 标签单值与多值

配置同时接受单值和多值：

```yaml
node_labels:
  region: cn-east
```

```yaml
node_labels:
  region:
    - cn-east
    - cn-north
```

配置加载后统一规范化为：

```go
map[string][]string
```

该转换只发生在启动阶段，不进入服务发现或 RPC 热路径。标签值列表中的重复项在加载时去重；空列表属于配置错误；匹配阶段使用预构建集合，不反复解析配置。

## 7. 筛选与连接关系

### 7.1 TCP

筛选后的可见快照中，只要某个远端 Node 至少存在一个匹配 Service，就向 TCP 连接管理器发布该 Node 的连接需求。

同一个 Node 存在多个匹配 Service 时，仍只产生一份 Node 级连接需求。`rpcClient` 是逻辑代理，不因 Service 或 RPC 方法数量创建独立连接。

某个 Node 的最后一个匹配 Service 消失时，服务发现系统移除对应路由并向连接管理器发布“不再需要该 Node”的结果。连接管理器立即关闭还是延迟关闭，由独立的连接管理设计确定。

### 7.2 NATS

NATS 模式下，当前 Node 维护的是到 NATS 的连接，而不是到每个远端 Node 的直连。`allow_discovery` 仍然决定：

- 哪些 Service 进入可见快照；
- 哪些 Service 进入 RPC 候选路由；
- 哪些 Service 产生发现与失去发现事件；
- Broadcast 语义上包含哪些目标 Service。

因此 TCP 与 NATS 共用同一套业务可见性，但底层连接数量含义不同。

## 8. 发现与失去发现事件

### 8.1 事件对象

公开 API 的最终命名后续确定，事件语义按以下外观描述：

```go
type DiscoveryEvent struct {
    NodeID   string
    Services []ServiceInfo
}

type IDiscoveryListener interface {
    OnDiscovered(event DiscoveryEvent)
    OnLost(event DiscoveryEvent)
}
```

事件按 Node 批量携带变化的 Service，避免同一个 Node 一次变化多个 Service 时创建大量独立调度任务。

### 8.2 差异事件

每次 Provider 快照变化后，服务发现系统对当前可见快照执行差异计算：

- 新 Node 出现：对其中全部匹配 Service 产生 `Discovered`；
- 已有 Node 新增 Service：只对新增的匹配 Service 产生 `Discovered`；
- 已有 Node 删除 Service：只对删除的可见 Service 产生 `Lost`；
- Node 下线、租约过期或从 Provider 消失：对该 Node 先前全部可见 Service 产生 `Lost`；
- Service 因关注规则不再匹配：产生 `Lost`；
- Service 因关注规则开始匹配：产生 `Discovered`；
- 收到内容相同的重复快照：不产生事件。

同一个快照版本中的事件按稳定顺序生成，具体稳定排序键由实例身份确定。业务代码不能依赖 Provider 原始返回顺序。

### 8.3 注册时补发当前快照

监听器注册时默认立即补发当前已经发现的 Service。

“注册监听器”和“取得用于补发的当前快照”必须是原子操作：

1. 监听器加入订阅集合；
2. 在同一个有序状态边界取得当前可见快照；
3. 把快照转换为 `Discovered` 补发事件；
4. 补发期间到达的新变化排在补发事件之后。

该规则保证：

- 不会错过注册前已经发现的 Service；
- 不存在“先查询、再订阅”之间的竞态窗口；
- 补发期间发生的新变化不会丢失；
- 相同状态不会因为补发和增量更新重复通知。

### 8.4 调度上下文

服务发现系统不在 etcd Watch、NATS、TCP 或 TimerEngine goroutine 中直接执行用户监听器。

所有补发和增量事件都作为普通任务进入监听方 Service 的 FIFO Ready 队列，并遵守同一执行槽规则：

- Service 同一时刻仍只有一个任务执行用户状态代码；
- Serial 场景不会产生额外并发访问；
- Cooperative 场景与 RPC 恢复、Timer 等任务按 Ready 队列规则交错；
- 监听器 panic 由 Service 任务边界恢复并记录，不破坏 Discovery Provider。

## 9. 发现状态与连接状态分离

`Discovered` 表示 Service 已进入当前 Node 的可见快照和候选路由，不表示 TCP 已经完成连接，也不表示远端 Service 已经成功处理请求。

`Lost` 表示 Service 已离开可见快照，不等同于一次瞬时 TCP 断线。

以下状态必须分开观察：

- 服务发现：`Discovered`、`Lost`；
- TCP Node 连接：Connected、Disconnected；
- NATS 连接：Connected、Disconnected；
- 后续定义的健康、退休和可路由状态。

服务发现更新必须先替换可查询快照和路由可见性，再把事件提交到 Service 调度队列。监听器执行时查询服务发现状态，应能看到与事件一致的新状态。

## 10. 配置校验与错误

Node 创建网络资源之前完成 `allow_discovery` 静态校验。以下情况属于配置错误：

- Service 名称为空；
- Node 标签键为空；
- Node 标签允许值为空；
- 多值标签配置为空列表；
- 标签值不是字符串或字符串列表；
- 同一 Node 的标签键重复且解析结果冲突；
- `allow_discovery` 显式配置为 `null`；
- 非空 `allow_discovery` 中存在空规则；
- 规则显式配置空的 `services` 列表或空的 `node_labels` 映射；
- 配置使用首版不支持的正则或通配表达方式。

重复的 Service 名称和标签允许值在加载时去重。错误信息必须包含 Node ID、规则序号、字段路径、错误值和修改建议。

## 11. 性能约束

- 配置解析、单值转列表、去重和匹配集合构建只在启动或明确的配置更新阶段执行；
- 热路径不编译或执行正则表达式；
- Provider 的重复快照通过内容比较或版本检查直接消除；
- 差异计算以 `NodeID + ServiceID` 为键，不进行全量字符串反射；
- 可见快照发布后只读，RPC 查询不与 Provider 更新共享长时间写锁；
- 同一 Node 的事件批量投递，避免逐 Service 创建调度任务；
- TCP 连接需求按 Node 去重；
- 实现阶段必须对全量同步、单 Service 增删、Node 下线和监听器补发进行延迟与内存分配基准测试。

## 12. 验收标准

后续实现至少需要验证：

1. Node 可以直接配置本地 `services` 和远端 `allow_discovery.services`；
2. 不配置 `contracts` 或 RPC 方法列表也能自动发布 RPC 能力；
3. 未配置 `allow_discovery` 时发现 Provider 范围内全部公开 Service；
4. 显式 `allow_discovery: []` 时不发现远端业务 Service；
5. `allow_discovery: null` 在创建网络资源前失败；
6. 空列表不会切断 Discovery Provider 自身的控制面连接；
7. 只配置 `services` 时不限制目标 Node 标签；
8. 只配置 `node_labels` 时匹配目标 Node 上全部公开 Service；
9. 空规则、空 `services` 列表和空 `node_labels` 映射在创建网络资源前失败；
10. 单值和多值 `node_labels` 产生相同的内部列表结构；
11. 同标签多值为 OR，不同标签为 AND，多条规则为 OR；
12. 标签使用区分大小写的精确匹配；
13. 空标签值、空标签允许值列表和非法类型在创建网络资源前失败；
14. TCP 只对至少含一个匹配 Service 的远端 Node 产生连接需求；
15. 同一 Node 多个匹配 Service 不产生多份连接需求；
16. NATS 与 TCP 得到相同的可见服务快照；
17. 新 Node、Service 新增、Service 删除和 Node 下线产生正确的差异事件；
18. 重复 Provider 快照不产生重复事件；
19. 监听器注册时收到当前快照补发；
20. 补发和并发增量更新之间不丢失、不重复且顺序稳定；
21. 事件进入监听方 Service 的 FIFO Ready 队列；
22. 事件执行前可查询快照已经更新；
23. 服务发现事件与 TCP/NATS 连接事件互不冒充。

## 13. 已确认结论

Origin v3 服务发现与关注筛选采用：

- 延续 v2 在 Node 下直接配置本地 Service 和允许发现 Service 的结构；
- 使用 `allow_discovery.services` 显式声明关注关系；
- 不根据业务代码或 RPC Client 使用情况自动推导关注关系；
- 未配置 `allow_discovery` 时发现 Provider 范围内全部公开 Service；
- 显式配置 `allow_discovery: []` 时不发现远端业务 Service；
- `allow_discovery` 不切断 Discovery Provider 自身的控制面连接；
- 单条规则缺少 `services` 或 `node_labels` 时，不限制缺少的维度；
- 空规则、空 `services` 列表和空 `node_labels` 映射属于配置错误；
- 配置不声明 RPC 契约和具体函数，RPC 能力由 Go 接口和生成代码自动发布；
- 发现监听对象是具体 Service 实例；
- 新监听器默认原子补发当前可见快照；
- 通过新旧可见快照 Diff 产生 `Discovered` 和 `Lost`；
- 事件按 Node 批量进入监听方 Service 的调度队列；
- Node 标签使用显式目标值，不自动匹配当前 Node 的同名标签；
- 标签支持单值和多值，内部统一规范化为字符串列表；
- 同标签多值使用 OR，不同标签使用 AND，多条规则使用 OR；
- 第一版只支持区分大小写的精确标签匹配，不支持正则和通配符；
- TCP 连接需求按目标 Node 去重；
- TCP 与 NATS 共用同一可见服务语义；
- 服务发现事件与 Transport 连接事件保持独立。

## 14. 后续讨论顺序

在本文结论基础上，后续按以下顺序继续设计：

1. ServiceName、ServiceID、动态实例和模板 Service 的身份模型；
2. 监听器注册、取消注册、多监听器和生命周期 API；
3. Discovery Provider 抽象，以及静态配置、Origin Discovery、etcd 的首版支持范围；
4. 全量快照、增量更新、版本号和 Provider 重连的一致性规则；
5. Node 退休、健康状态与可路由状态；
6. 单目标 RPC 的实例选择和定向路由；
7. TCP 连接建立、关闭延迟、心跳与重连策略。
