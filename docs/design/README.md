# Origin v3 设计文档索引

## 1. 目录目的

本目录是 Origin v3 的设计知识库。

设计工作分成三层：

1. `details/`：保存已经讨论确认的各系统详细设计，作为后续实现的设计储备。
2. `milestones/`：按可运行里程碑裁剪详细设计，说明某一阶段真正实现什么。
3. `../plans/`：设计确认后再编写可执行的实现计划，不在设计尚未确认时提前拆任务。

详细设计储备不等于首期实现范围。某个能力即使已经完成详细设计，也可以在后续里程碑实现。

## 2. 文档状态

| 状态 | 含义 |
|---|---|
| 已确认 | 方案已经和开发者确认，可以作为实现依据 |
| 部分进入 M0 | M0 只实现支撑最小闭环所需的子集 |
| 后续里程碑 | 设计保留，但不进入 M0 |
| 草案 | 需要开发者确认后才能据此编写实现计划 |

M0 表示首个最小可运行闭环。

## 3. 运行时与生命周期

| 详细设计 | 主要内容 | M0 范围 | 状态 |
|---|---|---|---|
| [Application 与 Node 生命周期](./details/2026-07-22-application-node-lifecycle-design.md) | Application 管理多个 Node、选择启动、显式启停顺序、网络隔离 | 核心进入 | 已确认 |
| [Service 启动与就绪](./details/2026-07-24-service-startup-and-readiness-design.md) | 基础设施先启动、`AwaitService`、就绪门和启动失败 | 核心进入 | 已确认 |
| [Service 退休](./details/2026-07-24-service-retirement-design.md) | 临时拒绝入站 RPC、其余任务继续、恢复服务、发现退休事件 | 核心进入 | 已确认 |
| [Service 优雅停止](./details/2026-07-24-service-graceful-stop-design.md) | 停止准入、排空、`OnStop`、收尾协程和超时保底 | 核心进入 | 已确认 |
| [Module 生命周期与运行模型](./details/2026-07-24-module-lifecycle-and-runtime-design.md) | 静态 Module 树、顺序启动、逆序释放、资源归属 | 核心进入 | 已确认 |
| [Service 协作式调度](./details/2026-07-23-service-cooperative-scheduling-design.md) | Service 同时只执行一个任务、`Await` 挂起恢复、超时与取消 | 核心进入 | 已确认 |
| [定时器系统](./details/2026-07-23-timer-system-design.md) | Node 级时间引擎、Service 投递、`ITimer`、暂停和取消 | 只实现必要子集 | 已确认 |
| [本地事件触发](./details/2026-07-24-local-event-dispatch-design.md) | Service 级同步与异步事件、Module 订阅和释放 | 只实现必要子集 | 已确认 |
| [Service 业务配置访问](./details/2026-07-24-service-business-configuration-access-design.md) | 按字段读取和结构体解析、Service 与 Module 共享配置 | 核心进入 | 已确认 |
| [统一错误码](./details/2026-07-24-unified-error-code-design.md) | 轻量错误、固定错误码、线协议和本地 cause | 核心进入 | 已确认 |

## 4. RPC 与分布式能力

| 详细设计 | 主要内容 | M0 范围 | 状态 |
|---|---|---|---|
| [RPC 接口与调用语义](./details/2026-07-23-rpc-interface-and-call-semantics-design.md) | Go 接口契约、Async/Await/Notify、Broadcast、统一 error | 只实现必要子集 | 已确认 |
| [RPC 数据类型与序列化](./details/2026-07-23-rpc-data-and-serialization-design.md) | 原生类型、普通结构体、Protobuf 类型及嵌套规则 | M0 先走 Protobuf 主路径 | 已确认 |
| [单目标 RPC 客户端与路由](./details/2026-07-24-rpc-single-target-client-and-routing-design.md) | 通过 NodeID、ServiceName 得到强类型生成客户端 | 核心进入 | 已确认 |
| [RPC 实例选择与路由策略](./details/2026-07-24-rpc-instance-selection-and-routing-strategy-design.md) | RoundRobin、Rand、ModKey 和自定义路由 | M0 只保留最小内建策略 | 已确认 |
| [服务发现与关注筛选](./details/2026-07-24-service-discovery-and-interest-filter-design.md) | 内置与 etcd 发现、关注契约和标签、发现事件 | 内置发现和必要筛选进入 | 已确认 |
| [模板 Service](./details/2026-07-24-service-template-design.md) | 复用 v2 模板方式，以新服务名创建实例 | 后续里程碑 | 已确认 |

## 5. 里程碑与迁移

- [M0 最小可运行闭环设计](./milestones/01-minimal-vertical-loop-design.md)：把上述储备设计裁剪成第一个可运行、可测试的端到端闭环。当前为草案。
- [Origin v2 功能盘点与 v3 迁移矩阵](../migration/v2-feature-inventory.md)：记录 v2 能力在 v3 中保留、重构、后置或待评估的安排，防止功能遗漏。

M0 设计经开发者确认后，再新增 `docs/plans/01-minimal-vertical-loop-plan.md`，将它拆成可执行的代码任务。

## 6. 文档维护规则

1. 不同且相对独立的系统使用独立设计文件。
2. 里程碑文档引用详细设计，不复制整份系统设计。
3. 新结论先更新对应详细设计，再更新受影响的里程碑和迁移矩阵。
4. 未确认方案必须标为草案或待确认，不混入已确认结论。
5. 每个实现计划都必须能追溯到里程碑设计和详细设计。
