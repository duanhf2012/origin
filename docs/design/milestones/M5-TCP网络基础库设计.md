# Origin 第三版 M5 TCP 网络基础库设计

> 文档状态：草案，等待开发者 Review
> 创建日期：2026-07-26
> 适用里程碑：M5
> 前置依赖：M0 工程基础、M1 日志库、M2 内存复用库

## 1. 目标

M5 实现一个只面向 Origin 框架内部的 TCP 字节传输基础库，为后续 M10 TCP RPC 提供稳定、
低延迟且可完整停止的连接能力。

M5 完成后必须能够：

1. 监听 TCP 地址并接收多条连接；
2. 使用 Context 连接一个 TCP 地址；
3. 按固定长度帧读取和发送完整消息；
4. 在申请消息体 Buffer 前拒绝非法长度；
5. 使用有界发送队列明确返回过载；
6. 正确处理短写、断线、并发关闭和重复关闭；
7. 在成功、失败和关闭路径配平 M2 Buffer 所有权；
8. 停止并等待本组件创建的全部 goroutine；
9. 用真实回环 TCP、故障注入、竞态检测和 Benchmark 保存基线。

M5 不实现 RPC 方法、请求响应关联、Node 身份、自动重连或业务 Service。它只保证后续 RPC
需要的传输能力，不提前实现 M10。

## 2. 本里程碑边界

### 2.1 M5 包含

- TCP Listener；
- TCP 客户端单次 Dial；
- Connection 状态与地址；
- 每条连接一个 ReadLoop 和一个 WriteLoop；
- 固定四字节大端长度帧；
- 最大消息长度；
- 有界发送消息数和有界待发送字节数；
- 非阻塞发送与明确的队列过载错误；
- TCP_NODELAY、KeepAlive 和写超时；
- 连接、Listener 的幂等关闭与等待；
- 内部 BufferPool 接入；
- 连接建立、断开和关键错误日志；
- Windows、Linux、macOS 构建及 Windows、Linux 原生测试。

### 2.2 M5 不包含

- RequestID、ContractID、MethodID、ServiceName 和 NodeID；
- RPC Request、Response、Notify、Async、Await 或 Broadcast；
- Protobuf、JSON、自定义结构体编解码；
- RPC 统一错误响应；
- Node 握手、身份校验和重复连接裁决；
- 连接池、自动重连、退避重试和服务发现；
- 心跳、应用层 Ping/Pong 和空闲连接判活；
- TLS、KCP、WebSocket、QUIC 或外部 gRPC；
- 消息压缩及压缩阈值配置；
- 半关闭和发送队列排空协议；
- Application、Node、Service 或业务配置模型；
- 面向业务使用者的通用 TCP Service。

这些能力分别属于 M6、M7～M12 或后续独立系统，不能因实现方便带入 M5。

## 3. v2 参考结论

### 3.1 v2 值得保留的做法

v2 的 `network.MsgParser`、`NetConn` 和 RPC `IWriter` 证明以下边界有实际价值：

- 网络层只读取和写入字节，不解析 RPC 方法；
- 每条 TCP 连接使用独立读写循环；
- 写入先进入队列，业务 goroutine 不直接执行阻塞网络写；
- TCP 与 NATS 最终都可以向 RPC Runtime 提供一个很小的“发送消息”能力；
- 网络 Buffer 在读写完成后统一回收。

### 3.2 v2 不直接照搬的部分

v2 当前存在以下问题，M5 必须修正：

1. `IRealClient` 同时包含连接管理、RPC 调用、反射、回调、本地调用和 NATS，接口过大；
2. NATS 和本地 Client 为满足大接口实现了多个空方法；
3. `MsgParser.Write` 没有把底层短写和写错误完整返回给调用方；
4. 发送队列只按消息数量限制，无法限制大消息占用的总内存；
5. 队列满时直接关闭连接，过载和真实断线被混成同一种状态；
6. 多处默认队列达到几十万甚至上百万，最坏内存无法估算；
7. 可配置大小端、帧头长度和极大默认包长扩大了错误配置与协议测试矩阵；
8. TCP Client 在网络层内部无限重连，使停止、Context 和上层连接策略难以统一；
9. Close、队列释放和连接状态由多个路径修改，竞态与重复释放风险较高；
10. TCP/NATS 的 RPC 编解码、压缩和响应逻辑存在重复。

