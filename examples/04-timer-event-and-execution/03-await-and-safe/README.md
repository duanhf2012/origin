# Await、RunSafe 与 GoSafe

三者解决的问题不同：`Await` 在等待外部操作时协作式让出当前 Service；`RunSafe` 为当前 goroutine 建立 panic 边界；`GoSafe` 启动一个同样受 panic 保护的后台 goroutine。

## 如何阅读示例

示例分别输出 Await 完成、RunSafe 完成和 GoSafe 完成。它没有故意触发 panic，因为 panic 处理是保护边界，不应成为正常控制流。

## 运行与练习

执行 `run.bat` 或 `./run.sh`，等待三行输出后按 `Ctrl+C`。可以把 Await 内部等待时间调长，观察 Service 不会因同步等待而永久占住调度；不要把 `RunSafe`、`GoSafe` 当作可以并发读写 Service 业务状态的许可。

对应教程：[Timer、Event 与执行](../../../docs/baseline/v3.0/guides/04-timer-event-and-execution.md)。
