# 监听发现与 Lost

这个无网络的演示 Provider 用来理解“发现事件”而不是模拟完整的生产服务。它先发布 `player-1:PlayerService`，监听器收到 `OnDiscovered`；500ms 后 Provider 提交空快照，监听器立即收到 `OnLost`。这样可以在没有 etcd、NATS 或第二个进程的情况下看懂事件顺序。

## 关键流程

`disappearingProvider.Start` 通过 Host 发布权威快照并报告 Ready；随后替换为空快照并报告 Recovering。监听 Service 会先收到 `OnDiscovered`，随后立即收到 `OnLost`。`Lost` 表示实例从当前权威可见快照消失，不等同于 `Retired`；`Retired` 仍会保留在快照中并触发 `OnStateChanged`。

本示例把监听注册代码直接放在 [main.go](./main.go) 的 `DiscoveryWatcherService` 中，便于复制到业务项目：

```go
func (target *DiscoveryWatcherService) OnInit() error {
    // 监听器归属于当前 Service；进入 OnStop 前由框架自动移除。
    _, err := target.AddDiscoveryListener(target)
    return err
}
```

`DiscoveryWatcherService` 自身实现 `OnDiscovered`、`OnStateChanged` 和 `OnLost`，所以可以直接把 `target` 传给 `AddDiscoveryListener`。回调收到的 `Event` 包含变化的 `NodeID` 和一组 `Services`，业务可以据此刷新候选、移除路由、触发重试或执行降级。

只有需要在 Service 仍处于运行状态时提前取消某项监听，才需要保存返回的 `ListenerID` 并调用 `RemoveDiscoveryListener`。正常随 Service 生命周期结束的监听不需要在 `OnStop` 中手动移除。

## 运行与练习

执行 `run.bat` 或 `./run.sh`，按日志顺序确认：

1. 先出现 `discovered node=player-1`；
2. 约 500ms 后出现 `lost node=player-1`。

业务代码应将 `Lost` 用作移除候选、恢复、降级或告警输入；不要为了平滑日志而吞掉这类状态事件。完整教程见[服务发现](../../../docs/baseline/v3.0/guides/08.discovery.md#我想监听上线下线和-lost)。

对应教程：[服务发现](../../../docs/baseline/v3.0/guides/08.discovery.md)。
