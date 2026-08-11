# TCP 网络模块使用指南

TCP Module 适合游戏客户端、网关、自定义长连接协议等需要直接收发逻辑消息的场景。它与 Origin
跨节点 RPC 是两套外观：业务 RPC 优先使用生成客户端；只有需要控制 Wire Format 时才使用本模块。

## 1. 先选择 Raw 还是 Router

- 已有自定义协议，或只处理字节：直接实现 `network.Handler`，这是最轻的路径；
- 使用 Protobuf：`protocol/pb` 固定为“2 字节 MessageID + protobuf payload”；
- 使用 JSON：`protocol/json` 固定为 `{"id":1,"data":{...}}`；
- 需要其他格式：实现无状态 `protocol.Codec`，继续复用 Router 的注册与分发。

不要为了统一形式给 Raw 消息套一个空 Codec。Gin/HTTP 也不接入这套 Session 外观。

## 2. 在业务 Module 中创建 Raw Server

推荐由项目自己的业务 Module 保存 Server 和消息处理逻辑，Service 只负责装配业务 Module：

```go
type GatewayTCPModule struct {
    service.Module
    server *tcp.Server
}

func (module *GatewayTCPModule) OnInit() error {
    handler := network.HandlerFuncs{Message: module.onMessage}
    cfg := tcp.DefaultServerConfig()
    if err := module.GetServiceConfigStrict("tcp.server", &cfg); err != nil {
        return err
    }
    options, err := cfg.Options(handler)
    if err != nil {
        return err
    }
    server, err := tcp.NewServer(cfg.Address, options)
    if err != nil {
        return err
    }
    module.server = server
    return module.AddModule(server)
}

func (module *GatewayTCPModule) onMessage(
    _ context.Context,
    session network.Session,
    payload []byte,
) error {
    // payload 只在回调返回前有效；Send 会安全复制。
    return session.Send(payload)
}

type GatewayService struct{ service.Service }

func (target *GatewayService) OnInit() error {
    return target.AddModule(&GatewayTCPModule{})
}
```

对应配置放在当前 Service 下。以下是可以直接运行的完整起始值，不是所有部署都应达到的容量目标：

```yaml
services:
  GatewayService:
    tcp:
      server:
        # 监听地址；0.0.0.0 暴露全部 IPv4 网卡，生产环境需同时配置网络访问控制。
        address: "0.0.0.0:19090"
        frame:
          # 长度字段建议保留 4 字节 Big Endian；使用 1/2 字节时必须同步降低消息上限。
          length_field_size: 4
          byte_order: big
        # OS TCP KeepAlive 首次探测前的空闲时间；不是业务心跳，0s 表示关闭。
        keep_alive: 30s
        # 首轮连接容量；按文件描述符、内存和压测结果调整，不要仅为“预留”而调大。
        max_sessions: 4096
        # 完整业务消息上限；建议按真实协议收紧。
        max_message_size: 64KB
        # 单连接入站积压：消息数上限与 Buffer 保留容量上限。
        receive_pending_messages: 64
        receive_pending_size: 256KB
        # 全部连接的入站 Buffer 总预算。
        receive_pending_total_size: 64M
        # 单连接出站积压：消息数上限与 Payload 保留容量上限。
        send_queue_messages: 256
        send_queue_size: 256KB
        # 全部连接排队及正在写出 Payload 的总预算。
        send_queue_total_size: 128M
        # 业务消息读空闲检查；0s 关闭。启用时应大于业务心跳最大间隔。
        read_idle_timeout: 0s
        # 单条完整消息写出上限，必须大于 0s。
        write_timeout: 15s
        # 发送队列连续高水位上限，超时关闭慢连接。
        slow_client_timeout: 10s
```

严格读取会拒绝未知字段，避免字段拼错后静默使用默认值。`Handler` 等运行期对象不写入 YAML，继续在
业务 Module 中显式注入。实际项目可以保留完整配置，也可以删掉不准备调整的字段，让
`DefaultServerConfig` 补齐；建议先只确认监听地址、帧格式、消息上限和连接上限。

调整容量时遵守三个关系：`max_message_size` 不得超过长度字段表达范围；
`receive_pending_size`/`send_queue_size` 不得小于该消息上限对应的池化容量；两个 `*_total_size`
不得小于对应的单连接字节上限。总预算是真正限制整个 Server 内存积压的边界，不能用
`max_sessions × 单连接上限` 直接替代。

