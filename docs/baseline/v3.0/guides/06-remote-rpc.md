# 06：跨节点 RPC

## 我想先用 TCP 调用另一个 Node

从 TCP 开始，因为它不需要 NATS。运行：[examples/06-remote-rpc/01-tcp-two-nodes](../../../../examples/06-remote-rpc/01-tcp-two-nodes)。

该示例在同一个 Application 中启动发现服务、`gateway-1` 与 `player-1`，并由网关执行：

```go
player, err := s.players.
    OnNode("player-1").
    AwaitGetPlayer(ctx, playerID)
```

`OnNode` 适合已知目标 Node；未指定时，客户端从发现目录的 Running 实例中选择一个候选。

契约、客户端和静态 Dispatcher 与传输无关。每个示例把普通 `PlayerService` 实现放在
自己的业务目录，只复用 `_support/tutorialrpc` 中的契约和 `player_service.rpc.gen.go`；
业务 Service 不需要生成文件。

## 我想改用 NATS

先启动开发依赖，再运行：[examples/06-remote-rpc/02-nats-two-nodes](../../../../examples/06-remote-rpc/02-nats-two-nodes)。

```text
examples\06-remote-rpc\02-nats-two-nodes\deps-up.bat
examples\06-remote-rpc\02-nats-two-nodes\run.bat
```

业务客户端外观不变，只替换 Node 的 `rpc.transport` 和 `rpc.nats` 配置。TCP 适合简单、直接的内网连接；NATS 适合已经运行 NATS 集群、希望由消息系统管理连接与恢复的部署。

配置若使用 `player-primary:PlayerService`，右侧模板名仍负责关联契约，左侧实际名用于发现
和路由。此时调用方使用 `BindPlayerServiceTo(s, "player-primary")`；不要尝试在业务代码中
调用不存在的 `SetName`。

## 我想按业务键选择实例或广播

运行：[examples/06-remote-rpc/03-route-and-broadcast](../../../../examples/06-remote-rpc/03-route-and-broadcast)。

```go
client.Route(playerID).AwaitGetPlayer(ctx, playerID)
client.RouteRoundRobin()
client.RouteRandom()
client.RouteBy(selector)
client.BroadcastRefresh(ctx, version)
```

自定义 Selector 必须同步、快速、可安全并发调用，并尽量使用无状态值类型以避免不必要分配。广播的部分失败不会被静默吞掉；检查返回的 `*rpc.BroadcastError`，读取成功数量和每个失败目标。

## 深入一点

默认选择会排除 Retired 实例；`IncludeRetired` 只应在业务明确需要时使用。远端发现事实、TCP/NATS 连接就绪和单次调用超时是不同状态：发现到实例不表示其连接已可用，调用仍需处理超时与传输不可用错误。
