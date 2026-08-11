# etcd 服务发现

此示例使用 etcd 保存服务目录，适合已有 etcd 基础设施的环境。业务 Service 与 Origin Provider 示例相同，只替换发现 Provider 的 YAML 配置。

## 前置条件与配置

先运行 `deps-up.bat` 或 `./deps-up.sh`，再用 `check-deps` 验证本机 `2379` 端口。未配置
`namespace` 时使用 `/origin`。

本示例配置为：

```yaml
discovery:
  type: etcd
  etcd:
    endpoints: [http://127.0.0.1:2379]
    # 当前配置下启动的所有 Node 都注册到该网络，
    # 并自动能发现该网络中的所有服务。
    local_network: game-partition-1

nodes:
  - id: battle-room-1
    # 给当前 Node 发布的服务记录添加游戏类型标签。
    labels: {game_type: battle}
    services: [Service]

  - id: card-room-1
    labels: {game_type: card}
    services: [Service]
```

如需读取其他网络，可以在 `discovery.etcd` 中增加：

```yaml
watch_networks:
  # 额外读取游戏分区二中的服务。
  - game-partition-2
```

它只增加读取范围，不改变当前 Node 的注册网络。筛选远端服务时，在当前 Node 下配置 `allow_discovery`：

```yaml
nodes:
  - id: gateway-1
    # 指定当前 Node 允许发现哪些远端服务。
    allow_discovery:
      - services: [Service]
        node_labels:
          # 根据远端 Node 的 nodes.labels 筛选服务。
          game_type: battle
    services: [Service]
```

## 运行与观察

执行 `run.bat` 或 `./run.sh`，预期看到其他 Node 的 discovered 日志。结束后运行 `deps-down`，仅关闭本示例 compose 启动的依赖。

## 生产提示

本示例为本地开发配置。生产应使用 HTTPS/TLS、最小权限账号及受控 endpoint，不应把认证信息提交到业务默认 YAML。

对应教程：[使用 etcd 进行服务发现](../../../docs/extensions/etcd-discovery.md)。
