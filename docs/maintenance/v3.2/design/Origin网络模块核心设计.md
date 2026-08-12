# Origin 网络模块核心设计

> 状态：TCP、WebSocket、KCP 纵向切片已实施并通过双平台验收
> 基线：v3.1.0 发布候选
> 目标：v3.2.0
> 兼容性：不兼容 v2 `sysmodule/netmodule` 外观；不改变 v3.1 已冻结外观
> 上位材料：[`Origin 网络系统模块能力分析与设计提案`](../proposals/Origin网络系统模块能力分析与设计提案.md)、
> [`Origin 网络事件与自定义协议扩展调研`](../proposals/Origin网络事件与自定义协议扩展调研.md)

## 1. 文档定位与决策原则

本文是网络模块实现、测试和教程的单一核心设计。两份上位材料继续保留能力分析和外部调研，
具体实现不得从调研材料中任选另一套方案；2026-08-10 开发者已确认第 13 节全部推荐结论。

决策遵循以下顺序：

1. 先保证连接生命周期、消息顺序、所有权、容量和停止行为正确；
2. 只统一 TCP、KCP、WebSocket 真实共有的 Session 与 Handler 语义；
3. Raw 是传输核心，PB、JSON 和自定义 Codec 是可选协议适配，不反向污染传输；
4. 优先复用 v3 已验证的 Buffer、环形队列、Service Scheduler 和生命周期能力；
5. 只加入当前 Server、Client、Dialer 和标准协议真正需要的扩展点；
6. 性能设计覆盖热路径的分配、复制和调度次数，但优化必须有 Benchmark/Profile 复验；
7. 不以“不兼容 v2”为理由保留旧接口，也不以“未来可能需要”为理由预建框架。

“最优”不是功能最多或理论上零复制，而是在当前需求下同时满足：安全默认、调用简单、资源有界、
热路径可预测、实现可以完整测试，并且后续扩展不需要破坏核心所有权契约。

## 2. 本轮 Review 的精简结论

| 原建议 | 复核结论 | 原因 |
| --- | --- | --- |
| 公开动态或冻结 Middleware 链 | 首批不提供 | Handler 包装即可组合日志、鉴权和业务限流，不需要第二套 Pipeline |
| 每 Session `CodecFactory` | 首批不提供 | 标准和首批自定义编码均可无状态共享；Factory 会增加对象和生命周期 |
| 通用 Admission Hook | 首批不提供 | WebSocket Origin/TLS 等在传输专属 Options 解决；登录鉴权由 OnOpen/首条消息处理 |
| 独立公开 Drain API | 首批不提供 | Module `OnStop(ctx)` 的截止时间已经能够表达停止接入、排空和强制关闭 |
| `SendMany/Broadcast` | 首批不提供 | 使用者循环 `Send` 已能正确工作；编码共享需要引用计数，先用数据证明必要性 |
| 内置 Token Bucket | 首批不提供 | 单帧、每 Session pending 和 Scheduler 上限先形成硬防线，避免过早增加计时状态 |
| 消息优先级、丢弃和合并策略 | 首批不提供 | 可靠性属于业务语义，传输层不能猜测哪些消息允许丢失 |
| 自定义 Framer/动态 Codec | 首批不提供 | 直接接触不可信字节流且显著扩大 Fuzz、生命周期和内存边界 |
| 公共 Buffer Pool/引用计数 | 明确不提供 | 容易产生悬挂引用、二次释放和跨连接数据泄漏 |

保留的首批能力是：统一 Session/Handler、Server/Client/Dialer、Raw/PB/JSON、自定义无状态 Codec、
结构化关闭原因、可写状态、Client 重连状态、双维有界容量、完整统计和确定性停止。

## 3. 分层与包边界

```text
sysmodule/network/                 Session、Handler、公共错误、关闭原因和统计快照
sysmodule/network/protocol/        MessageID、Codec、Encoder、Router 与注册辅助
sysmodule/network/protocol/pb/     Protobuf Codec
sysmodule/network/protocol/json/   JSON Codec
sysmodule/network/tcp/             TCP Server/Client/Dialer 与长度帧
sysmodule/network/websocket/       WebSocket Server/Client/Dialer
sysmodule/network/kcp/             KCP Server/Client/Dialer
internal/...                       Buffer、队列、连接状态机和具体 I/O 实现
```

公共包不声明包含所有后端参数的总 Options，也不为了运行期替换后端预先声明大而全的
`Server`/`Client` 接口。三个传输提供名称和语义一致的具体 Module、Client 和 Dialer；业务通过
共同的 `Session` 和 `Handler` 复用，切换传输时通常只替换构造器和传输专属 Options。

Gin/HTTP 不进入该结构。它继续使用请求/响应、路由和中间件模型，另立设计。

三个新传输共享一个内部 Session Runtime，它只拥有 Session 状态、Module 容量预算、Service 投递
和关闭顺序；每个传输适配器只实现唯一出站队列、逻辑消息读写、地址/握手信息和底层关闭。该内部
边界不公开，也不允许传输绕过 Runtime 另建自己的业务队列或回调路径。

v3 现有 `internal/tcpnet.Conn` 已服务 RPC。TCP Module 不在它外面再叠一条队列；本切片只对其真实
共有能力做小步增强：Big/Little Endian、惰性 Ring、消息/字节双维上限、共享总预算和高低水位。
RPC Wire Format 与公开外观不变，修改后必须重新运行全部 RPC/Discovery 测试。网络 Session Runtime
不与 RPC Runtime 合并，避免把业务 Handler、重连或 Session 外观反向带入 RPC Transport。

### 3.1 已冻结的公共 API 形状

以下代码冻结首批名称、职责和调用方向；实现可以增加不导出的辅助类型，但不得自行扩大公共面：

