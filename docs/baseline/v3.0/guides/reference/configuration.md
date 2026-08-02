# 配置参考

应用配置是一个目录中的 YAML/JSON 文件集合，框架加载后冻结。最小配置：

```yaml
nodes:
  - id: game-1
    services: [GameService]
```

常用顶层字段：

| 字段 | 用途 |
| --- | --- |
| `log` | 控制台/文件日志 |
| `buffer_pool` | BufferPool 使用统计 |
| `discovery` | 选择 `origin`、`etcd` 或已注册的自定义 Provider |
| `nodes` | Node 列表 |
| `services` | Service 公共业务配置 |
| `node_services` | Node+实际 ServiceName 专属业务配置 |

Node 常用字段：`id`、`services`、`private`、`labels`、`allow_discovery`、`scheduler`、`rpc`。

`id` 使用小写 kebab-case。RPC 仅在 Node 内配置：`transport: tcp` 搭配 `tcp.listen/advertise`，或 `transport: nats` 搭配 `nats.urls/namespace/auth/tls`。

完整字段语义与边界见教程的[配置](../02-configuration.md)、[跨节点 RPC](../06-remote-rpc.md)和[服务发现](../07-discovery.md)。
