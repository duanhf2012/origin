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

以下是可以直接运行的完整起始值。KCP 参数对链路较敏感，默认值已经过本项目双平台与弱网验收；
在自己的延迟、丢包率和消息分布下取得数据前，不建议同时修改多个参数：

```yaml
services:
  GatewayService:
    kcp:
      server:
        # UDP/KCP 监听地址；0.0.0.0 暴露全部 IPv4 网卡，需同时配置防火墙和 UDP 放行规则。
        address: "0.0.0.0:19092"
        frame:
          # KCP Stream 中的长度字段；双方必须一致，无既有协议时建议保留 4 字节 Big Endian。
          length_field_size: 4
          byte_order: big
        # 不含 UDP/IP 头；建议先用 1400，修改前实测链路分片并计入加密/FEC 额外头。
        mtu: 1400
        # 单位为 Segment；1024/1024 是低延迟起始值，调大前评估带宽与内存。
        send_window: 1024
        receive_window: 1024
        no_delay:
          # 实时消息建议开启；10ms 是允许的最小更新间隔。
          enabled: true
          interval: 10ms
          # 2 是快速重传的低延迟起始值；0 表示关闭。
          fast_resend: 2
          # true 以带宽换时延，公网发布前必须压测带宽代价。
          disable_congestion_control: true
        # 立即 ACK 会增加小包；没有测试依据时保持 false。
        ack_no_delay: false
        # 延迟写有利于批量但增加等待；实时消息保持 false。
        write_delay: false
        fec:
          # 0/0 关闭 FEC；只有确认丢包收益后才启用，且通信双方必须使用相同组合。
          data_shards: 0
          parity_shards: 0
        # 0 不设置 DSCP；非零值依赖操作系统权限和网络设备策略。
        dscp: 0
        # 0B 保留 OS Socket Buffer 默认值；监控到丢包并完成容量测试后再调大。
        socket_read_buffer: 0B
        socket_write_buffer: 0B
        # 首轮 Session 容量；按内存、带宽和压测结果调整。
        max_sessions: 4096
        # 完整业务消息上限；建议按真实协议收紧。
        max_message_size: 64KB
        # 单 Session 与整个 Server 的入站积压边界。
        receive_pending_messages: 64
        receive_pending_size: 256KB
        receive_pending_total_size: 64M
        # 单 Session 与整个 Server 的出站积压边界。
        send_queue_messages: 256
        send_queue_size: 256KB
        send_queue_total_size: 128M
        # KCP 无 FIN；必须大于业务心跳最大间隔，且不能设为 0s。
        read_idle_timeout: 60s
        # 单条完整消息写出上限，必须大于 0s。
        write_timeout: 15s
        # 发送队列连续高水位上限，超时关闭慢连接。
        slow_client_timeout: 10s
```

`ServerConfig` 与 `ClientConfig` 相互独立。共同容量字段与 TCP/WebSocket 同名，KCP 专属字段不会
出现在其他模块中。Dialer 不读取 YAML：从 `DefaultDialOptions` 开始在代码中覆盖，并调用
`NewDialer`。`BlockCrypt` 不进入 YAML，必须在 Config/Options 准备完成后由代码注入。

生产接入时先确认 UDP 端口可达、业务心跳和 `read_idle_timeout`，再按一项参数一轮压测的方式调整
MTU、窗口、NoDelay 或 FEC。不要把 FEC、Socket Buffer 和窗口一起调大后只观察平均延迟；至少同时
记录 P99 延迟、丢包/重传、带宽、CPU 和内存。

## 2. 函数、回调参数与执行协程

这里的“执行协程”表示并发访问域，不承诺固定 goroutine ID。地址、MTU、窗口等普通参数由调用函数
同步读取；真正会延后执行的是 Handler、StateChange、BlockCrypt、Codec 和注册的消息 Handler。

