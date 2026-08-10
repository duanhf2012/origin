# Origin 网络事件与自定义协议扩展调研

> 状态：调研与第二轮精简复核完成；作为研究依据保留，尚未允许实施
> 适用范围：v3.2 TCP、KCP、WebSocket 的 Server、Client 与 Dialer
> 上位提案：[`Origin 网络系统模块能力分析与设计提案`](Origin网络系统模块能力分析与设计提案.md)
> 实现依据：[`Origin 网络模块核心设计`](../design/Origin网络模块核心设计.md)

## 1. 问题与结论

本调研回答两个问题：

1. 连接建立、消息、断开和背压等通用事件应如何回调；
2. 使用者如何替换 Protobuf/JSON，实现自己的消息标识、编码和解码。

调研后的原始建议是拆分 v2 `IRawProcessor`。第二轮以“够用、易用、轻量”为目标复核后，首批不
公开通用 Middleware Pipeline，也不为每条 Session 建立 CodecFactory；最终职责为：

```text
入站：Socket → Framer → Raw Buffer → Service Scheduler → Router/Codec → MessageHandler
                   I/O 与所有权边界                 Service 串行执行边界

事件：Transport ───────────────────────────────→ Handler

出站：业务消息 → Codec.Encode → Session 有界队列 → Framer → Socket
```

- **Framer**：只负责字节流和完整逻辑消息之间的边界；
- **Codec**：只负责完整 Raw 消息与 `MessageID + Value` 之间的转换；
- **Router**：只负责按 MessageID 查找类型工厂和业务处理函数；
- **Handler**：只负责连接生命周期和最终业务回调。

用户所说的“自定义 Processor”在 v3.2 中建议命名为 `protocol.Codec`。`Processor` 容易再次演变为
无边界的综合对象；自定义 Codec 可以替换编码格式，但不能启动 goroutine、管理 Session、调用
生命周期回调或绕过 Service 调度。

## 2. 调研依据

成熟实现普遍把传输事件、消息边界、编解码、业务处理和观测拆开，但不意味着 Origin 必须复制
它们的全部扩展层：

- Netty 的 `ChannelPipeline` 按顺序传播入站/出站事件，同时把 `ByteToMessageDecoder`、
  `MessageToMessageCodec` 与最终 Handler 分成不同阶段；
- gRPC-Go 的 `encoding.Codec` 只包含 Marshal/Unmarshal，并要求注册在初始化期完成、运行时并发安全；
  gRPC 的 Interceptor 用于每次调用的横切逻辑，而不是管理底层 TCP 连接；
- gRPC 的 `stats.Handler` 明确用于监控，不作为业务 Interceptor 的替代品；
- Go `net/rpc` 通过 ClientCodec/ServerCodec 替换 Wire Format，业务方法不需要感知底层编码；
- Protobuf Go 提供 `Size` 和 `MarshalAppend`，可以直接复用框架提供的容量，避免编码后再次复制。

