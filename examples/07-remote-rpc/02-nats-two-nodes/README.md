# NATS 跨 Node RPC

此示例与 TCP 示例使用相同的 `PlayerServiceClient` 外观，只将 Application 的传输配置换为 NATS。适合已经拥有 NATS 集群、希望由消息系统处理连接和重连的部署。

## 前置条件

先运行 `deps-up.bat` 或 `./deps-up.sh` 启动仓库 compose 中的 NATS，再运行 `check-deps` 确认 `127.0.0.1:4222` 可达。结束后可执行 `deps-down` 停止仅由示例启动的依赖。

## 配置重点

顶层 `rpc.transport: nats` 与 `rpc.nats.urls` 选择 NATS 传输；`namespace` 用于隔离同一 NATS 中不同 Origin 环境。连接参数只写一次，每个 Node 会使用同一快照创建自己的连接。生产部署应在 NATS 配置 TLS 和最小权限凭据，而不是复制本地无认证地址。

## 契约与业务实现

- [`../../_support/tutorialrpc/player_service.go`](../../_support/tutorialrpc/player_service.go)：与 TCP 示例共用的契约。
- [`../../_support/tutorialrpc/player_service.rpc.gen.go`](../../_support/tutorialrpc/player_service.rpc.gen.go)：与传输无关的生成客户端和 Dispatcher。
- [`player_service.go`](player_service.go)：本示例业务实现；只通过编译期断言校验契约，不生成适配文件。
- [`main.go`](main.go)：保持与 TCP 示例相同的 RPC 调用代码。

Node 仍在冷启动时按模板名 `PlayerService` 自动装配，NATS Subject 和连接不会进入业务
Service 的生成或识别逻辑。

## 运行与观察

执行 `run.bat` 或 `./run.sh`，预期日志为 `remote NATS result: player-1001`。可只改 `urls` 指向其他可用 NATS，不需要改变任何业务 RPC 代码。

对应教程：[跨节点 RPC](../../../docs/baseline/v3.0/guides/07.remote-rpc.md)。
