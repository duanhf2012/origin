# KCP 网络模块使用指南

KCP Module 适合对时延和弱网恢复较敏感、能够开放 UDP 端口的游戏长连接。它使用与 TCP、WebSocket
相同的 `network.Session`、`Handler`、Server、Client、Dialer 和 Router；KCP/UDP 参数由自己的
Config 和 Options 表达，不会污染其他传输。

## 1. 最小接入

推荐由项目自己的业务 Module 保存 KCP 端点和回调，Service 只装配业务 Module：

```go
type GatewayKCPModule struct {
    service.Module
    server *kcp.Server
}

func (module *GatewayKCPModule) OnInit() error {
    handler := network.HandlerFuncs{
        Message: module.onMessage,
    }
    cfg := kcp.DefaultServerConfig()
    if err := module.GetServiceConfigStrict("kcp.server", &cfg); err != nil {
        return err
    }
    options, err := cfg.Options(handler)
    if err != nil {
        return err
    }
    server, err := kcp.NewServer(cfg.Address, options)
    if err != nil {
        return err
    }
    module.server = server
    return module.AddModule(server)
}

func (module *GatewayKCPModule) onMessage(
    _ context.Context,
    session network.Session,
    payload []byte,
) error {
    // payload 只在回调返回前有效；Send 会安全复制。
    return session.Send(payload)
}
```

完整可运行程序见
[`03-kcp-raw-self-call`](../../../../examples/13-network/03-kcp-raw-self-call/README.md)。配置应从
`Default*Config` 开始，再用 `GetServiceConfigStrict` 覆盖；这样可以省略不需要修改的字段，同时让
拼错的字段在启动期直接失败。

常用配置如下：

```yaml
kcp:
  server:
    address: "0.0.0.0:19092"
    frame: {length_field_size: 4, byte_order: big}
    mtu: 1400
    send_window: 1024
    receive_window: 1024
    no_delay: {enabled: true, interval: 10ms, fast_resend: 2, disable_congestion_control: true}
    fec: {data_shards: 0, parity_shards: 0}
    read_idle_timeout: 60s
```

`ServerConfig`、`ClientConfig` 和 `DialerConfig` 相互独立。共同容量字段与 TCP/WebSocket 同名，
KCP 专属字段不会出现在其他模块中。`BlockCrypt` 不进入 YAML，必须在 Config 转换成功后由代码注入。

## 2. KCP 与 TCP 不同的连接语义

KCP 建立客户端时只创建本地 UDP Session，没有 TCP 三次握手或 WebSocket Upgrade：

- `Client`/`Dialer` 的 `OnOpen` 只表示本地 Session 就绪，不证明服务端在线；
- 以首条登录应答或项目自己的握手消息确认业务可用，不要把 `OnOpen` 当成鉴权成功；
- KCP 没有 TCP FIN 或标准 Close 帧，一端本地关闭不会立即通知另一端；
- 默认 `ReadIdleTimeout` 为 60 秒，必须大于业务心跳最大间隔，用于回收静默失活 Session；
- 自动重连只能约束本地 DNS/socket 创建失败；对端不可达通常在读空闲到期后才被发现。

如果项目要求更快发现断线，应实现业务心跳并缩短读空闲值，但不要小于正常心跳抖动上限。

## 3. Client 与 Dialer

长期连接使用由 Service 托管的 `kcp.Client`：

```go
cfg := kcp.DefaultClientConfig()
if err := module.GetServiceConfigStrict("kcp.client", &cfg); err != nil {
    return err
}
options, err := cfg.Options(handler)
if err != nil {
    return err
}
client, err := kcp.NewClient(cfg.Address, options)
if err != nil {
    return err
}
return module.AddModule(client)
```

`kcp.Dialer` 只创建一次本地 Session，不自动停止或重连。它要求 owner Service 已处于
Running/Retired，调用方必须在 owner 停止前关闭返回的 Session。需要生命周期托管时使用 Client。

## 4. 帧、MTU 与窗口

KCP 固定启用 Stream Mode，并在字节流上叠加与 TCP 相同的无符号长度帧。默认是 4 字节 Big
Endian；也支持 1/2/4 字节和 Little Endian：

```go
options.Frame.LengthFieldSize = 2
options.Frame.ByteOrder = network.LittleEndian
```

通信双方的帧、加密和 KCP 核心参数必须匹配。主要运行时参数：

| 字段 | 默认值 | 说明 |
| --- | ---: | --- |
| `MTU` | `1400` | 不含 UDP/IP 头；修改前应实测链路分片 |
| `SendWindow` / `ReceiveWindow` | `1024 / 1024` | KCP Segment 窗口，范围 `1..65535` |
| `NoDelay.Interval` | `10ms` | KCP 更新间隔，只接受整毫秒的 `10ms..5s` |
| `NoDelay.FastResend` | `2` | 零关闭快速重传 |
| `NoDelay.DisableCongestionControl` | `true` | 时延优先；公网使用前要测带宽代价 |
| `ACKNoDelay` | `false` | 立即 ACK 会增加小包 |
| `WriteDelay` | `false` | 开启后批量更好，但会增加发送等待 |
| `DSCP` | `0` | 零不设置；非零依赖系统权限和网络策略 |
| `SocketReadBuffer` / `SocketWriteBuffer` | `0 / 0` | 零保留 OS 默认值，容量实测后再调大 |

实现严格拒绝 KCP 库会静默修正的非法值。库内 UDP 报文缓冲上限为 1500 字节；启用 BlockCrypt
会增加 20 字节头，启用 FEC 会再增加 8 字节头，因此两者同时启用时 `MTU` 不能超过 1472。

## 5. FEC 与加密

FEC 默认关闭。确认丢包率和冗余带宽可接受后再配置，例如：

```go
cfg.FEC = kcp.FECConfig{DataShards: 10, ParityShards: 3}
```

`0/0` 表示关闭；启用时两个值都必须为正，合计不能超过 256。FEC 只能恢复一定比例的丢包，不能
代替容量限制、鉴权或加密。

加密对象只允许从代码安全注入，不把静态密钥写进普通 YAML：

```go
block, err := kcpgo.NewAESBlockCrypt(loadKeyFromSecretStore())
if err != nil {
    return err
}
cfg := kcp.DefaultServerConfig()
options, err := cfg.Options(handler)
if err != nil {
    return err
}
options.BlockCrypt = block
```

示例中的 `kcpgo` 是 `github.com/xtaci/kcp-go/v5` 的导入别名。服务端与客户端必须使用匹配算法和
密钥；同一 BlockCrypt 实例被多个 Session 共享时实现必须并发安全。KCP 包加密不替代业务鉴权、
重放防护和密钥轮换。

## 6. 容量、所有权与停止

KCP 复用公共端点容量：单消息、入站待处理消息/字节、出站队列消息/字节和 Module 总预算全部有界。
`Session.Send` 非阻塞提交；过载返回 `ErrTransportOverloaded`，不会静默丢弃可靠消息。

- Raw `Send([]byte)` 会复制，调用返回后可复用原 Slice；
- `OnMessage` 的 payload 是借用视图，保存或异步使用前必须复制；
- `Writable` 只是瞬时水位提示，最终准入以 `Send` 返回值为准；
- Server/Client 必须通过 `AddModule` 管理，不要直接调用生命周期方法；
- 停止会先停止 UDP 准入和重连，再关闭 Session、等待 I/O，最后在 Service 中交付 `OnClose`。

PB、JSON 与自定义无状态 Codec 的用法和 TCP 完全相同；Codec 接收的是已经去掉 KCP 长度头的完整
逻辑消息，不应再处理粘包或拆包。