M5 参考 v2 的实际使用经验，不复用或迁移 v2 源码。

## 4. 总体分层

M5、M6 和后续 RPC 使用三层边界：

```text
M9 RPC Runtime
    │
    ├── M10 TCP RPC Adapter ── M5 TCP 基础库 ── net.TCPConn
    │
    └── M11 NATS RPC Adapter ─ M6 NATS 基础库 ─ nats.Conn
```

分层规则：

1. M5 公开的是 TCP 原生连接能力，不假装 NATS 也存在 TCP Connection；
2. M6 公开的是 NATS 原生发布、订阅和请求响应能力，不为兼容 TCP 增加假连接；
3. RPC 侧的小接口由 M10/M11 的使用方定义；
4. TCP Adapter 和 NATS Adapter 只转换目标、Buffer 所有权、入站来源和传输错误；
5. 序列化、RequestID、pendingCall、路由和统一 RPC 错误只实现一次，放在 RPC Runtime；
6. M5、M6 不相互依赖，也不依赖 RPC Runtime。

这样既保留 v2 `IWriter` 的窄适配思想，又避免重新建立 `IRealClient` 一类大接口。

## 5. 包与可见性

建议 M5 使用：

```text
internal/tcpnet
```

包名为：

```go
package tcpnet
```

采用内部包的原因：

- M5 当前唯一已确认用途是 Origin Native RPC；
- API 需要直接转移 `internal/bufferpool.Buffer` 所有权；
- 业务若直接依赖底层 Connection，会阻碍后续协议和关闭规则调整；
- v2 的通用 TCP/WS/KCP 业务 Service 属于后续独立适配器，不要求 M5 立即公开。

首版不建立 `transport/tcp` 公共目录，也不定义名为 `Network`、`Channel`、`Session` 的通用
大对象。M10 只在框架内部组合 `tcpnet`。

## 6. 建议 API 外观

以下 API 用于确认职责和调用外观，最终实现时允许在不改变语义的前提下微调字段排列。

### 6.1 连接配置

```go
type ConnectionOptions struct {
    Pool             *bufferpool.Pool
    Logger           log.Logger
    MaxMessageSize   int
    SendQueueFrames  int
    SendQueueBytes   int
    WriteTimeout     time.Duration
    KeepAlive        time.Duration
}

func DefaultConnectionOptions(pool *bufferpool.Pool) ConnectionOptions
```

默认值建议：

| 字段 | 默认值 | 原因 |
|---|---:|---|
| `MaxMessageSize` | `4 * 1024 * 1024` | 与已确认 RPC 最大消息 `4M` 一致 |
| `SendQueueFrames` | `1024` | 限制大量小消息 |
| `SendQueueBytes` | `8 * 1024 * 1024` | 限制大消息的最坏在途内存 |
| `WriteTimeout` | `15s` | 防止单次网络写永久占住 WriteLoop |
| `KeepAlive` | `30s` | 使用系统 TCP 保活辅助发现死连接 |
| `Logger` | Nop Logger | 测试和独立使用不依赖全局日志 |

`Pool` 必须显式传入且不能为 nil。Pool 由更高层持有，Connection 只借用，不负责关闭。

M5 的 Go Options 使用 `int` 和 `time.Duration`。未来 M7 配置模型负责把 `config.ByteSize` 和
`config.Duration` 转换为这些内部值，M5 不依赖配置加载包。

### 6.2 Listener 配置

```go
type ListenOptions struct {
    Connection     ConnectionOptions
    MaxConnections int
}

func DefaultListenOptions(pool *bufferpool.Pool) ListenOptions
```

`MaxConnections` 建议默认 `4096`。达到上限时立即关闭刚接受的连接，不创建 Connection 和
读写 goroutine。

### 6.3 事件处理器

```go
type Handler interface {
    OnOpen(conn *Conn)
    OnMessage(conn *Conn, payload []byte) error
    OnClose(conn *Conn, cause error)
}
```

语义：

