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
    server, err := websocket.NewServer(
        "127.0.0.1:19091",
        websocket.DefaultServerOptions(handler),
    )
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

这种结构把协议路由、连接状态和网络事件集中在业务 Module，避免业务代码散落到 Service。默认 Path
是 `/ws`，消息类型是 Binary。完整程序见
[`02-websocket-raw-self-call`](../../../../examples/13-network/02-websocket-raw-self-call/)。

`OnOpen → OnMessage/OnWritableChanged → OnClose` 都进入所属 Service 的串行上下文；同一 Session
保序，`OnClose` 恰好一次且最后执行。

## 2. 浏览器、Text 与 Origin

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

## 3. Client 与一次性 Dialer

长期连接使用由 Module 托管的 Client：

```go
options := websocket.DefaultClientOptions(handler)
options.Dial.Header = http.Header{"Authorization": []string{"Bearer ..."}}
options.Reconnect.Enabled = true // 默认 false

client, err := websocket.NewClient("wss://gateway.example.com/ws", options)
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

## 4. TLS 与子协议

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

## 5. 心跳、容量与错误

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

## 6. PB、JSON 与所有权

PB/JSON Router 与 TCP 完全相同：PB、Raw 使用默认 Binary；希望浏览器直接查看 JSON 时使用 Text。
WebSocket 不改变 PB/JSON 的 MessageID 或 Envelope。

- Raw `Send([]byte)` 会复制，返回后可立即复用原 Slice；
- `OnMessage` 的 payload 是借用视图，异步使用或保存前必须复制；
- 不向业务暴露内部 Pool、发送 Ring 或 Gorilla `Conn`；
- Server/Client 必须通过 `AddModule` 管理，不要直接调用生命周期方法。
