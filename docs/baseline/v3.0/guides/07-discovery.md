# 07：服务发现

## 我想使用 Origin 内置发现

运行：[examples/08-discovery/01-origin-provider](../../../../examples/08-discovery/01-origin-provider)。

Origin Provider 需要在任意一个 Node 配置唯一 `DiscoveryService`，并在顶层选择 `discovery.type: origin`：

```yaml
discovery:
  # 选择内置 Origin 发现 Provider。
  type: origin
  origin:
    # 服务目录失联后的存活租约。
    ttl: 5s
    server:
      # DiscoveryService 所在 Node。
      node: discovery-1
      # 服务端在本机监听的地址。
      listen: 127.0.0.1:18080
      # 发布给其他 Node 连接的地址。
      address: 127.0.0.1:18080
```

`DiscoveryService` 可以与该 Node 的其他 Service 共存，不要求专用 Node。

## 我想使用 etcd

运行：[examples/08-discovery/02-etcd-provider](../../../../examples/08-discovery/02-etcd-provider)。先执行其中的 `deps-up`，它只启动仓库已有 compose 中的 etcd 依赖。

```yaml
discovery:
  # 选择 etcd Provider。
  type: etcd
  etcd:
    # etcd 集群端点；生产环境应使用 TLS 地址与凭据。
    endpoints: [http://127.0.0.1:2379]
    # 当前 Node 写入的发现网络；读取范围会自动包含它。
    local_network: cn-east
    # 注册租约与一次请求的上限。
    ttl: 5s
    request_timeout: 3s
```

`namespace` 未配置时使用稳定默认值 `origin`；`local_network` 决定当前 Node 写入哪个发现网络，读取范围会自动包含该网络。配合 `watch_networks` 可以读取其他网络，去重后最多允许 64 个网络。

### 深入一点：读取其他网络

`watch_networks` 是 etcd Provider 已实现的配置，用于让一个 Node 读取 `local_network` 之外的网络分区：

```yaml
discovery:
  type: etcd
  etcd:
    endpoints: [http://127.0.0.1:2379]
    # 当前 Node 只向 cn-east 发布自己的服务记录。
    local_network: cn-east
    # 读取 cn-east、cn-north 和 cn-west 三个网络中的服务记录。
    watch_networks:
      - cn-north
      - cn-west
```

上例的读写方向是固定的：

- `local_network` 决定当前 Node 把自己的发现记录写入哪里；
- `local_network` 会自动包含在读取范围内，不需要重复写入 `watch_networks`；
- `watch_networks` 只增加读取范围，不会让当前 Node 把记录发布到这些网络；
- 配置重复网络时会自动去重，最终有效网络总数最多 64 个。

例如，`player-east` 发布到 `cn-east`，`player-north` 发布到 `cn-north`。配置了上例的网关可以同时读到两个 Node；但如果网关再配置：

```yaml
nodes:
  - id: gateway-1
    allow_discovery:
      - services: [PlayerService]
        node_labels:
          region: cn-east
```

它最终仍只会看到 `region=cn-east` 的 `PlayerService`。也就是说，Provider 网络范围是第一层筛选，`allow_discovery` 的 Service 和标签规则是第二层筛选。

当前 `examples/08-discovery/02-etcd-provider` 示例默认只启动 `cn-east` 网络的 Node，因此没有配置 `watch_networks`。要观察跨网络读取，需要让另一个进程使用相同的 etcd 集群和 namespace、但使用 `local_network: cn-north` 发布记录；观察进程则在 `watch_networks` 中加入 `cn-north`。该功能的配置归一化和跨网络 Watch 已由 etcd Provider 测试覆盖。

## 我想按区域筛选目标 Node

最常见的需求是：网关只调用同一区域的 `PlayerService`。先在提供服务的 Node 上发布它所在的区域：

```yaml
nodes:
  - id: player-1
    labels:
      # 声明当前 Node 位于 cn-east 区域。
      region: cn-east
    services: [PlayerService]
```