这种结构把协议路由、连接状态和网络事件集中在业务 Module，避免业务代码散落到 Service。完整程序见
[`01-tcp-raw-self-call`](../../../../examples/13-network/01-tcp-raw-self-call/)。

`Handler` 的 `OnOpen → OnMessage/OnWritableChanged → OnClose` 全部进入所属 Service 的串行上下文；
同一 Session 保序，`OnClose` 恰好一次且最后执行。

## 3. 函数、回调参数与执行协程

这里的“执行协程”表示并发访问域，不承诺固定 goroutine ID。`address`、`id`、`payload` 等普通参数
本身不会被延后执行；表中同时说明它们在哪一步被读取或复制。Handler、StateChange、Codec 和注册的
消息 Handler 等函数参数才会在之后被框架调用。

| 函数或函数族 | 函数参数实际执行位置 | 参数与所有权规则 | 可直接访问 Service 串行状态 |
| --- | --- | --- | --- |
| `Default*Config()`、`Default*Options(handler)`、`Config.Options(handler)` | 当前调用 goroutine；`handler` 只被保存，不在构造时调用 | 配置和普通参数同步读取 | 仅装配，不应处理业务状态 |
| `NewServer/NewClient/NewDialer(address, options)` | 当前调用 goroutine；Options 中的回调只被保存 | 构造器同步校验并保存配置 | 仅装配，不应处理业务状态 |
| `OnInit/OnStart/OnStop(ctx)` | Origin 管理的 Service 生命周期上下文；运行期 I/O 和 Client 重连另有内部 goroutine | 应通过 `AddModule` 让框架调用，不要手动并发调用 | 生命周期装配可以；不要当成普通消息回调 |
| `Handler.OnOpen/OnMessage/OnWritableChanged/OnClose` 及 `HandlerFuncs` 对应函数字段 | 所属 Service 工作协程串行执行；停止收口时 `OnClose` 在独占 finalizer 上下文执行 | `ctx` 和 `payload` 只在回调期间有效；保存 payload 前必须复制 | 是 |
| `ClientOptions.StateChange(ctx, snapshot)` | 所属 Service 串行上下文 | 不可变快照；不得阻塞，等待 I/O 时使用 `Await` | 是 |
| `Session.Send/Close/ID/Transport/LocalAddr/RemoteAddr/Context/Done/Writable/Cause/Stats` | 当前调用 goroutine；`Send/Close` 只同步提交，实际网络读写由内部 I/O goroutine 完成 | `Send` 返回前复制 payload；其他返回值按接口注释使用 | 调用本身并发安全，但不因此取得 Service 执行权 |
| `Server.Addr/Session/SessionCount/CloseSession/Stats`、`Client.Session/State/Stats` | 当前调用 goroutine | 返回并发安全快照或 Session；`CloseSession` 的 `OnClose` 稍后回到 Service | 调用本身并发安全，但不因此取得 Service 执行权 |
| `Dialer.Dial(ctx, owner)` | 调用 goroutine 同步等待；连接 I/O 在内部 goroutine，`OnOpen` 在 `owner` Service 工作协程 | `ctx` 控制整个拨号等待；返回 Session 由调用方关闭 | 在 `owner` 的 Task 中调用时必须放进 `Await`，否则会等待自己持有的执行权 |
| `protocol.NewRouter/Register/Freeze` | 当前调用 goroutine；注册的 Handler 只保存到构造期路由表 | 必须在 Module `OnInit` 冻结前完成注册 | 仅装配 |
| `Codec.Decode`、`RouterOptions.Unknown`、注册的消息 Handler | 所属 Service 工作协程，位于 `OnMessage` 的同一个 Task | Decode 的 payload 是借用视图；消息 Handler 可以访问 Service 状态 | 是 |
| `Router.Send` 与 `Codec.Encode` | 调用 `Send` 的当前 goroutine 同步执行 | Encoder 仅在本次 Encode 内有效；跨 goroutine 调用时 Codec 和消息值必须并发安全 | 只有调用方原本就在 Service 上下文时才可以 |

最容易出错的是 `Dialer.Dial`：它虽然由当前 goroutine 发起，却要等待 `owner` Service 中的 `OnOpen`。
从该 Service 的 Timer、事件、RPC 或网络 Handler 内拨号时，应使用 `module.Await(ctx, func(waitCtx
context.Context) error { ... })` 释放 Service 执行权后再调用。

