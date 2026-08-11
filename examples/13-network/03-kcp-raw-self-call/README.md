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

完整说明见 [KCP 网络模块使用指南](../../../docs/maintenance/v3.2/guides/KCP网络模块使用指南.md)。