```go
package network

type SessionID uint64

type Transport uint8

const (
	TransportTCP Transport = iota + 1
	TransportWebSocket
	TransportKCP
)

type ByteOrder uint8

const (
	BigEndian ByteOrder = iota + 1
	LittleEndian
)

type Session interface {
	ID() SessionID
	Transport() Transport
	LocalAddr() net.Addr
	RemoteAddr() net.Addr
	Context() context.Context
	Done() <-chan struct{}
	Send([]byte) error
	Close(error)
	Writable() bool
	Cause() error
	Stats() SessionStats
}

type Handler interface {
	OnOpen(context.Context, Session) error
	OnMessage(context.Context, Session, []byte) error
	OnWritableChanged(context.Context, Session, bool)
	OnClose(context.Context, Session, error)
}

type HandlerFuncs struct {
	Open            func(context.Context, Session) error
	Message         func(context.Context, Session, []byte) error
	WritableChanged func(context.Context, Session, bool)
	Close           func(context.Context, Session, error)
}

type EndpointOptions struct {
	Handler                 Handler
	MaxSessions             int
	MaxMessageSize          int
	ReceivePendingMessages  int
	ReceivePendingSize      int64
	ReceivePendingTotalSize int64
	SendQueueMessages       int
	SendQueueSize           int64
	SendQueueTotalSize      int64
	ReadIdleTimeout         time.Duration
	WriteTimeout            time.Duration
	SlowClientTimeout       time.Duration
}

func DefaultEndpointOptions(handler Handler) EndpointOptions
func (EndpointOptions) Validate() error
```

`HandlerFuncs` 实现 `Handler`，nil 字段为安全空操作。`EndpointOptions` 是三个传输唯一共享的配置；
地址、帧、TLS、KCP 和 WebSocket 参数不得加入该结构。`SessionStats`、`EndpointStats` 和 Client
状态只包含固定数值/枚举快照，不返回内部 Map、Buffer 或可变集合。

协议包冻结为：

```go
package protocol

type MessageID uint16
type Message struct {
	ID    MessageID
	Value any
}

type Resolver interface {
	New(MessageID) (any, bool)
}

type Codec interface {
	Decode([]byte, Resolver) (Message, error)
	Encode(*Encoder, MessageID, any) error
}

type RouterOptions struct {
	Codec           Codec
	Open            func(context.Context, network.Session) error
	WritableChanged func(context.Context, network.Session, bool)
	Close           func(context.Context, network.Session, error)
	Unknown         func(context.Context, network.Session, MessageID, []byte) error
}

func NewRouter(RouterOptions) (*Router, error)
func Register[T any](*Router, MessageID,
	func(context.Context, network.Session, *T) error) error
func (r *Router) Send(network.Session, MessageID, any) error
```

`Encoder` 只提供有界 `Len`、`Append`、`AppendByte` 和 `Reserve`；它不能由使用者构造，也不能返回
底层 Pool。Router 在网络 Module `OnInit` 时自动冻结，之后 `Register` 返回无效状态错误。
`protocol/pb.NewCodec` 接收协议端序，`protocol/json.NewCodec` 使用固定 `id/data` Envelope。

TCP 首批外观冻结为：

```go
package tcp

type FrameOptions struct {
	LengthFieldSize int
	ByteOrder       network.ByteOrder
}

type ServerOptions struct {
	Network   network.EndpointOptions
	Frame     FrameOptions
	KeepAlive time.Duration
}

func DefaultServerOptions(network.Handler) ServerOptions
func NewServer(string, ServerOptions) (*Server, error)

type DialOptions struct {
	Network   network.EndpointOptions
	Frame     FrameOptions
	KeepAlive time.Duration
}

func DefaultDialOptions(network.Handler) DialOptions
func NewDialer(string, DialOptions) (*Dialer, error)
func (d *Dialer) Dial(context.Context, service.IService) (network.Session, error)

type ReconnectOptions struct {
	Enabled      bool
	MaxAttempts  int
	InitialDelay time.Duration
	MaxDelay     time.Duration
	Jitter       float64
}

type ClientOptions struct {
	Dial       DialOptions
	Reconnect  ReconnectOptions
	StateChange func(context.Context, network.ClientStateSnapshot)
}

func DefaultClientOptions(network.Handler) ClientOptions
func NewClient(string, ClientOptions) (*Client, error)
```

`Server` 和 `Client` 嵌入 `service.Module`，只能通过 `Service.AddModule` 管理。Server 提供 `Addr`、
`Session`、`SessionCount`、`CloseSession`、`Stats`；Client 提供 `Session`、`State`、`Stats`。Dialer
只进行一次连接尝试，不启动重试。托管 Client 的重试次数必须有界，默认关闭自动重连；开启时
默认最多 10 次，使用 200ms 起始、5s 上限和 20% 抖动。

## 4. 核心数据流

```text
入站 I/O goroutine
  Socket → Framer → 完整 Raw Buffer → Session pending 额度 → Service Scheduler
                                                          ↓
Service 串行上下文
  Raw Handler，或 Router → Codec.Decode → 类型 Handler → 自动释放 Raw Buffer/pending 额度

出站调用方
  Raw 安全复制，或 Codec.Encode → 框架拥有的 Payload → Session Send Queue → Writer → Socket
```

入站不再建立一个 v2 风格的“每连接 Channel + Service Scheduler”双重消息队列。Read Loop 取得完整
消息后只做帧校验和 pending 额度预留，随后直接提交一个 Service Task。协议解码和业务处理在该
Task 中连续完成，因此：

- 同一 Session 的消息顺序自然继承单 Read Loop 和 Scheduler FIFO；
- 等待处理的对象仍是有明确字节大小的 Raw Buffer，不提前堆积不可估算的 PB/JSON 对象；
- 省去一次队列跳转、Channel 容量和额外唤醒；
- Codec 与 Handler 都在 Service 串行上下文执行，首批自定义 Codec 不需要为 Decode 自建并发状态。

帧长、空帧和最大消息校验仍在 I/O 边界完成；格式、消息 ID 和业务结构错误由 Service 中的
Codec/Router 处理，并形成结构化协议关闭原因。

## 5. Session 与事件契约

### 5.1 最小公共 Session

最终方法名在正式设计确认时冻结，但公共语义只包含：

- Module 作用域内稳定、非零且不复用到活动连接的数值 ID；
- 传输类型、本地地址、远端地址和只读握手信息；
- `Context`/`Done`，Session 关闭时取消；
- 并发安全的 `Send([]byte)`、`Close(error)`、`Writable()` 和最终 `Cause()`；
- 只读统计快照。

