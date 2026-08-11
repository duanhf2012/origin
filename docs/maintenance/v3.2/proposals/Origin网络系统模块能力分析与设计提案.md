# Origin 网络系统模块能力分析与设计提案

> 状态：能力分析与第二轮精简复核完成；尚未允许实施
> 基线：v3.1.0 发布候选
> 目标：v3.2.0
> 兼容性：不兼容 v2 `sysmodule/netmodule` 外观；不改变 v3.1 已冻结外观
> 参考实现：`origin/v2/sysmodule/netmodule`、`origin/v2/network`

## 1. 目标与已确认原则

本提案先分析 v2 TCP、KCP、WebSocket 和 Gin 模块已有能力及缺口，再确定 v3.2 的公共边界。
当前已确认：

1. 新功能进入 v3.2，v3.1 发布候选继续冻结；
2. TCP、KCP、WebSocket 应尽量提供一致外观，使业务 Handler 和主要调用代码不依赖具体传输；
3. 一致外观只统一真实共有语义，不把 KCP、WebSocket 专属参数塞进无关实现；
4. Gin/HTTP 请求响应模型与长连接 Session 模型差异明显，单独分析和设计；
5. v2 包名、类型名和函数名不是兼容契约，v3 可以按当前规范重新命名；
6. 首批范围包含 Server、Client 和 Dialer，不把重要的出站连接能力推迟到未知版本；
7. 在原始字节消息之外，内置 Protobuf 和 JSON 标准协议，使使用者可以开箱即用；
8. 发送路径在不弱化所有权安全的前提下减少复制，不以“零拷贝”名义暴露易误用的 Slice 转移；
9. TCP/KCP 长度帧同时支持 Big Endian 和 Little Endian，Big Endian 作为默认值；
10. 先完成公共契约和协议设计，再按 TCP、WebSocket、KCP 分批实施与验收。

设计优化必须控制范围：只有能够减少当前重复、消除已知风险或支撑已确认功能的抽象才进入设计；
不为假设中的传输、协议或兼容需求预留层次。性能优化同样以基准或 Profile 证据为准，但消息大小、
队列上限、所有权和背压属于正确性约束，必须在设计阶段确定。

本轮复核后的规范性选择集中在
[`Origin 网络模块核心设计`](../design/Origin网络模块核心设计.md)。本文保留能力范围和决策背景，
实现不得从本文与补充调研中自行拼接另一套方案。

## 2. v2 实际能力

### 2.1 共有能力

v2 的三个连接型 Module 都在底层连接 goroutine 与 Service 事件处理之间建立适配，并提供以下
近似能力：

- 监听地址和最大连接数；
- 每条连接的 ID、登记、查询、主动关闭和远端地址；
- 连接建立、收到消息、未知消息和断开事件；
- 通过 `IRawProcessor` 解码、路由和编码业务消息；
- 有界发送 Channel、最大消息长度、读写超时；
- 向指定连接发送编码消息或原始字节。

三个模块概念相似，但类型、配置、错误处理和方法名称分别实现，没有稳定的公共 Session、
Server、Client、Handler、统计或错误契约，业务不能只替换构造器来切换传输。

### 2.2 TCP

| 能力 | v2 状态 |
| --- | --- |
| 服务端 | TCP Listen、最大连接数、`TCP_NODELAY` |
| 消息边界 | 1/2/4 字节长度前缀，支持大小端、最小/最大读写长度 |
| 发送 | 每连接有界 Channel，可发送编码消息和原始消息 |
| 超时 | 每轮读 Deadline、每次写 Deadline |
| 内存 | 可替换字节池，但要求上层手工回收读取数据 |
| 客户端 | 底层 `TCPClient` 支持自动重连，但没有形成 Module 公共外观 |
| 生命周期 | 缺少完整 `OnStop`、等待和端口释放契约 |

