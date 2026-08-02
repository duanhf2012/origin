# 发现 Lost 练习

这是服务发现 Lost 示例的排错入口。演示 Provider 先产生 `discovered`，随后提交空快照并产生 `lost`，无需网络或外部中间件。

## 运行与观察

执行 `run.bat` 或 `./run.sh`，确认日志先后顺序。若业务监听器只处理上线而忽略 Lost，就会保留过期的远端视图，随后可能向已经不可用的实例继续路由。

## 恢复原则

Lost 是立即状态事实；业务应据此降级、清理本地关联状态、触发重连或告警。不要加业务层防抖来掩盖中间断线，否则可能造成服务间状态不一致。

对应教程：[服务发现](../../../docs/baseline/v3.0/guides/07-discovery.md) 与 [故障排查](../../../docs/baseline/v3.0/guides/12-troubleshooting.md)。
