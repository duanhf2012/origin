# NATS 跨 Node RPC

此示例与 TCP 示例使用相同的 `PlayerRPC` 客户端外观，只将 Node 的传输配置换为 NATS。适合已经拥有 NATS 集群、希望由消息系统处理连接和重连的部署。

## 前置条件

先运行 `deps-up.bat` 或 `./deps-up.sh` 启动仓库 compose 中的 NATS，再运行 `check-deps` 确认 `127.0.0.1:4222` 可达。结束后可执行 `deps-down` 停止仅由示例启动的依赖。

## 配置重点

`rpc.transport: nats` 与 `rpc.nats.urls` 选择 NATS 传输；`namespace` 用于隔离同一 NATS 中不同 Origin 环境。生产部署应在 NATS 配置 TLS 和最小权限凭据，而不是复制本地无认证地址。

## 运行与观察

执行 `run.bat` 或 `./run.sh`，预期日志为 `remote NATS result: player-1001`。可只改 `urls` 指向其他可用 NATS，不需要改变任何业务 RPC 代码。

对应教程：[跨节点 RPC](../../../docs/baseline/v3.0/guides/06-remote-rpc.md)。
