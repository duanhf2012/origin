# Await、RunSafe 与 GoSafe

示例同时覆盖 `SetDefaultAwaitTimeout`、`Await`、`DispatchAsync`、`RunSafe`、`GoSafe` 和 `ExecutionStats`。它们都围绕同一个 Service 调度器，但解决的问题不同。

## 如何阅读示例

`Await` 在等待外部操作时协作式让出执行权；`DispatchAsync` 提交一个后续串行任务；`RunSafe` 在当前 goroutine 建立 panic 边界；`GoSafe` 启动带 panic 保底的后台 goroutine。示例没有故意触发 panic，因为安全边界不应成为正常控制流。

## 运行与练习

执行 `run.bat` 或 `./run.sh`，观察 Await、同步安全任务、后台任务、派发任务和执行统计日志。可把等待时间改到超过默认 500ms，观察超时统计；不要把 `GoSafe` 当作可以无锁读写 Service 状态的许可。

对应教程：[Timer、Event 与执行](../../../docs/baseline/v3.0/guides/04-timer-event-and-execution.md)。