- 同一连接按 `OnOpen → OnMessage... → OnClose` 顺序触发；
- `OnOpen` 在 ReadLoop 开始读消息前执行；
- 同一连接的三个回调不会并发执行；
- `OnMessage` 返回错误会关闭该连接；
- `OnClose` 恰好执行一次；
- `payload` 只在 `OnMessage` 返回前有效，处理器不得保存；
- Handler 是框架内部协议适配器，不能直接执行 Service 业务逻辑；
- Handler panic 属于框架内部故障，不按业务异常恢复继续使用连接。ReadLoop 的最外层
  goroutine 边界捕获现场、记录一次堆栈、释放资源，并以 `CodeInternal` 原因触发
  `OnClose`；M10 接入后必须据此把所属 Node 标记为失败并进入受控停止。

每条 Connection 固定绑定一个 Handler。首版不提供运行中更换 Handler 或多个监听器链。

### 6.4 Listener 和 Dial

```go
func Listen(address string, options ListenOptions, handler Handler) (*Listener, error)

func Dial(
    ctx context.Context,
    address string,
    options ConnectionOptions,
    handler Handler,
) (*Conn, error)
```

`Listen` 成功返回时端口已经绑定，AcceptLoop 已经启动。`Dial` 只尝试一次，连接 Deadline
完全由传入 Context 决定，不在 M5 内重试。

```go
func (listener *Listener) Addr() net.Addr
func (listener *Listener) Close(ctx context.Context) error

func (conn *Conn) LocalAddr() net.Addr
func (conn *Conn) RemoteAddr() net.Addr
func (conn *Conn) Send(buffer *bufferpool.Buffer) error
func (conn *Conn) Close()
func (conn *Conn) Wait(ctx context.Context) error
```

`Conn.Close` 只发起幂等关闭，不能等待自身 ReadLoop，否则从 `OnMessage` 内关闭会死锁。
需要等待资源全部退出时显式调用 `Wait`。`Listener.Close(ctx)` 关闭监听和全部所属连接并
等待退出；Context 到期后返回错误，但不重新打开资源。

## 7. 长度帧协议

M5 固定使用：

```text
+----------------------+----------------------+
| payload_length uint32| payload              |
| big endian, 4 bytes  | payload_length bytes |
+----------------------+----------------------+
```

规则：

1. 长度字段表示 payload 长度，不包含四字节帧头；
2. 网络字节序固定为 Big Endian；
3. 帧头固定四字节，不提供一、二、四字节配置；
4. payload 长度必须大于零；
5. payload 长度不能超过 `MaxMessageSize`；
6. `MaxMessageSize` 不能大于 `math.MaxUint32`；
7. 长度校验必须发生在取得完整 payload Buffer 之前；
8. 非法长度立即关闭连接，不尝试跳过当前帧继续解析；
9. 半帧后 EOF、超时和其他 I/O 错误关闭连接；
10. 一个 TCP 包可以包含多帧，一帧也可以拆成多个 TCP 包，读取统一使用
    `io.ReadFull`，不能依赖单次 `Read` 的边界。

固定协议比 v2 的可变帧头和可变大小端更容易跨平台验证，也避免两个 Node 因配置不一致
产生无法定位的解析错误。

M5 帧内没有版本、压缩标志、RequestID 或错误码。它们属于 M10 RPC payload 的线协议。

## 8. Buffer 所有权

### 8.1 接收

```text
ReadLoop
  → 栈上 [4]byte 读取长度
  → 校验长度
  → Pool.Acquire(payloadLength)
  → io.ReadFull
  → OnMessage(conn, buffer.Bytes())
  → buffer.Release()
```

约束：

- ReadLoop 始终拥有接收 Buffer；
- `OnMessage` 只是同步借用 `[]byte`；
- `OnMessage` 返回成功、错误或连接关闭都由 ReadLoop 释放；
- M10 必须在回调返回前完成 RPC 帧头解析及 Protobuf 解码；
- 解码结果不能引用输入 Buffer；
- Service 永远看不到网络 Buffer，也不负责释放。

该规则不使用引用计数，能够避免业务保存 payload 后导致池化内存泄漏或 ABA。

### 8.2 发送

建议把 RPC payload Buffer 直接转移给 Connection：