v3 已有 RPC 使用的 `internal/tcpnet`，具备长度帧、Buffer 唯一所有权、有界发送队列、最大连接数、
读写超时、KeepAlive、并发关闭和完整等待。它可以复用为新 TCP Module 的内部基础，但不能直接
公开：它当前只有 Big Endian，且不包含业务 Session、Client/Dialer 和 Service Module 语义。

### 2.3 KCP

| 能力 | v2 状态 |
| --- | --- |
| 服务端 | 基于 `kcp-go/v5`，支持最大 Session 数 |
| KCP 参数 | MTU、收发窗口、NoDelay、Interval、快速重传、拥塞控制 |
| UDP 参数 | Socket 收发 Buffer、DSCP |
| FEC | Data/Parity Shards |
| 加密 | 预留 `BlockCrypt`，但 Module 配置没有完整传入 Server |
| 消息边界 | 在 KCP 流上继续叠加与 TCP 相同的长度帧 |
| 客户端 | 有底层实现，但生命周期、重连和公共外观不完整 |

KCP 不能仅被视为“UDP 版 TCP”。其 MTU、FEC、窗口、重传、NAT、伪造报文风险和加密需要单独
设计；`kcp-go/v5` 也会成为 v3.2 新的直接依赖。

### 2.4 WebSocket

| 能力 | v2 状态 |
| --- | --- |
| 服务端 | HTTP Upgrade，可选 TLS 证书 |
| 消息边界 | 使用 WebSocket 原生 Message，可选 Text/Binary |
| 限制 | 最大连接数、最大消息、发送 Channel |
| 客户端 | 底层 `WSClient` 支持自动重连，Module 未统一公开 |
| 安全 | `CheckOrigin` 固定返回 true；缺少 Path、Origin、子协议、鉴权和代理信任策略 |
| 存活检测 | 缺少 Ping/Pong、空闲检测和标准 Close Code 管理 |
| 生命周期 | 缺少 HTTP Server 优雅停止和全部连接等待 |

WebSocket 具备与 TCP/KCP 相同的“长连接 + 有序逻辑消息”业务模型，因此可以共享 Session 和
Handler；HTTP Upgrade、安全策略、Header、Message Type 和 Close Code 继续属于 WebSocket
子包。

### 2.5 Protobuf 与 JSON Processor

v2 Protobuf Processor 使用消息类型 ID 加 Protobuf Payload，JSON Processor 从 `typ` 字段识别
消息；二者都把编解码、注册、路由和连接回调集中在一个大接口中。它们证明内置标准协议是现实
需求，但旧接口过大、注册可变性和并发边界不清，不能原样迁移。

v3.2 保留“注册消息类型后直接收发”的使用体验，将协议编解码和业务生命周期分离：传输层只
交付完整消息，Protobuf/JSON 协议层负责消息标识、编解码和类型路由，业务 Handler 仍在 Service
串行上下文执行。

### 2.6 Gin/HTTP

Gin Module 的核心是单次请求/响应、路由、中间件、状态码和超时，不存在长连接 Session 消息
序列。v2 实现还存在 goroutine 启动错误处理、`log.Fatal`、代理 Header 信任和超时后继续写响应
等问题。该模块后续单独决定继续使用 Gin，还是基于 v3 已有 `net/http` 生命周期能力建立业务
HTTP Module；不加入本提案的统一 Session 外观。

该独立决策现已形成 [`Origin Gin 与 HTTP Client 能力分析`](Origin%20Gin与HTTP%20Client能力分析.md)：
保留 Gin 作为业务 HTTP Server Module 的路由外观，同时增加不属于 Module、按 Dialer 原则由代码持有的
HTTP Client；二者仍不加入长连接 Session 外观。

## 3. v2 不能直接迁移的边界

