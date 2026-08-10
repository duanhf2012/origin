# 配置参考

应用配置是一个目录中递归扫描的 YAML/JSON 文件集合，框架加载、合并后冻结。支持扩展名
`.json`、`.yml`、`.yaml`（扩展名大小写不敏感），入口必须是目录而不是单文件。

最小配置：

```yaml
nodes:
  # Node 的小写 kebab-case 身份。
  - id: game-1
    # 在该 Node 创建的实际 Service 实例。
    services: [GameService]
```

常用顶层字段：

| 字段 | 用途 |
| --- | --- |
| `log` | 同步/异步模式、控制台/文件级别与格式、文件滚动和保留 |
| `buffer_pool` | BufferPool 使用统计 |
| `rpc` | 整个 Application 统一使用的远程 RPC 传输、容量与 NATS 连接参数 |
| `discovery` | 选择 `origin`、`etcd` 或已注册的自定义 Provider |
| `nodes` | Node 列表；跨文件 Sequence 按稳定路径顺序追加 |
| `services` | Service 公共业务配置 |
| `node_services` | Node+实际 ServiceName 专属完整业务配置 |

Node 常用字段：`id`、`services`、`private`、`labels`、`allow_discovery`、`scheduler`；仅 TCP
模式的 Node 还需要 `rpc.tcp.listen/advertise`。

`id` 使用小写 kebab-case。顶层 `rpc.transport` 在 Application 级选择 `tcp` 或 `nats`。
TCP 的共享队列和超时配置写在顶层 `rpc.tcp`，每个 Node 只在 `rpc.tcp.listen/advertise` 写
自身地址；NATS 的 `urls/namespace/auth/tls` 只在顶层 `rpc.nats` 写一次，所有 Node 分别以
该快照建立连接。未配置顶层 `rpc` 时全部 Node 仅支持本地调用，`nodes[].rpc` 会被拒绝。

多文件规则：Mapping 递归合并，Sequence 追加，标量/`null`/类型冲突重复时报错；不会按
Sequence 元素的 `id` 合并或去重。JSON 使用严格语法，YAML/JSON 根节点都必须是 Mapping。
浮点值必须是有限数，不接受 YAML 的 `.inf`、`-.inf` 或 `.nan`。字符串值支持
`${ENV_NAME}` 环境变量替换，缺失变量会失败，不支持默认值表达式或字段名替换。

完整字段语义与示例见[配置](../02.configuration.md)、[跨节点 RPC](../07.remote-rpc.md)和
[服务发现](../08.discovery.md)。