不提供通用可变属性 Map。玩家、账号和场景绑定由所属 Service 使用 SessionID 建立自己的状态。
不承诺 `Send` 成功代表对端已经收到或处理，只表示消息已被本地有界发送队列接受。

### 5.2 强类型 Handler

传输层 Handler 处理 Raw 逻辑消息：

```go
type Handler interface {
	OnOpen(context.Context, Session) error
	OnMessage(context.Context, Session, []byte) error
	OnWritableChanged(context.Context, Session, bool)
	OnClose(context.Context, Session, error)
}
```

代码仅表达契约，尚不是已冻结 API。框架同时提供 `HandlerFuncs`，空函数字段使用安全默认值。
PB/JSON Router 实现该 Handler 并把 `OnMessage` 转成类型安全的协议 Handler；使用者不需要手工
解析 Raw 字节。

事件规则：

1. 同一 Session 始终是 `Open → (Message | WritableChanged)* → Close`；
2. `OnOpen` 成功前不读取第一条业务消息，返回错误立即关闭；
3. `OnMessage` 返回错误只关闭当前 Session；
4. `OnWritableChanged` 只在高/低水位状态真正变化时通知，使用者仍应以 `Send` 返回值为最终裁决；
5. `OnClose` 恰好一次且始终最后，不返回错误；
6. 全部业务回调在所属 Service 串行上下文运行；回调 panic 被恢复并形成明确关闭原因；
7. Module 必须证明 Scheduler 满、Service 停止和并发 Close 时仍能完成最终 Close，不允许改到
   I/O goroutine 直接回调来规避调度问题。

横切行为通过普通 Handler 包装器组合。首批不建立 Origin 专属 Middleware 接口、动态 Pipeline
或第二套网络 Event Bus。

### 5.3 Client 与 Dialer

- Dialer 只执行一次受 `context.Context` 约束的连接尝试；
- Dialer 只接受 Running/Retired owner，返回的 Session 必须由调用方在 owner 停止前关闭；需要自动
  停止或重连时使用托管 Client；
- 托管 Client 是 `service.IModule`，可配置重连，并暴露 `Connecting`、`Connected`、
  `Reconnecting`、`Stopped` 状态；
- 重连使用单一所有者、指数退避、上限、抖动和可取消 Timer，不累积 goroutine 或连接；
- 每次建连产生新 Session 和新 ID，不复活已经 Close 的 Session；
- Server 与 Client 建立的 Session 使用相同发送、回调、容量和关闭契约；
- 服务调用自己的 RPC/网络入口必须作为共同契约测试：同进程回环、同主机 TCP 和 Ubuntu
  环境均验证，不能因 Client 与 Server 共属一个 Service 而死锁或破坏顺序。

## 6. 协议、Router 与自定义 Codec

### 6.1 Raw 是稳定核心

TCP/KCP Framer 和 WebSocket 原生消息边界最终都向 Handler 交付一条完整 Raw 消息。只需要自定义
Wire Format 或自己分发消息的使用者可以直接使用 Raw Handler，不需要创建伪 Codec。

### 6.2 Codec 的最小职责

首批 Codec 只负责“完整 Raw 消息”和“MessageID + Value”的双向转换。概念接口为：

```go
type Codec interface {
	Decode(frame []byte, resolver Resolver) (Message, error)
	Encode(dst *Encoder, id MessageID, value any) error
}
```

`Resolver` 是 Router 的冻结只读类型解析能力；Codec 读取 ID 后通过它创建目标对象。`Encoder` 是
框架拥有的有界追加写入器，不暴露 Pool，也不能在调用返回后继续使用。这样只需两个 Codec 方法，
又避免 Codec 与 Router 各保存一份注册表。

约束如下：

- Codec 在 Module 启动前安装并冻结，首批必须无状态或只读，允许被并发 Encode；
- Decode 在 Service 串行上下文执行；Encode 可能由允许并发 Send 的调用方执行，因此自定义实现
  不能保存本次调用的临时状态；
- Codec 不得保存入站 `frame` 或 `Encoder` 的写入视图；返回的 `Message.Value` 也不得引用
  `frame` 的底层数组。确实需要借用 Raw 字节时直接使用 Raw Handler，其生命周期明确到当前回调；
- 输入和输出都必须经过统一最大消息限制，Codec 不能绕过容量；
- Decode/Encode panic 均被恢复：Decode 关闭当前 Session，Encode 向调用者返回错误；
- 需要每 Session 协商状态、动态切换或压缩字典时另立真实需求，不在首批加入 `CodecFactory`。

### 6.3 Router

Router 维护 `MessageID → 对象工厂 + 类型 Handler` 的冻结表：

- MessageID 使用 `uint16`，`0` 保留为非法值；
- 注册只发生在 Module `OnInit`，重复 ID、nil 工厂、nil Handler 和类型不一致使启动失败；
- 未知 ID 默认返回协议错误并关闭 Session；可显式设置一个 Unknown Handler；
- PB/JSON 提供泛型注册辅助，使业务 Handler 直接接收具体消息指针；
- 运行期只读，不使用反射查找、字符串方法名或动态注册；
- Router 只分发消息，不管理 Listener、Session、重连、队列或 goroutine。

### 6.4 内置 Wire Format

**Protobuf**

```text
+--------------------+----------------------+
| MessageID: uint16  | protobuf payload     |
+--------------------+----------------------+
```

- MessageID 默认 Big Endian，可独立选择 Little Endian；该选项与 TCP/KCP 长度帧端序不是同一项；
- Payload 使用 `google.golang.org/protobuf/proto`；
- Encoder 预估 `proto.Size` 后使用 append 风格编码，避免先 Marshal 再复制到发送 Buffer；
- 空 protobuf Payload 合法，但 MessageID 必须非零且已注册。

**JSON**

```json
{"id":1001,"data":{"name":"player-1"}}
```