1. `IRawProcessor` 同时承担编解码、注册、路由和生命周期回调，接口过大；v3 不原样迁移；
2. 旧 Event 和 `PackType` 只是网络 goroutine 到 Service 的内部桥接，不应成为使用者外观；
3. 连接 ID 使用 MongoDB ObjectID 或 UUID，引入无关依赖；v3 使用 Module 实例拥有的轻量 ID；
4. 队列满、Scheduler 过载、协议错误和主动关闭没有统一错误语义，部分路径只写日志；
5. Module 缺少完整停止，不能证明 Listener、连接、Writer、Buffer 和 goroutine 已回收；
6. 没有每 Session 入站公平上限，单个客户端可以占满 Service 队列；
7. 用户回调 panic、恶意帧、消息 ID 冲突、重复关闭和启动失败回滚没有系统验证；
8. v2 大小端能力应按当前客户端协议需求重新设计，而不是作为旧兼容代码照搬；
9. WebSocket 允许任意 Origin，KCP 加密配置链路不完整，不能沿用其安全默认值；
10. 客户端固定间隔、无退出约束的自动重连不符合 v3 的 Context、退避和停止原则。

## 4. v3.2 统一能力设计

### 4.1 包边界

建议结构如下，最终公开名称在正式契约设计中冻结：

```text
sysmodule/network/                 公共 Session、Handler、Stats 和错误语义
sysmodule/network/protocol/        公共消息标识与 Router 契约
sysmodule/network/protocol/pb/     内置 Protobuf 标准协议
sysmodule/network/protocol/json/   内置 JSON 标准协议
sysmodule/network/tcp/             TCP Server、Client、Dialer 和长度帧
sysmodule/network/kcp/             KCP Server、Client、Dialer 及专属参数
sysmodule/network/websocket/       WebSocket Server、Client、Dialer 及专属能力
```

不建立一个包含所有后端参数的“大一统网络模块”，也不公开 `internal/tcpnet`。传输实现依赖公共
契约，公共包不反向依赖具体实现。三个传输分别提供语义一致的具体 Server、Client 和 Dialer，
首批不为了运行期多态额外声明大接口。

### 4.2 Server、Client、Dialer 与 Session

- Server 和托管 Client 实现 `service.IModule`，由 `Service.AddModule` 管理，不公开绕过生命周期
  的独立 Start/Stop；
- Dialer 表示一次受 `context.Context` 约束的连接尝试，不隐含永久重试；
- 托管 Client 表示一个长期出站端点，可配置重连。重连同一时间只有一次尝试，使用指数退避、
  上限和抖动，停止时立即取消，不允许 goroutine、Timer 或连接累积；
- Server 接入和 Client 建连后都产生相同 Session，复用发送、关闭、统计和 Handler 语义；
- Session 使用 Module 作用域内稳定、非零的数值 ID，提供传输类型、本地/远端地址、`Send`、
  `Close`、`Done` 和最终关闭原因；
- Handler 统一接收 Open、Message、Close；同一 Session 严格保持 Open → Message → Close，
  不同 Session 可以交错；
- Handler 在所属 Service 的串行执行上下文运行，不在网络读写 goroutine 直接执行业务；
- Server 支持按 ID 查询和关闭 Session；Client 支持读取当前 Session，但业务必须处理重连后的
  Session ID 变化。

### 4.3 原始、Protobuf 与 JSON 消息外观

三种外观建立在同一 Session 和传输契约之上：

1. **Raw**：Handler 接收完整逻辑消息字节，适合自定义协议；
2. **Protobuf**：内置消息 ID、类型注册、编解码和路由，使用现有
   `google.golang.org/protobuf`，不引入另一套 Protobuf 运行时；
3. **JSON**：内置稳定 Envelope、消息 ID、类型注册、编解码和路由，避免要求每个业务结构都
   自行重复实现分发字段解析。

Protobuf 和 JSON 的 Wire Format、消息 ID 宽度、未知消息策略及注册 API 必须在独立协议设计中
明确，并提供跨 Server/Client 的 Golden Test。注册表构建完成后冻结，运行期只读；恶意格式、
未知 ID 和超大消息在进入业务 Handler 前被拒绝。协议层是可选适配器，不把 Protobuf/JSON
耦合进 TCP、KCP 或 WebSocket 的传输实现。

