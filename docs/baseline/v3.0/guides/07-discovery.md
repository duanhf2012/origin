# 07：服务发现

## 我想使用 Origin 内置发现

运行：[examples/07-discovery/01-origin-provider](../../../../examples/07-discovery/01-origin-provider)。

Origin Provider 需要在任意一个 Node 配置唯一 `DiscoveryService`，并在顶层选择 `discovery.type: origin`：

```yaml
discovery:
  type: origin
  origin:
    ttl: 5s
    server:
      node: discovery-1
      listen: 127.0.0.1:18080
      address: 127.0.0.1:18080
```

`DiscoveryService` 可以与该 Node 的其他 Service 共存，不要求专用 Node。

## 我想使用 etcd

运行：[examples/07-discovery/02-etcd-provider](../../../../examples/07-discovery/02-etcd-provider)。先执行其中的 `deps-up`，它只启动仓库已有 compose 中的 etcd 依赖。

```yaml
discovery:
  type: etcd
  etcd:
    endpoints: [http://127.0.0.1:2379]
    local_network: cn-east
    ttl: 5s
    request_timeout: 3s
```

`namespace` 未配置时使用稳定默认值 `origin`；`local_network` 用于多网络环境中选择本地可达端点，最多允许 64 个网络。

## 我想监听上线、下线和 Lost

运行：[examples/07-discovery/03-watch-and-lost](../../../../examples/07-discovery/03-watch-and-lost)。断线会立即产生 Lost 事实；框架不做防抖，避免中间断开又恢复却没有状态事件而造成业务视图不一致。

## 我想等待远端服务出现

运行：[examples/07-discovery/05-await-service](../../../../examples/07-discovery/05-await-service)。在启动依赖另一个 Node 时，先等待发现目录出现目标，再读取其快照：

```go
if err := s.AwaitNodeService(ctx, "player-1", "PlayerService"); err != nil {
    return err
}
instance, ok := s.FindDiscoveredService("player-1", "PlayerService")
```

`AwaitService` 等待任意 Node 上的指定 Service；`AwaitNodeService` 等待明确 Node。两者都复用 `Service.Await`，应传入生命周期或当前业务任务的 `Context`，并处理超时和取消。

## 我想替换为 Consul 或其他发现系统

运行：[examples/07-discovery/04-custom-provider](../../../../examples/07-discovery/04-custom-provider)。应用只需要注册一个小型 `discovery/provider.Provider` Factory：读取 Provider 配置、发布/撤销当前 Node、向 Host 报告完整快照。

示例演示 SPI，不伪装为可直接连接 Consul 的实现；真正的 Consul Provider 应作为独立包实现并通过相同注册点加入 Application。

## 深入一点：安全和边界

发现通常用于内网，但 etcd 仍应在生产环境启用 TLS 和最小认证；Origin 内置发现也应部署在受限网络中。Provider 只能报告规范化快照，框架负责目录更新、路由唤醒和发布顺序。
