# Origin v3.2 文档

v3.2 已进入纵向切片实施阶段。公共网络契约、TCP、WebSocket、KCP、Gin HTTP Module 与 HTTP Client
已完成对应纵向切片；KCP 运行时默认值经过 Windows 与 Ubuntu 弱网验证后，已提供严格 Service Config。

## 网络模块教程

TCP、WebSocket 与 KCP 使用相同的 `network.Session`、`Handler`、Server、Client、Dialer、容量和错误
语义。建议先运行对应的 Raw 回环 Example，确认生命周期和服务自调用，再阅读使用指南接入 PB、
JSON 或自定义 Codec。

| 网络模块 | 适用场景 | 使用指南 | Example |
| --- | --- | --- | --- |
| TCP | 游戏客户端、自定义长度帧、直接二进制长连接 | [TCP 网络模块使用指南](guides/TCP网络模块使用指南.md) | [TCP Raw 服务自调用](../../../examples/13-network/01-tcp-raw-self-call/README.md) |
| WebSocket | 浏览器、网关、HTTP Upgrade、WS/WSS 长连接 | [WebSocket 网络模块使用指南](guides/WebSocket网络模块使用指南.md) | [WebSocket Raw 服务自调用](../../../examples/13-network/02-websocket-raw-self-call/README.md) |
| KCP | UDP 弱网、低时延游戏长连接 | [KCP 网络模块使用指南](guides/KCP网络模块使用指南.md) | [KCP Raw 服务自调用](../../../examples/13-network/03-kcp-raw-self-call/README.md) |

全部网络 Example 见 [`examples/13-network`](../../../examples/13-network/README.md)。三个示例都从完整
默认配置开始，通过所属 Service 的严格配置覆盖启动。每份 Example YAML 都给出完整、带注释的
Server 起始配置；教程说明字段关系和调整依据，并逐组列出公开函数及函数参数的实际执行协程，避免
把默认值误当成所有部署的最优值，也避免在网络 I/O goroutine 中误访 Service 串行状态。

## HTTP 组件教程

Gin Server 与 HTTP Client 不加入长连接 Session 外观：前者使用请求/响应和路由模型，后者是由代码
长期持有的并发 Client。先运行同 Service 自调用 Example，再按实际入口选择普通或 Safe 路由。

| 组件 | 适用场景 | 使用指南 | Example |
| --- | --- | --- | --- |
| Gin HTTP Module | 业务 HTTP API、普通请求回调、Service 串行 Safe 回调与分层鉴权 | [Gin HTTP Module 使用指南](guides/Gin%20HTTP%20Module使用指南.md) | [Gin Safe 路由与 HTTP 自调用](../../../examples/14-http/01-gin-safe-self-call/README.md) |
| HTTP Client | 服务间 HTTP、流式响应、有界完整响应、连接池与同 Service 自调用 | [HTTP Client 使用指南](guides/HTTP%20Client使用指南.md) | [Gin Safe 路由与 HTTP 自调用](../../../examples/14-http/01-gin-safe-self-call/README.md) |

## MongoDB 组件教程

MongoDB Module 不重复包装官方 CRUD。教程按三层说明：普通数据访问直接使用 `Collection`；索引、Session 和事务使用 Module 便利层；从 Service 工作协程等待数据库 I/O 时使用 `Await` 释放执行权。

| 组件 | 适用场景 | 使用指南 | Example |
| --- | --- | --- | --- |
| MongoDB Module | 玩家数据、道具、邮件、幂等记录、条件更新、批量写与跨文档事务 | [MongoDB Module 使用指南](guides/MongoDB%20Module使用指南.md) | [MongoDB 游戏存储](../../../examples/15-mongodb/01-game-store/README.md) |

Example 包含配置、索引、CRUD、两类 Upsert、条件扣金币、乐观锁、有界多行查询、BulkWrite、幂等奖励、事务转账和安全删除，并明确每种调用和回调所在的 goroutine。

## Redis 组件教程

Redis Module 按三层使用：高频基础命令使用精确返回值的便利层；长尾命令、Pipeline、事务、Watch 和 Lua 使用官方 Client 组合层；
Service 串行业务通过 `Await` 释放工作协程。Module 支持 Standalone、Sentinel、Cluster、有界连接池、TLS/ACL 和不自动续租的 Lease Lock。

| 组件 | 适用场景 | 使用指南 | Example |
| --- | --- | --- | --- |
| Redis Module | 缓存、会话、在线集合、匹配队列、基础整数排行、位图、原子 Lua 与并发收敛 | [Redis Module 使用指南](guides/Redis%20Module使用指南.md) | [Redis 游戏场景示例](../../../examples/16-redis/README.md) |

四个 Example 分别覆盖缓存与会话、集合与基础排行、Pipeline/Lua/乐观并发、分布式 Lease Lock；每个示例都把 Key、编解码和业务规则集中在业务 Module 中。

## 设计与实施资料

本目录沿用 `../v3.1/README.md` 定义的文档分层：

- `proposals/`：能力分析、问题定义和范围提案；
- `design/`：通过提案确认后形成的正式设计；
- `plans/`：设计明确允许实施后形成的执行计划；
- `changes/`：实施过程中的变更记录；
- `reports/`：测试、稳定性和验收报告；
- `guides/`：面向使用者的教程与迁移说明。