通用事件、自定义编码和消息路由的详细职责边界见
[`Origin 网络事件与自定义协议扩展调研`](Origin网络事件与自定义协议扩展调研.md)。结论是使用
强类型 Raw Handler，并把 Framer、Codec、Router 和 Handler 分离；横切逻辑使用 Handler 包装，
首批不建立 Middleware Pipeline。自定义编码只替换无状态/只读 Codec，不接管 Session 生命周期、
Service 调度或 Buffer Pool。

### 4.4 发送所有权与零拷贝边界

目标是“安全前提下最少复制”，不是承诺所有 API 都绝对零拷贝：

- 安全默认 `Send([]byte)` 在返回前复制数据；否则调用者在返回后复用或修改 Slice 会造成竞态、
  数据损坏甚至跨 Session 泄漏；
- 协议层使用框架控制的 Encoder：Codec 返回后 Buffer 归框架所有。它允许自定义编码器直接写入
  最终发送 Buffer，又不要求调用者转移已有 Slice 的所有权；首批不额外公开通用填充式 Session API；
- 内置 Protobuf 使用预估大小后直接编码到框架 Buffer；JSON 编码产生的独占结果直接入队，均
  不再做第二次防御性复制；
- 入站 Buffer 在 Handler 返回前有效，返回后由框架唯一回收；需要长期保存时由业务显式复制；
- 首批不公开“把任意 `[]byte` 所有权强行移交框架”的 API，也不暴露 Buffer Pool。只有基准证明
  仍有必要且契约可验证时，再增加更底层能力。

该设计使普通调用安全，标准 PB/JSON 和高级编码路径避免重复复制，且不增加悬挂引用和池污染风险。

### 4.5 大小端与消息边界

不支持 Little Endian 并不会使 TCP 无法通信，但会使使用 Little Endian 帧头的现有客户端无法
接入。由于网络模块面向通用游戏客户端，Little Endian 是当前有效能力，不应被误判为兼容负担。

- TCP 和 KCP 长度帧支持 1/2/4 字节前缀；1 字节无端序差异，2/4 字节可选择 Big Endian 或
  Little Endian；
- 默认使用 Big Endian，选择在构造时冻结并预选编码/解码函数，热路径不重复判断；
- Protobuf 二进制 Envelope 中的定长整数采用独立的协议端序选项，并与长度帧端序明确区分；
- WebSocket 自带消息边界，不额外套长度帧；只有其二进制应用协议中的整数受协议端序影响；
- 两种端序必须执行完全相同的长度、溢出、最小值和最大值校验，并共享契约测试。

### 4.6 公共与专属配置

公共配置只表达各实现能够保证相同语义的字段：

- 最大活动 Session 数；
- 每 Session 入站 pending 与出站队列的消息数和字节数；
- 每 Module 入站 pending 与出站队列的总字节预算；
- 最大逻辑消息大小；
- 读空闲、拨号、握手和单次写超时；
- Handler 与结构化 Logger。

监听地址属于 Server，远端地址与重连属于 Client/Dialer。TCP 帧、KeepAlive、TLS；KCP 的
MTU/FEC/窗口/重传/加密；WebSocket 的 Path、Origin、Header、Text/Binary、子协议、TLS、
Ping/Pong 和 Close Code 分别保留在具体 Options 中。

## 5. 游戏服务器过载策略调研与 Origin 结论

公开资料无法严格统计“大多数游戏服务器”的内部实现，因此不把个别产品行为表述为行业占比。
但代表性的游戏服务端和网络框架呈现出一致的工程模式：

- Nakama 对实时连接设置最大消息大小和有界出站队列，出站等待消息超过上限时把客户端视为过慢
  并断开；权威比赛的输入、调用、延迟广播和加入尝试也使用独立有界队列；
- Unity Transport 的发送/接收队列有固定容量，队列满返回 `NetworkSendQueueFull`，容量增大需要
  付出明确内存代价；可靠流水线的在途包也有上限；
