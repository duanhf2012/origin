# WebSocket 网络模块使用指南

WebSocket Module 适合浏览器、网关和需要穿过 HTTP 基础设施的长连接。业务事件仍使用统一的
`network.Session` 与 `network.Handler`；只有 Upgrade、Origin、Text/Binary、TLS 和心跳留在
`websocket` 子包。

## 1. 在业务 Module 中创建 Server

推荐由项目自己的业务 Module 保存 Server 和消息处理逻辑，Service 只负责装配业务 Module：

```go
type GatewayWebSocketModule struct {
    service.Module
    server *websocket.Server
}

func (module *GatewayWebSocketModule) OnInit() error {
    handler := network.HandlerFuncs{Message: module.onMessage}
    cfg := websocket.DefaultServerConfig()
    if err := module.GetServiceConfigStrict("websocket.server", &cfg); err != nil {
        return err
    }
    options, err := cfg.Options(handler)
    if err != nil {
        return err
    }
    // TLSConfig 和 CheckOrigin 等运行期安全策略在这里注入。
    server, err := websocket.NewServer(cfg.Address, options)
    if err != nil {
        return err
    }
    module.server = server
    return module.AddModule(server)
}

func (module *GatewayWebSocketModule) onMessage(
    _ context.Context,
    session network.Session,
    payload []byte,
) error {
    // payload 只在回调返回前有效；Send 会安全复制。
    return session.Send(payload)
}

type GatewayService struct{ service.Service }

func (target *GatewayService) OnInit() error {
    return target.AddModule(&GatewayWebSocketModule{})
}
```

`address` 决定监听 Socket，`path` 决定 HTTP Upgrade 路由，因此保持两个字段。以下是完整起始值；
生产部署应按实际入口、协议和容量收紧：

```yaml
services:
  GatewayService:
    websocket:
      server:
        # 监听地址；0.0.0.0 暴露全部 IPv4 网卡，生产环境需同时配置网络访问控制。
        address: "0.0.0.0:19091"
        # Upgrade 路由，必须以 / 开头且不含查询或片段；反向代理转发路径必须与它一致。
        path: "/ws"
        # Raw/PB 使用 binary；浏览器直接发送 JSON 文本时使用 text。
        message_type: binary
        # HTTP Upgrade 最长时间，必须大于 0s。
        handshake_timeout: 10s
        # WebSocket 协议控制帧心跳，不进入业务 OnMessage；两项同为 0s 时关闭。
        ping_interval: 30s
        # 启用时必须大于 ping_interval，建议先取其 2 倍。
        pong_timeout: 60s
        # 允许协商的应用子协议；没有明确需求时保持为空。
        subprotocols: []
        # 首轮连接容量；按文件描述符、内存和压测结果调整。
        max_sessions: 4096
        # 单条完整 Data Message 上限；建议按真实协议收紧。
        max_message_size: 64KB
        # 单连接与整个 Server 的入站积压边界。
        receive_pending_messages: 64
        receive_pending_size: 256KB
        receive_pending_total_size: 64M
        # 单连接与整个 Server 的出站积压边界。
        send_queue_messages: 256
        send_queue_size: 256KB
        send_queue_total_size: 128M
        # 业务 Data Message 读空闲；Ping/Pong 不刷新它，0s 表示关闭。
        read_idle_timeout: 0s
        # 单条完整 Data Message 写出上限，必须大于 0s。
        write_timeout: 15s
        # 发送队列连续高水位上限，超时关闭慢连接。
        slow_client_timeout: 10s
```

未出现的字段使用 `DefaultServerConfig` 的默认值；严格读取会拒绝未知字段。TLS、Origin 回调和 Header
保存运行期对象或安全策略，不进入普通 YAML。生产环境通常先确认 `address`、`path`、
`message_type`、Origin/TLS 策略和 `max_sessions`，其余容量从默认值开始压测；不要因为最大连接数较大
就同步放大每连接队列，端点总预算才是限制整体积压内存的边界。

这种结构把协议路由、连接状态和网络事件集中在业务 Module，避免业务代码散落到 Service。默认 Path
是 `/ws`，消息类型是 Binary。完整程序见
[`02-websocket-raw-self-call`](../../../../examples/13-network/02-websocket-raw-self-call/)。

`OnOpen → OnMessage/OnWritableChanged → OnClose` 都进入所属 Service 的串行上下文；同一 Session
保序，`OnClose` 恰好一次且最后执行。

