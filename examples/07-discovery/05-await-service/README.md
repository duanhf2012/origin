# 等待发现服务

这个无外部依赖的示例通过一个演示 Provider 发布 `player-1:PlayerService`。`GatewayService.OnStart` 使用 `AwaitNodeService` 等待它出现，然后用 `FindDiscoveredService` 读取快照。

```text
run.bat
```

预期日志：`discovery target is ready: player-1`。

对应教程：[服务发现](../../../docs/baseline/v3.0/guides/07-discovery.md)。