```text
RPC Encoder
  → Pool.Acquire(payloadLength)
  → 直接编码到 buffer.Bytes()
  → Conn.Send(buffer)
  → WriteLoop 写 [4]byte 长度头和 payload
  → buffer.Release()
```

WriteLoop 的队列项使用值类型保存四字节帧头、Buffer 指针和记账长度。底层通过
`net.Buffers` 或等价的完整写入循环发送帧头和 payload，正确处理短写，同时避免为了 TCP
帧头再复制一份完整 payload。

所有权规则：

1. `Send` 成功后，Buffer 所有权转移给 Connection，调用方立即失去所有权；
2. `Send` 返回错误时没有转移，调用方仍负责 Release；
3. WriteLoop 是成功入队 Buffer 的唯一释放者；
4. 完整写入、写失败和连接关闭均释放一次；
5. 关闭时尚未发送的队列项全部释放；
6. RPC 取消不能回收已经成功入队的 Buffer；
7. `Send` 不接受 nil 或长度为零的 Buffer；传入已释放 Buffer 属于框架内部违反所有权
   不变量，沿用 M2 `Buffer.Bytes()` 的 panic 规则；
8. payload 超过最大消息长度时拒绝入队，所有权仍归调用方。

这一做法相对 M2 储备文档中的“完整 TCP 帧 Buffer”有一处优化：Buffer 只保存 RPC payload，
四字节长度头保存在预分配队列项中。它减少一次完整消息复制，也让同一 RPC payload
Buffer 可以由 TCP Adapter 或 NATS Adapter 接管。开发者确认 M5 后，应同步修订 M2 详细
设计中的发送路径表述。

## 9. 每条连接的 goroutine 模型

每条 Connection 固定创建两个 goroutine：

1. ReadLoop：顺序读取帧、执行内部 Handler 并最终负责触发 `OnClose`；
2. WriteLoop：顺序取得发送队列项、完整写入并释放 Buffer。

不为每个消息创建 goroutine，也不为连接额外常驻一个协调 goroutine。

关闭协调：

```text
任一方发现错误或调用 Close
  → sync.Once 提交关闭原因
  → 标记不再接收 Send
  → 关闭底层 net.Conn，打断正在阻塞的 Read/Write
  → 通知 WriteLoop 停止并释放队列
  → ReadLoop 等待 WriteLoop 退出
  → OnClose
  → 关闭 Connection done
```

关键不变量：

- 关闭状态只提交一次；
- 第一个有效关闭原因保留，后续错误不覆盖；
- Send 状态检查、队列写入和关闭提交必须消除 `send on closed channel` 竞态；
- 发送 channel 不由多个路径关闭；
- `OnClose` 不在持有 Connection 或 Listener 锁时执行；
- `Wait` 只等待 done，不取得其他资源锁；
- 连接所有 goroutine 都能通过关闭底层 `net.Conn` 被唤醒。
- ReadLoop 和 WriteLoop 各自在最外层安装一次框架 goroutine 恢复边界；恢复边界只负责
  保存现场、提交 `CodeInternal`、释放资源并通知上层失败，不把 panic 后的连接恢复运行。

## 10. 有界发送与背压

发送队列同时限制：

- 待发送帧数量；
- 待发送 payload 总字节数。

两个限制缺一不可：

- 只限制数量时，少量 `4M` 消息仍可能占用大量内存；
- 只限制字节时，海量极小消息仍可能占用大量队列槽和对象。

`Send` 使用非阻塞准入：

1. 先校验连接状态和 Buffer；
2. 在同一同步边界内检查帧数与字节额度；
3. 两项都有容量才入队并转移所有权；
4. 任一项不足立即返回 `CodeTransportOverloaded`；
5. 不等待队列、不丢弃旧消息、不静默丢新消息，也不因队满关闭连接。

RPC Runtime 收到过载后立即完成本次调用，不把本地队列等待时间隐藏在网络层。需要业务
重试时由更高层结合幂等性、Deadline 和路由决定，M5 不自动重试。

## 11. TCP 参数

首版固定：