| 函数或函数族 | 函数参数实际执行位置 | 参数与所有权规则 | 可直接访问 Service 串行状态 |
| --- | --- | --- | --- |
| `Default*Config()`、`Default*Options(handler)`、`Config.Options(handler)` | 当前调用 goroutine；`handler` 和 `BlockCrypt` 只被保存 | 配置同步读取 | 仅装配，不应处理业务状态 |
| `NewServer/NewClient/NewDialer(address, options)` | 当前调用 goroutine；Options 中的回调和接口只被保存 | 构造器同步校验配置 | 仅装配，不应处理业务状态 |
| `OnInit/OnStart/OnStop(ctx)` | Origin 管理的 Service 生命周期上下文；UDP/KCP 更新、连接读写和 Client 重连另有内部 goroutine | 应通过 `AddModule` 让框架调用 | 生命周期装配可以；不要当成普通消息回调 |
| `Handler.OnOpen/OnMessage/OnWritableChanged/OnClose` 及 `HandlerFuncs` 对应函数字段 | 所属 Service 工作协程串行执行；停止收口时 `OnClose` 在独占 finalizer 上下文执行 | `ctx` 和 `payload` 只在回调期间有效；保存 payload 前必须复制 | 是 |
| `ClientOptions.StateChange(ctx, snapshot)` | 所属 Service 串行上下文 | 不可变快照；等待外部 I/O 时使用 `Await` | 是 |
| `BlockCrypt` 的加解密方法 | KCP 内部 UDP/更新 I/O goroutine，不同 Session 可能并发调用 | 同一实例可被多个 Session 共享，必须并发安全，不能访问 Service 私有状态 | 否 |
| `Session.Send/Close` 与全部查询方法 | 当前调用 goroutine；实际 KCP 编解码和 UDP I/O 在内部 goroutine | `Send` 返回前复制 payload；调用不改变当前执行权 | 只有调用方原本就在 Service 上下文时才可以访问业务状态 |
| `Server.Addr/Session/SessionCount/CloseSession/Stats`、`Client.Session/State/Stats` | 当前调用 goroutine | 返回并发安全快照或 Session；最终 `OnClose` 稍后回到 Service | 调用本身不取得 Service 执行权 |
| `Dialer.Dial(ctx, owner)` | 调用 goroutine 同步创建本地 Session 并等待结果；`OnOpen` 在 `owner` Service 工作协程 | 成功不表示远端可达；返回 Session 由调用方关闭 | 在 `owner` Task 内必须放入 `Await`，避免等待自己持有的执行权 |
| `protocol.NewRouter/Register/Freeze` | 当前调用 goroutine；注册的 Handler 只保存 | 必须在 Module `OnInit` 冻结前注册完成 | 仅装配 |
| `Codec.Decode`、`RouterOptions.Unknown`、注册的消息 Handler | 所属 Service 工作协程，位于 `OnMessage` 的同一个 Task | Decode payload 是借用视图 | 是 |
| `Router.Send` 与 `Codec.Encode` | 调用 `Send` 的当前 goroutine 同步执行 | Encoder 只在本次 Encode 内有效；跨 goroutine 调用时 Codec 和消息值必须并发安全 | 只有调用方原本就在 Service 上下文时才可以 |

特别注意：KCP `Dial` 没有远端握手，但仍会等待本地 `OnOpen` 在 `owner` Service 执行。从该 Service
的 Timer、事件、RPC 或网络 Handler 内调用时，同样必须用 `Await` 先释放执行权。

## 3. KCP 与 TCP 不同的连接语义

KCP 建立客户端时只创建本地 UDP Session，没有 TCP 三次握手或 WebSocket Upgrade：

- `Client`/`Dialer` 的 `OnOpen` 只表示本地 Session 就绪，不证明服务端在线；
- 以首条登录应答或项目自己的握手消息确认业务可用，不要把 `OnOpen` 当成鉴权成功；
- KCP 没有 TCP FIN 或标准 Close 帧，一端本地关闭不会立即通知另一端；
- 默认 `ReadIdleTimeout` 为 60 秒，必须大于业务心跳最大间隔，用于回收静默失活 Session；
- 自动重连只能约束本地 DNS/socket 创建失败；对端不可达通常在读空闲到期后才被发现。

如果项目要求更快发现断线，应实现业务心跳并缩短读空闲值，但不要小于正常心跳抖动上限。

## 4. Client 与一次性 Dialer

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

## 5. 帧、MTU 与窗口

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

## 6. FEC 与加密

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

## 7. 容量、所有权与停止

KCP 复用公共端点容量：单消息、入站待处理消息/字节、出站队列消息/字节和 Module 总预算全部有界。
`Session.Send` 非阻塞提交；过载返回 `ErrTransportOverloaded`，不会静默丢弃可靠消息。

| Server 字段 | 默认起始值 | 调整建议 |
| --- | ---: | --- |
| `max_sessions` | `4096` | 按实际并发、内存和带宽压测；不要只为预留而调大 |
| `max_message_size` | `64KB` | 按真实最大消息收紧；调大时同步检查帧宽度和单 Session 字节预算 |
| `receive_pending_messages` / `receive_pending_size` | `64 / 256KB` | 限制单 Session 占用的业务任务和入站 Buffer |
| `receive_pending_total_size` | `64M` | 限制全部 Session 的入站积压，必须不小于单 Session 上限 |
| `send_queue_messages` / `send_queue_size` | `256 / 256KB` | 限制单 Session 出站积压；满时由业务处理过载错误 |
| `send_queue_total_size` | `128M` | 限制全部 Session 排队及正在写出的 Payload |
| `read_idle_timeout` | `60s` | 必须为正并大于业务心跳最大间隔 |
| `write_timeout` / `slow_client_timeout` | `15s / 10s` | 两者必须为正；先保留默认值，再依据尾延迟和慢连接率调整 |

- Raw `Send([]byte)` 会复制，调用返回后可复用原 Slice；
- `OnMessage` 的 payload 是借用视图，保存或异步使用前必须复制；
- `Writable` 只是瞬时水位提示，最终准入以 `Send` 返回值为准；
- Server/Client 必须通过 `AddModule` 管理，不要直接调用生命周期方法；
- 停止会先停止 UDP 准入和重连，再关闭 Session、等待 I/O，最后在 Service 中交付 `OnClose`。

PB、JSON 与自定义无状态 Codec 的用法和 TCP 完全相同；Codec 接收的是已经去掉 KCP 长度头的完整
逻辑消息，不应再处理粘包或拆包。
