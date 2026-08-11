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

完整说明见 [WebSocket 网络模块使用指南](../../../docs/maintenance/v3.2/guides/WebSocket网络模块使用指南.md)。