- TCP_NODELAY：开启，降低小 RPC 帧等待；
- KeepAlive：默认开启，周期使用 Options；
- Linger：不主动设置为零，避免正常 Close 强制发送 RST；
- ReadDeadline：不设置；
- WriteDeadline：每个队列项开始写前设置 `now + WriteTimeout`；
- Dial：使用 Context；
- Listen：使用 `net.ListenConfig`；
- 地址：交给 Go `net` 包解析，支持 IPv4 与 IPv6。

M5 不设置 ReadDeadline，是因为空闲但健康的 Node 连接可能长期没有业务数据，而 M5 尚未
实现应用层心跳。M10 的握手和后续连接管理将决定心跳及失活策略，不能把 RPC 默认 `15s`
超时误当成连接空闲超时。

## 12. Listener 生命周期

Listener 持有：

- 底层 `net.Listener`；
- AcceptLoop；
- 当前 Connection 集合；
- 最大连接数；
- Handler、Options 和 Logger；
- 关闭状态与等待信号。

AcceptLoop 规则：

1. 临时 Accept 错误使用 `5ms` 起、最多 `1s` 的有界指数退避；
2. Listener 已关闭时直接退出，不记录误导性 Error；
3. 达到连接上限时关闭新 socket，不创建 goroutine；
4. 接受成功后设置 TCP 参数，再加入集合并启动 Connection；
5. 任一步失败按逆序关闭 socket 并移除登记；
6. Connection `OnClose` 完成后从 Listener 集合移除；
7. `Listener.Close(ctx)` 先停止 Accept，再关闭连接，最后等待集合清空。

Accept 的临时错误只在同一次连续失败期间退避；成功接受连接后重置退避。M5 不无限重试
Dial，Accept 退避只用于仍然有效的 Listener。

## 13. 关闭语义

M5 使用立即传输关闭，不提供“发送队列 Drain 后再关闭”：

- `Conn.Close()` 立即拒绝新 Send；
- 正在写的系统调用由关闭底层连接打断；
- 尚未写完和尚未开始的队列 Buffer 被释放；
- `Listener.Close(ctx)` 关闭全部 Connection 并等待；
- Context 到期只结束调用方等待，不把组件重新标记为运行；
- 多次 Close 返回相同终态，不 panic；
- TCP 半关闭统一按完整连接关闭处理。

上层优雅停止会先停止 RPC 准入并排空 pendingCall，再关闭 Transport。此时正常情况下发送
队列已经为空。M5 再增加 Drain 会引入第二套 Deadline、半关闭和失败裁决，首版收益不足。

## 14. 错误码

M5 建议在统一 `errs` 包增加以下 Transport 通用错误码，M6 后续复用相同语义：

| 错误码 | 建议值 | 语义 |
|---|---:|---|
| `CodeTransportUnavailable` | `3001` | Dial、Accept 或底层 I/O 使传输不可用 |
| `CodeTransportClosed` | `3002` | 本地组件已经关闭，不能再执行操作 |
| `CodeTransportOverloaded` | `3003` | 连接数、发送帧数或待发送字节达到上限 |
| `CodeTransportProtocol` | `3004` | 远端长度帧非法或违反传输协议 |
| `CodeTransportMessageTooLarge` | `3005` | 本地发送或远端声明的消息超过上限 |

规则：

- 本地 error 保留底层 cause，方便 `errors.Is` 和日志定位；
- 不把远端地址、凭证或消息内容写入稳定 Message；
- `io.EOF`、`net.ErrClosed` 和 Context 错误按发生阶段映射；
- RPC Adapter 在 M10/M11 把这些错误完成到 pendingCall；
- M5 不生成 RPC Response，也不决定某个错误是否可重试。

## 15. 与 M6 和 RPC Adapter 的衔接

### 15.1 不建立共同的大基础接口

TCP 的核心对象是 Connection，NATS 的核心对象是 Subject 和 Subscription。以下行为无法在
不制造假概念的情况下完全统一：

- TCP 逐连接读写，NATS 通过 Broker 发布订阅；
- TCP Send 成功表示进入本地队列，NATS Publish 成功表示交给 NATS Client；
- TCP 需要最大连接数，NATS 需要订阅和重连状态；
- NATS 原生 Request/Reply 不等于 Origin RequestID/pendingCall。

因此 M5 不提前定义要求 M6 实现的 `Connection`、`Listen` 或 `Request` 大接口。

