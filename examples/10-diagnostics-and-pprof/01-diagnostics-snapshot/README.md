# 读取 Diagnostics 快照

`Application.Diagnostics()` 返回统一的不可变诊断快照，适合业务代码、诊断 HTTP 和监控适配层共同读取。示例同时展示 `Application.State`、`Nodes`、`Node(id)`、`Node.ID`、`Node.Private`、`Node.Service`、`Node.ServiceStatus`、`HealthStatus`、`TransportStatus`、`DiscoveryStatus`、`Node.Diagnostics()` 与进程级 `log.Info()`。

## 示例流程

应用启动后读取一次快照，输出 Application 状态、Node 数量和 Go goroutine 数量。快照不包含
日志输出状态，避免把低频的日志管理配置混入常用的进程、Node、RPC、发现和调度诊断数据。
确实需要查询 Console/File 当前启用状态与级别时，使用日志包的 `log.CurrentStatus()`；代码只
依赖公开诊断和日志外观，不直接访问 Node、日志或 RPC 内部对象。

## 运行与练习

执行 `run.bat` 或 `./run.sh`，阅读输出字段。可在 Timer 中周期性重新读取快照，比较数值变化；不要缓存第一次返回的快照来代表未来状态。

对应教程：[Diagnostics 与 pprof](../../../docs/baseline/v3.0/guides/09-diagnostics-and-pprof.md)。
日志状态和运行时控制见[日志输出与管理教程](../../../docs/maintenance/v3.1/guides/logging.md)。
