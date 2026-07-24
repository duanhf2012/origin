# Origin v3 Application 与 Node 生命周期设计

## 1. 文档状态

- 状态：已确认
- 确认日期：2026-07-22
- 适用版本：Origin v3
- 讨论范围：Application、Node 的所有权关系、网络隔离、启动选择、启停顺序及失败处理

本文记录已经确认的设计结论。RPC 契约、服务发现与连接筛选、Service 执行模型将在各自的设计文档中单独讨论，不在本文中提前确定。

## 2. 背景

Origin v2 的运行层级为 Node、Service、Module，一个进程只能启动一个 Node。Node 标识、Cluster、Service 注册表和信号处理等状态由包级全局对象持有，因此无法在同一进程中安全地创建多个彼此独立的 Node。

Origin v3 需要在 Node 之上增加 Application。Application 作为程序启动和停止的管理单元，可以在同一个操作系统进程内承载并管理多个 Node。开发环境可利用这一能力在单个进程中启动多个 Node；生产环境仍以“一个进程运行一个 Node”为主要部署方式。

开发环境的多 Node 运行必须尽量复现生产环境的网络行为和启停时序，避免因为共享连接、内存短路或并行启停而掩盖只会在真实部署中出现的问题。

## 3. 目标

1. 建立清晰的 `Application → Node → Service → Module` 所有权层级。
2. 一个 Application 可以配置多个 Node，并可在启动时选择其中一个或多个 Node。
3. 多个 Node 在同一进程中运行时仍保持身份、服务、网络和生命周期隔离。
4. 各 Node 顺序启动、顺序停止，模拟生产环境逐个启停程序的行为。
5. 启停顺序支持完整配置、部分配置和完全不配置，并且在所有情况下都能得到稳定、可预测的有效顺序。
6. 配置错误、启动失败和停止失败均有明确且可测试的处理规则。

## 4. 非目标

本文不确定以下内容：

- RPC 使用代码生成还是运行时注册；
- 服务发现的关注规则及 TCP 连接筛选策略；
- Service 的单协程、多协程和协作式调度模型；
- Node 内部多个 Service 的具体启停顺序模型。

这些内容与 Application 生命周期存在接口关系，但会在后续设计中分别确认。

## 5. 核心层级与职责

### 5.1 Application

Application 对应一个正在运行的操作系统进程，是程序级生命周期管理器。它负责：

- 加载并校验 Application 和 Node 配置；
- 创建并持有本次选中的 Node；
- 解析有效启动顺序和有效停止顺序；
- 按顺序启动、停止和回滚 Node；
- 接收程序级退出信号，并触发统一停止流程；
- 汇总 Node 启停过程中产生的错误和状态。

Application 不负责：

- 合并多个 Node 的身份；
- 合并多个 Node 的 TCP/NATS 连接状态；
- 直接持有某个 Node 的 Service 或 Module；
- 绕过 Node 的生命周期直接操作其内部对象。

### 5.2 Node

Node 是独立的逻辑运行单元。每个 Node 独立持有：

- 唯一 Node ID；
- Service 注册表和 Service 实例；
- RPC 服务端、RPC 客户端及连接管理状态；
- 服务发现状态和路由状态；
- Node 级配置与生命周期状态。

Node 不再依赖包级全局单例保存运行状态。同一个 Application 中的两个 Node 必须能够独立启动、停止、失败和释放资源。

### 5.3 Service 与 Module

Service 只属于一个 Node，Module 只属于一个 Service 或其 Module 子树。禁止 Service 或 Module 通过包级全局注册表隐式访问其他 Node 的对象。跨 Node 交互统一通过后续确定的 RPC 与路由抽象完成。

## 6. 网络隔离原则

### 6.1 每个 Node 拥有独立网络端点

生产环境主要采用一个进程运行一个 Node。为了让开发环境能够暴露与生产环境相同的问题，即使多个 Node 位于同一个 Application 中，每个 Node 仍应拥有独立的监听地址、连接管理器和连接生命周期。

Application 不共享单一 TCP 监听端点，也不把多个 Node 复用到同一条 Application 级连接中。

### 6.2 同进程 Node 之间不自动短路

默认开发和集成测试模式下，同一 Application 内不同 Node 之间的调用仍经过真实配置的 TCP 或 NATS 传输，不自动替换为内存直接调用。这样可以覆盖：

- 单个 Node 独立断线和重连；
- 连接建立与关闭时序；
- Node 级背压、拥塞和超时；
- Node 级握手、鉴权和发现状态变化；
- 一个 Node 失败而同进程其他 Node 继续运行；
- 多连接之间的竞争和资源上限。

如果未来增加 `inproc` 传输，它必须是显式启用的测试能力，不能替代默认集成测试和生产等价性验证。

## 7. Node 选择与配置顺序

配置中的 `nodes` 声明顺序是所有缺省行为的唯一稳定基准。启停顺序只决定顺序，不决定哪些 Node 会被启动。

启动入口可以指定一个 Node、多个 Node 或全部 Node。Application 先计算完整有效顺序，再按本次选择的 Node 集合进行过滤。未被本次启动的 Node 不参与启动、停止和失败回滚。

示例配置：

```yaml
application:
  nodes:
    - id: node_a
    - id: node_b
    - id: node_c
    - id: node_d

  start_order: [node_c, node_a]
  stop_order: [node_b]
```

## 8. 有效启动顺序

Application 按以下规则生成有效启动顺序：

1. 未配置或配置为空的 `start_order`：完全使用 `nodes` 的声明顺序。
2. `start_order` 只配置部分 Node：先保留已配置 Node 的明确顺序，再把未配置 Node 按 `nodes` 声明顺序追加到末尾。
3. 选择部分 Node 启动时，在完整有效启动顺序上过滤未选中的 Node，剩余 Node 的相对顺序不变。

