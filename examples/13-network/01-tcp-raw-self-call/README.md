# TCP Raw 回环

本示例不依赖 NATS、etcd 或其他进程。`NetworkService` 只装配业务 `EchoTCPModule`，后者把
TCP Server 与 Client 作为子 Module 管理，并集中实现全部网络业务回调。Client 在 `OnOpen`
发送消息，Server 收到后原样回显，用于验证服务调用自己的网络入口不会死锁。

示例采用 `NetworkService → EchoTCPModule → Server/Client` 的结构。实际项目可以在自己的业务
Module 中保存连接状态、注册消息路由、使用定时器和处理网络事件，无需把这些代码放进 Service。

从仓库根目录运行：

```bash
go run ./examples/13-network/01-tcp-raw-self-call start \
  --app-name tcp-raw-self-call \
  --config ./examples/13-network/01-tcp-raw-self-call/config --node network-1
```

Windows 可执行 `run.bat`，Linux/macOS 可执行 `./run.sh`。预期依次看到 Server 和 Client 收到
`hello from the same service`。示例使用 `127.0.0.1:19090`；端口被占用时先修改 `main.go` 中的
Server 与 Client 地址，并保持两处一致。

完整说明见 [TCP 网络模块使用指南](../../../docs/maintenance/v3.2/guides/TCP网络模块使用指南.md)。