## 2. 函数、回调参数与执行协程

这里的“执行协程”表示并发访问域，不承诺固定 goroutine ID。URL、Path、Header 和 SessionID 等
普通参数会由调用函数同步读取或克隆；真正会延后执行的是 Handler、StateChange、CheckOrigin、TLS
回调、Codec 和注册的消息 Handler。

| 函数或函数族 | 函数参数实际执行位置 | 参数与所有权规则 | 可直接访问 Service 串行状态 |
| --- | --- | --- | --- |
| `Default*Config()`、`Default*Options(handler)`、`Config.Options(handler)` | 当前调用 goroutine；`handler` 只被保存 | 配置同步读取，Header、子协议和 TLS 配置在构造路径克隆 | 仅装配，不应处理业务状态 |
| `NewServer/NewClient/NewDialer(addressOrURL, options)` | 当前调用 goroutine；Options 中的回调只被保存 | 构造器同步校验并冻结可变配置 | 仅装配，不应处理业务状态 |
| `OnInit/OnStart/OnStop(ctx)` | Origin 管理的 Service 生命周期上下文；HTTP Upgrade、连接读写、心跳和重连另有内部 goroutine | 应通过 `AddModule` 让框架调用 | 生命周期装配可以；不要当成普通消息回调 |
| `Handler.OnOpen/OnMessage/OnWritableChanged/OnClose` 及 `HandlerFuncs` 对应函数字段 | 所属 Service 工作协程串行执行；停止收口时 `OnClose` 在独占 finalizer 上下文执行 | `ctx` 和 `payload` 只在回调期间有效；保存 payload 前必须复制 | 是 |
| `ClientOptions.StateChange(ctx, snapshot)` | 所属 Service 串行上下文 | 不可变快照；等待外部 I/O 时使用 `Await` | 是 |
| `ServerOptions.CheckOrigin(request)` | 当前 HTTP Upgrade 请求的网络 goroutine，且不同连接可以并发调用 | `request` 只用于本次检查；只能访问不可变或并发安全数据 | 否 |
| `TLSConfig` 的证书、配置和校验回调 | TLS 握手所在的网络 goroutine，且不同连接可以并发调用 | TLSConfig 已克隆，但回调闭包捕获的数据仍须并发安全 | 否 |
| `Session.Send/Close` 与全部查询方法 | 当前调用 goroutine；实际帧读写与 Ping/Pong 在内部 I/O goroutine | `Send` 返回前复制 payload；调用不改变当前执行权 | 只有调用方原本就在 Service 上下文时才可以访问业务状态 |
| `Server.Addr/Session/SessionCount/CloseSession/Stats`、`Client.Session/State/Stats` | 当前调用 goroutine | 返回并发安全快照或 Session；最终 `OnClose` 稍后回到 Service | 调用本身不取得 Service 执行权 |
| `Dialer.Dial(ctx, owner)` | 调用 goroutine 同步等待 DNS/TCP/TLS/Upgrade；`OnOpen` 在 `owner` Service 工作协程 | `ctx` 控制拨号；返回 Session 由调用方关闭 | 在 `owner` Task 内必须放入 `Await`，避免等待自己持有的执行权 |
| `protocol.NewRouter/Register/Freeze` | 当前调用 goroutine；注册的 Handler 只保存 | 必须在 Module `OnInit` 冻结前注册完成 | 仅装配 |
| `Codec.Decode`、`RouterOptions.Unknown`、注册的消息 Handler | 所属 Service 工作协程，位于 `OnMessage` 的同一个 Task | Decode payload 是借用视图 | 是 |
| `Router.Send` 与 `Codec.Encode` | 调用 `Send` 的当前 goroutine 同步执行 | Encoder 只在本次 Encode 内有效；跨 goroutine 调用时 Codec 和消息值必须并发安全 | 只有调用方原本就在 Service 上下文时才可以 |

因此，Origin/Token 检查如果放在 `CheckOrigin` 中就必须是并发安全的无状态判断；需要读取当前 Service
玩家或权限数据时，应在连接建立后的业务 Handler 中完成。Service Task 内使用一次性 Dialer 时，先用
`Await` 释放执行权。

## 3. 浏览器、Text 与 Origin

浏览器直接发送 JSON 文本时，两端都设置 Text：

```go
options.MessageType = websocket.TextMessage
```

Text Payload 必须是有效 UTF-8。使用 PB 或任意二进制协议时保留默认 Binary。连接只接受配置的
一种类型；类型不匹配会按协议错误关闭，避免同一端点出现两套含义。