对于上面的示例，完整有效启动顺序为：

```text
node_c → node_a → node_b → node_d
```

Application 必须顺序启动 Node，不并行启动。前一个 Node 完成启动并进入 `Ready` 状态后，才能启动下一个 Node。

Node 内部先启动 Transport、RPC Runtime 和服务发现等基础设施，再执行业务 Service 的 `OnStart`。Service 可以在 `OnStart` 中等待已经启动的远端 Node，但不能依赖有效启动顺序中尚未开始的后续 Node；否则会等待到启动 Context 结束并导致 Node 启动失败。详细规则见 [Service 启动与就绪设计](./2026-07-24-service-startup-and-readiness-design.md)。

## 9. 有效停止顺序

Application 按以下规则生成有效停止顺序：

1. 未配置或配置为空的 `stop_order`：使用本次实际启动顺序的严格反序。
2. `stop_order` 只配置部分 Node：先保留已配置 Node 的明确顺序，再把未配置 Node 按本次实际启动顺序的反序追加到末尾。
3. 没有在本次运行中成功启动的 Node，从有效停止顺序中忽略。

对于上面的示例，完整有效停止顺序为：

```text
node_b → node_d → node_a → node_c
```

Application 必须顺序停止 Node，不并行停止。前一个 Node 完成停止并进入 `Stopped` 状态后，才停止下一个 Node。

如果前一个 Node 的 Service 在 `OnStop` 中需要调用后一个 Node 的 `DBService` 等依赖服务，`stop_order` 必须保证被依赖 Node 后停止。后一个 Node 在依赖方 `OnStop` 完成前保持可路由；不能先让所有 Node 同时退休，再依赖普通 RPC 完成存档。Service finalizer、Await 和基础设施保活规则见 [Origin v3 Service 优雅停止设计](./2026-07-24-service-graceful-stop-design.md)。

## 10. 生命周期状态

Application 至少应能观察 Node 的以下状态：

- `Created`：对象已经创建，尚未开始启动；
- `Starting`：正在初始化和启动；
- `Ready`：启动完成，可以提供服务；
- `Stopping`：正在停止并释放资源；
- `Stopped`：停止和资源释放完成；
- `Failed`：启动或运行过程失败。

状态转换必须由 Node 自身管理，Application 只发出生命周期命令并观察结果，不能直接修改 Node 内部状态。

## 11. 配置校验

Application 在创建任何 Node 之前完成全部静态校验。以下情况属于配置错误，Application 必须拒绝启动：

- `nodes` 中存在重复或空的 Node ID；
- `start_order` 或 `stop_order` 包含未知 Node ID；
- `start_order` 或 `stop_order` 内存在重复 Node ID；
- 启动入口指定了配置中不存在的 Node ID；
- 同一 Application 内启用 TCP 传输的多个 Node，其监听地址发生冲突。

顺序列表允许只配置部分 Node，也允许完全不配置，因此“没有覆盖全部 Node”不是配置错误。

## 12. 错误与回滚

### 12.1 启动失败

任意 Node 启动失败时：

1. Application 立即停止启动后续 Node；
2. 记录失败 Node 和原始错误；
3. 从有效停止顺序中筛选出本次已经成功启动的 Node；
4. 按筛选后的顺序逐个停止这些 Node；
5. 返回包含启动错误和回滚错误的聚合结果。

失败 Node 如果已经分配了部分资源，必须由其自身启动失败处理负责释放；Application 不越过 Node 边界清理其内部资源。

### 12.2 停止失败

任意 Node 停止失败时，Application 记录错误，但仍继续停止有效停止顺序中的后续 Node。全部停止尝试结束后，Application 返回聚合错误。单个 Node 的停止失败不能阻止其他 Node 释放资源。

### 12.3 重复命令

对已经 `Ready` 的 Node 重复执行启动，或对已经 `Stopped` 的 Node 重复执行停止，统一按幂等成功处理，不得触发第二套并发生命周期流程，也不得重复分配或重复释放资源。Node 处于 `Starting` 或 `Stopping` 等过渡状态时收到不兼容的生命周期命令，应返回明确的状态错误。

## 13. 与 Origin v2 的兼容关系

v3 保留 Node、Service、Module 的概念和总体层级，在 Node 之上新增 Application。兼容目标是保留用户对这些概念的理解及主要功能，而不是复用 v2 的全局单例实现。

生产环境只选择一个 Node 启动时，Application 仍然存在，但其有效启停顺序中只有一个 Node。这样单 Node 与多 Node 使用同一套入口和生命周期逻辑，避免形成两套实现。

## 14. 验收标准

后续实现至少需要通过以下验证：

1. 一个 Application 能在同一进程中依次启动多个拥有独立端口的 Node。
2. 多 Node 运行时不存在共享 Node ID、Service 注册表或连接状态。
3. 未配置启停顺序时，启动使用声明顺序，停止使用实际启动顺序的反序。
4. 部分配置启停顺序时，最终顺序符合本文规则，并在重复运行中保持一致。
5. 选择部分 Node 启动时，只启动选中 Node，且保持有效顺序中的相对次序。
6. Node 启动失败后不会继续启动后续 Node，并会停止已经成功启动的 Node。
7. Node 停止失败不会阻止其他 Node 的停止，最终可以获得完整聚合错误。
8. 同一 Application 内不同 Node 的 TCP/NATS 调用不会自动变为内存直接调用。
9. 配置中出现未知、重复或地址冲突的 Node 时，在创建 Node 前返回配置错误。
