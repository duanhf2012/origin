# Await、RunSafe 与 GoSafe

示例同时覆盖 `SetDefaultAwaitTimeout`、`Await`、`DispatchAsync`、`RunSafe`、`GoSafe` 和 `ExecutionStats`。它们都围绕同一个 Service 调度器，但解决的问题不同。

## 如何阅读示例

`Await` 在等待外部操作时协作式让出执行权；`DispatchAsync` 把函数放入 Service 的有界串行队列，常与 `GoSafe` 配合，让后台 goroutine 完成 I/O 或计算后把结果交回 Service 更新状态，返回成功只表示入队；`RunSafe` 在当前 goroutine 同步执行并返回 panic 错误；`GoSafe` 类似 `go func`，启动后台 goroutine 并只记录 panic。示例没有故意触发 panic，因为安全边界不应成为正常控制流。

## RunSafe 与 GoSafe 怎么选

两者都只是 panic 边界，不会自动保护 Service 的业务字段：

| 方法 | 执行方式 | 返回语义 | 适用场景 |
| --- | --- | --- | --- |
| `RunSafe` | 当前 goroutine 同步执行 | 等待 `fn` 完成；panic 转为错误 | Worker 循环中的单个 Job，需要失败后继续下一项 |
| `GoSafe` | 新 goroutine 异步执行 | 只表示 goroutine 已启动；后台 panic 记录日志后退出 | 启动独立后台 Worker，由业务负责取消和等待 |

```go
// 当前调用会等 Job 执行完；panic 不会越过 RunSafe。
if err := target.RunSafe(func() { processOneJob(job) }); err != nil {
    target.Logger().Error("job failed")
}

// 立即返回；workerCtx 取消后，OnStop 仍要等待 worker 退出。
_ = target.GoSafe(func() { runWorker(workerCtx) })
```

如果后台 Worker 需要修改 Service 状态，先在 Worker 中计算局部结果，再通过
`DispatchAsync` 交回 Service 串行任务；不要因为使用了 `RunSafe` 或 `GoSafe` 就直接并发读写 Service 字段。

```go
_ = target.GoSafe(func() {
    result := "background result" // 后台只处理局部数据。
    _ = target.DispatchAsync(func(context.Context) {
        target.Logger().Info(result) // 回到 Service 串行任务后再处理结果。
    })
})
```

注意：`Await` 的等待函数执行期间，当前 Service 已让出执行权，同一个 Service 的其他任务可能
同时运行。因此不要在等待函数中读取或修改 Service 的公共可变字段；否则等待函数与其他任务
一读一写同一字段而没有同步，就会产生 data race。等待函数只返回或填充局部结果，`Await` 返回
后再更新 Service 状态：

```go
var result PlayerList
err := target.Await(ctx, func(waitCtx context.Context) error {
    result = loadPlayers(waitCtx) // 只写本次调用的局部结果，不访问 target 的业务字段。
    return nil
})
if err == nil {
    target.players = result // Await 返回后重新处于 Service 串行任务中。
}
```

日志方法本身是并发安全的；`GoSafe`、`RunSafe` 只提供 panic 边界，不会自动为业务字段加锁。

## 运行与练习

执行 `run.bat` 或 `./run.sh`，观察 Await、同步安全任务、后台任务、派发任务和执行统计日志。可把等待时间改到超过默认 500ms，观察超时统计；不要把 `GoSafe` 当作可以无锁读写 Service 状态的许可。

对应教程：[Timer、Event 与执行](../../../docs/baseline/v3.0/guides/05.timer-event-and-execution.md)。
