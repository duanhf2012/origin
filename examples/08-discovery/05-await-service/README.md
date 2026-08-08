# 等待发现服务

此示例通过无外部依赖的演示 Provider 发布 `player-1:PlayerService`。`GatewayService.OnStart` 先等待目标出现，再执行精确查询和列表查询，适合启动依赖远端服务的业务。

## 关键代码

```go
if err := s.AwaitService(ctx, "PlayerService"); err != nil {
    // 任意 Node 都没有目标、超时或取消时结束当前启动/业务步骤。
    return err
}
if err := s.AwaitNodeService(ctx, "player-1", "PlayerService"); err != nil {
    // 等待指定 player-1 Node 发布目标 Service。
    return err
}
// 读取 player-1 当前发布的单个 Service 快照。
instance, ok := s.FindDiscoveredService("player-1", "PlayerService")
// 读取所有同名候选的当前快照。
instances := s.ListDiscoveredServices("PlayerService")
```

`AwaitService` 等待任意 Node 的指定 Service；`AwaitNodeService` 等待明确 Node；`FindDiscoveredService` 精确查询；`ListDiscoveredServices` 返回同名候选快照。发现监听通过 `AddDiscoveryListener` 注册，并在所属 Service 进入 `OnStop` 前由框架自动移除；只有运行期间需要提前取消时才调用 `RemoveDiscoveryListener`。

## 运行与练习

执行 `run.bat` 或 `./run.sh`，预期日志为 `discovery target is ready: player-1`。把目标名称改错可观察等待失败；真实 Provider 中再把等待结果与后续 RPC 连接/调用错误分别处理。

对应教程：[服务发现](../../../docs/baseline/v3.0/guides/07-discovery.md)。
