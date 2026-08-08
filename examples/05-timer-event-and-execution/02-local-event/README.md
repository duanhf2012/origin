# Service 本地事件

本地事件只在一个 Service 内分发，不是跨 Node 消息机制。监听器应在 `OnInit` 注册，这样业务任务开始前监听关系已经固定。示例同时覆盖 `NotifyEventSync`、`NotifyEventAsync` 和 `EventStats`。

## 流程

Timer 先进入 Service 任务上下文，再同步通知玩家 `1001`；监听器在当前任务中立即完成，并同步通知一个嵌套的审计事件。嵌套事件的监听器执行完成后，外层监听器才返回。随后异步通知玩家 `1002`，它作为普通后续 Service 任务排队执行。异步提交只保存 Event 接口值，提交成功后生产者不能再修改 payload。

同步监听器可以这样嵌套另一个同步事件：

```go
func onPlayerJoined(ctx context.Context, event service.Event) error {
    joined := event.(PlayerJoined)
    return target.NotifyEventSync(ctx, PlayerAudit{PlayerID: joined.PlayerID})
}
```

但不要在同步监听器中调用 `Await`。`Await` 会释放 Service 执行权，可能让其他任务插入；如果监听器需要等待数据库或 RPC，应改用异步事件，或在通知事件前先完成等待。

例如，需要在监听器中等待数据库时，改成异步通知：

```go
// 异步事件监听器作为后续 Service 任务执行，因此可以 Await。
func onPlayerJoinedAsync(ctx context.Context, event service.Event) error {
    joined := event.(PlayerJoined)
    return target.Await(ctx, func(waitCtx context.Context) error {
        return loadPlayerExtraData(waitCtx, joined.PlayerID)
    })
}

// 调用方只保证事件已入队，不等待监听器完成。
if err := target.NotifyEventAsync(PlayerJoined{PlayerID: 1002, Mode: "async"}); err != nil {
    target.Logger().Error("async event submission failed")
}
```

## 运行与练习

执行 `run.bat` 或 `./run.sh`，预期依次看到 sync 玩家日志、audit 日志和 async 玩家日志，随后看到
`sync=2 async=1 failures=0` 的统计（外层和嵌套事件各计一次同步通知）。可增加第二个监听器观察订阅顺序；同步监听器不能在嵌套调用中执行 `Await`。

对应教程：[Timer、Event 与执行](../../../docs/baseline/v3.0/guides/04-timer-event-and-execution.md)。