- 顶层字段固定为数值 `id` 和 `data`，ID 范围与 Protobuf 一致；
- 使用稳定的标准库 `encoding/json`，不把实验性 `encoding/json/v2` 作为 v3.2 公共行为基础；
- `json.Marshal` 返回调用方独占 Slice，框架可直接接管该结果入队，不做第二次防御性复制；
- Decode 先取得 ID 和 `json.RawMessage`，再按 Router 注册类型解码 `data`；
- 遵循标准库的未知字段行为；不为重复字段检测自建 JSON Parser。大小上限在解析前执行。

PB、JSON 都必须保存跨 Server/Client、Big/Little Endian（适用时）和异常输入 Golden Test。

## 7. 内存池设计

### 7.1 结论

使用内存池，但只池化框架内部、所有权清晰的临时字节 Buffer；不沿用 v2 的公开手工回收池，也不
把“使用 Pool”当成内存上限。

首批直接复用 v3 `internal/bufferpool`：

- 每个网络 Module 明确拥有一个 Pool，不使用包级全局 Pool；
- 16 B～64 KiB 使用 2 次幂分档 `sync.Pool`；超过 64 KiB 按实际长度分配，释放后交给 GC；
- `sync.Pool` 中对象随时可能被运行时回收，只用于降低临时分配，不承担缓存命中或容量正确性；
- 生产默认关闭逐 Buffer 原子统计；测试和诊断构造可开启用量跟踪，验证取得/释放配平；
- Release 不清零整块内存。框架只暴露已经完整写入的有效长度，任何失败路径都不得提交未初始化区；
- 不池化 Session、Codec、Router、队列节点、PB/JSON 对象和业务消息。后续只有 Profile 显示为
  稳定热点且所有权可以证明时，才为具体对象单独设计。

