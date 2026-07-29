# Origin 第三版 M6 NATS 基础库设计

> 文档状态：已实现并通过验收
> 创建日期：2026-07-26
> Review 日期：2026-07-26
> 适用里程碑：M6
> 前置依赖：M0 工程基础、M1 日志库、M3 基础配置库、M5 Transport 错误码

## 1. 目标

M6 实现一个只面向 Origin 框架内部的 Core NATS 基础库，为 M15 NATS RPC、后续 NATS
服务发现控制面以及可能出现的内部消息模块提供共同的连接能力。

M6 完成后必须能够：

1. 连接一个或多个 NATS Server；
2. 发布普通消息，允许空 payload；
3. 建立普通订阅和 Queue Group 订阅；
4. 观察连接、断开、重连、最终关闭和异步错误事件；
5. 为订阅设置消息数与字节数双重 Pending 上限；
6. 对 Connection 和 Subscription 执行立即关闭或有 Deadline 的排空；
7. 正确停止并等待 `nats.go` 创建的连接及订阅资源；
8. 使用真实 NATS Server 验证发布、订阅、重连、过载、认证、TLS 和停止；
9. 保存延迟、吞吐、分配和竞态检测基线。

M6 只负责 NATS 连接、Subject、原始字节消息和资源生命周期，不实现 Origin RPC
RequestID、ContractID、MethodID、ServiceName、代码生成、路由、服务发现或业务 Service
调度。M15 再把 M11 RPC Runtime、M12 静态编解码和 M13 远端调用状态与 M6 组合起来。

## 2. 为什么使用官方客户端

