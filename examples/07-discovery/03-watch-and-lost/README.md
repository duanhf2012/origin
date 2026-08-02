# 监听发现与 Lost

这个无网络的演示 Provider 先发布 `player-1:PlayerService`，500ms 后提交空快照。它把 Lost 展示为立即生效的状态事实，而不是经过防抖后才通知的猜测。

## 关键流程

`disappearingProvider.Start` 通过 Host 发布权威快照并报告 Ready；随后替换为空快照并报告 Recovering。监听 Service 会先收到 `discovered`，随后立即收到 `lost`。

## 运行与练习

执行 `run.bat` 或 `./run.sh`，按日志顺序确认 `discovered` 在前、`lost` 在后。业务代码应将 Lost 用作恢复、降级或告警输入；不要为了平滑日志而吞掉这类状态事件。

对应教程：[服务发现](../../../docs/baseline/v3.0/guides/07-discovery.md)。