然后在使用服务的 Node 上通过 `allow_discovery.node_labels` 写出目标区域：

```yaml
nodes:
  - id: gateway-1
    services: [GatewayService]
    allow_discovery:
      - services: [PlayerService]
        node_labels:
          # 只发现 cn-east 区域 Node 上的 PlayerService。
          region: cn-east
```

这里有两个容易混淆的 `services` 字段：

- `nodes[].services` 声明当前 Node 本地运行哪些 Service，例如 `[GatewayService]`；
- `allow_discovery.services` 声明当前 Node 允许发现哪些远端 Service，例如 `[PlayerService]`。

因此，上面规则中的 `services: [PlayerService]` 表示“只关注满足标签条件的 `PlayerService`”，不会发现这些 Node 上的其他 Service。多个服务名表示“或”：

```yaml
allow_discovery:
  - services: [PlayerService, ChatService]
    node_labels:
      region: cn-east
```

这表示发现 `cn-east` 区域 Node 上的 `PlayerService` 或 `ChatService`。

如果省略规则中的 `services`，则不限制远端 Service 名称，只按其他条件筛选：

```yaml
allow_discovery:
  - node_labels:
      # 发现 cn-east 区域 Node 上的全部公开 Service。
      region: cn-east
```

`services: []` 不是“发现零个 Service”的写法，而是无效配置，会在启动前拒绝。需要关闭当前 Node 的全部远端业务服务发现时，应直接写：

```yaml
allow_discovery: []
```

这里的 `labels.region` 是目标 Node 发布的属性，`allow_discovery.node_labels.region` 是当前 Node 对目标属性提出的条件。上述配置启动后，`gateway-1` 只能发现 `region=cn-east` 的 Node 上的 `PlayerService`；`cn-north` 或没有 `region` 标签的 Node 都不会进入它的可见服务列表。

`labels` 使用紧凑 YAML 写法也完全等价：

```yaml
labels: {region: cn-east}
```

发布 `region` 本身不会改变任何 Node 的发现范围；只有使用方配置了 `allow_discovery.node_labels` 后，它才参与筛选。

### 深入一点：自定义标签键

`region` 只是最常用的业务约定，并不是框架保留字段。标签名和值均由业务定义，只要都是非空字符串即可。例如，业务需要按部署环境、可用区和集群筛选时，可以在服务提供方同时发布多个标签：

```yaml
nodes:
  - id: player-1
    labels:
      region: cn-east
      environment: production
      zone: zone-a
      cluster: game-main
    services: [PlayerService]
```

框架不会因为标签名叫作 `environment`、`zone` 或 `cluster` 而改变行为；只有筛选条件引用同名标签时，它才会影响发现结果。每个发布标签键只对应一个值：例如 `player-1` 只能处于一个 `region`，因此发布侧写 `region: cn-east`，而不是值列表。

### 深入一点：筛选多个候选值

使用方可以对同一个标签键接受多个值。例如，网关可调用华东和华北的 `PlayerService`：

```yaml
nodes:
  - id: gateway-1
    services: [GatewayService]
    allow_discovery:
      - services: [PlayerService]
        node_labels:
          # region=cn-east 或 region=cn-north 均可匹配。
          region: [cn-east, cn-north]
```

同一个标签键的多个候选值是“或”关系。上例等价于“目标 `region` 为 `cn-east` 或 `cn-north`”。

### 深入一点：按多个维度同时筛选

在同一条规则中增加不同标签键时，它们必须同时满足。例如，只调用华东生产环境的 `PlayerService`：

```yaml
nodes:
  - id: gateway-1
    services: [GatewayService]
    allow_discovery:
      - services: [PlayerService]
        node_labels:
          region: cn-east
          environment: production
```

这条规则要求目标 Node 同时具有 `region=cn-east` 和 `environment=production`。如果只满足其中一个，或缺少其中任一标签，都不会匹配。`services` 与 `node_labels` 也是“且”关系：目标既要是 `PlayerService`，又要通过所有标签条件。