默认 Origin 策略是安全同源：请求带 `Origin` 时，其 Host 必须与目标 Host 相同。只有明确需要
跨域时才设置 `CheckOrigin`，并校验完整允许列表：

```go
allowed := map[string]bool{"https://game.example.com": true}
options.CheckOrigin = func(request *http.Request) bool {
    return allowed[request.Header.Get("Origin")]
}
```

不要使用无条件 `return true`。非浏览器客户端通常不发送 Origin，不受同源检查影响；登录鉴权仍放在
首条业务消息或业务协议中，不把 `CheckOrigin` 当作用户身份认证。

## 4. Client 与一次性 Dialer

长期连接使用由 Module 托管的 Client：

```go
cfg := websocket.DefaultClientConfig()
if err := module.GetServiceConfigStrict("websocket.client", &cfg); err != nil {
    return err
}
options, err := cfg.Options(handler)
if err != nil {
    return err
}
options.Dial.Header = http.Header{"Authorization": []string{"Bearer ..."}}

client, err := websocket.NewClient(cfg.URL, options)
if err != nil {
    return err
}
return module.AddModule(client)
```

这里的 `module` 是项目自己的业务 Module。需要同时提供 Server 和 Client 时，先加入 Server、再加入
Client，使 Client 启动连接前监听端已经就绪；停止时框架会按相反顺序清理。

请求 Header 不能覆盖 WebSocket 握手保留字段。开启重连后默认最多尝试 10 次，退避从 200ms 增长到
5s，并带 20% 抖动；每次成功连接都会产生新的 Session。

`websocket.Dialer` 只拨号一次，不重连。它要求 owner Service 已处于 Running/Retired，调用方必须在
owner 停止前关闭返回的 Session。需要自动停止或重连时使用 Client。
Dialer 不读取 YAML，也没有 `DialerConfig`；从 `DefaultDialOptions(handler)` 开始在代码中按需覆盖。

## 5. TLS 与子协议

WSS Server 使用标准 `tls.Config`：

```go
certificate, err := tls.LoadX509KeyPair("server.crt", "server.key")
if err != nil {
    return err
}
options.TLSConfig = &tls.Config{
    MinVersion:   tls.VersionTLS12,
    Certificates: []tls.Certificate{certificate},
}
```

Client URL 使用 `wss://`。默认使用系统根证书；私有 CA 应加入 `TLSConfig.RootCAs`，生产环境不要设置
`InsecureSkipVerify`。Server 与 Client 通过 `Subprotocols` 按顺序协商应用子协议；没有真实协商
需求时保持为空。

## 6. 心跳、容量与错误

默认每 30s 发送 Ping，60s 未收到 Pong 即关闭连接。两项必须同时为零或同时为正；同时设为零可关闭
协议心跳。`Network.ReadIdleTimeout` 独立限制两条完整业务消息之间的空闲时间，Pong 不会把业务
空闲时间重置。

WebSocket 使用原生 Message 边界，不再嵌套 TCP 长度帧，也没有端序选项。公共容量默认与 TCP
相同：64KiB 单消息、每 Session 64 条/256KiB 入站、256 条/256KiB 出站，以及端点级总字节预算。

`Session.Send` 非阻塞提交：

- `nil`：发送队列已经接管消息；
- `ErrTransportOverloaded`：容量已满，可靠消息应稍后重试或关闭连接；
- `ErrTransportClosed`：连接已经关闭；
- `ErrTransportMessageTooLarge`：消息超过上限；
- `ErrTransportProtocol`：Text 不是有效 UTF-8 或收到错误消息类型。

`Writable` 只是高低水位提示，最终以 `Send` 返回值为准。队列持续高水位超过
`SlowClientTimeout` 时会关闭慢连接。

## 7. PB、JSON 与所有权

PB/JSON Router 与 TCP 完全相同：PB、Raw 使用默认 Binary；希望浏览器直接查看 JSON 时使用 Text。
WebSocket 不改变 PB/JSON 的 MessageID 或 Envelope。

- Raw `Send([]byte)` 会复制，返回后可立即复用原 Slice；
- `OnMessage` 的 payload 是借用视图，异步使用或保存前必须复制；
- 不向业务暴露内部 Pool、发送 Ring 或 Gorilla `Conn`；
- Server/Client 必须通过 `AddModule` 管理，不要直接调用生命周期方法。