### 15.2 RPC 使用方定义最小热路径接口

M10/M11 实现时，RPC Runtime 只定义自己真正需要的小接口，建议外观：

```go
type sender interface {
    Send(
        ctx context.Context,
        target Target,
        packet *bufferpool.Buffer,
    ) error
}
```

其中：

- `Target`、NodeID 和寻址属于 RPC/连接管理层，不进入 M5/M6；
- `packet` 只包含 Origin RPC payload，不包含 TCP 长度头或 NATS Subject；
- Send 成功后 Adapter 取得 Buffer 所有权；
- Send 失败时所有权仍归 RPC Runtime；
- TCP Adapter 选择目标 Connection 并调用 `Conn.Send`；
- NATS Adapter 生成 Subject、Publish，并在底层已接管或复制数据后释放 Buffer；
- 入站 Adapter 把来源和借用的 payload 同步交给同一 RPC 解码入口。

首版共同接口只统一发送热路径，不强行统一 Start、Subscribe、Reconnect、Connection Event
和 Close。它们由 Node 分别持有具体 Adapter 并按真实生命周期管理。若 M10/M11 实现证明
还需要第二个共同方法，再在使用方增加，不提前猜测。

### 15.3 避免重复逻辑

以下内容只能有一份实现：

| 内容 | 唯一归属 |
|---|---|
| RPC 序列化和线协议 | M9/M10 RPC Runtime |
| RequestID 与 pendingCall | M10/M11 RPC Runtime |
| 默认 `15s` Deadline | RPC Runtime |
| TCP 长度帧 | M5 TCP |
| NATS Subject 和订阅 | M6/M11 NATS Adapter |
| NodeID 到连接或 Subject 的映射 | M10/M11 Adapter |
| Transport 错误到 RPC 完成 | M10/M11 Adapter |

TCP 和 NATS 不分别复制一次 RPC Client、代码生成或业务调用 API。

## 16. 日志与可观测性

M5 通过注入的 `log.Logger` 记录低频生命周期和异常：

- Listen 成功与失败；
- Dial 成功与失败；
- 连接建立与断开；
- 非法帧；
- 队列过载；
- Accept 临时错误；
- 关闭超时和未完成 goroutine。

正常的每帧收发不写日志。日志字段只保存稳定的小字段，如本地地址、远端地址、队列水位
和错误码，不记录 payload。

连接状态、累计字节、队列高水位等长期指标不在 M5 建立完整 Metrics 系统。Benchmark 和
测试可以读取内部测试快照；正式监控接口在出现明确监控里程碑时设计。

## 17. 测试设计

### 17.1 单元测试

至少覆盖：

- Options 默认值和全部非法组合；
- 空地址、nil Pool、nil Handler；
- `0`、`1`、最大值、最大值加一和 `uint32` 溢出长度；
- 大端帧头；
- 帧头和消息体的逐字节短读；
- 短写、多次短写、写零字节和写错误；
- Send 成功与失败时的 Buffer 所有权；
- 队列帧数满、字节数满以及释放后恢复额度；
- Close 与 Send 并发；
- 重复 Close、重复 Wait 和 Context 取消；
- OnOpen、OnMessage、OnClose 顺序及恰好一次；
- Handler 返回错误；
- Handler、ReadLoop 和 WriteLoop 最外层 panic 后的关闭、堆栈和失败通知；
- ReadLoop 与 WriteLoop 分别先失败；
- Listener 达到连接上限；
- Accept 部分初始化失败和逆序清理；
- 所有接收、发送和关闭错误路径的 Buffer 统计归零。

短读写和系统错误使用内部可控 `net.Conn` 测试替身，不为覆盖率向生产 API 增加通用注入
接口。

### 17.2 Fuzz

Fuzz 固定帧解析：

- 任意四字节长度；
- 截断帧头；
- 截断 payload；
- 多帧粘连；
- 恰好最大值和越界值；
- 输入不得 panic；
- 非法输入不得在校验前申请声明长度的 Buffer。

### 17.3 真实集成测试

使用 `127.0.0.1:0` 建立真实 Listener 和 Dial，验证：

