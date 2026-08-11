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
`hello from the same service`。示例从 `services.NetworkService.tcp` 严格读取配置；端口被占用时
修改 `config/application.yaml` 中 Server 与 Client 的地址，并保持两处一致。

## 配置怎么改

示例 YAML 有意列出完整 Server 起始值，可直接复制到自己的 Service；没有准备调整的字段也可以删除，
由 `DefaultServerConfig` 补齐。第一次接入通常只需要确认：

- `address`：`127.0.0.1` 只允许本机访问；对外监听时选择实际网卡并配置防火墙；
- `frame`：必须与客户端一致；没有既有协议时保留 4 字节 Big Endian；
- `max_sessions`：默认 `4096` 只是首轮容量，按文件描述符、内存和压测结果调整；
- `max_message_size`：默认 `64KB`，建议按真实最大业务消息收紧；
- `read_idle_timeout`：默认关闭；项目有业务心跳时可设为大于最大心跳间隔的值。

不要为了提高吞吐同时放大每连接队列和 Server 总预算。队列满时 `Send` 会返回过载错误，应先根据监控
确认瓶颈在业务处理、客户端写入还是容量配置，再只调整对应边界。

完整说明见 [TCP 网络模块使用指南](../../../docs/maintenance/v3.2/guides/TCP网络模块使用指南.md)。
