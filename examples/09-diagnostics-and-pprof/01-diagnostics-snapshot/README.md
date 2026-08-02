# 读取 Diagnostics 快照

`Application.Diagnostics()` 返回统一的不可变诊断快照，适合业务代码、诊断 HTTP 和监控适配层共同读取。它不是一个可长期保存并自动更新的实时对象。

## 示例流程

应用启动后读取一次快照，输出 Application 状态、Node 数量和 Go goroutine 数量。代码只依赖 Application 的公开诊断外观，不直接访问 Node 或 RPC 的内部计数器。

## 运行与练习

执行 `run.bat` 或 `./run.sh`，阅读输出字段。可在 Timer 中周期性重新读取快照，比较数值变化；不要缓存第一次返回的快照来代表未来状态。

对应教程：[Diagnostics 与 pprof](../../../docs/baseline/v3.0/guides/09-diagnostics-and-pprof.md)。
