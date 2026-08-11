# WebSocket Raw 回环

本示例不依赖 NATS、etcd 或浏览器。一个 `NetworkService` 同时托管 WebSocket Server 与 Client，
Client 在 `OnOpen` 发送消息，Server 原样回显，用于验证服务调用自己的 WebSocket 入口不会死锁。

从仓库根目录运行：

```bash
go run ./examples/13-network/02-websocket-raw-self-call start \
  --app-name websocket-raw-self-call \
  --config ./examples/13-network/02-websocket-raw-self-call/config --node network-1
```

Windows 可执行 `run.bat`，Linux/macOS 可执行 `./run.sh`。预期依次看到 Server 和 Client 收到
`hello through websocket`。示例使用 `127.0.0.1:19091` 和默认 `/ws` Path；端口被占用时同时修改
Server 地址与 Client URL。

完整说明见 [WebSocket 网络模块使用指南](../../../docs/maintenance/v3.2/guides/WebSocket网络模块使用指南.md)。