当前入口：

- [`Origin 网络系统模块能力分析与设计提案`](proposals/Origin网络系统模块能力分析与设计提案.md)
- [`Origin 网络事件与自定义协议扩展调研`](proposals/Origin网络事件与自定义协议扩展调研.md)
- [`Origin 网络模块核心设计`](design/Origin网络模块核心设计.md)：已经确认的单一实现依据，包含
  公共 API、内存池、消息队列、所有权、背压、协议和实施门禁。
- [`Origin Gin 与 HTTP Client 能力分析`](proposals/Origin%20Gin与HTTP%20Client能力分析.md)
- [`Origin Gin 与 HTTP Client 核心设计`](design/Origin%20Gin与HTTP%20Client核心设计.md)：已确认的 HTTP
  服务端 Module 与代码持有 Client 实现依据。
- [`Origin MongoDB Module 核心设计`](design/Origin%20MongoDB%20Module核心设计.md)：已确认的 MongoDB
  生命周期、多集群、配置、便利层、原子操作、测试和游戏场景 Example 实现依据。
- [`Origin Redis Module 核心设计`](design/Origin%20Redis%20Module核心设计.md)：已最终确认的 Redis
  Standalone/Sentinel/Cluster、生产配置、高频便利层、Pipeline/Lua、分布式锁、测试和游戏场景 Example
  实现依据。
- [`Origin Kafka Module 核心设计`](design/Origin%20Kafka%20Module核心设计.md)：已确认的 Producer/Consumer
  分离外观、Raw/JSON/PB、Origin Service 协程集成、自由 Sarama 模式、可靠性、测试和完整 Example
  实现依据。
- [`Gin HTTP Module 使用指南`](guides/Gin%20HTTP%20Module使用指南.md)：普通/Safe 路由选择、配置、所有权，
  以及每组公开函数和函数参数的实际执行协程。
- [`HTTP Client 使用指南`](guides/HTTP%20Client使用指南.md)：连接池、请求/响应所有权、Service Await，
  以及 Client 扩展回调的实际执行协程。

两份网络 Proposal 保存能力分析和调研依据，不单独授权实现。网络核心设计已允许按 TCP、WebSocket、
KCP 纵向切片实施；Gin 与 HTTP Client 已完成实施；MongoDB、Redis 与 Kafka 核心设计已经确认。MySQL
按当前项目优先级暂缓，不进入本轮设计与实现。接下来严格按 MongoDB、Redis、Kafka 的顺序分别制定计划、
实现、测试、补齐教程与 Example并验收；Kafka 的 Ubuntu Docker 环境只在 Kafka 实施阶段安装并保留。
每个实施切片都必须独立完成计划、测试、文档和验收。

当前实施计划：

- [`TCP 网络首批纵向切片实施计划`](plans/TCP网络首批纵向切片实施计划.md)
- [`WebSocket 网络纵向切片实施计划`](plans/WebSocket网络纵向切片实施计划.md)
- [`KCP 网络纵向切片实施计划`](plans/KCP网络纵向切片实施计划.md)
- [`Gin 与 HTTP Client 纵向切片实施计划`](plans/Gin与HTTP%20Client纵向切片实施计划.md)：实施与双平台验收完成。
- [`MongoDB Module 纵向切片实施计划`](plans/MongoDB%20Module纵向切片实施计划.md)
- [`Redis Module 纵向切片实施计划`](plans/Redis%20Module纵向切片实施计划.md)
- [`Kafka Module 纵向切片实施计划`](plans/Kafka%20Module纵向切片实施计划.md)

当前变更与验收材料：

- [`TCP 网络首批纵向切片变更记录`](changes/TCP网络首批纵向切片变更记录.md)
- [`TCP 网络首批纵向切片验收报告`](reports/TCP网络首批纵向切片验收报告.md)
- [`WebSocket 网络纵向切片变更记录`](changes/WebSocket网络纵向切片变更记录.md)
- [`WebSocket 网络纵向切片验收报告`](reports/WebSocket网络纵向切片验收报告.md)
- [`KCP 网络纵向切片变更记录`](changes/KCP网络纵向切片变更记录.md)
- [`KCP 网络纵向切片验收报告`](reports/KCP网络纵向切片验收报告.md)
- [`Gin 与 HTTP Client 纵向切片变更记录`](changes/Gin与HTTP%20Client纵向切片变更记录.md)
- [`Gin 与 HTTP Client 纵向切片验收报告`](reports/Gin与HTTP%20Client纵向切片验收报告.md)
- [`MongoDB Module 纵向切片变更记录`](changes/MongoDB%20Module纵向切片变更记录.md)
- [`MongoDB Module 纵向切片验收报告`](reports/MongoDB%20Module纵向切片验收报告.md)
- [`Redis Module 纵向切片变更记录`](changes/Redis%20Module纵向切片变更记录.md)
- [`Redis Module 纵向切片验收报告`](reports/Redis%20Module纵向切片验收报告.md)

v3.1 的维护资料继续保留在 `../v3.1/`，不得混入本目录。