M6 建议使用官方 [nats.go](https://github.com/nats-io/nats.go) 客户端，不自行实现 NATS
协议。

原因：

- NATS 客户端不仅包含 Publish/Subscribe，还包含协议解析、Ping/Pong、集群 Server
  发现、认证、TLS、自动重连、重订阅、Request Inbox 复用和慢消费者处理；
- 自行实现会显著扩大网络协议、认证和异常恢复风险；
- 官方库遵守 Apache-2.0 许可证并承诺 Go API 向后兼容；
- M6 包装层可以保持很薄，只固化 Origin 需要的默认值、错误码、生命周期和测试。

建议固定生产依赖：

```text
github.com/nats-io/nats.go v1.52.0
```

该版本发布于 2026-05-07，`go.mod` 要求 Go 1.25，满足 Origin 最低 Go 1.26.5。
依赖版本必须固定，不使用 `latest`、分支或伪版本。

## 3. v2 实现复核

v2 的 `rpc/natsserver.go`、`rpc/natsclient.go` 和 `rpc/rpcnats.go` 已经证明“一个 Node
共用一条 NATS 连接”可行，但不直接复制到 v3。

v2 中需要修正的问题：

1. NATS 连接、Subject、RPC 编解码、压缩和调用关联混在同一包；
2. `os.<NodeID>`、`oc.<NodeID>` 及 `fnode` Header 被写死在 Transport 内；
3. `MaxReconnects(-1)` 永久重连，不符合 v3 的有界重试原则；
4. 连接事件依赖包级日志和全 Service 广播，没有实例隔离；
5. Subscription 没有独立所有者、关闭、排空和等待外观；
6. NATS Client 的 `Close`、`Run`、`OnClose` 是空实现，资源边界不完整；
7. 没有显式 Pending 上限、慢消费者处理、Dropped 统计和过载策略；
8. 没有用户名密码、Token、Credentials、NKey 和完整 TLS 配置；
9. 多段 RPC 数据先拼接到新的 `[]byte`，产生额外 payload 复制；
10. Publish 成功、Server 收到、订阅者收到和业务处理完成的语义没有区分。

M6 保留“一条 Node 连接由 Node 自己持有”的方向，但把 RPC、发现和业务事件全部移出基础
库。

### 3.1 成熟 NATS RPC 实现调研

本次设计复核了以下公开实现和官方资料：

- NATS 官方 Request/Reply：请求由普通 Publish、Reply Subject 和 Inbox 组成；现代
  `nats.go` 使用一条通配响应订阅复用多个并发请求，而不是为每个请求永久创建订阅；
- NATS 官方 `micro` 包：Endpoint 默认支持 Queue Group，并允许为 Endpoint 设置消息数和
  字节数 Pending 上限；
- `nats-rpc/nrpc`：生成 RPC 代码，在服务端提供有界 WorkerPool、最大 Pending 和
  `SERVERTOOBUSY` 错误；
- Core NATS 慢消费者规则：客户端 Pending 达到上限时会报告慢消费者并丢弃消息，不能把
  无限 Pending 当作可靠队列；
- Core NATS Drain：服务退出前停止新投递，处理已经 Pending 的消息，再关闭连接。

参考资料：

- [NATS Request/Reply](https://docs.nats.io/nats-concepts/core-nats/reqreply)
- [NATS Queue Groups](https://docs.nats.io/nats-concepts/core-nats/queue)
- [NATS Slow Consumers](https://docs.nats.io/running-a-nats-service/nats_admin/slow_consumers)
- [nats.go micro](https://github.com/nats-io/nats.go/tree/v1.52.0/micro)
- [nats-rpc/nrpc](https://github.com/nats-rpc/nrpc)

对 Origin 的可采用结论：

1. 保留 v2 已验证的稳定 Subject 订阅与普通 Publish 模型，不使用原生 NATS Request；
2. 每个 Node 使用稳定的请求 Subject 和响应 Subject，不为 Service 或单次 RPC 动态建立
   Subject 或 Subscription；
3. RPC RequestID、Deadline、调用类型和错误码全部放在线协议，由 RPC Runtime 处理；
4. Origin RPC 使用有界入口队列；队列满时，请求类消息立即回复统一过载错误，不能静默
   丢弃后只等调用方超时；
5. Notify 不需要响应，过载时只能计数、限频告警并按既定策略丢弃，不能伪造成功；
6. 普通 Origin RPC 不使用 Queue Group 代替服务发现和路由；
7. Core NATS 维持至多一次语义，RPC 不因超时或断线自动重试；
8. 停服使用 Drain，并与 Origin Service 排空和退休状态协调。

没有直接照搬成熟项目的完整 RPC 包。它们通常把 Protobuf、Subject、服务执行池和 NATS
绑定在一起，而 Origin 已经确定要让 TCP、NATS 和外部 gRPC 接入同一套自有 RPC Runtime。

## 4. 里程碑边界

### 4.1 M6 包含

- Core NATS，不包含 JetStream；
- 多 URL 初始连接；
- 连接名称、Server 随机选择和 NoEcho；
- 用户名密码、Token、Credentials 文件、NKey Seed 文件；
- CA、客户端证书、ServerName 和显式不校验证书选项；
- 有界自动重连和重连期间发送缓冲；
- 普通 Publish、Subscribe 和 Queue Subscribe；
- 订阅 Pending 消息数、字节数和 Dropped 统计；
- Flush、Drain、Close 和 Wait；
- 连接状态与内部事件；
- Origin Transport 错误映射；
- 真实 NATS Server 集成测试和 Benchmark。

### 4.2 M6 不包含

- JetStream、Stream、Consumer、持久化、Ack 或重投；
- Origin RPC 线协议和 RPC pendingCall；
- NATS 原生 Request/Reply 包装；
- NodeID、ServiceName 或 Contract Subject；
- Broadcast Subject 规则；
- 服务发现注册、TTL、退休状态或关注筛选；
- 跨 TCP/NATS 桥接；
- Protobuf、JSON 或普通 Go 结构体编解码；
- 消息压缩；
- 业务 Module 或 Service 回调；
- 自定义 NATS Client 替换接口；
- 为 NATS 和 TCP 建立共同的大型 Transport 接口。

M13/M15 的 RPC 使用方在需要时定义最小适配接口。M6 不为了外观统一而让 TCP 假装拥有
Subject，也不让 NATS 假装拥有逐 Node TCP Connection；但 M13/M15 必须把两者接入同一
RPC Runtime、同一线协议语义和同一 Service 调度入口，不能形成两套 RPC 实现。

## 5. 包与依赖边界

生产代码建议放在：

```text
internal/natsnet/
├── options.go
├── error.go
├── message.go
├── conn.go
├── subscription.go
└── event.go
```

包名使用 `natsnet`，与 `internal/tcpnet` 保持可辨识的同级关系。

生产依赖只允许：

- 标准库；
- `github.com/nats-io/nats.go v1.52.0`；
- M0 `errs`；
- M1 `log`。

M6 不依赖 Application、Node、Service、RPC Runtime 或 `bufferpool`。`nats.go` 已经为收包
分配 `nats.Msg.Data`，强行再复制到 BufferPool 只会增加一次完整 payload 复制。

## 6. 配置与默认值

建议代码配置外观：

```go
type Options struct {
    Name                    string
    URLs                    []string
    NoRandomize             bool
    NoEcho                  bool
    MaxMessageSize          int
    ConnectTimeout          time.Duration
    DefaultOperationTimeout time.Duration
    DrainTimeout            time.Duration
    PingInterval            time.Duration
    MaxPingsOutstanding     int
    Reconnect               ReconnectOptions
    Subscription            SubscriptionDefaults
    Auth                    AuthOptions
    TLS                     TLSOptions
    Logger                  log.Logger
}

type ReconnectOptions struct {
    Enabled       bool
    MaxAttempts   int
    Wait          time.Duration
    Jitter        time.Duration
    TLSJitter     time.Duration
    BufferSize    int
}

type SubscriptionDefaults struct {
    PendingMessages int
    PendingBytes    int
}
```

建议默认值：

| 配置 | 默认值 | 说明 |
|---|---:|---|
| `NoRandomize` | `false` | 多个 Seed Server 默认随机选择 |
| `NoEcho` | `false` | 不破坏同一连接可能需要的本地 Subject 投递 |
| `MaxMessageSize` | `4M` | 与已确认的 RPC 上限一致 |
| `ConnectTimeout` | `2s` | 单次 Server 连接和握手上限 |
| `DefaultOperationTimeout` | `15s` | Flush、Drain 未带 Deadline 时使用 |
| `DrainTimeout` | `30s` | NATS 内部排空保底 |
| `PingInterval` | `30s` | 比官方两分钟默认值更早发现黑洞连接 |
| `MaxPingsOutstanding` | `2` | 连续两次 Ping 无响应后判定失活 |
| `Reconnect.Enabled` | `true` | 已成功连接后允许自动恢复 |
| `Reconnect.MaxAttempts` | `60` | 不允许无限重连 |
| `Reconnect.Wait` | `2s` | 使用成熟客户端默认节奏 |
| `Reconnect.Jitter` | `500ms` | 减少大量 Node 同时重连 |
| `Reconnect.TLSJitter` | `1s` | TLS 建连成本更高，使用更大抖动 |
| `Reconnect.BufferSize` | `8M` | 重连期间本地待发协议缓冲上限 |
| `Subscription.PendingMessages` | `16384` | 单订阅待处理消息数量上限 |
| `Subscription.PendingBytes` | `8M` | 单订阅待处理消息字节上限 |

`Name` 和 `URLs` 必填。M7 创建 Node 时，连接名称建议使用
`<app-name>.<node-id>`，方便在 NATS 监控中定位。

用户配置继续遵守 v3 规则：

```yaml
nodes:
  - id: chat-1
    scheduler:
      default_await_timeout: 15s
    rpc:
      transport: nats
      max_message_size: 4M
      nats:
        name: game-dev.chat-1
        urls:
          - nats://127.0.0.1:4222
        no_randomize: false
        no_echo: false
        connect_timeout: 2s
        operation_timeout: 15s
        drain_timeout: 30s
        ping_interval: 30s
        max_pings_outstanding: 2
        reconnect:
          enabled: true
          max_attempts: 60
          wait: 2s
          jitter: 500ms
          tls_jitter: 1s
          buffer_size: 8M
        subscription:
          pending_messages: 16384
          pending_size: 8M
```

M6 只实现 Options 和校验，不在本里程碑把该结构接入完整 Node 配置；M7/M15 接入时再把
对应配置片段解析为 `natsnet.Options`。

## 7. 认证与 TLS

建议认证结构：

```go
type AuthOptions struct {
    Username        string
    Password        string
    Token           string
    CredentialsFile string
    NKeySeedFile    string
}

type TLSOptions struct {
    Enabled            bool
    CAFile             string
    CertFile           string
    KeyFile            string
    ServerName         string
    InsecureSkipVerify bool
}
```

认证规则：

1. 用户名密码、Token、Credentials 和 NKey Seed 四种模式互斥；
2. Password 不能在 Username 为空时单独出现；
3. 客户端证书和私钥必须同时配置；
4. CA、证书、Key 和 Credentials 文件在创建连接前校验；
5. `InsecureSkipVerify` 默认关闭，只允许显式开启；
6. 日志和错误不得包含 Password、Token、Seed、Credentials 内容或带 UserInfo 的完整 URL；
7. 日志中的 Server URL 必须去掉 UserInfo 和 Query；
8. 不允许在同一连接中混用明文与 TLS Seed URL，避免无意降级。

Credentials 和 NKey 使用官方客户端能力，不自行读取或解析私钥内容。

## 8. 公开外观

M6 是内部包，但 API 应保持稳定、精简，供后续内部组件复用。

```go
func Connect(
    ctx context.Context,
    options Options,
    eventHandler EventHandler,
) (*Conn, error)

func (conn *Conn) Publish(
    subject string,
    payload []byte,
) error

func (conn *Conn) Subscribe(
    ctx context.Context,
    subject string,
    options SubscriptionOptions,
    handler MessageHandler,
) (*Subscription, error)

func (conn *Conn) Flush(ctx context.Context) error
func (conn *Conn) Status() Status
func (conn *Conn) Stats() ConnStats
func (conn *Conn) Drain(ctx context.Context) error
func (conn *Conn) Close()
func (conn *Conn) Wait(ctx context.Context) error
```

订阅外观：

```go
type SubscriptionOptions struct {
    Queue           string
    PendingMessages int
    PendingBytes    int
}

func (subscription *Subscription) Subject() string
func (subscription *Subscription) Queue() string
func (subscription *Subscription) Stats() SubscriptionStats
func (subscription *Subscription) Drain(ctx context.Context) error
func (subscription *Subscription) Close()
```

规则：

- `SubscriptionOptions.Queue == ""` 表示普通订阅；
- 非空 Queue 表示 NATS Queue Group，不另建 `QueueSubscribe` 重复方法；
- `Subscribe` 成功前执行一次有 Deadline 的 Flush，保证 Server 已经处理订阅命令；
- `Close` 是立即停止，不等待 Pending 消息；
- `Drain` 停止新投递、处理已经 Pending 的消息并等待关闭；
- `Drain` 超时后强制 Close，避免资源永久无法退出；
- Connection Close/Drain 会统一关闭其全部 Subscription；
- Subscription 可以先于 Connection 单独关闭；
- 全部 Close、Drain 和 Wait 幂等；
- `EventHandler` 允许为 nil，`MessageHandler` 不允许为 nil。

## 9. Message 与 payload 所有权

```go
type Message struct {
    Subject string
    Data    []byte
}

type MessageHandler func(message Message)
```

### 9.1 发送

`nats.go v1.52.0` 的 Core NATS Publish 会在返回前把协议头和 payload 复制进客户端自己的
写缓冲或重连缓冲。

因此：

1. `Publish` 返回后，调用方可以立即复用或释放原 payload；
2. M15 可以把 `bufferpool.Buffer.Bytes()` 传给 M6，并在 Publish 返回后释放 Buffer；
3. M6 包装层不能为了“保险”再复制一次 payload；
4. 必须通过测试锁定“Publish 返回后修改源切片不改变接收数据”的依赖行为；
5. 依赖升级时必须重新执行该所有权测试。

### 9.2 接收

`nats.go` 已经为入站 `Msg` 和 `Msg.Data` 分配可由 Go GC 管理的对象。M6 直接把 Subject
和 Data 组成轻量 Message 值交给 Handler，不再次复制。

Handler 把 `Data` 视为只读：

- 同步解码可以直接读取；
- 需要跨 goroutine 或长期持有时，由新的所有者自行复制；
- M15 若需要把消息转入异步 Service 调度队列，应在 NATS 回调中完成必要的协议校验，
  再把明确拥有的数据对象投递给调度器；
- M6 不为入站消息增加引用计数或 BufferPool 二次包装。

### 9.3 GC 压力与 BufferPool 决策

使用原始 `[]byte` 仍然会有 GC 压力，但这是 `nats.go` 协议边界内已经发生的分配：

1. 入站消息由 `nats.go` 为 `Msg` 和 Data 分配内存；
2. 出站消息在 Publish 返回前被复制进 `nats.go` 自己的写缓冲或重连缓冲；
3. `nats.go` 没有暴露可替换的入站 payload Allocator；
4. M6 再把 Data 复制到 BufferPool，无法撤销第 1 步的分配，只会同时保留原 Data 和池化
   Buffer，并增加一次 payload 全量复制；
5. 出站先使用 BufferPool 组装 RPC 帧仍然有价值，但把它交给 M6 后，官方客户端依然要
   完成一次自身持有的复制。

因此首版决定：

- M6 入站直接使用只读 Data，不复制到 BufferPool；
- M15 可以用 BufferPool 构建统一 RPC 线协议，Publish 返回后立即归还 Buffer；
- M15 的 NATS 回调必须快速校验和解码，不能无界持有 Data；
- RPC Runtime 在回调返回后不得继续引用原始 Data；需要异步调度的数据必须在回调内解码
  到明确拥有的 Request/参数对象；
- 不为统一 TCP/NATS 所有权而在热路径引入 `Packet` 接口、每消息 Release 闭包或引用计数；
- 若 M15 基准证明大字节字段复制成为主要瓶颈，再单独 Review“可转移所有权 Packet”方案，
  不在 M6 预先增加复杂度。

### 9.4 空消息

nil 和长度为零的 payload 都是合法 Core NATS 消息。M6 不因为 payload 为空而拒绝
Publish 或 Handler 投递。

## 10. 消息大小

M6 在 Publish 前检查 `MaxMessageSize`，超过上限返回
`CodeTransportMessageTooLarge`。

收到消息时：

1. NATS Server 的 `max_payload` 和官方客户端协议解析先形成第一层边界；
2. M6 在进入 Handler 前再次检查 Origin `MaxMessageSize`；
3. 超限消息不进入 Handler，并通过异步错误事件和日志报告；
4. M15 在反序列化前再次按 RPC 配置检查。

与 TCP 不同，NATS 协议解析和 `Msg.Data` 分配由官方客户端完成，Origin 无法在该客户端
分配前执行自己的大小检查。不能为了满足表面一致性自行重写 NATS 协议。

因此部署 NATS RPC 时，NATS Server 的 `max_payload` 必须大于或等于 Origin
`max_message_size`；默认 `4M` 需要在 Server 端同步配置。
[NATS Server 配置文档](https://docs.nats.io/running-a-nats-service/configuration)不建议把
`max_payload` 设置得过大，M6 不允许用无限值绕过内存边界。

## 11. Publish、Flush 与投递语义

Core NATS 是至多一次投递，不提供持久化和消费确认。

`Publish` 返回 nil 只表示：

- Subject 和 payload 通过本地校验；
- 消息已经复制到 `nats.go` 的本地写缓冲或有界重连缓冲。

它不表示：

- NATS Server 已经收到；
- 任一订阅者已经收到；
- Origin Service 已经处理。

`Flush(ctx)` 通过 Ping/Pong 确认 Server 已经处理当前连接在 Flush 前写出的协议命令，但
仍不表示订阅者完成业务处理。

普通 RPC 和 Notify 热路径不能每次 Publish 后自动 Flush，否则会增加一次网络往返。
只有订阅建立、测试同步点、停服边界或业务确实需要 Server 接收屏障时显式 Flush。

## 12. 发布订阅与 RPC 关联边界

M6 不包装 NATS 原生 `Request`、`RequestWithContext` 或 Reply Inbox。M6 只提供普通
Publish 和长期 Subscribe。

M15 的 Origin RPC 固定沿用 v2 的核心模式：

1. Node 的 RPC Runtime 启动时订阅由自身 NodeID 确定的稳定请求 Subject 和响应 Subject；
2. 每个 Node 只建立一个逻辑请求入口和一个逻辑响应入口，不按本地 Service 数量增加订阅；
3. 调用方由 M13 补齐的统一远端 RPC Runtime 创建 RequestID、pendingCall 和 Deadline；
4. 服务发现和 Route 先选出逻辑目标 `NodeID + ServiceName`；
5. M15 只使用目标 NodeID 计算 Node 级请求 Subject，并通过普通 `Publish` 发送；
6. 请求数据包携带 RequestID、目标 ServiceName、方法、来源 NodeID 和 Deadline；
7. 目标 Node 的 RPC Runtime 读取 ServiceName，投递到对应 Service 的独立有界调度队列；
8. 远端处理完成后，使用来源 NodeID 计算 Node 级响应 Subject 并通过普通 `Publish` 返回；
9. 响应数据包携带原 RequestID，来源 Node 的 RPC Runtime 根据 RequestID 完成 pendingCall；
10. Notify 使用相同 Node 请求 Subject，但在线协议中标记为无需响应；
11. 不为 Service 或单次 RPC 创建 Subject、Subscription、Channel 或 NATS 层定时器。

这与 v2 的 `os.<NodeID>` 请求 Subject、`oc.<NodeID>` 响应 Subject 和数据包序号关联方式
保持同一原理。v3 可以规范 Subject 前缀和命名，但地址粒度仍然是 Node；ServiceName 和
RequestID 都属于 RPC 线协议。

这里的 RequestID 就是业务通常所说的“某次 RPC SessionID”。v3 统一命名为 RequestID，
避免与服务发现中用于区分 Node 进程代次的 SessionID 混淆。

NATS 不知道 RequestID，不维护 pendingCall，也不判断某条响应属于哪个 RPC。没有订阅者
时不依赖 NATS `503 No Responders`，而由服务发现先判断目标是否存在，并由 RPC 默认
`15s` Deadline 作为最终保底。

## 13. 订阅、Pending 与慢消费者

NATS 异步 Subscription 自带客户端 Pending 队列。M6 不再建立第二层消息队列或每消息
goroutine，而是在订阅创建后立即调用 `SetPendingLimits`。

双重上限：

- `PendingMessages` 限制待回调消息数；
- `PendingBytes` 限制待回调 payload 总量；
- 任一达到上限都可能触发 NATS 慢消费者错误和消息丢弃。

M6 不静默处理慢消费者：

1. 通过 `EventAsyncError` 报告 `CodeTransportOverloaded`；
2. 日志包含 Subject、Queue、Pending 和 Dropped，不记录 payload；
3. `Subscription.Stats()` 暴露 Pending、PendingBytes 和 Dropped；
4. Handler panic 被捕获、记录堆栈并丢弃当前消息，Subscription 继续处理后续消息；
5. M6 不在慢消费者时自动关闭共享 Connection；
6. M15 可以根据 RPC 消息分类决定告警、停止 Subscription、让 Node 退休或停服。

M15 的 Handler 已经收到消息、但 Origin RPC 入口队列已满时：

- Request 必须从请求包读取来源 NodeID，向该 Node 的稳定响应 Subject 返回统一
  `CodeServiceQueueFull` 错误，响应包保留原 RequestID；
- Notify 无需响应，只增加 Dropped/Overloaded 统计并限频记录；
- 不阻塞 NATS Subscription goroutine 等待 Service 队列腾出空间；
- 不为每条消息创建 goroutine；
- 不自动扩容为无界队列；
- NATS Pending 溢出发生在 Handler 之前，无法逐条回复，因此必须持续监控 Slow Consumer，
  不能把 Pending 上限当作业务削峰队列。

[NATS 慢消费者文档](https://docs.nats.io/using-nats/developer/connecting/events/slow)
说明了客户端 Pending 上限、Slow Consumer 事件和消息丢弃语义。需要持久化、Ack 和重投
的业务必须使用后续独立 JetStream 设计，不能在 M6 用无限 Pending 模拟可靠队列。

## 14. Handler 执行规则

MessageHandler 在 `nats.go` 的订阅回调 goroutine 中执行。

要求：

- M6 不额外创建每消息 goroutine；
- 同一异步 Subscription 的 Handler 按 NATS 客户端顺序执行；
- 不同 Subscription 可以并发；
- Handler 不能直接执行 Service 业务逻辑；
- M15 Adapter 只做快速协议校验、必要所有权转换和调度投递；
- Handler 不能无限阻塞；
- Handler panic 由 M6 在最外层恢复，记录一次堆栈后继续；
- 连接锁、Subscription 锁和 M6 状态锁都不能跨 Handler 调用持有。

这里与 M5 TCP Handler 的共同点是“网络 goroutine 不执行 Service 业务”。不同点是 NATS
单条消息错误无法通过返回 error 反馈给 Broker，因此 MessageHandler 不返回 error。

## 15. 连接与重连

### 15.1 初始连接

`Connect(ctx, options, handler)` 必须在返回前完成初始连接。M6 不启用
`RetryOnFailedConnect`：

- 初始 NATS 不可用时直接返回错误；
- M7 Node 启动策略决定是否重试、等待或启动失败；
- 不返回一个尚未连接但表面可用的 Conn；
- 初始连接重试不会隐藏在 M6 后台。

`nats.go` 初始 Connect 没有原生 Context 参数。实现时使用只在初始阶段生效的
Context-aware Dialer：

1. TCP Dial 使用调用方 Context；
2. 初始协议握手期间 Context 取消会关闭对应临时 socket；
3. Connect 结束后停止并等待全部初始取消观察 goroutine；
4. 后续自动重连改用普通有超时 Dial，不继续持有初始 Context；
5. 不能通过“Connect 返回、后台 goroutine 以后再清理”的方式泄漏资源。

### 15.2 已连接后的重连

初始成功后使用 `nats.go` 成熟的自动重连和自动重订阅：

- 不手工重建 Subscription；
- 重连次数必须有限；
- 每次等待包含 Jitter，避免大量 Node 同时冲击 NATS 集群；
- 重连成功后原 Subscription 继续工作；
- 达到上限后进入 Closed，Wait 返回 `CodeTransportUnavailable`；
- 不允许 `MaxAttempts < 0`；
- 连接正在重连时，Publish 可能进入有界 Reconnect Buffer；
- Buffer 满时返回 `CodeTransportOverloaded`；
- M15 的 RPC 请求必须在线协议中携带 Deadline，远端拒绝已经过期的迟到请求。

M6 不把“重连期间本地接受”描述成“远端已收到”。这属于 NATS 的有界缓冲语义，不是
RPC 自动重试。

## 16. 状态和事件

建议状态：

```go
type Status uint8

const (
    StatusConnecting Status = iota
    StatusConnected
    StatusReconnecting
    StatusDraining
    StatusClosed
)
```

建议事件：

```go
type EventType uint8

const (
    EventConnected EventType = iota
    EventDisconnected
    EventReconnected
    EventLameDuck
    EventAsyncError
    EventClosed
)

type Event struct {
    Type    EventType
    URL     string
    Subject string
    Err     error
}

type EventHandler func(event Event)
```

规则：

- EventHandler 是内部基础设施回调，不是 Service 业务回调；
- 后续 Node/RPC 接入层按自身生命周期把事件转换为受控调度事件；
- M6 不为事件建立第二个有界队列；
- EventHandler 必须快速返回；
- EventHandler panic 被恢复并记录，不破坏 `nats.go` 回调调度器；
- Event URL 经过脱敏，不包含认证信息；
- Connected 和 Reconnected 明确区分；
- Disconnected 不表示服务失去发现；
- Closed 才是当前 Conn 不会自行恢复的终态；
- LameDuck 表示 Server 正在优雅退出，M6 只通知，不自行切换 Node 状态。

## 17. Close、Drain 与 Wait

[NATS Drain 文档](https://docs.nats.io/using-nats/developer/receiving/drain)定义的连接排空
顺序是先排空 Subscription，再 Flush Publish，最后关闭 Connection。

M6 保留两种明确动作：

- `Close()`：立即关闭，未处理 Pending 和本地未写出数据可能丢失；
- `Drain(ctx)`：停止新 Publish/Subscribe，排空已有 Subscription 和 Publish，等待终态。

Drain 规则：

1. Context 没有 Deadline 时使用 `DefaultOperationTimeout`；
2. 同时受 Options `DrainTimeout` 约束，实际采用更早的 Deadline；
3. 超时后强制 Close；
4. 超时返回 `CodeDeadlineExceeded`；
5. 正常 Drain 返回 nil；
6. Drain 期间新 Publish 和 Subscribe 返回 `CodeTransportClosed`；
7. 多次 Drain、Close 和 Wait 不 panic；
8. 立即 Close 后 Wait 返回 `CodeTransportClosed`；
9. 网络故障耗尽重连后 Wait 返回首个有效 Transport 原因；
10. EventClosed 在资源终态发布一次。

Subscription 也提供立即 Close 和有 Deadline 的 Drain；Connection 是全部 Subscription 的
最终所有者。

## 18. 错误映射

M6 复用 M5 已登记的 Transport 错误码，不新增 NATS 专用错误结构。

| NATS/Context 情况 | Origin Code |
|---|---|
| Context 取消 | `CodeCanceled` |
| Flush、Drain 超时 | `CodeDeadlineExceeded` |
| 空 Context、Subject、URL 或非法 Options | `CodeInvalidArgument` / `CodeInvalidConfig` |
| 无 Server、断开、重连耗尽 | `CodeTransportUnavailable` |
| Connection/Subscription 已关闭或正在 Drain | `CodeTransportClosed` |
| Reconnect Buffer 满、Slow Consumer | `CodeTransportOverloaded` |
| 本地或远端消息超过限制 | `CodeTransportMessageTooLarge` |
| NATS 协议错误 | `CodeTransportProtocol` |
| Handler/EventHandler panic | `CodeInternal`，仅本地日志和事件 |

底层 `nats.go` error 作为本地 cause 保留，程序判断只使用 Origin Code。认证和权限失败暂时
映射 `CodeTransportUnavailable` 并保留 cause，不为每种 Broker 配置错误增加公共错误码。

## 19. 统计与日志

```go
type ConnStats struct {
    InMessages  uint64
    OutMessages uint64
    InBytes     uint64
    OutBytes    uint64
    Reconnects  uint64
}

type SubscriptionStats struct {
    PendingMessages int
    PendingBytes    int
    DroppedMessages int
}
```

日志规则：

- 记录初始连接成功/失败、断开、重连、Lame Duck、最终关闭和异步错误；
- 慢消费者错误按 Connection + Subject 限频，避免日志风暴；
- 正常 Publish 和每条 Message 不写日志；
- 不记录 payload、Token、Password、Seed 或 Credentials 内容；
- URL 必须脱敏；
- 后续接入 Node 时通过 Logger.With 绑定稳定的 `node_id` 和 `transport=nats`。

Stats 是按需快照，不在每条消息上增加 M6 自己的原子计数；优先读取 `nats.go` 已维护的
统计。

## 20. 性能设计

M6 的低延迟原则：

1. 不在官方客户端外再加发送队列；
2. 不为每条消息创建 goroutine；
3. 不在 Publish 前复制 payload；
4. Publish 返回后由上层立即释放自己的 Buffer；
5. 入站 Message 直接引用 `nats.Msg.Data`，不复制到 BufferPool；
6. 不对每条 Publish 执行 Flush；
7. Handler 使用函数类型，不建立反射分发；
8. 状态事件和日志不进入普通消息热路径；
9. 不使用 Headers 承载 Origin RPC 元数据，M15 统一放入 RPC payload 或 Subject；
10. Origin RPC 不调用原生同步 Request，避免与 M11 重复建立响应 Channel、pending 状态和
    Deadline；
11. M15 每个 Node 只使用稳定的请求/响应 Subscription，不按 Service 或 RPC 调用创建
    Subscription；
12. 用 Benchmark 对比直接 `nats.go` 与 `natsnet` 包装层，检查额外分配。

无法避免的成本：

- 官方客户端会把 Publish 数据复制到自己的写缓冲；
- 官方客户端会为入站 `nats.Msg` 和 Data 分配内存；
- NATS 多一跳 Broker，延迟通常高于 Node 间直接 TCP。

M6 不使用 unsafe、自定义 NATS 协议或复杂对象池绕过这些成熟客户端边界。

## 21. Subject 与 M15 边界

M6 只把 Subject 当作调用方提供的 NATS 地址：

- 不添加 `origin.`、`rpc.`、NodeID 或 ServiceName 前缀；
- 不解析通配符；
- 不自动修改 Queue Group；
- 不通过 Header 注入来源 Node；
- 不知道 Request、Notify、Broadcast 或 Discovery。

M15 单独设计：

- 基于 NodeID 的稳定 RPC Request/Response Subject；
- App/环境隔离前缀；
- NodeID、ServiceName、ContractID 和版本；
- 单 Node 单逻辑广播订阅；
- Deadline、RequestID 和错误码线协议；
- 退休、路由和服务发现变化。

这样不会把 v2 的 `os.<NodeID>`、`oc.<NodeID>` 再次固化到通用 NATS 包。

### 21.1 服务关注与 NATS 订阅的边界

服务发现和 NATS Subscription 是两层不同概念：

- `allow_discovery` 按 `NodeID + ServiceName` 决定当前 Node 能看见和关注哪些远端服务；
- RPC Route 在这些可见实例中选择最终 `NodeID + ServiceName`；
- NATS Adapter 取得选择结果后，只使用 NodeID 计算目标 Node 的请求 Subject；
- ServiceName 放入统一 RPC 数据包，由目标 Node 的 RPC Runtime 做本地 Service 分发；
- 关注一个远端 Service 不会创建一条新的 NATS Subscription，也不会创建专属 NATS
  Connection；
- 本地新增或停止 Service 不改变 Node 级请求/响应 Subscription 数量。

这样既保留服务级发现、筛选和路由，又维持 v2 的 Node 级传输模型，并与 TCP 的 Node 级
Connection 多路复用保持一致。

### 21.2 Queue Group 使用边界

官方 NATS 微服务通常使用 Queue Group 在多个相同 Endpoint 间随机分担请求，这适合“任一
无状态实例均可处理”的服务。

Origin RPC 首版不这样处理。普通 RPC 已经通过服务发现、`RouteRandom`、`RouteKey`、
`RouteNode`、Node Label 和退休状态选出明确目标 Node；如果随后再把请求交给 Queue Group
随机选择，会产生以下问题：

- 绕过已经确认的路由策略；
- 相同 Player Key 的调用可能落到不同 Node，破坏顺序和状态归属；
- 退休 Node 是否接收请求由 Broker 随机结果决定；
- Broadcast 无法保证“每个被发现目标 Node 各投递一次”；
- 重复 NodeID 等部署错误可能被 Queue Group 隐藏。

因此 M15 首版规则为：

1. Request、Notify 和逐 Node Broadcast 均发布到明确目标 Node 的稳定 Subject；
2. 目标 Node 使用普通 Subscription，不加入 Queue Group；
3. Broadcast 由 RPC Runtime 根据发现结果逐 Node 投递，延续已经确认的简单语义；
4. Queue Group 仅作为 M6 通用能力保留，不进入默认 Origin RPC；
5. 未来若需要“由 Broker 随机选择任一无状态实例”，必须作为显式路由策略单独设计，不能
   暗中改变现有 Route 语义。

## 22. 与 M5 TCP 的共同点和差异

共同点：

- 都是内部基础库；
- 都使用 Transport 错误码；
- 都有明确 Close/Wait 和资源所有者；
- 都不执行 Service 业务逻辑；
- 都限制消息大小和内存；
- 都由 M13/M15 Adapter 接入同一 RPC Runtime。

差异：

- TCP 是点到点 Connection，NATS 是 Node 到 Broker 的共享连接；
- TCP 使用 Origin 自有有界发送队列，NATS 使用官方客户端写缓冲和重连缓冲；
- TCP 可以在分配 payload 前检查长度头，NATS 的首次分配由官方客户端完成；
- TCP Handler 接管 Buffer 所有权，NATS Handler 接收 GC 管理的只读 Message；
- TCP 和 M6 都只向 RPC Runtime 提供字节发送与接收，Origin RPC 不使用 NATS Inbox；
- NATS 支持 Queue Group、自动重订阅和 Broker Lame Duck。

不建立强行抹平这些差异的共同基础接口。

### 22.1 RPC 适配约束

“不建立共同大接口”不等于 TCP 和 NATS 各自实现一套 RPC。适配边界固定如下：

- M11 拥有生成客户端、普通 Go 静态编解码、同 Node 调用和 Dispatcher 语义，M12 只补充
  自定义静态 Codec；
- M13 在统一 RPC Runtime 中补齐 RequestID、pendingCall、默认 `15s` Deadline、远端取消、
  统一错误码和响应完成；
- M13 TCP Adapter 只负责把统一 RPC 帧发送到选定 TCP Connection，并把入站帧交给统一
  RPC Runtime；
- M15 NATS Adapter 只负责把同一 RPC 帧发布到选定的稳定 Subject，并把请求/响应订阅收到
  的数据交给统一 RPC Runtime；
- 两个 Adapter 使用相同的 Request/Response/Notify/Broadcast 消息分类和 RPC 线协议；
- 两个 Adapter 进入同一个 Service 调度入口，业务 Service 和生成代码不知道当前使用
  TCP 还是 NATS；
- `AsyncXxx`、`AwaitXxx`、`NotifyXxx` 和 Broadcast 的用户外观不因 Transport 改变；
- Transport 发送成功都只表示“本地传输层已接受”，RPC 完成必须等待统一 RPC Runtime 收到响应或
  Deadline；
- RPC Runtime 的同步入站处理函数在返回后不能继续引用传入的原始字节；TCP Adapter 随后归还
  BufferPool，NATS Adapter 随后释放对 `Msg.Data` 的引用。

共同点停在 RPC 语义和线协议层，不强求共同的底层对象：

- TCP 入站持有 `*bufferpool.Buffer`，需要显式归还；
- NATS 入站持有 GC 管理的 `[]byte`，无需也不能归还给 Origin BufferPool；
- Adapter 分别处理所有权，再调用同一 RPC Runtime 入站函数；
- 首版不创建 `TransportMessage` 接口或 `Release func()`，避免每消息接口装箱、闭包和逃逸。

这套边界既保证“RPC 与 TCP 适配”，又避免为了形式统一增加热路径复制。

## 23. 测试设计

### 23.1 单元测试

- 默认值和全部非法 Options；
- URL、认证互斥、TLS 文件和脱敏；
- Context 默认 Deadline；
- NATS error 到 Origin Code 映射；
- Message 大小边界、空 payload 和空 Subject；
- 状态机、首个关闭原因、重复 Close/Wait；
- Handler 和 EventHandler panic；
- Subscription 默认值和非法 Pending；
- Stats 快照；
- Publish 返回后的源切片复用规则；
- 入站 Handler 返回后不保留 Data 的所有权约束。

### 23.2 真实 NATS 集成测试

建议测试使用官方 `nats-server/v2` 在进程内启动真实 Server，不创建手工测试 `main`。

建议固定测试依赖：

```text
github.com/nats-io/nats-server/v2 v2.14.3
```

该依赖只由 `tests/integration/natsnet` 导入，不进入生产二进制，但会出现在根 `go.mod` 和
`go.sum`。它会增加测试依赖规模，换取无 Docker、无预装二进制、可重复的真实协议测试。

真实测试覆盖：

- 普通 Publish/Subscribe；
- 空 payload；
- Queue Group 只由一个成员接收；
- 多个稳定 Node Subject 的隔离发布订阅；
- 请求和响应普通 Publish 的双向传输；
- Subscribe 返回前 Flush 屏障；
- Server 停止和同端口重启后的断开、重连和自动重订阅；
- 有限重连耗尽；
- Reconnect Buffer 满；
- Pending 消息数和字节数上限；
- Slow Consumer 和 Dropped；
- Connection/Subscription Drain；
- 用户名密码、Token、Credentials/NKey；
- CA、Server 证书和双向 TLS；
- 最大 payload；
- 多 Connection 隔离；
- 连接和订阅全部关闭后的 goroutine 回收。

跨包集成测试放在：

```text
tests/integration/natsnet/
```

### 23.3 Benchmark

- 直接 `nats.go Publish` 与 `natsnet.Publish`；
- 64B、1KB、16KB、64KB 和 1M payload；
- 单 Publisher、并行 Publisher；
- 单 Subscription 吞吐；
- 稳定请求/响应 Subject 的普通 Publish 往返；
- Handler 包装额外分配；
- 入站直接使用 Data 与“复制到 BufferPool”的 `B/op`、`allocs/op`、CPU 对照；
- Publish 后每次 Flush 的延迟对照，但生产默认不使用；
- 重连 Buffer 的内存边界。

Benchmark 保存 `ns/op`、`B/op`、`allocs/op`，不使用跨机器绝对阈值。

## 24. 跨平台与质量门禁

M6 完成前必须执行：

```text
gofmt
go vet ./...
go test ./...
go test -race ./...
go test -count=20 ./internal/natsnet/...
go test -count=10 ./tests/integration/natsnet/...
go test -coverprofile cover.out ./internal/natsnet/...
go tool cover -func cover.out
go test -run '^$' -bench . -benchmem ./internal/natsnet/...
scripts\buildwin.bat
scripts\buildlinux.bat
GOOS=darwin GOARCH=amd64 go build ./...
GOOS=darwin GOARCH=arm64 go build ./...
```

Windows 和 Linux 必须分别执行真实 NATS Server 集成测试。macOS 至少完成 amd64/arm64
交叉构建；具备真实环境时补充原生集成测试。

## 25. 实施顺序建议

设计确认后再建立 M6 实施计划，建议按以下步骤执行：

1. 固定 `nats.go` 和真实测试 Server 依赖；
2. 实现 Options、认证、TLS、URL 脱敏和错误映射；
3. 实现 Context-aware 初始连接和事件状态；
4. 实现 Publish 和 Flush；
5. 实现 Subscription、Pending、Slow Consumer 和统计；
6. 实现 Close、Drain 和 Wait；
7. 补齐真实 Server 重连、认证、TLS 和过载测试；
8. 完成 Fuzz、Benchmark、逃逸、竞态和跨平台验收；
9. 回写设计、计划、索引、复核清单和迁移矩阵；
10. 形成 M6 独立提交。

## 26. 已确认结论

以下方案已于 2026-07-26 全部确认，作为 M6 实施与验收基线：

1. 生产固定使用 `nats.go v1.52.0`；
2. 测试固定使用 `nats-server/v2 v2.14.3` 进程内真实 Server，接受测试依赖增加；
3. 包名使用 `internal/natsnet`；
4. M6 只支持 Core NATS，不支持 JetStream；
5. 一个 Node 持有一条 NATS Connection，Connection 复用全部 RPC/发现订阅；
6. 初始连接失败直接返回，由 Node 启动层决定重试；连接成功后的重连最多 60 次；
7. 默认 Ping `30s`、两次未响应判定失活；
8. 默认重连等待 `2s`，普通 Jitter `500ms`、TLS Jitter `1s`；
9. 默认 Reconnect Buffer `8M`，其“本地接受但可能延迟发送”语义必须显式；
10. 默认单 Subscription Pending 为 `16384` 条和 `8M`；
11. MessageHandler 不返回 error，panic 只丢当前消息并继续订阅；
12. Connection 和 Subscription 同时提供立即 `Close` 与有 Deadline 的 `Drain`；
13. M6 使用原始 `[]byte`，不接入 BufferPool，也不额外复制 payload；接受官方客户端的
    固有 GC 分配，并通过 Benchmark 记录基线；
14. M6 不定义任何 Origin Subject，全部留给 M15 和服务发现系统；
15. 不建立 TCP/NATS 共同大接口，但 M13/M15 必须接入同一 RPC Runtime、M12 Codec 和统一线协议；
16. M15 不使用 NATS 原生 Request/Reply；每个 Node 使用稳定的 Node 级请求/响应 Subject
    和普通 Publish，ServiceName 与 RequestID 放入统一 RPC 线协议；
17. 普通 Origin RPC 不使用 Queue Group，继续由服务发现和 Route 明确选择目标 Node；
18. RPC 入口过载时 Request 立即回复统一过载错误，Notify 计数并丢弃，不阻塞 NATS
    Subscription goroutine。

## 27. 实施与验收结果

M6 已于 2026-07-26 按本设计完成实现：

1. 新增内部包 `internal/natsnet`，提供严格配置校验、Context-aware 初始连接、有限重连、
   Publish、Flush、普通订阅、Queue Group、Pending 双重上限、统计、事件、Close、Drain 和
   Wait；
2. 用户名密码、Token、Credentials File、NKey Seed、TLS 与双向 TLS 配置已经接入官方
   `nats.go` 客户端；认证方式互斥，错误、事件与 URL 均执行敏感信息脱敏；
3. 空 payload 可以正常发布和接收；包装层不对出站 payload 再复制，也不把入站
   `Msg.Data` 二次复制到 BufferPool；
4. 初始连接可以由 Context 中止；成功连接后的断线使用有限自动重连，并由事件报告断开、
   重连、慢消费者、Lame Duck 和最终关闭；
5. Connection 与 Subscription 的关闭和排空均为幂等、有界操作；排空超时会强制关闭，
   不会让停服永久等待；
6. 单元测试和进程内真实 NATS Server 集成测试覆盖发布订阅、Queue Group、空消息、大小
   边界、用户名密码、Token、NKey、TLS、双向 TLS、慢消费者、重连、重订阅、Handler
   panic、排空超时及并发关闭；
7. Windows 全仓测试、全仓竞态检测、重复测试、`go vet`、Windows/Linux 构建和 macOS
   amd64/arm64 交叉构建通过；
8. Linux amd64 测试二进制已在 Ubuntu 真实运行；已安装的三节点 NATS 集群通过跨节点
   发布订阅与节点故障重连测试，测试结束后三个节点均恢复运行；
9. `internal/natsnet` 与跨包集成测试合并统计的语句覆盖率为 `87.2%`；
10. Windows/amd64 上 `Message` 包装基准为 `0.5545 ns/op`、`0 B/op`、
    `0 allocs/op`，包装本身没有引入堆分配。

详细实施步骤和验证记录见
[M6 NATS 基础库实施计划](../../plans/M6-NATS基础库实施计划.md)。