该边界与 Go 官方 `sync.Pool` 的定位一致，也接近 gRPC-Go 对分档 Buffer 与明确释放责任的处理：
[Go sync.Pool](https://pkg.go.dev/sync#Pool)、
[Go GC 指南](https://go.dev/doc/gc-guide)、
[gRPC-Go mem](https://pkg.go.dev/google.golang.org/grpc/mem)。

### 7.2 所有权与复制

| 路径 | 所有权策略 | 复制行为 |
| --- | --- | --- |
| 入站 I/O | Pool Buffer → Service Task → Handler 返回后自动 Release | Socket 必须写入框架 Buffer 一次 |
| `Session.Send([]byte)` | 返回前复制到框架 Buffer，成功入队后转移给 Queue | 一次安全复制 |
| PB `Router.Send` | 直接编码到框架 Encoder/Buffer，成功后转移给 Queue | 不做编码后的第二次复制 |
| JSON `Router.Send` | 标准库 Marshal 后复制到最终 Pool Buffer，再由 Queue 独占 | 一次有界复制；不扩大 Encoder API |
| 自定义 Codec | 只写框架 Encoder，成功后由 Queue 独占 | 可避免第二次复制 |

Handler 中收到的 Raw `[]byte` 只在当前同步回调返回前有效。业务要保存、异步处理或跨 Service
传递时必须显式复制。首批不提供 `SendOwned([]byte)`，避免调用方在转移后继续修改 Slice。

JSON 的一次复制是有意的安全取舍：首批不为单一标准 Codec 公开 `Adopt`/`SendOwned`，也不改变
`Encoder` 已冻结的最小 API。只有后续 Benchmark/Profile 证明该复制是稳定主要瓶颈，才单独设计
不会把可变 Slice 所有权暴露给业务的内部优化。

### 7.3 当前性能证据

2026-08-10 在 Windows/AMD Ryzen 7 7840HS、Go 当前仓库工具链上运行现有 Benchmark：

- 16 B～64 KiB 池化取得/释放约 14～15 ns/op、0 allocs/op；
- 同尺寸逃逸直接分配从约 22 ns/op/16 B 增长到约 10～12 µs/op/64 KiB，均为 1 alloc/op；
- 128 KiB 超池阈值路径按设计保持普通分配，约 31 µs/op、约 131 KiB/op；
- TCP 发送环形队列约 35～38 ns/op、0 allocs/op；
- 当前 `net.Buffers` 对比测试约 70 ns/op、72 B/op，而拼接复制约 394～456 ns/op、1152 B/op。

这些结果只证明当前选择有继续使用和 Linux 复验的价值，不是发布阈值。正式实现仍要在 Ubuntu
执行代表性 32 B、256 B、4 KiB、64 KiB 和配置最大消息的吞吐、P50/P95/P99、分配及 GC 对比。

## 8. 消息队列与背压

### 8.1 不沿用 v2 Channel 队列

v2 每连接使用 `chan []byte`，默认槽位较大，只按消息数限制，发送前通过 `len == cap` 观察状态；
它无法限制大消息占用的总字节，也把存储、唤醒和关闭所有权混在 Channel 语义中。v3.2 不照搬。

### 8.2 出站队列

每 Session 使用 MPSC、单 Writer 的 FIFO：

- `internal/container/ringqueue` 保存值类型发送项；外层一把短临界区 Mutex 保护生产者和关闭；
- 初始只分配 `min(16, max_messages)` 个槽位，按 2 倍增长到硬上限，不按峰值预分配；
- 容量同时检查等待消息数和 Payload 保留容量，任一达到上限即拒绝；Pool Buffer 按 `cap`、独占
  普通 Slice 也按 `cap` 计费，而不是只按 `len` 低估真实存活内存；
- Module 另有全部 Session 共享的出站总字节预算，防止大量连接同时填满各自队列；
- Session 容量检查、Module 预算预留、入队和所有权转移形成一个可回滚事务。失败时 Queue 不取得
  Payload，已经取得的 Module 额度立即归还；
- 容量为 1 的通知 Channel 只做合并唤醒，不保存数据；唯一 Writer 负责顺序写和最终 Release；
- 出队立即清空槽位引用并扣减 Session 等待消息/字节计数；Module 总字节额度保留到 Writer 完成
  或失败并真正释放 Payload，保证正在写出的数据仍计入活跃内存；Close 原子禁止新入队并释放全部
  剩余 Payload；
- TCP 使用长度头与 Payload 的 scatter/gather 写，优先复用 `net.Buffers`，不为连续帧强制拼接；
- 首批逐逻辑消息写出，不预先加入跨消息批处理。只有 Linux Benchmark/Trace 证明系统调用是热点
  且尾延迟可控时再增加有上限批处理。

该结构保留 Channel 易唤醒的优点，同时让存储增长、双维容量和 Buffer 所有权可以独立验证。
Go `net.Buffers` 可以利用系统的 scatter/gather 写能力：
[Go net.Buffers 源码](https://go.dev/src/net/net.go)。

### 8.3 背压策略

- `Send` 永不阻塞 Service Runner，也不静默丢弃；队列达到硬上限返回稳定 `ErrOverloaded`；
- 高水位固定为消息数或字节上限的 80%，低水位固定为两者上限的 50%，不把比例变成首批配置；
- 任一维度达到高水位后 `Writable=false`；只有两个维度都回到低水位才恢复 `true`；
- Writable 通知按 Session 合并，只投递最新且尚未交付的状态，防止水位抖动产生任务风暴；
- Writer 使用强制 Write Timeout。队列持续高于高水位超过 Slow Client Timeout 时关闭慢 Session；
  不为每 Session 创建独立 Timer goroutine，而由现有 Writer 进展和写截止时间裁决；
- 首批可靠 FIFO 不提供 DropNewest、DropOldest、Coalesce 或 Priority。业务明确存在可替代状态消息
  后，再设计独立的状态发布能力，不能作为传输队列开关。

高低水位参考 Netty 的可写状态；硬容量和显式失败与 Unity Transport、慢连接断开与 Nakama 的
公开策略一致：
[Netty WriteBufferWaterMark](https://netty.io/4.1/api/io/netty/channel/WriteBufferWaterMark.html)、
[Unity Transport 队列](https://docs.unity.cn/Packages/com.unity.transport%402.0/manual/faq.html)、
[Nakama 配置](https://heroiclabs.com/docs/nakama/getting-started/configuration/)。

### 8.4 入站额度，不建立第二个数据队列

每 Session 保存原子或同一锁保护的 `pending_messages` 与 `pending_size`。Read Loop 取得完整帧后先
预留两项额度，再提交 Service Task：

1. 超过单 Session 任一额度：释放当前 Buffer，以入站过载关闭来源 Session；
2. 超过 Module 入站 pending 总字节预算：释放当前 Buffer，以 Module 入站过载关闭来源 Session；
3. Service Scheduler 拒绝任务：归还 Session/Module 额度和 Buffer，以 Service 过载关闭来源 Session；
4. Task 被接受：Handler 返回、错误、panic 或终态跳过时统一归还全部额度和 Buffer；
5. 已接受 Task 保持 FIFO；Session Close 后执行前检查终态，跳过业务但仍完成资源归还；
6. 最终 Close 是生命周期保证，不得因普通消息额度或 Scheduler 满而丢失。实现计划必须单独证明
   Running、Stopping 和截止超时三种状态的交付/最终化路径。

这组 pending 额度限制单连接占用 Service Scheduler 的份额；Scheduler `MaxTasks` 继续作为整个
Service 的全局后备上限，不再建立网络专属全局数据队列。Module 总预算只是原子容量预留，不保存
消息；按 Buffer/Slice 的保留容量计费，并一直持有到 Handler/Writer 释放。`sync.Pool` 中当前未被
使用的缓存不计入该预算，因为它可能随时被 Go 运行时回收；消息数上限另行约束 Buffer 对象和队列
槽位等元数据。

### 8.5 默认容量的确定方式

机制和下表的首轮默认值在正式设计确认时冻结，TCP 按这些值实现，不能由实现者随手填写。TCP
里程碑验收前再通过 Ubuntu 容量测试校准；数据要求修改默认值时，先回写并复核设计再调整代码：

| 配置 | 验证起点 | 说明 |
| --- | ---: | --- |
| `max_sessions` | `4096` | 与当前内部 TCP 默认基线一致，实际按部署容量调整 |
| `max_message_size` | `64KB` | 覆盖绝大多数实时游戏消息，且匹配当前池化上界；允许显式调高 |
| `receive_pending_messages` | `64` | 限制单 Session 可占 Scheduler 的任务数 |
| `receive_pending_size` | `256KB` | 能容纳至少四条默认最大消息 |
| `receive_pending_total_size` | `64M` | 限制当前 Module 全部待处理 Raw Payload |
| `send_queue_messages` | `256` | 兼顾大量小消息，不复制 v2 的超大槽位默认值 |
| `send_queue_size` | `256KB` | 与消息数共同形成实际内存上限 |
| `send_queue_total_size` | `128M` | 限制当前 Module 全部排队及正在写出的 Payload |
| `write_timeout` | `15s` | 防止 Writer 永久阻塞 |
| `slow_client_timeout` | `10s` | 高水位持续时间，不等同单次 Write Timeout |

校准必须同时报告 `max_sessions × 每 Session 上限` 的未加总预算理论占用、Module 总预算、实际
按需分配、高水位断开率和尾延迟。发布默认值以 TCP 测试报告为准回写正式设计；配置校验要求单
消息上限不超过对应方向的 Session 与 Module 字节容量，并且不超过所选长度字段可表达范围。

## 9. 长度帧与传输专属边界

- TCP/KCP 使用 1/2/4 字节无符号长度前缀；2/4 字节支持 Big/Little Endian，默认 Big Endian；
- 端序和长度宽度在构造时冻结并预选读写函数，热路径不重复解析配置；
- WebSocket 使用原生 Message 边界，不再嵌套长度帧；Text/Binary、Path、Origin、Header、TLS、
  子协议、Ping/Pong 和标准 Close Code 属于 WebSocket Options；
- TCP 的 KeepAlive、NoDelay、TLS 属于 TCP Options；
- KCP 的 MTU、窗口、NoDelay、FEC、DSCP 和加密属于 KCP Options，不提升为公共配置；
- 公共配置只包含各后端能保证完全相同语义的 Session 数、逻辑消息上限、pending/queue 容量、
  Module 收发总字节预算、读空闲、写超时、慢连接超时和 Handler。

JSON/YAML 中的容量和时间继续遵守 Origin 统一配置规则，分别使用 `64KB`、`15s` 等带单位字符串；
Go Options 校验完成后保存为整数 Byte 和 `time.Duration`，热路径不解析文本。

### 9.1 Service 配置的最终外观

TCP、WebSocket 和 KCP 分别只公开自己的 `ServerConfig` 与 `ClientConfig`，不公开一个包含三种传输
全部字段的总 Config。三套 Config 对真正同义的容量和超时使用相同字段名，转换到运行期时再复用
内部校验；传输专属字段只存在于自己的包中。配置对象只保存可序列化数据，`Handler`、
`StateChange`、TLS、WebSocket Origin/Header 和 KCP `BlockCrypt` 继续由代码注入。

`Dialer` 是调用点持有的一次性运行时能力，不属于 Service 生命周期配置。三种传输均通过
`DefaultDialOptions(handler)` 取得默认值、在代码中按需覆盖，再调用 `NewDialer`；不提供
`DialerConfig`、`DefaultDialerConfig` 或 YAML `dialer` 节点。这样可以避免临时拨号参数进入长期服务
配置，也不会让使用者误以为 Dialer 会被框架托管或自动重连。

本节冻结的是目标外观。KCP 已按“先完成 Server/Client/Dialer 和运行期 Options，再通过公共契约与
Ubuntu 弱网验证，最后实现 Config 到 Options 映射”的顺序完成；没有为匹配文档保留无效参数或兼容层。

Server 与托管 Client Config 都提供完整默认值：

```go
func DefaultServerConfig() ServerConfig
func DefaultClientConfig() ClientConfig
```

使用者先取得默认值，再从所属 Service 的相对路径严格覆盖，最后转换为现有 Options。严格读取必须拒绝
未知字段，避免把 `write_timeout` 拼错后静默使用默认值；配置切片实现时同步为 `IServiceConfig` 增加
`GetServiceConfigStrict`，不在网络包复制一套配置解析器。

```go
cfg := tcp.DefaultServerConfig()
if err := module.GetServiceConfigStrict("tcp.server", &cfg); err != nil {
	return err
}
options, err := cfg.Options(handler)
if err != nil {
	return err
}
server, err := tcp.NewServer(cfg.Address, options)
```

推荐的 Service 配置外观如下。这里只列出通常需要确认的字段；省略的容量字段仍由对应
`Default*Config` 补齐，不要求使用者复制整份默认值：

```yaml
services:
  GatewayService:
    tcp:
      server:
        # TCP 监听地址；必须包含端口。生产环境按实际网卡配置，不应照抄回环地址。
        address: "0.0.0.0:19090"
        frame:
          # Payload 前无符号长度字段的字节数；只允许 1、2、4。
          length_field_size: 4
          # 长度字段端序；允许 big、little，双方必须一致。
          byte_order: big
        # OS TCP KeepAlive 首次探测前的空闲时间；0s 关闭。它不是业务心跳。
        keep_alive: 30s
        # 完整业务消息的最大长度，入站和出站同时生效。
        max_message_size: 64KB
        # 只统计完整业务消息；0s 关闭读空闲检查。
        read_idle_timeout: 0s
        # 一条完整消息写出的最长时间，必须大于 0s。
        write_timeout: 15s

      client:
        # 托管 Client 的远端 TCP 地址。
        address: "127.0.0.1:19090"
        # 每次建连尝试的最长时间，避免 DNS 或握手长期阻塞 Module 启动/重连。
        dial_timeout: 10s
        frame: {length_field_size: 4, byte_order: big}
        keep_alive: 30s
        reconnect:
          # 默认不自动重连；开启后仍受最大尝试次数限制。
          enabled: false
          # 每轮初始失败或断线后的重试上限；达到上限后进入 Stopped。
          max_attempts: 10
          # 第一次重试的等待时间。
          initial_delay: 200ms
          # 指数退避的单次等待上限。
          max_delay: 5s
          # 退避随机抖动比例；0.2 表示在基准值附近加入最多 20% 抖动。
          jitter: 0.2

    websocket:
      server:
        # HTTP/WebSocket 监听地址。
        address: "0.0.0.0:19091"
        # Upgrade 路由；与监听地址语义不同，因此保留为独立字段。
        path: "/ws"
        # binary 适合 Raw/PB；text 适合浏览器直接处理 JSON，且 Payload 必须是 UTF-8。
        message_type: binary
        # HTTP Upgrade 握手最长时间。
        handshake_timeout: 10s
        # WebSocket 协议控制帧心跳；二者同时为 0s 时关闭。
        ping_interval: 30s
        # 发出协议 Ping 后等待协议 Pong 的上限，必须大于 ping_interval。
        pong_timeout: 60s
        # 可接受的 WebSocket 子协议；空列表表示不协商子协议。
        subprotocols: []
        max_message_size: 64KB
        # 只统计业务 Data Message；协议 Ping/Pong 不刷新该时间。
        read_idle_timeout: 0s
        write_timeout: 15s

      client:
        # Client 使用完整 ws/wss URL，路径直接包含在 URL 中。
        url: "ws://127.0.0.1:19091/ws"
        message_type: binary
        handshake_timeout: 10s
        ping_interval: 30s
        pong_timeout: 60s
        subprotocols: []
        reconnect: {enabled: false, max_attempts: 10, initial_delay: 200ms, max_delay: 5s, jitter: 0.2}

    kcp:
      server:
        # UDP/KCP 监听地址。
        address: "0.0.0.0:19092"
        frame: {length_field_size: 4, byte_order: big}
        # UDP 数据报使用的 KCP MTU；默认 1400，修改前必须结合链路 MTU 测试分片。
        mtu: 1400
        # KCP 发送/接收窗口，单位为 Segment；首轮默认均为 1024。
        send_window: 1024
        receive_window: 1024
        no_delay:
          # 开启 KCP 低延迟模式。
          enabled: true
          # KCP 内部更新间隔；默认 10ms，不是业务 Tick。
          interval: 10ms
          # 累计多少次跨越 ACK 后快速重传；0 关闭，低延迟默认 2。
          fast_resend: 2
          # true 关闭 KCP 拥塞控制，以时延优先；公网弱网发布前必须压测带宽代价。
          disable_congestion_control: true
        # true 会立即发送 ACK、降低确认时延但增加小包；默认 false。
        ack_no_delay: false
        # true 将 Write 延迟到下个更新周期以利批量传输；实时消息默认 false。
        write_delay: false
        fec:
          # 0/0 表示关闭 FEC；启用时服务端与客户端必须使用相同组合。
          data_shards: 0
          parity_shards: 0
        # DSCP 0 表示不标记；非零值依赖操作系统权限和网络设备策略。
        dscp: 0
        # 0B 表示保留操作系统 Socket Buffer 默认值；只在容量测试后调大。
        socket_read_buffer: 0B
        socket_write_buffer: 0B
        max_message_size: 64KB
        # KCP 没有可靠的无流量断线通知；默认 60s，必须大于业务心跳最大间隔。
        read_idle_timeout: 60s
        write_timeout: 15s

      client:
        # 托管 KCP Client 的远端 UDP 地址。
        address: "127.0.0.1:19092"
        frame: {length_field_size: 4, byte_order: big}
        mtu: 1400
        send_window: 1024
        receive_window: 1024
        no_delay: {enabled: true, interval: 10ms, fast_resend: 2, disable_congestion_control: true}
        ack_no_delay: false
        write_delay: false
        fec: {data_shards: 0, parity_shards: 0}
        dscp: 0
        socket_read_buffer: 0B
        socket_write_buffer: 0B
        read_idle_timeout: 60s
        reconnect: {enabled: false, max_attempts: 10, initial_delay: 200ms, max_delay: 5s, jitter: 0.2}

```

YAML 允许把 WebSocket `address` 与 `path` 写成一行，例如
`server: {address: "0.0.0.0:19091", path: "/ws"}`，但 Go Config 仍保留两个字段：`address`
决定监听 Socket，`path` 决定 HTTP Upgrade 路由；反向代理可能分别改写它们，合成一个 URL 会混淆
监听地址和对外访问地址。WebSocket Ping/Pong 是 RFC 6455 控制帧，由传输层消费，不进入
`Handler.OnMessage`；业务层仍可另行定义自己的心跳消息。协议语义见
[RFC 6455 §5.5.2](https://datatracker.ietf.org/doc/html/rfc6455.html#section-5.5.2)，TCP KeepAlive
字段对应 Go 的 [`TCPConn.SetKeepAlivePeriod`](https://pkg.go.dev/net#TCPConn.SetKeepAlivePeriod)。

完整公共容量字段及默认值如下；Server 暴露总预算，单 Session 的 Client/Dialer 不暴露冗余的
`max_sessions` 和总预算字段，转换时令总预算等于对应单 Session 上限：

| 字段 | Server 默认值 | Client/Dialer 默认值 | 语义 |
| --- | ---: | ---: | --- |
| `max_sessions` | `4096` | 不公开，固定 `1` | 当前端点同时活动的 Session 上限 |
| `max_message_size` | `64KB` | `64KB` | 入站和出站完整逻辑消息上限 |
| `receive_pending_messages` | `64` | `64` | 单 Session 已投递但业务尚未处理完的消息数上限 |
| `receive_pending_size` | `256KB` | `256KB` | 单 Session 待处理 Buffer 保留容量上限 |
| `receive_pending_total_size` | `64M` | 不公开，等于单 Session 上限 | 当前 Server 全部待处理 Buffer 总预算 |
| `send_queue_messages` | `256` | `256` | 单 Session 等待写出的完整消息数上限 |
| `send_queue_size` | `256KB` | `256KB` | 单 Session 排队 Payload 保留容量上限 |
| `send_queue_total_size` | `128M` | 不公开，等于单 Session 上限 | 当前 Server 排队及正在写出的 Payload 总预算 |
| `read_idle_timeout` | TCP/WS `0s`；KCP `60s` | 同对应传输 | 完整业务消息读空闲上限；`0s` 表示关闭 |
| `write_timeout` | `15s` | `15s` | 一条完整消息写出的强制上限，不允许关闭 |
| `slow_client_timeout` | `10s` | `10s` | 发送队列连续处于高水位的最长时间 |

范围控制结论：TCP `NoDelay` 固定开启，不增加一个几乎不会正确修改的配置字段；KeepAlive 只公开
首次探测空闲时间，不提前暴露平台相关的探测间隔和次数；KCP Stream Mode 因统一长度帧固定开启；
v2 的 `MinMsgLen` 被协议校验替代，分离的 `MaxReadMsgLen`/`MaxWriteMsgLen` 合并为
`max_message_size`，`PendingWriteNum` 被消息数、字节数和总预算三层上限替代。废弃的 DUP、动态热更新、
普通 YAML 中的静态加密密钥都不进入首批外观。

KCP 上述数值已通过 Windows 回环及 Ubuntu `80±20ms` 延迟、`5%` 丢包、`10%` 乱序验证，现冻结为
v3.2 默认值。该结果只证明默认值满足当前功能与弱网基线，不代表所有业务负载下的性能最优值；项目仍应
按实际消息大小、在线数和链路质量压测后显式调整。
WebSocket 默认不得允许任意 Origin；TLS、Origin 校验和 KCP 加密在代码中显式注入并单独测试，不能因
YAML 未出现对应字段而省略安全能力。

## 10. 生命周期、错误和可观测性

### 10.1 停止顺序

1. 停止新监听、拨号、重连和发送准入；
2. 在 `OnStop(ctx)` 截止时间内排空已接受的发送和 Service Task；
3. 截止时间到达后取消 I/O，关闭全部 Session；
4. 等待 Reader、Writer、Client 重连循环和 Listener 全部退出；
5. 释放队列 Buffer，完成尚未交付的 Close 最终化；
6. 验证 Session、goroutine、端口和诊断模式下的 Pool 使用量归零。

首批不再增加独立 `Drain` 公共状态机。若业务需要“服务仍运行时只停止接入”，收集真实编排需求后
再设计，不能让两个停止入口形成竞争。

### 10.2 稳定错误类别

至少可通过 `errors.Is` 区分：无效配置、未运行/已停止、拨号/握手失败、远端关闭、主动关闭、
读空闲、写超时、协议错误、消息过大、未知消息、发送过载、入站过载、Service 过载、Handler
错误、Codec 错误和 Module 停止。底层 OS 错误保留为 Cause，但不泄漏凭证或完整不可信 Payload。

### 10.3 低成本统计

Server、Client 和 Session 提供快照：活动/累计连接、收发消息/字节、当前/峰值队列消息和字节、
Module 当前/峰值 pending 与发送 Payload 字节、过载拒绝、水位转换、慢连接关闭、协议错误、重连
次数及最终关闭类别。热路径只维护固定原子计数，不为每 MessageID 建立无界标签；详细日志必须限频。

## 11. 测试和性能门禁

### 11.1 共同契约

- 同一套 Server/Client/Session/Handler 契约测试运行于 TCP、WebSocket 和 KCP；
- Open/Message/Writable/Close 全顺序、恰好一次、错误、panic 和重复 Close；
- Raw、PB、JSON 和自定义 Codec 双向 Golden Test；注册冻结、未知/重复/零 ID；
- 两个方向的消息数/字节边界，边界前成功、等于上限、越界失败和失败后所有权；
- 慢 Reader/Writer、部分写、超时、水位迟滞、Scheduler 满和入站洪泛；
- 首次拨号失败、取消、重连抖动、停止中拨号以及服务自己调用自己的回环场景；
- 启动每一步故障注入、逆序回滚、端口/Buffer/goroutine 泄漏和重复启停；
- TCP/KCP 1/2/4 字节长度与 Big/Little Endian 全组合；WebSocket Origin、Ping/Pong、TLS/Close；
- Fuzz 覆盖 Framer、PB/JSON Envelope 和自定义 Codec 边界；并发路径执行 Race。

重点核心功能以可达语句和分支接近 100% 为目标。不能稳定制造的平台错误需记录原因，并用真实
集成、Race、Fuzz 或故障注入补证；不以无意义断言换取覆盖率数字。

### 11.2 环境与性能

- 开发期在 Windows 执行单元、Race 可替代检查和回环；
- 最终必须在用户指定 Ubuntu `192.168.8.3` 环境执行真实 TCP/WebSocket/KCP、Race、Fuzz、
  资源泄漏、服务自调用和长时间稳定性测试；凭证不写入仓库、日志或报告；
- Benchmark 至少覆盖 32 B、256 B、4 KiB、64 KiB 和配置最大消息，分别测试单连接、多连接、
  队列空闲、积压、高水位和硬拒绝；
- 保存吞吐、ns/op、allocs/op、B/op、P50/P95/P99、CPU/Heap Profile 和 GC 影响；
- 性能优化阶段仍在正确性实现和功能测试之后，但上述容量、复制和分配边界在设计阶段先冻结。

## 12. 实施切片与门禁

1. **确认本设计**：已完成；公共 Go 形状和首轮默认值已经冻结并允许实施；
2. **公共基础 + TCP**：Session/Handler、Router/Codec、Raw/PB/JSON、Server/Client/Dialer、队列与
   回环，先验证全部公共风险；Ubuntu 容量数据要求调整默认值时先更新设计；
3. **WebSocket**：复用公共契约，只增加 Upgrade、安全和 WS 生命周期；
4. **KCP Module**：先引入依赖，完成 Server/Client/Dialer、运行期 Options、UDP/KCP 专属安全、公共
   契约、弱网和容量测试；本阶段不实现 KCP Service Config；
5. **KCP Config**：以上一步已经验收的 Options 和实测默认值为唯一输入，实现独立 Config、严格读取、
   默认值、转换校验和配置驱动 Example，不反向修改底层能力来迁就预设配置；
6. **整体收口**：共同契约、覆盖率、Ubuntu 稳定性、教程和带完整中文注释的 Example；
7. **性能优化**：根据 Benchmark/Profile 只处理已证明热点，随后完整回归功能、Race、Fuzz 和稳定性；
8. **Gin/HTTP**：单独提案，不加入长连接接口。

每个切片同时完成实现、测试、Benchmark、文档和验收后再进入下一个。TCP 证明公共契约不成立时
先修订设计，不建立临时兼容层后继续复制到 WebSocket/KCP。

## 13. 已确认决策

开发者已于 2026-08-10 确认以下整批核心选择：

| # | 推荐结论 | 主要影响 |
| --- | --- | --- |
| 1 | Raw Handler 是传输核心，PB/JSON Router 作为 Handler 适配 | 传输保持轻量，标准协议仍开箱即用 |
| 2 | 首批不提供 Middleware，使用 Handler 包装组合 | 少一个公共抽象和运行时链 |
| 3 | Codec 首批无状态/只读，不提供每 Session Factory | API 和生命周期更简单；暂不支持动态协商状态 |
| 4 | MessageID 固定 `uint16`、0 非法 | 2 字节开销，最多 65535 个业务 ID |
| 5 | PB 为 `uint16 ID + payload`，JSON 为 `id/data` Envelope | Wire Format 可写 Golden Test，Server/Client 一致 |
| 6 | 使用 Module 私有 `internal/bufferpool`，仅池化 16 B～64 KiB 字节 | 减少 GC，不暴露手工 Release |
| 7 | 出站采用 Mutex + 可增长 Ring + 合并唤醒，不沿用 v2 Channel | 双维容量、惰性分配和所有权更清楚 |
| 8 | 入站只做 Session/Module pending 额度并直接 Dispatch，不建立第二数据队列 | 少一次排队与唤醒，靠预算与 Scheduler 保持全局有界 |
| 9 | 高/低水位固定 80%/50%，可靠消息只返回过载/关闭慢连接 | 不静默丢弃，不增加首批策略矩阵 |
| 10 | 首批不提供 Admission、公开 Drain、Broadcast、Token Bucket、优先级 | 控制范围；传输安全默认和硬容量仍必须完成 |
| 11 | 默认容量先按第 8.5 节作为 Ubuntu 校准起点，测试后冻结 | 避免把经验数字直接当成最终最优值 |
| 12 | TCP → WebSocket → KCP，最后整体与性能收口 | 先用最低依赖路径验证公共设计 |

本文已经进入 `design/` 并允许按第 12 节实施。TCP 验收数据如要求修改发布默认值，必须先更新
本文并重新复核对应差异，不能只修改代码。
