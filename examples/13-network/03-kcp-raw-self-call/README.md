# KCP Raw 回环

本示例不依赖 NATS、etcd 或其他进程。`NetworkService` 只装配业务 `EchoKCPModule`，后者把
KCP Server 与 Client 作为子 Module 管理，并集中实现全部网络业务回调。Client 创建本地 Session
后发送消息，Server 收到后原样回显，用于验证服务调用自己的 KCP 入口不会死锁。

从仓库根目录运行：

```bash
go run ./examples/13-network/03-kcp-raw-self-call start \
  --app-name kcp-raw-self-call \
  --config ./examples/13-network/03-kcp-raw-self-call/config --node network-1
```

Windows 可执行 `run.bat`，Linux/macOS 可执行 `./run.sh`。预期依次看到 Server 和 Client 收到
`hello through kcp`。端口被占用时，同时修改 `config/application.yaml` 中 Server 和 Client 的
`address`。

KCP 没有 TCP/HTTP 式远端握手：`OnOpen` 只表示本地 UDP Session 已经建立，不能证明服务端可用；
业务应以首条应答或登录握手确认可用性，并保留正数 `ReadIdleTimeout` 检测静默失活。KCP Service
配置从完整默认值开始严格覆盖，配置文件只需列出希望明确调整的字段；拼错字段会在启动期报错。
`BlockCrypt` 不应写入普通配置文件，需要在 `Options` 转换后从代码安全注入。

## 函数与参数在哪个协程执行

| 示例函数或参数 | 执行位置 | 使用规则 |
| --- | --- | --- |
| `OnInit`、`Default*Config`、`Config.Options(handler)`、`NewServer/NewClient` | Origin 启动装配上下文；`handler` 和 `BlockCrypt` 此时只被保存 | 只做配置和注册，不在这里处理网络消息 |
| `onServerMessage`、`onClientOpen`、`onClientMessage` 及其 `ctx/session/payload` 参数 | `NetworkService` 工作协程串行执行 | 可以直接访问当前 Service 私有状态；payload 只在回调返回前有效 |
| `session.Send(payload)` | 当前 Handler 所在的 Service 工作协程同步提交；实际 KCP/UDP 写出在内部 I/O goroutine | Send 返回前已经复制 payload |
| `BlockCrypt` 加解密方法（本示例未设置） | KCP 内部 I/O goroutine，不同 Session 可能并发调用 | 实现必须并发安全，不能直接读取 Service 私有状态 |
| Client 的 KCP 更新、读取、写出和重连 | 框架内部 I/O/重连 goroutine | 业务结果通过 Handler 回到 Service |

KCP 没有远端握手，但一次性 `Dialer.Dial(ctx, 当前Service)` 仍会等待本地 `OnOpen`。从 Timer、事件、
RPC 或 Handler 中调用时必须放入 `Await`，先释放当前 Service 执行权。

## 配置怎么改

示例 YAML 列出完整 Server 起始值。第一次接入先确认 UDP 端口确实可达、业务心跳和
`read_idle_timeout`，再考虑协议调优：

- `mtu: 1400`、窗口 `1024/1024` 和 10ms NoDelay 是已经验收的起始值，不是所有公网链路的最优值；
- 帧格式和 FEC 分片必须与客户端一致；FEC 默认 `0/0`，没有丢包与带宽数据时不要开启；
- `ack_no_delay`、`write_delay`、DSCP 和 Socket Buffer 均保持默认，除非压测能证明收益；
- `max_sessions: 4096`、`max_message_size: 64KB` 按真实并发、带宽和消息大小收紧；
- 每轮只调整一类参数，并同时观察 P99 延迟、重传、带宽、CPU 和内存。

启用加密或 FEC 后会增加协议头，修改 MTU 时必须保留相应空间；不要直接把 MTU 提高到 1500。

完整说明见 [KCP 网络模块使用指南](../../../docs/maintenance/v3.2/guides/KCP网络模块使用指南.md)。
