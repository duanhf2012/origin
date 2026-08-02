# 04：Timer、Event 与安全执行

## 我想稍后或按周期执行任务

运行：[examples/04-timer-event-and-execution/01-delay-and-cron](../../../../examples/04-timer-event-and-execution/01-delay-and-cron)。

```go
s.AfterFunc(300*time.Millisecond, func(ctx context.Context, id service.TimerID) {
    // 只执行一次
})

s.CronFunc("*/1 * * * * *", func(ctx context.Context, id service.TimerID) {
    // 每秒一次；支持 5 或 6 段数字 Cron
})
```

Timer 回调已经处于所属 Service 的执行上下文；不要再为同一份 Service 状态额外启动 goroutine。

## 我想在同一 Service 内通知事件

运行：[examples/04-timer-event-and-execution/02-local-event](../../../../examples/04-timer-event-and-execution/02-local-event)。先在 `OnInit` 注册监听器，再从业务任务中通知：

```go
err := s.SubscribeEvent(playerJoinedEvent, handler) // OnInit
err = s.NotifyEventSync(ctx, PlayerJoined{PlayerID: 1001})
```

## 我想等待操作或安全地执行独立工作

运行：[examples/04-timer-event-and-execution/03-await-and-safe](../../../../examples/04-timer-event-and-execution/03-await-and-safe)。

- `Await`：等待 I/O、RPC 等操作，并让 Service 在等待时继续处理其他任务。
- `RunSafe`：当前 goroutine 的独立、已隔离工作；不授予并发修改 Service 状态的权限。
- `GoSafe`：业务自行管理生命周期的后台 goroutine 的 panic 保底；仍要自行使用 Context、CancelFunc、WaitGroup 在 `OnStop` 清理。

## 深入一点

同步事件处理器可以嵌套同步事件，但不能在其中 `Await`。异步事件只保证已进入队列，不能在提交后修改事件 payload。Timer、事件和 RPC 回调都遵循 Service 的单执行语义；要并发处理纯计算或外部 I/O 时，应明确隔离状态并选择 `Await` 或受控后台任务。