同一条规则中的多个服务名是“或”关系；如果同时配置 `services` 和 `node_labels`，服务名条件与所有标签条件之间是“且”关系。例如：

```yaml
allow_discovery:
  - services: [PlayerService, ChatService]
    node_labels:
      region: cn-east
      environment: production
```

它表示发现“华东生产环境中的 `PlayerService` 或 `ChatService`”。

### 深入一点：使用多条筛选规则

需要表达两组独立条件时，增加多条规则。规则之间是“或”关系：

```yaml
allow_discovery:
  # 规则一：华东生产环境的 PlayerService。
  - services: [PlayerService]
    node_labels:
      region: cn-east
      environment: production
  # 规则二：任意环境的 ChatService，只要求位于华北。
  - services: [ChatService]
    node_labels:
      region: cn-north
```

满足规则一或规则二的服务都会被发现。标签匹配使用区分大小写的精确比较，不支持通配符或正则表达式。

### 深入一点：默认范围与发现网络

完全省略 `allow_discovery` 时，Node 会发现 Provider 范围内的全部公开 Service；明确配置 `allow_discovery: []` 时，不发现任何远端业务 Service。空规则（例如 `- {}`）没有明确筛选意图，会作为配置错误拒绝启动。

`discovery.etcd.local_network` 与 `nodes[].labels.region` 也不是同一个概念。`local_network` 决定 etcd Provider 将当前 Node 写入哪个发现网络；加上 `watch_networks` 后决定 Provider 读取哪些网络。`labels.region` 只是业务筛选使用的普通标签。Provider 先按网络范围读取，再由 `allow_discovery` 按服务名和标签过滤。两者可以都写成 `cn-east`，但框架不会自动关联，配置也可以不同。

标签筛选既适用于 etcd Provider，也适用于 Origin Provider 和第三方 Provider；它们只负责提供目录，筛选规则由框架统一执行。

## 我想监听上线、下线和 Lost

服务发现不只是用来“找到一个 RPC 地址”，业务通常还需要知道远端实例什么时候出现、状态什么时候变化、什么时候已经不能再使用。监听器就是接收这些可见目录变化的入口。

运行：[examples/08-discovery/03-watch-and-lost](../../../../examples/08-discovery/03-watch-and-lost)。这个示例不依赖 etcd 或真实网络，而是注册一个可控的内存 Provider，故意按以下时间线提交快照：

```text
启动
  │
  ├─ Provider 发布 player-1:PlayerService (Running)
  │       └─ 监听器收到 OnDiscovered
  │
  └─ 500ms 后 Provider 发布空快照
          └─ 监听器收到 OnLost
```

示例中 `watcher-1` 是本地 Node，`DiscoveryWatcherService` 在 `OnInit` 中注册监听器。完整可运行代码见：[03-watch-and-lost/main.go](../../../../examples/08-discovery/03-watch-and-lost/main.go)。

```go
func (target *DiscoveryWatcherService) OnInit() error {
    // 监听器归属于当前 Service；进入 OnStop 前由框架自动移除。
    _, err := target.AddDiscoveryListener(target)
    return err
}
```

正常随 Service 生命周期结束的监听不需要保存 `ListenerID`。停止流程会先停止发现事件准入并删除该 Service 的全部监听器，再排空已经接收的任务，最后调用 `OnStop`。只有需要在 Service 仍运行时提前取消某一项监听，才需要保存返回 ID 并调用 `RemoveDiscoveryListener`。

监听器需要实现三个回调：

| 回调 | 什么时候触发 | 业务通常做什么 |
| --- | --- | --- |
| `OnDiscovered` | 远端 Service 从不可见变为可见，或新会话重新出现 | 建立候选、刷新路由、解除降级 |
| `OnStateChanged` | 同一个远端 Service 的 `Running/Retired` 状态变化 | 更新状态、暂停或恢复业务流量 |
| `OnLost` | 已经可见的远端 Service 不再出现在权威快照中 | 移除候选、断开关联、重试、降级或告警 |