- Netty 使用发送 Buffer 高低水位切换 `Channel.isWritable()`，让应用在硬失败前感知背压；
- Unreal 建议限制高频可靠 RPC，允许对不可靠高频事件使用丢弃；其部分不可靠 RPC 队列达到
  上限时丢弃旧项。

资料：
[Nakama 配置](https://heroiclabs.com/docs/nakama/getting-started/configuration/)、
[Unity Transport 队列说明](https://docs.unity.cn/Packages/com.unity.transport%402.0/manual/faq.html)、
[Unity Transport 可靠流水线](https://docs.unity.cn/Packages/com.unity.transport%402.4/manual/pipelines-usage.html)、
[Netty WriteBufferWaterMark](https://netty.io/4.1/api/io/netty/channel/WriteBufferWaterMark.html)、
[Unreal 网络建议](https://dev.epicgames.com/documentation/unreal-engine/networking-overview-for-unreal-engine?lang=en-US)、
[Unreal 网络队列配置](https://dev.epicgames.com/documentation/en-us/unreal-engine/console-commands-for-network-debugging-in-unreal-engine)。

据此，Origin 不提供一个粗粒度的全局“丢弃/阻塞/断开”开关，而采用分层策略：

1. **接入防护**：限制活动连接数、握手时间、最大消息、每 Session pending 数量/字节及 Module
   收发总字节；
2. **软背压**：出站达到高水位时标记 Session 不可写并记录指标，降到低水位后恢复；首批不增加
   跨传输的暂停读取状态机；
3. **可靠消息默认策略**：不阻塞 Service Runner，不静默丢弃。主动 `Send` 在本地队列满时返回
   稳定的过载错误；持续过慢并达到硬字节/消息上限的远端 Session 被关闭；
4. **入站硬上限**：单 Session 额度耗尽时拒绝当前消息并关闭来源 Session，避免恶意或失控连接
   占满全局 Scheduler；Scheduler 拒绝任务时关闭来源 Session；
5. **可替代状态消息**：只有业务明确声明为不可靠、可合并或只保留最新值时，才允许
   DropNewest、DropOldest 或 Coalesce；首批传输层不实现这些策略，业务出现真实需求后另立设计；
6. **可观测性**：区分本地发送拒绝、慢客户端断开、入站过载和接入拒绝，并暴露次数、字节、
   高低水位持续时间及断开原因。

这是游戏服务端常见做法的组合：可靠控制消息优先保持语义，实时状态允许由业务显式降级，任何
队列都不能无限增长。

## 6. 生命周期、失败与测试要求

- 普通运行期 Close 通过 Service 队列投递；进入 Service Stopping 后不再创建新任务；
- Module `OnStop` 先停止监听、拨号和重连，再关闭并等待全部 Session；已接受任务先排空，剩余
  Close 回调在停止上下文中安全执行，保证恰好一次且不死锁；
- Handler 返回错误时关闭 Session；其后已排队消息执行前检查终态并跳过；
- 每个回调单独隔离 panic，任一 Session 的错误不能阻止其他连接和 Listener 清理；
- 启动任一步失败按逆序释放 Listener、Session、Timer、Buffer 和 goroutine；
- Server、Client、Dialer 提供同构统计和稳定错误，可使用 `errors.Is` 分类。

测试至少覆盖：

- 公共契约测试在 TCP、KCP、WebSocket 的 Server 与 Client 上共同运行；
- Raw、PB、JSON 双向 Golden Test，未知 ID、重复注册、恶意输入和大小上限；
- 自定义 Codec、Router 和 Handler 包装的错误、panic、并发、冻结与资源回收；
- TCP/KCP 的 1/2/4 字节帧与 Big/Little Endian 组合；
- 队列消息数/字节数上限、软高低水位、慢客户端、入站洪泛和可靠消息不静默丢弃；
- 首次拨号失败、重连、停止中拨号、连接抖动及 Server 自己调用自己服务的回环场景；
- 重复关闭、部分启动失败、停止并发、Handler panic、Buffer/goroutine/端口回收；
- Race、Fuzz/恶意帧、Windows 测试及 Ubuntu 环境的真实协议、Race 和稳定性复验；
- 重点核心功能尽量达到 100% 语句和分支覆盖；不能达到的路径逐项说明原因并用集成、Race、Fuzz
  或故障注入补证，不能只追求总覆盖率数字。

## 7. 范围与实施顺序

v3.2 首批网络范围包含公共契约、Raw/PB/JSON、TCP、WebSocket、KCP 的 Server、Client 和 Dialer。
“首批范围”表示本版本的交付边界，不表示一次性提交全部代码。按以下可独立验收的纵向切片实施：

1. **正式设计**：先冻结公共事件、Codec/Router 扩展、标准 PB/JSON Wire Format、所有权、错误、
   过载和生命周期；
2. **公共基础 + TCP 全链路**：实现公共能力、协议层、TCP Server/Client/Dialer 和回环测试，验证
   最大公共风险；
3. **WebSocket 全链路**：复用公共契约，补齐 Upgrade 安全、Ping/Pong、TLS 和浏览器语义；
4. **KCP 全链路**：最后引入新依赖，单独验证 UDP/KCP 参数、安全、容量和弱网行为；
5. **整体收口**：公共契约测试、跨平台与 Ubuntu 稳定性、教程和完整示例；
6. **性能阶段**：在正确性和测试收口后执行 Benchmark/Profile，只实施有证据且收益明确的优化，
   重新运行功能、Race、Fuzz 和稳定性测试；
7. **Gin/业务 HTTP Module**：另立能力提案和设计，不受长连接统一外观约束。

每个传输切片都要完成设计复核、实现、测试、文档和验收后再进入下一个，避免在三个后端同时铺开
未验证的抽象。若 TCP 实施证明公共接口不成立，应先回到正式设计修订，不把临时兼容层传播到
WebSocket 和 KCP。

## 8. 本轮确认清单

| 项目 | 修订结论 | 状态 |
| --- | --- | --- |
| 目标版本 | 进入 v3.2.0，v3.1 继续冻结 | 已确认 |
| 包结构 | `sysmodule/network` 公共包及 `tcp`、`kcp`、`websocket` 子包 | 已确认 |
| 首批范围 | 包含各传输的 Server、Client、Dialer，按纵向切片分批验收 | 已按意见修订 |
| 业务回调 | 统一 Handler，进入所属 Service 串行上下文 | 已确认 |
| 消息外观 | Raw + 内置 Protobuf/JSON 标准协议，协议与传输解耦 | 已按意见修订 |
| 扩展外观 | 强类型 Raw Handler；Framer、Codec、Router 分离；首批不建 Middleware | 第二轮精简，待整体确认 |
| v2 外新增能力 | 可写事件、Context、Client 状态、Session/Module 有界容量和确定性停止 | 第二轮精简，待整体确认 |
| 发送所有权 | 安全默认复制；框架填充式 API 与内置协议避免重复复制 | 已按意见修订 |
| 帧协议 | Big/Little Endian 均支持，Big Endian 默认 | 已按意见修订 |
| 内存池 | 只复用 Module 私有内部字节池；不公开 Pool，不池化业务对象 | 第二轮新增，待整体确认 |
| 消息队列 | 出站 Ring + 合并唤醒；入站 pending 直接 Dispatch；Session 双限 + Module 总字节预算 | 第二轮新增，待整体确认 |
| 过载策略 | 有界容量 + 软背压 + 可靠消息不静默丢弃 + 慢连接硬断开 | 第二轮收敛，待整体确认 |
| 后续顺序 | 正式设计 → TCP → WebSocket → KCP → 整体收口/性能；Gin 独立 | 已确认 |

[`Origin 网络模块核心设计`](../design/Origin网络模块核心设计.md) 已完成整批确认并允许按纵向切片
实施。TCP 实现后的 Ubuntu 容量数据再决定发布默认值是否需要修订。
