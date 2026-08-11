# WebSocket Raw 回环

本示例不依赖 NATS、etcd 或浏览器。`NetworkService` 只装配业务 `EchoWebSocketModule`，后者把
WebSocket Server 与 Client 作为子 Module 管理，并集中实现全部网络业务回调。Client 在
`OnOpen` 发送消息，Server 原样回显，用于验证服务调用自己的 WebSocket 入口不会死锁。

示例采用 `NetworkService → EchoWebSocketModule → Server/Client` 的结构。实际项目可以在自己的
业务 Module 中保存连接状态、注册消息路由、使用定时器和处理网络事件，无需把这些代码放进 Service。

从仓库根目录运行：

```bash
go run ./examples/13-network/02-websocket-raw-self-call start \
  --app-name websocket-raw-self-call \
  --config ./examples/13-network/02-websocket-raw-self-call/config --node network-1
```

Windows 可执行 `run.bat`，Linux/macOS 可执行 `./run.sh`。预期依次看到 Server 和 Client 收到
`hello through websocket`。示例从 `services.NetworkService.websocket` 严格读取配置；端口或 Path
需要改变时，同时修改 `config/application.yaml` 中的 Server 字段与 Client URL。

## 函数与参数在哪个协程执行

| 示例函数或参数 | 执行位置 | 使用规则 |
| --- | --- | --- |
| `OnInit`、`Default*Config`、`Config.Options(handler)`、`NewServer/NewClient` | Origin 启动装配上下文；`handler` 此时只被保存 | 只做配置和注册，不在这里处理网络消息 |
| `onServerMessage`、`onClientOpen`、`onClientMessage` 及其 `ctx/session/payload` 参数 | `NetworkService` 工作协程串行执行 | 可以直接访问当前 Service 私有状态；payload 只在回调返回前有效 |
| `session.Send(payload)` | 当前 Handler 所在的 Service 工作协程同步提交；实际 WebSocket 写出在内部 I/O goroutine | Send 返回前已经复制 payload |
| `CheckOrigin(request)` 与 TLS 回调（本示例未设置） | HTTP Upgrade/TLS 握手的网络 goroutine，不同连接可并发调用 | 只能访问不可变或并发安全数据，不能直接读取 Service 私有状态 |
| Client 的连接、心跳、读取、写出和重连 | 框架内部 I/O/重连 goroutine | 业务结果通过 Handler 回到 Service |

在 Timer、事件、RPC 或 Handler 中调用一次性 `Dialer.Dial(ctx, 当前Service)` 时必须放入 `Await`，
否则 Dial 等待的 `OnOpen` 无法取得当前 Service 执行权。

## 配置怎么改

示例 YAML 列出完整 Server 起始值；没有准备调整的字段可以删除，由 `DefaultServerConfig` 补齐。
第一次接入优先确认：

- `address` 与 `path`：前者决定监听 Socket，后者决定 Upgrade 路由；反向代理路径必须一致；
- `message_type`：Raw/PB 使用 `binary`；浏览器直接发送 JSON 文本才使用 `text`；
- `ping_interval`/`pong_timeout`：默认 `30s/60s`，是协议控制帧；必须同时关闭或同时启用；
- `max_sessions` 与 `max_message_size`：默认 `4096/64KB`，按连接规模和真实消息上限压测；
- TLS、Origin 白名单和响应 Header：属于运行期安全策略，在代码中注入，不写普通 YAML。

`read_idle_timeout` 只观察业务 Data Message，Ping/Pong 不刷新它。没有业务空闲断开需求时保持 `0s`；
需要该策略时应大于正常业务消息或业务心跳的最大间隔。

完整说明见 [WebSocket 网络模块使用指南](../../../docs/maintenance/v3.2/guides/WebSocket网络模块使用指南.md)。