每个回调收到一个 `discovery.Event`：

```go
func (target *DiscoveryWatcherService) OnDiscovered(_ context.Context, event discovery.Event) {
    // event.NodeID 是发生变化的远端 Node；Services 可能一次包含多个 Service。
    target.Logger().Info(fmt.Sprintf(
        "discovered node=%s services=%v", event.NodeID, event.Services,
    ))
}

func (target *DiscoveryWatcherService) OnStateChanged(_ context.Context, event discovery.Event) {
    // Running/Retired 等状态变化在这里处理。
    target.Logger().Info(fmt.Sprintf(
        "state changed node=%s services=%v", event.NodeID, event.Services,
    ))
}

func (target *DiscoveryWatcherService) OnLost(_ context.Context, event discovery.Event) {
    target.Logger().Info(fmt.Sprintf(
        "lost node=%s services=%v", event.NodeID, event.Services,
    ))
}
```

`Lost` 与 `Retired` 不是一回事：`Retired` 仍然存在于发现快照中，只会触发 `OnStateChanged`；`Lost` 表示该实例已经从当前可见快照中消失。断线、租约失效或 Provider 提交空快照，都可能导致从“已发现”变为 `Lost`，具体由 Provider 报告的权威快照决定。

框架不对 `Lost` 做防抖。一次断开后即使很快恢复，也会先产生 `Lost`，恢复后再产生 `OnDiscovered`，这样业务不会因为短暂中间状态被隐藏而继续使用已经不可确认的实例。监听器回调按所属 Service 的 FIFO 调度执行；回调跨异步等待后，应重新查询当前快照，不要把事件对象当作永久状态保存。

执行 `run.bat` 或 `./run.sh` 后，预期先看到类似 `discovered node=player-1`，约 500ms 后看到 `lost node=player-1`。该示例的 Provider 实现见：[03-watch-and-lost/main.go](../../../../examples/08-discovery/03-watch-and-lost/main.go)。

## 我想等待远端服务出现

运行：[examples/08-discovery/05-await-service](../../../../examples/08-discovery/05-await-service)。在启动依赖另一个 Node 时，先等待发现目录出现目标，再读取其快照：

```go
if err := s.AwaitService(ctx, "PlayerService"); err != nil {
    // 任意 Node 上都没有目标、超时或取消时结束当前流程。
    return err
}
if err := s.AwaitNodeService(ctx, "player-1", "PlayerService"); err != nil {
    // 指定 Node 尚未发布目标 Service 时继续等待或返回错误。
    return err
}
// 读取指定 Node 的当前发现快照。
instance, ok := s.FindDiscoveredService("player-1", "PlayerService")
// 读取全部同名 Service 候选快照。
instances := s.ListDiscoveredServices("PlayerService")
```

`AwaitService` 等待任意 Node 上的指定 Service；`AwaitNodeService` 等待明确 Node；`FindDiscoveredService` 和 `ListDiscoveredServices` 读取当前快照。两种等待都复用 `Service.Await`，应传入生命周期或当前业务任务的 `Context`，并处理超时和取消。监听器通过 `AddDiscoveryListener` 注册，随 Service 停止时由框架自动移除；只有需要提前取消时才调用 `RemoveDiscoveryListener`。

## 我想替换为 Consul 或其他发现系统

运行：[examples/08-discovery/04-custom-provider](../../../../examples/08-discovery/04-custom-provider)。应用只需要注册一个小型 `discovery/provider.Provider` Factory：读取 Provider 配置、发布/撤销当前 Node、向 Host 报告完整快照。

示例演示 SPI，不伪装为可直接连接 Consul 的实现；真正的 Consul Provider 应作为独立包实现并通过相同注册点加入 Application。

## 深入一点：安全和边界

发现通常用于内网，但 etcd 仍应在生产环境启用 TLS 和最小认证；Origin 内置发现也应部署在受限网络中。Provider 只能报告规范化快照，框架负责目录更新、路由唤醒和发布顺序。