- 双向多帧收发；
- 并发多连接；
- 消息顺序；
- 对端断开；
- 本地关闭；
- 大消息；
- 队列过载后连接仍可继续使用；
- Listener 关闭后全部连接和 goroutine 退出；
- BufferPool 开启统计后最终归零。

M5 不创建可手工运行的 `main` 测试程序。跨包集成测试放在
`tests/integration/tcpnet`；简单测试与源码同目录。只有必须跨进程验证时才在
`tests/helpers` 增加专用辅助程序。

### 17.4 质量门禁

提交前至少执行：

```text
gofmt
go vet ./...
go test ./...
go test -count=20 ./internal/tcpnet/...
go test -count=10 ./tests/integration/tcpnet/...
go test -race ./...
Windows 原生构建与测试
Linux 原生构建与测试
macOS amd64/arm64 交叉构建
逐函数覆盖率检查
```

测试必须频繁执行并尽量覆盖所有稳定可达路径。无法稳定制造的内核级故障需要记录原因和
剩余风险，不能为了覆盖率引入复杂生产抽象。

## 18. Benchmark 与性能验证

M5 保存以下基线：

- 小消息 `64B`、典型消息 `1KB`、中消息 `16KB`、大消息 `1M`；
- 单连接串行往返；
- 单连接并发发送；
- 多连接并发；
- 每次 Send 的 `allocs/op` 与 `B/op`；
- 启用和关闭 Buffer 使用量统计的差异；
- p50、p95、p99 延迟；
- 吞吐量；
- 队列接近满载时的延迟；
- 突发流量结束后的 Buffer 未归还和堆滞留；
- `net.Buffers` 帧头加 payload 与复制完整帧两种方案的对照。

首版先用数据确认 `net.Buffers` 是否在 Windows 和 Linux 上稳定减少复制且不明显增加
复杂度。如果收益不成立，则按照开发原则与开发者确认后改用一次完整帧复制，不能只凭
推测保留更复杂路径。

M5 不预先写死绝对 QPS 或 p99 门槛。验收保存可复现环境、命令和基线，M10 再记录带 RPC
编解码的端到端指标。

## 19. 实施顺序

设计确认后，M5 实施计划建议按以下最小步骤执行：

1. 增加 Transport 错误码和 ConnectionOptions 校验；
2. 实现长度帧读写与可控短读写测试；
3. 实现 Connection、Buffer 所有权和有界发送；
4. 实现 Listener、Dial 和真实回环测试；
5. 完成关闭、并发、泄漏和故障路径；
6. 增加 Fuzz、Benchmark、覆盖率审计和跨平台验证；
7. 回写实施结果并提交 M5。

在本设计通过 Review 前，不创建 M5 实施计划，不编写 `internal/tcpnet` 生产代码。

## 20. 开工前待确认

建议开发者逐项确认：

1. M5 使用内部包 `internal/tcpnet`，暂不作为业务公共 TCP 库；
2. 固定四字节 Big Endian 长度头，不提供大小端和帧头长度配置；
3. payload 必须非空，默认最大 `4M`；
4. 每连接两个 goroutine，不为每消息创建 goroutine；
5. 发送同时限制 `1024` 帧和 `8M` payload；
6. 队列满返回过载，不关闭连接、不阻塞等待、不丢日志式静默处理；
7. `Conn.Close` 发起关闭，`Wait(ctx)` 负责等待；
8. Listener 关闭立即停止传输，不在 M5 增加 Drain；
9. TCP_NODELAY 固定开启，KeepAlive 默认 `30s`，WriteTimeout 默认 `15s`；
10. M5 不设置 ReadDeadline，心跳与判活留到 M10；
11. 发送 Buffer 只保存 RPC payload，WriteLoop 使用独立四字节头，确认后同步修订 M2
    储备文档；
12. Transport 使用 `3001`～`3005` 五个轻量统一错误码；
13. RPC Adapter 由 M10/M11 使用方定义最小 Send 接口，M5/M6 不实现共同大接口；
14. M5 不自动重连，不包含 Node 握手、NodeID 或 RPC 语义；
15. Benchmark 对比 scatter/gather 和完整帧复制，再决定最终发送实现。

全部确认后，才能把复核清单更新为“允许实施”并生成 M5 实施计划。