## 4. Client 与一次性 Dialer

长期连接使用 `tcp.Client`，它由 Module 自动停止：

```go
cfg := tcp.DefaultClientConfig()
if err := module.GetServiceConfigStrict("tcp.client", &cfg); err != nil {
    return err
}
options, err := cfg.Options(handler)
if err != nil {
    return err
}
client, err := tcp.NewClient(cfg.Address, options)
if err != nil {
    return err
}
return module.AddModule(client)
```

这里的 `module` 是项目自己的业务 Module。需要同时提供 Server 和 Client 时，先加入 Server、再加入
Client，使 Client 启动连接前监听端已经就绪；停止时框架会按相反顺序清理。

`ClientConfig` 默认关闭重连；开启后每轮最多重试 10 次，退避从 200ms 增长到 5s，并带 20% 抖动。
每次重连都会产生
新的 Session 和 SessionID；不要把旧 Session 当作恢复后的连接。

`tcp.Dialer` 只尝试一次，不重连。它要求 owner Service 已经处于 Running/Retired，调用方必须在
owner 停止前关闭返回的 Session。需要自动停止或重连时使用 Client，不要用 Dialer 自建后台循环。
Dialer 不读取 YAML，也没有 `DialerConfig`；从 `DefaultDialOptions(handler)` 开始在代码中按需覆盖。

## 5. PB、JSON 与自定义 Codec

Router 在构造期注册消息，Module `OnInit` 会自动冻结它：

```go
codec, err := protocolpb.NewCodec(network.BigEndian)
if err != nil {
    return err
}
router, err := protocol.NewRouter(protocol.RouterOptions{Codec: codec})
if err != nil {
    return err
}

err = protocol.Register(router, 1001,
    func(ctx context.Context, session network.Session, request *gamepb.LoginRequest) error {
        return router.Send(session, 1002, &gamepb.LoginResponse{OK: true})
    },
)
```

示例中的 `gamepb.LoginRequest`/`LoginResponse` 表示使用者生成的 protobuf 类型，`protocolpb` 是
`sysmodule/network/protocol/pb` 的导入别名。JSON 的注册方式相同，
只需把 Codec 换成 `json.NewCodec()`；JSON 消息可使用普通结构体。

MessageID 是非零 `uint16`。PB MessageID 端序和 TCP 长度帧端序是两个独立配置，不要误以为修改
其中一个会同步修改另一个。未知 ID 默认按协议错误关闭 Session；需要忽略或记录时配置 `Unknown`。

## 6. 帧、容量与过载

TCP 每条消息前有 1、2 或 4 字节无符号长度。默认 4 字节 Big Endian；游戏客户端使用小端时设置：

```go
options.Frame.ByteOrder = network.LittleEndian
```

使用配置时从 `DefaultServerConfig`/`DefaultClientConfig` 开始；直接使用 Go Options 时仍从对应
`Default*Options` 开始，只修改需要的字段。默认限制包括 64KiB 单消息、
每 Session 64 条/256KiB 入站待处理、256 条/256KiB 出站队列，以及 Module 级总字节预算。容量按
Buffer 保留容量记账，不是只看 payload 长度。

`Session.Send` 非阻塞提交：

- `nil`：本地队列已经接管数据；
- `ErrTransportOverloaded`：容量已满，可靠消息应由业务稍后重试或关闭连接，不能静默丢弃；
- `ErrTransportClosed`：Session 已关闭；
- `ErrTransportMessageTooLarge`：消息超过配置。

`Writable` 和 `OnWritableChanged` 只提供高低水位提示，最终仍以 `Send` 返回值为准。队列持续高水位
超过 `SlowClientTimeout` 时会关闭慢连接，避免单个客户端长期占用内存。

## 7. 所有权与停止

- Raw `Send([]byte)` 会复制，调用返回后可立即复用原 Slice；
- `OnMessage` 的 payload 是借用视图，保存或异步使用前必须复制；
- Router 编码后的 Buffer 由框架接管，业务不能保存 Encoder 或跨 goroutine 使用；
- 不向业务暴露 Pool、引用计数 Buffer 或内部队列。

Server/Client 必须通过 `AddModule` 管理，不要直接调用它们的生命周期方法。停止时 Module 会停止
接入和重连、关闭底层连接、等待 I/O 退出，再在 Service finalizer 中交付剩余 `OnClose`。