资料：
[Netty ChannelPipeline](https://netty.io/4.1/api/io/netty/channel/ChannelPipeline.html)、
[Netty Codec 包](https://netty.io/4.1/api/io/netty/handler/codec/package-summary.html)、
[Netty ChannelInboundHandler](https://netty.io/4.1/api/io/netty/channel/ChannelInboundHandler.html)、
[gRPC-Go encoding](https://pkg.go.dev/google.golang.org/grpc/encoding)、
[gRPC Interceptors](https://grpc.io/docs/guides/interceptors/)、
[gRPC-Go stats](https://pkg.go.dev/google.golang.org/grpc/stats)、
[Go net/rpc Codec](https://go.dev/src/net/rpc/client.go)、
[Protobuf Go MarshalAppend](https://pkg.go.dev/google.golang.org/protobuf/proto)。

Origin 不直接复制 Netty 的任意动态 Pipeline。Origin 的 Service 已经提供串行执行语义；横切逻辑
使用普通 Handler 包装器即可构造并冻结。这样既继承职责分离原则，也不新增 Origin 专属中间件
协议和运行时链。

## 3. 通用事件回调设计

### 3.1 不使用弱类型通用 Event 作为网络主外观

不推荐以 `Handle(Event{Type, Data any})` 作为主要 API，也不把每条网络消息转成公开
`service.Event`：

- 使用者需要反复 type switch，编译期无法发现遗漏；
- Open、Message、Writable、Close 的错误返回和所有权语义不同，统一成一个 Event 后反而模糊；
- Service Event 支持多监听器，而一条入站 Buffer 只能有一个明确的最终所有者；
- 网络消息属于高频单消费者路径，通用事件总线的 fan-out 和动态类型检查没有必要。

网络 Module 应直接向所属 Service Scheduler 提交内部任务，再调用一个终端 Handler。业务如果
需要把网络消息转成自己的领域事件，可以在 Handler 内显式调用现有 Service Event API。

### 3.2 推荐的强类型 Handler

正式 API 名称可以调整，但语义建议固定为：

```go
type Handler interface {
	OnOpen(context.Context, Session) error
	OnMessage(context.Context, Session, []byte) error
	OnWritableChanged(context.Context, Session, bool)
	OnClose(context.Context, Session, error)
}
```

同时提供 `HandlerFuncs`，使用者只填写需要的函数，未填写项使用安全默认行为，避免为简单场景
编写空方法。PB/JSON Router 自身实现该 Raw Handler，将 `OnMessage` 转成类型安全的协议 Handler。

事件契约：

- 同一 Session 严格保证 `Open → (Message | WritableChanged)* → Close`；
- `OnOpen` 成功前不调用 Message；`OnOpen` 返回错误立即进入 Close；
- `OnMessage` 返回错误关闭当前 Session；不会影响其他 Session；
- `WritableChanged` 只在跨越高/低水位时触发，不为每次队列变化触发；
- `OnClose` 恰好一次、始终最后调用且不返回错误；最终原因通过参数和 `Session.Cause()` 获取；
- 所有业务 Handler 都在所属 Service 串行上下文执行，使用者无需为这些回调之间的共享状态加锁；
- Handler panic 被恢复并转换为稳定关闭原因，同时记录指标；
- Module 停止期间仍保证已接受事件和最终 Close 的顺序，不在 Scheduler 停止准入后偷偷投递任务。

`OnError` 不作为独立业务事件：终止性错误通过 Close 原因表达，非终止 I/O 细节进入统计和限频日志，
避免同一故障既触发 Error 又触发 Close 而产生重复、乱序处理。

### 3.3 Client 状态与 Session 事件分离

托管 Client 还需要连接级状态：`Connecting`、`Connected`、`Reconnecting`、`Stopped`。该状态属于
Client Module，不伪装成 Session 事件：

- 每次成功建连产生一个新 Session，并正常触发 Open/Close；
- Client 状态回调说明重连进程，不复用旧 Session ID；
- 重连回调同样进入所属 Service 执行上下文；
- Dialer 只有单次连接结果，不产生 Reconnecting 状态。

### 3.4 首批使用 Handler 包装，不建立 Middleware 接口

鉴权状态检查、业务限流、日志和 Tracing 可以由普通结构包装下一个 Handler，在 `OnInit` 前完成
组合。首批不提供动态注册、排序、短路和恢复规则组成的 Middleware API。故障恢复、Buffer 释放、
最终 Close 与统计仍由框架边界保证，不能交给包装器决定。

当至少两个独立模块出现完全一致、手工包装明显重复的需求后，再从真实代码提炼小型辅助函数；
不提前建立网络专属 Pipeline。

## 4. 自定义 Codec 与 Router 设计

### 4.1 Codec 的最小职责

第二轮复核后的概念契约如下，代码只表达职责，不是已经冻结的最终签名：

```go
type Codec interface {
	Decode(frame []byte, resolver Resolver) (Message, error)
	Encode(dst *Encoder, id MessageID, value any) error
}
```

`Resolver` 是 Router 的冻结只读类型解析能力。Codec 读取 ID 后由 Resolver 创建解码目标，避免
Codec 和 Router 各自维护注册表。`Message` 包含非零 MessageID 和解码后的 Value。`Encoder` 是
框架控制的有界追加写入器，不把 Buffer Pool 或任意所有权转移能力暴露给 Codec。

关键约束：

- Codec 在 Module 启动前安装并冻结，首批必须无状态或只读；Decode 在 Service 串行上下文执行，
  Encode 允许被并发 Send 调用，因此不能保存本次调用的可变状态；
- I/O goroutine 只完成帧长校验、pending 额度和 Service 投递，Decode 与业务 Handler 在同一
  Service Task 中执行，避免排队期间持有膨胀后的 PB/JSON 对象；
- Encode 在发送调用返回前同步完成，之后只把框架拥有的结果 Buffer 放入有界队列；
- Encoder 提供 append 风格的框架自有容量，使 Protobuf 和自定义二进制协议可以直接形成最终
  Buffer，同时防止 Codec 返回来源不明或仍被外部引用的 Slice；
- Codec 不得保存入站 `frame`；返回的 Message 也不得引用其底层数组，确需保留时必须自行复制；
  不得保存或异步使用 Encoder 借出的写入区域；
- Codec panic 被恢复：入站按协议错误关闭 Session，出站向调用者返回错误；
- Codec 输出仍要经过最大消息和队列字节数校验，不能通过自定义实现绕过资源上限；
- Codec 和 Router 在 Module 启动前冻结，运行期不允许替换或注册消息。

需要每 Session 协商状态、压缩字典、动态密钥或运行期切换时另立真实需求，再决定是否增加
CodecFactory；首批不为这些尚未确认的能力增加对象和生命周期。

### 4.2 Router 与 Codec 分离

Router 维护 `MessageID → 类型工厂 + Handler` 的只读表：

- 注册发生在 `OnInit`，重复 ID、零 ID、nil 工厂和类型不一致直接启动失败；
- Router 在 Service Task 中调用 Codec Decode，成功后立即调用对应 Handler；
- 未知 ID 默认作为协议错误关闭 Session；可以显式安装 Unknown Handler 决定忽略或记录；
- PB/JSON 提供类型安全的注册辅助函数；自定义 Codec 可以复用同一个 Router；
- 只想处理原始字节的使用者直接安装 Raw Handler，不必创建伪 Codec 或 Router；
- 需要完全自定义业务分发时，也可以在 Raw Handler 内完成，但仍受统一生命周期和 Service 调度约束。

这样，替换 PB、JSON 或自定义二进制格式时不会改变 Open/Close 和业务处理顺序；更换 TCP、KCP、
WebSocket 时也不需要更改 Codec 和 Router。完整接口与 Wire Format 以核心设计草案为准。

### 4.3 Framer 与 Codec 必须分开

Codec 接收的是完整逻辑消息，不处理 TCP 粘包/拆包。TCP/KCP 首批提供已确认的 1/2/4 字节长度
前缀和大小端组合；WebSocket 使用原生消息边界。

暂不公开任意自定义 Framer。原因是 Framer 直接接触未受信任字节流、累积 Buffer 和最大长度，
一旦接口不严谨就可能造成无限等待、无限内存、死循环或 Buffer 泄漏。若已有客户端确实使用
“长度字段不在开头、固定头、分隔符”等 Wire Format，应先收集真实格式，再为 TCP/KCP 单独设计
受限 `FrameCodec`，并要求 Fuzz 与恶意流测试；不能让 Codec 越层读取 Socket 来规避默认 Framer。

### 4.4 v2 Processor 能力映射

| v2 能力 | v3.2 归属 |
| --- | --- |
| `ConnectedRoute` | `Handler.OnOpen` |
| `DisConnectedRoute` | `Handler.OnClose` |
| `Unmarshal` / `Marshal` | `protocol.Codec` |
| `MsgRoute` | `protocol.Router` |
| `UnknownMsgRoute` | Router 的显式 Unknown Handler/Policy |
| `SetByteOrder` | TCP/KCP Frame Options 或二进制 Codec Options |
| Processor 内的 Buffer Pool | 框架内部 Encoder 与 Buffer 所有权，不向 Codec 暴露池 |

这不是兼容层：v2 自定义 Processor 迁移时需要按职责拆开，避免把旧接口和旧并发问题带入 v3.2。

## 5. v2 之外的能力复核

### 5.1 v3.2 首批必须补充

1. **可写状态事件**：高低水位转换时通知业务，配合非阻塞 Send，避免只能轮询或盲目重试；
2. **结构化关闭原因**：区分主动关闭、远端关闭、空闲、协议错误、过载、Handler/Codec 错误和
   Module 停止，并支持 `errors.Is`；
3. **Session Context**：连接建立时创建、关闭时取消；暴露只读 SessionInfo、TLS/WS 握手信息和
   可信远端地址，不提供并发不明的通用属性 Map；
4. **Client 状态与重连观测**：重连次数、当前退避、最近失败和当前 Session 可查询；
5. **分层有界容量**：出站队列、入站 pending 同时限制每 Session 消息数/字节数，并限制每 Module
   总字节数；单 Session 或大量 Session 都不能无限占用 Service 与内存；
6. **确定性 Module Stop**：`OnStop(ctx)` 停止接入、排空、强制关闭和等待全部资源，不再增加一套
   公开 Drain 状态机；
7. **配置冻结与基础能力查询**：启动前验证全部组合；Session 可查询传输和握手信息，运行期不能
   修改 Wire Format；
8. **协议与回调可观测性**：按关闭类别、Codec 错误、慢 Handler 和水位转换统计；
    MessageID 指标必须限制基数，未知 ID 不直接作为无限标签值。

WebSocket Origin 默认安全策略应沿用官方库的显式允许模型，而不是 v2 的任意 Origin：
[coder/websocket AcceptOptions](https://pkg.go.dev/github.com/coder/websocket)。

### 5.2 完成 TCP 验证后再决定

- **通用 Admission Hook**：传输专属安全 Options 和 OnOpen/首条消息不能满足真实接入策略时再加；
- **独立公开 Drain**：只有部署编排需要“Module 继续运行但停止接入”时再设计；
- **批量发送/编码共享**：先用循环 Send 保证正确；Profile 证明复制或编码是广播热点后再决定内部
  引用计数，首批不公开引用所有权；
- **消息/字节 Token Bucket**：当前单帧、pending 和 Scheduler 硬上限不足以抵御真实持续洪泛时
  再加入，并通过时钟和公平性测试；
- **消息优先级队列**：只有控制消息确实被大批状态消息阻塞且有基准证据时增加；必须防止低优先级
  永久饥饿；
- **自定义 Framer**：只有真实客户端 Wire Format 无法使用长度前缀时增加；
- **协议协商/运行期 Codec 切换**：首批一个 Module 固定一种协议；有多协议同端口需求后另设计；
- **压缩和应用层加密 Pipeline**：优先 TLS/KCP 已有安全能力；有流量和 CPU 数据后再增加；
- **共享引用计数 Buffer 外观**：批量发送先由框架内部安全实现，不向使用者公开池或引用计数；
- **Handler 独立并行执行**：当前遵循 Service 串行模型；只有明确业务需要并重新设计状态一致性后
  才讨论。

### 5.3 明确不增加

- 不增加运行期动态注册和替换 Codec/Handler；
- 不增加每 Session CodecFactory，除非出现真实的协议协商状态；
- 不增加任意类型、任意 fan-out 的第二套网络事件总线；
- 不增加首批 Middleware/Pipeline，横切逻辑使用 Handler 包装；
- 不增加 Session 通用可变属性 Map，业务绑定关系由 Service 自己管理；
- 不让自定义 Codec 直接访问 Socket、Buffer Pool、Scheduler 或 Listener；
- 不承诺 Send 成功代表对端已经处理，只代表消息被本地发送队列接受；
- 不为了未来可能出现的 UDP/QUIC/HTTP3 提前扩展当前公共接口。

## 6. 测试与验收重点

除主提案已有测试外，本扩展点还必须验证：

- Open、Message、WritableChanged、Close 的全组合顺序和恰好一次语义；
- OnOpen/OnMessage、Handler 包装、Codec、Router、Unknown Handler 各自返回错误和 panic；
- 自定义 Codec 的短包、超大输出、Encoder 越界、保存借用 Buffer 等错误实现；
- 注册冻结、重复 ID、类型不匹配以及 PB/JSON/自定义 Codec 共享契约；
- 一个慢 Session 的水位变化不影响其他 Session；出站和入站按消息数/字节数同时达到边界；
- 传输专属接入安全拒绝后无 Session/Buffer/goroutine 泄漏；
- Module Stop 截止时间与立即 Session Close 在并发 Send、重连下的确定行为；
- 自定义 Codec Fuzz、Race，以及 Windows/Ubuntu 的真实回环与服务自调用测试。

## 7. 第二轮复核结论

| 项目 | 推荐结论 | 状态 |
| --- | --- | --- |
| 事件外观 | 强类型 Raw Handler + HandlerFuncs，不以弱类型 Event 作为主 API | 已收敛，待整体确认 |
| 回调范围 | Open、Message、WritableChanged、Close；Client 状态单独处理 | 已收敛，待整体确认 |
| 自定义编码名称 | 使用 `protocol.Codec`，不继续使用含义过宽的 Processor | 已收敛，待整体确认 |
| 职责拆分 | Framer、Codec、Router、Handler 分离；首批不建 Middleware | 第二轮精简 |
| Codec 实例 | Module 共享的无状态/只读 Codec；暂不提供 CodecFactory | 第二轮精简 |
| Decode 位置 | Raw Buffer 直接进入 Service Task，再 Decode 和 Route | 第二轮精简 |
| 注册时机 | OnInit 构建并冻结，运行期只读 | 已收敛，待整体确认 |
| 自定义 Framer | 首批不公开；有真实 Wire Format 后单独设计 | 已收敛，待整体确认 |
| 新增首批能力 | 可写事件、关闭原因、Context、Client 状态、分层有界容量、确定性停止 | 第二轮精简 |
| 延后能力 | Admission、公开 Drain、Broadcast、Token Bucket、优先级、动态协商和公开 Buffer | 第二轮精简 |

完整所有权、队列、内存池、Wire Format、默认值校准和实施门禁统一以
[`Origin 网络模块核心设计`](../design/Origin网络模块核心设计.md) 为准，不从本调研文档单独启动实现。
